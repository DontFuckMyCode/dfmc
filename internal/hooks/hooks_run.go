package hooks

// hooks_run.go — per-hook execution machinery: runOne, command/shell
// wrapping, env projection with secret-scrubbing + injection-safe
// quoting, and CheckConfigPermissions for the world-writable-config
// warning. Pulled out of hooks.go so the dispatcher file (types,
// New, Fire, condition matching, inventory) stays focused. Companion
// sibling: hooks.go owns Dispatcher lifecycle + Fire + Count +
// Inventory + Describe + conditionMatches.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/security"
)

// hookOutputCap is the maximum bytes captured per stream from a single
// hook. cmd.CombinedOutput's old behaviour was unbounded — a runaway
// hook (a `tail -f` mistake, a stuck binary writing infinite progress
// dots, an attacker-controlled hook writing /dev/urandom) could grow
// the buffer until DFMC OOM'd. 1 MiB per stream is generous for any
// real hook output and bounded for safety.
const hookOutputCap = 1 << 20 // 1 MiB

// runOne executes a single hook with timeout + env projection. All
// errors are captured in the Report — we never panic the caller.
//
// Output is captured into bounded buffers so a misbehaving hook can't
// drive memory growth. When a stream hits the cap we keep the head
// (the bit a human would actually read) and drop the rest with a
// truncation marker so the report stays parseable.
func (d *Dispatcher) runOne(ctx context.Context, event Event, h compiledHook, payload Payload) Report {
	to := h.timeout
	if to <= 0 {
		to = d.defaultTO
	}
	if h.disabledReason != "" {
		return Report{
			Event:    event,
			Name:     h.name,
			Command:  h.command,
			ExitCode: -1,
			Err:      fmt.Errorf("hook disabled: %s", h.disabledReason),
		}
	}
	runCtx, cancel := context.WithTimeout(ctx, to)
	defer cancel()

	if hasDangerousHookArg(h.args) {
		return Report{
			Event:    event,
			Name:     h.name,
			Command:  h.command,
			ExitCode: -1,
			Err:      fmt.Errorf("hook blocked: argv contains shell metacharacters"),
		}
	}

	cmd := hookCommand(runCtx, h)
	// Strip secret-shaped env vars (ANTHROPIC_API_KEY, GITHUB_TOKEN,
	// AWS_*, etc.) before forwarding to user-configured hook subprocess.
	// MCP did this since inception; hooks did not — a hook script that
	// runs `printenv > /tmp/log` or posts env to a webhook for debugging
	// would silently exfiltrate every provider key. ScrubEnv is allow-by-
	// default with a deny-list of secret-shaped key suffixes; users who
	// genuinely need a specific key in a hook can pass an allowlist via a
	// future `env_passthrough` config (mirrors MCP's surface).
	cmd.Env = append(security.ScrubEnv(os.Environ(), nil), hookEnv(event, payload)...)
	// Process-group isolation: when the hook spawns child processes
	// (`sleep 60 &`, `npm install &`, an orphaned background daemon),
	// exec.CommandContext's default SIGKILL on timeout only reaches the
	// shell — children survive and may run forever. Setpgid (Unix) or
	// the Windows equivalent groups everything together so the post-
	// timeout cleanup below can kill the whole tree at once. Falls
	// back to default behaviour on platforms we don't special-case
	// (current support: Linux/Darwin/Windows).
	applyProcessGroupIsolation(cmd)
	// exec.CommandContext's default cancel only SIGKILLs the shell. A child
	// the hook spawned survives — and crucially still holds the inherited
	// stdout/stderr pipe write-ends, so cmd.Wait() blocks until that orphan
	// exits on its own (the post-Run group kill below then fires far too
	// late). Cancel the whole process group at timeout instead, so the
	// orphan dies immediately and Wait returns promptly. WaitDelay is a
	// backstop that force-closes the pipes if anything still holds them.
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			killProcessGroup(cmd.Process.Pid)
		}
		return nil
	}
	cmd.WaitDelay = 2 * time.Second

	stdoutBuf := newBoundedBuffer(hookOutputCap)
	stderrBuf := newBoundedBuffer(hookOutputCap)
	cmd.Stdout = stdoutBuf
	cmd.Stderr = stderrBuf

	start := time.Now()
	err := cmd.Run()
	dur := time.Since(start)
	// On context-cancel timeout, kill the whole process group so any
	// child the hook spawned doesn't outlive the parent. No-op when
	// the hook completed cleanly.
	if runCtx.Err() != nil && cmd.Process != nil {
		killProcessGroup(cmd.Process.Pid)
	}

	report := Report{
		Event:    event,
		Name:     h.name,
		Command:  h.command,
		Duration: dur,
		Stdout:   stdoutBuf.String(),
		Stderr:   stderrBuf.String(),
	}
	if err != nil {
		report.Err = err
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			report.ExitCode = exitErr.ExitCode()
		} else {
			report.ExitCode = -1
		}
	}
	return report
}

