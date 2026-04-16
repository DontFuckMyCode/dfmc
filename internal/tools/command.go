package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type RunCommandTool struct {
	cfg runCommandConfig
}

func NewRunCommandTool(cfg runCommandConfig) *RunCommandTool {
	return &RunCommandTool{cfg: cfg}
}

func (t *RunCommandTool) Name() string { return "run_command" }

func (t *RunCommandTool) Description() string {
	return "Run a direct command inside the project root with timeout, no shell interpreter, and guarded blocked-command checks."
}

func (t *RunCommandTool) Execute(ctx context.Context, req Request) (Result, error) {
	if t == nil {
		return Result{}, fmt.Errorf("run_command config is unavailable")
	}
	if !t.cfg.allowShell {
		return Result{}, fmt.Errorf("run_command is disabled by security.sandbox.allow_shell=false")
	}

	command := strings.TrimSpace(asString(req.Params, "command", ""))
	if command == "" {
		return Result{}, fmt.Errorf("command is required")
	}
	if isBlockedShellInterpreter(command) {
		return Result{}, fmt.Errorf("shell interpreters are blocked for run_command: %s", command)
	}

	args, err := commandArgs(req.Params["args"])
	if err != nil {
		return Result{}, err
	}
	if err := ensureCommandAllowed(command, args, t.cfg.blocked); err != nil {
		return Result{}, err
	}

	workDir, err := commandWorkingDir(req.ProjectRoot, asString(req.Params, "dir", "."))
	if err != nil {
		return Result{}, err
	}
	execPath := command
	if looksLikePath(command) {
		execPath, err = EnsureWithinRoot(req.ProjectRoot, command)
		if err != nil {
			return Result{}, err
		}
	}

	timeout := resolveCommandTimeout(req.Params, t.cfg.timeout)
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	beforeChanged, _ := gitChangedFilesSnapshot(req.ProjectRoot)
	cmd := exec.CommandContext(runCtx, execPath, args...)
	cmd.Dir = workDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	afterChanged, _ := gitChangedFilesSnapshot(req.ProjectRoot)

	output := strings.TrimSpace(strings.TrimSpace(stdout.String()) + joinCommandStderr(stderr.String(), stdout.Len() > 0))
	res := Result{
		Output: output,
		Data: map[string]any{
			"command":           command,
			"resolved_command":  execPath,
			"args":              args,
			"dir":               filepath.ToSlash(relPathOrAbsolute(req.ProjectRoot, workDir)),
			"timeout_ms":        timeout.Milliseconds(),
			"workspace_changed": !slices.Equal(beforeChanged, afterChanged),
		},
	}
	if !slices.Equal(beforeChanged, afterChanged) {
		res.Data["changed_files"] = afterChanged
	}

	if err == nil {
		return res, nil
	}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return res, fmt.Errorf("command timed out after %s", timeout)
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		res.Data["exit_code"] = exitErr.ExitCode()
		return res, fmt.Errorf("command exited with code %d", exitErr.ExitCode())
	}
	return res, err
}

type runCommandConfig struct {
	allowShell bool
	timeout    time.Duration
	blocked    []string
}

func commandArgs(raw any) ([]string, error) {
	switch v := raw.(type) {
	case nil:
		return nil, nil
	case string:
		return splitCommandArgs(v)
	case []string:
		return append([]string(nil), v...), nil
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			out = append(out, fmt.Sprint(item))
		}
		return out, nil
	default:
		return splitCommandArgs(fmt.Sprint(v))
	}
}

func splitCommandArgs(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var (
		args    []string
		current strings.Builder
		quote   rune
	)
	flush := func() {
		if current.Len() == 0 {
			return
		}
		args = append(args, current.String())
		current.Reset()
	}
	for _, r := range raw {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
				continue
			}
			current.WriteRune(r)
		case r == '"' || r == '\'':
			quote = r
		case r == ' ' || r == '\t' || r == '\n':
			flush()
		default:
			current.WriteRune(r)
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quoted args value")
	}
	flush()
	return args, nil
}

