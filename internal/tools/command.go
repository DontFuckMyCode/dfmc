package tools

// command.go — RunCommandTool entry point. Owns the Tool interface
// implementation, request validation flow, exec.CommandContext
// wiring, output capture, and result shaping. The detail work is
// split across siblings:
//
//   - command_args.go      argv tokenization (commandArgs,
//                          splitCommandArgs) and timeout resolution
//                          (resolveCommandTimeout, clampCommandTimeout)
//   - command_validate.go  binary blocklist + arg-sequence policy
//                          (ensureCommandAllowed, canonicalCommandBinary,
//                          isBlockedBinary, checkBlockedArgSequences),
//                          shell-metacharacter / interpreter / script-
//                          runner detection
//   - command_recovery.go  hint generators for shell-line packing
//                          (suggestRunCommandRecovery,
//                          detectBinaryArgsPacking, suggestSplitRunCommand)

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// runCommandOutputCap bounds stdout + stderr capture per invocation.
// 4 MiB covers a verbose `cargo build`, `npm install`, or full
// `pytest` run with traces; beyond that the agent gets the head plus
// a truncation marker, which is more useful than crashing the parent
// on a runaway producer.
const runCommandOutputCap = 4 << 20 // 4 MiB

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
		return Result{}, fmt.Errorf("run_command is disabled by security.sandbox.allow_command=false (legacy key: allow_shell=false); this flag disables the whole run_command tool, not only shell interpreters")
	}

	command := strings.TrimSpace(asString(req.Params, "command", ""))
	if command == "" {
		return Result{}, missingParamError("run_command", "command", req.Params,
			`{"command":"go","args":["build","./..."],"timeout_ms":60000}`,
			`command is the binary to execute (e.g. "go", "npm", "pytest"). Pass arguments as args (string[] or whitespace-joined string). NOT a shell line — no &&, ||, |, >, &. For shell composition, run separate calls or invoke a script you write to disk first.`)
	}
	if isBlockedShellInterpreter(command) {
		return Result{}, fmt.Errorf("shell interpreters are blocked for run_command: %s", command)
	}
	if token := detectShellMetacharacter(command); token != "" {
		// Bail out before path resolution turns a shell-line into a
		// "file does not exist" mystery. The model passed something
		// like `cd /repo && go build ./...` as `command` — an entire
		// shell line — because it assumed run_command shells out.
		// It does not. Fail with a message that names the offender
		// and shows the right shape so the next tool call self-corrects.
		// Detect the very common `cd <dir> && <real cmd>` shape and
		// rewrite the example to use the `dir` parameter (which exists
		// precisely for this case) — that's the recovery the model
		// should learn, not "issue two tool calls".
		if hint := suggestRunCommandRecovery(command); hint != "" {
			return Result{}, fmt.Errorf(
				"run_command does not invoke a shell — `command` must be a single binary, not a shell line. "+
					"Found shell syntax %q in command. Recover by calling: %s. "+
					"`dir` sets the working directory (no `cd` needed); for sequential steps issue separate tool_calls",
				token, hint)
		}
		return Result{}, fmt.Errorf(
			"run_command does not invoke a shell — `command` must be a single binary, not a shell line. "+
				"Found shell syntax %q in command. Pass the binary in `command` and arguments in `args`, e.g. "+
				`{"command":"go","args":["build","./..."]}. `+
				"To run in a subdirectory, use the `dir` parameter (not `cd`). "+
				"For dependent steps, issue separate tool_calls (the engine runs them in order)",
			token)
	}

	args, err := commandArgs(req.Params["args"])
	if err != nil {
		return Result{}, err
	}
	if token, value := detectShellSubstitutionArg(args); token != "" {
		return Result{}, fmt.Errorf(
			"run_command args are passed literally to the target binary; shell substitution is never expanded. "+
				"Found shell syntax %q in args element %q. If you intended a literal value, keep it exactly as text; "+
				"if you intended command substitution, resolve it in a prior tool call and pass the concrete result here",
			token, value)
	}
	if hasScriptRunnerWithEvalFlag(args) {
		return Result{}, fmt.Errorf("run_command args contain a script-runner inline-eval flag (-e, -c, -r) which is not supported — these allow arbitrary code execution and are blocked")
	}
	for _, arg := range args {
		if isBlockedShellInterpreter(arg) {
			return Result{}, fmt.Errorf("shell interpreters are blocked for run_command args as well: %s", arg)
		}
	}
	// Catch the second-most-common LLM packing mistake: `command:"go build ./..."`
	// with no `args` set. That's not shell syntax (so detectShellMetacharacter
	// passes), but the binary slot has been stuffed with bin+args. exec.LookPath
	// would just say "executable file not found" — useless feedback. Catch it
	// here and show the split shape. Skip the check when `args` is non-empty
	// (the model already split things; trust it) or when command looks like
	// a path (Windows "Program Files\foo.exe" is legitimate).
	if len(args) == 0 {
		if bin, rest, packed := detectBinaryArgsPacking(command); packed {
			return Result{}, fmt.Errorf(
				"run_command: `command` looks like binary+arguments packed together (%q). "+
					"`command` is argv[0] only — the binary name. Move the rest to `args`. Recover by calling: %s",
				command, suggestSplitRunCommand(bin, rest))
		}
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

	var gitErr string

	beforeChanged, beforeErr := gitChangedFilesSnapshot(ctx, req.ProjectRoot)
	if beforeErr != nil {
		// Non-git directory or git error — surface it so workspace_changed
		// is not misleading; we can't reliably detect changes without git.
		beforeChanged = nil
		gitErr = beforeErr.Error()
	}

	cmd := exec.CommandContext(runCtx, execPath, args...)
	cmd.Dir = workDir
	// Bounded capture: stdout + stderr each cap at runCommandOutputCap
	// so an LLM-issued `cargo build --verbose` against a giant
	// workspace, or `cat huge.log`, can't grow the parent heap to
	// gigabytes. The agent already truncates downstream tool output,
	// but that truncation runs AFTER everything is in memory — too
	// late to save us if the producer is a firehose.
	stdout := newBoundedBuffer(runCommandOutputCap)
	stderr := newBoundedBuffer(runCommandOutputCap)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err = cmd.Run()

	var afterErr error
	var afterChanged []string
	afterChanged, afterErr = gitChangedFilesSnapshot(ctx, req.ProjectRoot)
	if afterErr != nil {
		afterChanged = nil
		if gitErr != "" {
			gitErr = gitErr + "; after: " + afterErr.Error()
		} else {
			gitErr = afterErr.Error()
		}
	}

	output := strings.TrimSpace(strings.TrimSpace(stdout.String()) + joinCommandStderr(stderr.String(), stdout.Len() > 0))
	data := map[string]any{
		"command":           command,
		"resolved_command":  execPath,
		"args":              args,
		"dir":               filepath.ToSlash(relPathOrAbsolute(req.ProjectRoot, workDir)),
		"timeout_ms":        timeout.Milliseconds(),
		"workspace_changed": !slices.Equal(beforeChanged, afterChanged),
	}
	if gitErr != "" {
		data["git_error"] = gitErr
	}
	if !slices.Equal(beforeChanged, afterChanged) {
		data["changed_files"] = afterChanged
	}
	res := Result{Output: output, Data: data}

	if err == nil {
		return res, nil
	}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return res, fmt.Errorf("command timed out after %s", timeout)
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		data["exit_code"] = exitErr.ExitCode()
		res = Result{Output: output, Data: data}
		// Non-zero exit code is the command's own failure signal, not a system error.
		// The exit code is already in data["exit_code"] for callers to inspect.
		// Return Result (not error) so the engine marks this as success=true and
		// the TUI renders the full output. If the caller cares about the exit code,
		// they check data["exit_code"] != 0.
		return res, nil
	}
	return res, err
}

type runCommandConfig struct {
	allowShell bool
	timeout    time.Duration
	blocked    []string
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

func gitChangedFilesSnapshot(ctx context.Context, projectRoot string) ([]string, error) {
	stdout, _, _, err := runGit(ctx, projectRoot, 30*time.Second, "status", "--short", "--untracked-files=all", "--")
	if err != nil {
		return nil, err
	}
	lines := splitLines(strings.ReplaceAll(stdout, "\r\n", "\n"))
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
