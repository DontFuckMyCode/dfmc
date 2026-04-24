// gh_runner.go — shared infrastructure for the gh tool surface.
// Mirrors the git_runner.go safety model: argv-only exec.Command (no
// shell), bounded stdout/stderr capture, per-call timeout, and the
// basic sanity checks (no flag injection, no destructive subcommands).
//
// Each gh subcommand lives in its own file (e.g. gh_pr.go, gh_issue.go)
// to keep the surface modular. This file provides only the exec helper.

package tools

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const (
	ghDefaultTimeout = 30 * time.Second
	ghMaxTimeout     = 120 * time.Second
)

// ghSafeSubcommands is the allowlist of gh subcommands this surface
// exposes. Subcommands not listed here return a "not yet supported"
// error so callers know the interface is intentionally narrow rather
// than silently falling through to an unrestricted runner.
var ghSafeSubcommands = map[string]struct{}{
	"pr":     {},
	"issue":  {},
	"run":    {},
	"repo":   {},
	"api":    {}, // raw API calls — limited to GET, handled in gh_api.go
}

// runGH executes `gh <args...>` inside projectRoot and returns
// (stdout, stderr, exit, err). Never invokes a shell.
func runGH(ctx context.Context, projectRoot string, timeout time.Duration, args ...string) (string, string, int, error) {
	if timeout <= 0 {
		timeout = ghDefaultTimeout
	}
	if timeout > ghMaxTimeout {
		timeout = ghMaxTimeout
	}

	if len(args) == 0 {
		return "", "", 0, fmt.Errorf("gh requires at least one subcommand")
	}

	sub := strings.ToLower(args[0])
	if _, ok := ghSafeSubcommands[sub]; !ok {
		return "", "", 0, fmt.Errorf("gh %s: subcommand not supported by this tool surface (supported: pr, issue, run, repo, api GET)", sub)
	}

	// Block flag injection: any arg starting with `-` that appears before
	// the subcommand subgroup (pr list, issue view, etc.) is rejected.
	// gh itself is generally safe but this prevents a confused model
	// from passing `-` values in a position git would interpret as a flag.
	for _, a := range args[1:] {
		if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") {
			// Single-dash args are not valid gh invocations in any position
			return "", "", 0, fmt.Errorf("gh argument %q looks like a flag; use --flag=value or separate the flag", a)
		}
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "gh", args...)
	cmd.Dir = projectRoot
	stdout := newBoundedBuffer(runCommandOutputCap)
	stderr := newBoundedBuffer(runCommandOutputCap)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	exitCode := 0
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		exitCode = exitErr.ExitCode()
	}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return stdout.String(), stderr.String(), exitCode, fmt.Errorf("gh timed out after %s", timeout)
	}
	return stdout.String(), stderr.String(), exitCode, err
}

// resolveGHTimeout reads the optional per-call timeout param.
func resolveGHTimeout(params map[string]any) time.Duration {
	if raw := strings.TrimSpace(asString(params, "timeout", "")); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			return d
		}
	}
	if ms := asInt(params, "timeout_ms", 0); ms > 0 {
		return time.Duration(ms) * time.Millisecond
	}
	return ghDefaultTimeout
}