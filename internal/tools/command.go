package tools

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
					"`dir` sets the working directory (no `cd` needed); for sequential steps issue separate tool_calls.",
				token, hint)
		}
		return Result{}, fmt.Errorf(
			"run_command does not invoke a shell — `command` must be a single binary, not a shell line. "+
				"Found shell syntax %q in command. Pass the binary in `command` and arguments in `args`, e.g. "+
				`{"command":"go","args":["build","./..."]}. `+
				"To run in a subdirectory, use the `dir` parameter (not `cd`). "+
				"For dependent steps, issue separate tool_calls (the engine runs them in order).",
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
				"if you intended command substitution, resolve it in a prior tool call and pass the concrete result here.",
			token, value)
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

	beforeChanged, err := gitChangedFilesSnapshot(req.ProjectRoot)
	if err != nil {
		beforeChanged = nil
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

// ensureCommandAllowed gates execution against the default block list
// plus any user-configured patterns. The checks are ordered from
// cheapest/most-specific to most-permissive:
//
//  1. Binary-name block: strips path + .exe and matches against a
//     fixed list of destructive or privilege-escalating binaries. This
//     catches rm, mkfs, sudo, shutdown, etc. regardless of how they
//     were invoked.
//  2. Structured arg-sequence block: for binaries that ARE legitimate
//     (git, dd) but have specific flag combinations that are
//     destructive. Token-based, so `echo "git reset --hard"` does not
//     false-positive.
//  3. User-configured patterns: kept as substring matches over the
//     joined command+args for back-compat with .dfmc/config.yaml
//     entries. Users opt into this shape knowing it matches substrings.
//
// Substring matching over the *defaults* was the old behaviour and led
// to false positives like blocking `go build -o format ./...` (pattern
// "format " matches inside the args) and `echo "mkfs is cool"`
// (pattern "mkfs" matches the echoed string). The token-based checks
// below avoid that class of bug entirely.
func ensureCommandAllowed(command string, args []string, userBlocked []string) error {
	binary := canonicalCommandBinary(command)
	if isBlockedBinary(binary) {
		return fmt.Errorf("command blocked by policy: %s", command)
	}
	if err := checkBlockedArgSequences(binary, args); err != nil {
		return err
	}
	if len(userBlocked) > 0 {
		full := strings.ToLower(strings.TrimSpace(strings.Join(append([]string{command}, args...), " ")))
		for _, item := range userBlocked {
			pattern := strings.ToLower(strings.TrimSpace(item))
			if pattern == "" {
				continue
			}
			if strings.Contains(full, pattern) {
				return fmt.Errorf("command blocked by policy: %s", item)
			}
		}
	}
	return nil
}

// canonicalCommandBinary extracts a lower-case, .exe-stripped binary
// name from a command string. Doing this once up front keeps the
// block checks simple and platform-symmetric (rm.exe and rm both look
// like "rm").
func canonicalCommandBinary(command string) string {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		// filepath.Base("") returns "." which is not what we want —
		// the caller almost certainly has an upstream empty-command
		// guard, but keep this defensive.
		return ""
	}
	binary := strings.ToLower(filepath.Base(trimmed))
	return strings.TrimSuffix(binary, ".exe")
}

// isBlockedBinary reports whether a canonicalised binary name is on
// the "never run this directly" list. Grouped by rationale so future
// maintainers can reason about whether to add entries.
func isBlockedBinary(binary string) bool {
	switch binary {
	// Destructive filesystem / disk operations.
	case "rm", "del", "rmdir", "format", "mkfs", "diskpart", "dd":
		return true
	// Privilege escalation — running these lifts the agent out of the
	// user's normal permissions, which defeats the purpose of a
	// sandboxed tool.
	case "sudo", "doas", "su", "runas", "pkexec":
		return true
	// System-level control. Even a transient invocation like `shutdown
	// -r now` can kill an unsaved session.
	case "shutdown", "reboot", "halt", "poweroff", "init", "telinit":
		return true
	// Broad process termination. `killall sshd` is the shape we want
	// to refuse; narrow-scope `kill PID` is allowed because it's the
	// normal way to signal a specific process.
	case "killall", "pkill":
		return true
	}
	return false
}