// hookCommand preserves the historical shell-wrapped command mode while also
// supporting shell-free argv hooks (`command` + `args`) for payload-safe
// dispatches that avoid shell metacharacter expansion entirely.
// hasDangerousHookArg returns true when any element in h.args is a
// shell metacharacter that would give an argv hook shell-like
// interpretation of user-supplied input.
func hasDangerousHookArg(args []string) bool {
	for _, arg := range args {
		if arg == "||" || arg == "&&" || arg == "|" || arg == ";" ||
			strings.HasPrefix(arg, "$()") || strings.HasPrefix(arg, "${") ||
			strings.HasPrefix(arg, "`") || strings.HasPrefix(arg, "<(") || strings.HasPrefix(arg, ">(") ||
			// Single-var expansion (e.g. $USER) is dangerous in hook args
			// since it lets an attacker inject content via env vars.
			// $() and ${} are already caught above.
			(strings.HasPrefix(arg, "$") && len(arg) > 1 && arg[1] != '(') {
			return true
		}
	}
	return false
}

func hookCommand(ctx context.Context, h compiledHook) *exec.Cmd {
	if !h.useShell {
		return exec.CommandContext(ctx, h.command, h.args...)
	}
	return shellCommand(ctx, h.command)
}

// shellCommand wraps the user's command string in the platform's default
// shell so users can write shell idioms (pipes, &&, env expansion)
// directly in their config.
func shellCommand(ctx context.Context, command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		// cmd.exe is the lowest-common-denominator on Windows; users with
		// PowerShell or Git Bash can still call them explicitly via
		// `powershell -c "..."` or `bash -c "..."` inside the command.
		return exec.CommandContext(ctx, "cmd.exe", "/C", command)
	}
	return exec.CommandContext(ctx, "sh", "-c", command)
}

func validateShellHookCommand(command string) error {
	if len(command) > 8192 {
		return fmt.Errorf("shell hook command too long (%d bytes)", len(command))
	}
	if strings.ContainsRune(command, '\x00') {
		return fmt.Errorf("shell hook command contains NUL byte")
	}
	if strings.ContainsAny(command, "\r\n") {
		return fmt.Errorf("shell hook command must be a single line; use command+args or a script file")
	}
	return nil
}

// hookEnv projects the Payload into DFMC_<KEY>=<value> env vars, always
// including DFMC_EVENT. Keys are uppercased and sanitized (alphanumerics only);
// values are quoted to prevent shell injection. This keeps arbitrary payload
// keys and values from shell-injecting via env names or values.
func hookEnv(event Event, payload Payload) []string {
	env := []string{"DFMC_EVENT=" + string(event)}
	for k, v := range payload {
		key := sanitizeEnvKey(k)
		if key == "" {
			continue
		}
		env = append(env, "DFMC_"+key+"="+sanitizeEnvValue(v))
	}
	return env
}

// sanitizeEnvKey replaces any non-alphanumeric with underscores and
// uppercases the rest. An empty result means we reject the key.
func sanitizeEnvKey(raw string) string {
	var b strings.Builder
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteByte(byte(r - 32))
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// sanitizeEnvValue quotes the value so that shell expansion cannot break out
// of the env-var assignment. Unix uses single-quote wrapping with embedded
// quote escaping (' -> '\”); Windows cmd.exe uses double-quote wrapping with
// % doubling (%%) to block %VAR% expansion inside quoted strings, and ^
// escaping for other specials. This prevents payload injection when a hook
// command references $DFMC_<KEY> in a shell context.
func sanitizeEnvValue(raw string) string {
	if raw == "" {
		return "''"
	}
	if runtime.GOOS == "windows" {
		// cmd.exe expands %VAR% inside double quotes, so escape % as %%.
		// Also escape " and \ to prevent quote-breakout and path interpretation.
		var b strings.Builder
		b.Grow(len(raw) * 2)
		for _, r := range raw {
			switch r {
			case '%':
				b.WriteString("%%")
			case '"':
				b.WriteString("^\"")
			case '\\':
				b.WriteString("^\\")
			case '!':
				b.WriteString("^!")
			case '^':
				b.WriteString("^^")
			default:
				b.WriteRune(r)
			}
		}
		return "\"" + b.String() + "\""
	}
	// Unix: single quotes prevent all $ expansion. Escape embedded single
	// quotes as '\'' (close, escaped ', reopen).
	var b strings.Builder
	b.Grow(len(raw) + 4)
	b.WriteByte('\'')
	for _, r := range raw {
		if r == '\'' {
			b.WriteString("'\\''")
		} else {
			b.WriteRune(r)
		}
	}
	b.WriteByte('\'')
	return b.String()
}

// CheckConfigPermissions warns if the DFMC config file is group or
// world-writable, which would allow an attacker who can write to the
// config to achieve arbitrary code execution via hook commands.
func CheckConfigPermissions(configPath string) string {
	// Windows doesn't have POSIX permission bits; Go's os.Stat synthesizes
	// 0666 for any read-write file, which would make this check fire on
	// every Windows install. Skip — file ACLs there are governed by the
	// NTFS DACL, not the simulated mode bits.
	if runtime.GOOS == "windows" {
		return ""
	}
	info, err := os.Stat(configPath)
	if err != nil {
		return ""
	}
	mode := info.Mode().Perm()
	if mode&0020 != 0 || mode&0002 != 0 {
		return fmt.Sprintf("warning: %s is group/world-writable (mode %03o); "+
			"hook commands run with full shell interpretation and should be treated as trusted", configPath, mode)
	}
	return ""
}