func resolveCommandTimeout(params map[string]any, fallback time.Duration) time.Duration {
	if fallback <= 0 {
		fallback = 30 * time.Second
	}
	if params == nil {
		return fallback
	}
	if raw := strings.TrimSpace(asString(params, "timeout", "")); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			return clampCommandTimeout(d, fallback)
		}
	}
	if ms := asInt(params, "timeout_ms", 0); ms > 0 {
		return clampCommandTimeout(time.Duration(ms)*time.Millisecond, fallback)
	}
	return fallback
}

func clampCommandTimeout(requested, limit time.Duration) time.Duration {
	if requested <= 0 {
		return limit
	}
	if limit > 0 && requested > limit {
		return limit
	}
	return requested
}

func ensureCommandAllowed(command string, args []string, blocked []string) error {
	full := strings.ToLower(strings.TrimSpace(strings.Join(append([]string{command}, args...), " ")))
	for _, item := range append(defaultBlockedCommandPatterns(), blocked...) {
		pattern := strings.ToLower(strings.TrimSpace(item))
		if pattern != "" && strings.Contains(full, pattern) {
			return fmt.Errorf("command blocked by policy: %s", item)
		}
	}
	binary := strings.ToLower(filepath.Base(strings.TrimSpace(command)))
	switch binary {
	case "rm", "del", "rmdir", "format", "mkfs", "diskpart":
		return fmt.Errorf("command blocked by policy: %s", command)
	case "git":
		if hasArgSequence(args, "reset", "--hard") || hasArgSequence(args, "clean", "-fd") || hasArgSequence(args, "clean", "-fdx") || hasArgSequence(args, "checkout", "--") || hasArgSequence(args, "restore", "--source") {
			return fmt.Errorf("command blocked by policy: git %s", strings.Join(args, " "))
		}
	}
	return nil
}

func defaultBlockedCommandPatterns() []string {
	return []string{
		"rm -rf /",
		"dd if=",
		"mkfs",
		"del /f",
		"rmdir /s",
		"format ",
	}
}

func hasArgSequence(args []string, seq ...string) bool {
	if len(seq) == 0 || len(args) < len(seq) {
		return false
	}
	for i := 0; i <= len(args)-len(seq); i++ {
		match := true
		for j := 0; j < len(seq); j++ {
			if !strings.EqualFold(strings.TrimSpace(args[i+j]), seq[j]) {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func commandWorkingDir(projectRoot, raw string) (string, error) {
	dir := strings.TrimSpace(raw)
	if dir == "" || dir == "." {
		return projectRoot, nil
	}
	return EnsureWithinRoot(projectRoot, dir)
}

func looksLikePath(command string) bool {
	command = strings.TrimSpace(command)
	return strings.Contains(command, "/") || strings.Contains(command, "\\") || strings.HasPrefix(command, ".")
}

func isBlockedShellInterpreter(command string) bool {
	binary := strings.ToLower(filepath.Base(strings.TrimSpace(command)))
	switch binary {
	case "cmd", "cmd.exe", "powershell", "powershell.exe", "pwsh", "pwsh.exe", "bash", "sh", "zsh", "fish", "nu":
		return true
	default:
		return false
	}
}

func joinCommandStderr(stderr string, hasStdout bool) string {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		return ""
	}
	if !hasStdout {
		return stderr
	}
	return "\n\n[stderr]\n" + stderr
}

func relPathOrAbsolute(root, target string) string {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return target
	}
	if rel == "." {
		return "."
	}
	return rel
}

func gitChangedFilesSnapshot(projectRoot string) ([]string, error) {
	cmd := exec.Command("git", "-C", projectRoot, "status", "--short", "--untracked-files=all", "--")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n")
	changed := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimRight(line, " ")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if len(line) > 3 {
			changed = append(changed, filepath.ToSlash(strings.TrimSpace(line[3:])))
		} else {
			changed = append(changed, line)
		}
	}
	return changed, nil
}
