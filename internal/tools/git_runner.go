// git_runner.go — the shared infrastructure that every git_* tool rides
// on. Nothing here is a tool by itself; together they are the safety
// boundary between a model-supplied argv and a real `git` invocation:
//
//   - runGit / runGitWithAllowedBlockedArgs: argv-only exec.Command (no
//     shell), bounded stdout/stderr capture, per-call timeout, and the
//     blocklist check that keeps --no-verify / --amend / --upload-pack=
//     out of user-controlled args.
//   - blockedGitArg / normalizeBlockedGitAllowlist: the blocklist itself
//     plus its per-call override hook (used sparingly when a tool
//     legitimately needs a flag the global list would refuse).
//   - rejectGitFlagInjection: the CVE-2018-17456 guardrail — refuses any
//     non-empty ref/revision/branch/path value that starts with `-`,
//     because git parses those as options before the `--` separator.
//   - resolveGitTimeout: small "seconds | duration string" reader for the
//     per-tool `timeout` / `timeout_ms` param.
//
// Plus the trivial output/argv helpers every tool reaches for:
// stringSliceArg (string / []string / []any unification), joinGitOutput
// (stdout + stderr formatting), splitLines, firstNonEmptyLine, and
// blockedBranchName (shared branch-name sanity check).

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
	gitDefaultTimeout = 30 * time.Second
	gitMaxTimeout     = 120 * time.Second
)

// runGit executes `git <args...>` inside projectRoot and returns (stdout,
// stderr, exit, err). Never invokes a shell. The caller is responsible for
// vetting args — blockedGitArg handles the shared "never allow this flag"
// list. Empty projectRoot falls back to the process CWD, consistent with the
// rest of the tool registry.
func runGit(ctx context.Context, projectRoot string, timeout time.Duration, args ...string) (string, string, int, error) {
	return runGitWithAllowedBlockedArgs(ctx, projectRoot, timeout, nil, args...)
}

func runGitWithAllowedBlockedArgs(ctx context.Context, projectRoot string, timeout time.Duration, allowed map[string]struct{}, args ...string) (string, string, int, error) {
	if timeout <= 0 {
		timeout = gitDefaultTimeout
	}
	if timeout > gitMaxTimeout {
		timeout = gitMaxTimeout
	}
	allowed = normalizeBlockedGitAllowlist(allowed)
	for _, a := range args {
		if blockedGitArg(a, allowed) {
			return "", "", 0, fmt.Errorf("git argument blocked by policy: %s", a)
		}
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "git", args...)
	cmd.Dir = projectRoot
	// Bounded capture — same reasoning as run_command. `git log -p`
	// across a large repo, `git diff main..feature` over a sweeping
	// refactor, `git show <huge-commit>`: all can produce tens of MB
	// of patch output. Capping at 4 MiB per stream keeps the parent
	// heap bounded; the agent gets the head + a truncation marker
	// instead of an OOM.
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
		return stdout.String(), stderr.String(), exitCode, fmt.Errorf("git timed out after %s", timeout)
	}
	return stdout.String(), stderr.String(), exitCode, err
}

// blockedGitArg enforces the safety rules from CLAUDE.md's git-safety
// protocol for anything that lands in these tools. Destructive flags route
// users back to explicit shell approval via run_command.
func blockedGitArg(arg string, allowed map[string]struct{}) bool {
	a := strings.ToLower(strings.TrimSpace(arg))
	if _, ok := allowed[a]; ok {
		return false
	}
	switch a {
	case "--no-verify", "--no-gpg-sign", "--amend", "-i", "--interactive", "--force", "-f", "--hard", "--no-checkout":
		return true
	}
	for _, prefix := range []string{"--exec=", "--receive-pack=", "--upload-pack="} {
		if strings.HasPrefix(a, prefix) {
			return true
		}
	}
	return false
}

func normalizeBlockedGitAllowlist(allowed map[string]struct{}) map[string]struct{} {
	if len(allowed) == 0 {
		return nil
	}
	normalized := make(map[string]struct{}, len(allowed))
	for key := range allowed {
		normalized[strings.ToLower(strings.TrimSpace(key))] = struct{}{}
	}
	return normalized
}

// rejectGitFlagInjection refuses any user-supplied value that looks like
// a git option (prefixed with `-`). git treats `--upload-pack=cmd`,
// `--exec=cmd`, etc. as flags rather than refs, which is the shape of
// CVE-2018-17456 — a malicious or confused model passing
// revision="--upload-pack=/tmp/x.sh" would have git execute that path.
// Path args ARE separated with `--` everywhere, but ref/revision/branch
// values land in argv before any separator, so we have to reject at
// the callsite. Empty values are allowed (caller is expected to skip
// them); the check fires only on non-empty `-`-prefix strings.
func rejectGitFlagInjection(kind, value string) error {
	if value == "" || !strings.HasPrefix(value, "-") {
		return nil
	}
	return fmt.Errorf(
		"git: %s value %q starts with `-`; refused to prevent flag injection (CVE-2018-17456 class). "+
			"git would parse this as a command-line option, not as a %s. "+
			"If the value is legitimate, rename it or invoke git via run_command with explicit `--` quoting.",
		kind, value, kind)
}

// resolveGitTimeout reads the optional per-call timeout param (seconds or
// duration string).
func resolveGitTimeout(params map[string]any) time.Duration {
	if raw := strings.TrimSpace(asString(params, "timeout", "")); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			return d
		}
	}
	if ms := asInt(params, "timeout_ms", 0); ms > 0 {
		return time.Duration(ms) * time.Millisecond
	}
	return gitDefaultTimeout
}

func stringSliceArg(raw any) []string {
	switch v := raw.(type) {
	case nil:
		return nil
	case []string:
		out := make([]string, 0, len(v))
		for _, s := range v {
			s = strings.TrimSpace(s)
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s := strings.TrimSpace(fmt.Sprint(item))
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		parts := strings.Split(v, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	return nil
}

func joinGitOutput(stdout, stderr string) string {
	stdout = strings.TrimRight(stdout, "\n")
	stderr = strings.TrimRight(stderr, "\n")
	switch {
	case stdout == "" && stderr == "":
		return ""
	case stdout == "":
		return stderr
	case stderr == "":
		return stdout
	default:
		return stdout + "\n\n[stderr]\n" + stderr
	}
}

func splitLines(raw string) []string {
	out := []string{}
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func firstNonEmptyLine(raw string) string {
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return ""
}

func blockedBranchName(name string) bool {
	if strings.TrimSpace(name) == "" {
		return true
	}
	for _, r := range name {
		if r == ' ' || r == '\t' || r == '\n' || r == '~' || r == '^' || r == ':' || r == '?' || r == '*' {
			return true
		}
	}
	if strings.Contains(name, "..") {
		return true
	}
	if strings.HasPrefix(name, "-") {
		return true
	}
	return false
}