// checkBlockedArgSequences catches destructive invocations of
// legitimate binaries. Token-based to avoid the substring false
// positives of the old pattern-list approach.
func checkBlockedArgSequences(binary string, args []string) error {
	switch binary {
	case "git":
		// git reset --hard, git clean -fd/-fdx, git checkout --,
		// git restore --source, git push --force / --force-with-lease.
		if hasArgSequence(args, "reset", "--hard") ||
			hasArgSequence(args, "clean", "-fd") ||
			hasArgSequence(args, "clean", "-fdx") ||
			hasArgSequence(args, "clean", "-fx") ||
			hasArgSequence(args, "checkout", "--") ||
			hasArgSequence(args, "restore", "--source") ||
			hasArgSequence(args, "push", "--force") ||
			hasArgSequence(args, "push", "-f") ||
			hasArgSequence(args, "push", "--force-with-lease") {
			return fmt.Errorf("command blocked by policy: git %s", strings.Join(args, " "))
		}
	}
	return nil
}

func hasArgSequence(args []string, seq ...string) bool {
	if len(seq) == 0 || len(args) < len(seq) {
		return false
	}
	for i := range len(args) - len(seq) + 1 {
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

// detectShellMetacharacter scans `command` for syntax that only a shell
// interpreter understands. We don't run a shell, so finding any of these
// inside the binary slot is a sign the model packed a whole shell line
// into `command` (e.g. `cd /repo && go build ./...`). Returns the first
// offending token for use in the error message; empty string means the
// command looks like a plain binary invocation.
//
// We deliberately scan only `command`, not `args` — putting `>` or `&&`
// in args is fine because the binary just sees them as positional
// arguments. The footgun is exclusively when shell syntax shows up in
// the slot that becomes argv[0].
func detectShellMetacharacter(command string) string {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return ""
	}
	// Multi-char operators first so e.g. `&&` doesn't get reported as `&`.
	for _, op := range []string{"&&", "||", ">>", "2>&1", "2>", "<<"} {
		if strings.Contains(cmd, op) {
			return op
		}
	}
	// Single-char shell operators. `|` last so we don't false-positive on
	// the rare absolute path containing `|` (Windows reserves it).
	for _, op := range []string{";", "|", ">", "<", "`", "$("} {
		if strings.Contains(cmd, op) {
			return op
		}
	}
	if hasStandaloneShellAmpersand(cmd) {
		return "&"
	}
	// `cd ` at the start is the other classic LLM tell — the model is
	// trying to chdir-then-run inside one command. Treat it as shell-y.
	if strings.HasPrefix(strings.ToLower(cmd), "cd ") {
		return "cd "
	}
	return ""
}

func hasStandaloneShellAmpersand(cmd string) bool {
	for i, r := range cmd {
		if r != '&' {
			continue
		}
		prevWS := i == 0 || isShellWhitespace(rune(cmd[i-1]))
		nextWS := i == len(cmd)-1 || isShellWhitespace(rune(cmd[i+1]))
		if prevWS || nextWS {
			return true
		}
	}
	return false
}

func detectShellSubstitutionArg(args []string) (string, string) {
	for _, arg := range args {
		switch {
		case strings.Contains(arg, "$("):
			return "$(", arg
		case strings.Contains(arg, "`"):
			return "`", arg
		}
	}
	return "", ""
}

func isShellWhitespace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r'
}

// suggestRunCommandRecovery turns a shell-line command that the model
// fed into `command` into a copy-pasteable recovery tool_call shape.
// We focus on the single most common case caught in real sessions:
// `cd <dir> && <real command>` (and the `;` variant). When that
// pattern matches we extract the directory and the trailing command
// so the model sees exactly which tokens go into `command`, `args`,
// and `dir`. For anything else we return "" and the caller emits the
// generic example. Keep this conservative — a wrong-looking suggestion
// is worse than the generic one.
func suggestRunCommandRecovery(command string) string {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return ""
	}
	if !strings.HasPrefix(strings.ToLower(cmd), "cd ") {
		return ""
	}
	// Find the first `&&` or `;` separator after `cd <dir>`.
	rest := cmd[3:]
	sepIdx, sepLen := -1, 0
	if i := strings.Index(rest, "&&"); i >= 0 {
		sepIdx, sepLen = i, 2
	}
	if i := strings.Index(rest, ";"); i >= 0 && (sepIdx == -1 || i < sepIdx) {
		sepIdx, sepLen = i, 1
	}
	if sepIdx < 0 {
		return ""
	}
	dir := strings.TrimSpace(rest[:sepIdx])
	tail := strings.TrimSpace(rest[sepIdx+sepLen:])
	if dir == "" || tail == "" {
		return ""
	}
	// Strip surrounding quotes from dir, normalize separators.
	dir = strings.Trim(dir, `"'`)
	dir = filepath.ToSlash(dir)
	// Split tail into binary + args using whitespace; keep it simple
	// (no quote-aware tokenization) since the goal is a hint, not an
	// exec.
	parts := strings.Fields(tail)
	if len(parts) == 0 {
		return ""
	}
	bin := parts[0]
	rawArgs := parts[1:]
	// JSON-encode args array with %q-style quoting on each element.
	argLits := make([]string, 0, len(rawArgs))
	for _, a := range rawArgs {
		argLits = append(argLits, fmt.Sprintf("%q", a))
	}
	return fmt.Sprintf(
		`{"name":"run_command","args":{"command":%q,"args":[%s],"dir":%q}}`,
		bin, strings.Join(argLits, ","), dir,
	)
}

// detectBinaryArgsPacking flags the `command:"go build ./..."` shape:
// no shell syntax (so detectShellMetacharacter passed), but the binary
// slot has whitespace-separated tokens — almost certainly bin+args
// packed together. Returns (bin, rest, true) when the pattern matches,
// otherwise zero+false. Conservative on purpose: we skip anything that
// looks like a real path (Windows "Program Files\foo.exe", Unix
// "/usr/local/bin/my prog", etc.) so legitimate quoted paths with
// spaces aren't false-positived.
func detectBinaryArgsPacking(command string) (string, string, bool) {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return "", "", false
	}
	// Quoted command (`"foo bar.exe" --flag`) — leave it alone; the model
	// is being explicit about the path.
	if strings.HasPrefix(cmd, `"`) || strings.HasPrefix(cmd, `'`) {
		return "", "", false
	}
	idx := strings.IndexAny(cmd, " \t")
	if idx <= 0 {
		return "", "", false
	}
	bin := cmd[:idx]
	rest := strings.TrimSpace(cmd[idx+1:])
	if rest == "" {
		return "", "", false
	}
	// If the binary token itself has a path separator, treat the whole
	// thing as a path with embedded spaces (rare but legitimate). Same
	// for ".exe" without separators — could still be a packed call, so
	// we let the path-separator check be the discriminator.
	if strings.ContainsAny(bin, "/\\") {
		return "", "", false
	}
	return bin, rest, true
}

// suggestSplitRunCommand renders the recovery shape for the binary+args
// packing case: split the offender on whitespace and JSON-encode each
// token into the `args` array. Same conservative tokenization as
// suggestRunCommandRecovery — if the model packed clever quoting
// nonsense, the hint will at least show the right *shape* even if the
// exact tokens need adjustment.
func suggestSplitRunCommand(bin, rest string) string {
	parts := strings.Fields(rest)
	argLits := make([]string, 0, len(parts))
	for _, p := range parts {
		argLits = append(argLits, fmt.Sprintf("%q", p))
	}
	return fmt.Sprintf(
		`{"name":"run_command","args":{"command":%q,"args":[%s]}}`,
		bin, strings.Join(argLits, ","),
	)
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
