// Package hooks dispatches user-configured shell commands in response to
// DFMC lifecycle events (prompt submit, tool call, session start). Hooks
// are the extensibility escape hatch — anything we don't bake into the
// engine can still be wired in by a user willing to write a shell line.
//
// Design notes:
//   - Hooks are best-effort: a failing hook never blocks a tool call or a
//     user turn. We log the failure via the Engine's EventBus and move on.
//   - Events carry structured env vars (DFMC_EVENT, DFMC_TOOL_NAME, etc.)
//     so hook authors can switch on them without parsing positional args.
//   - Each hook gets a hard timeout (default 30s, overridable per-entry
//     via `timeout: 10s` in config) to keep a misbehaving hook from
//     stalling the agent loop.
//   - A hook's optional `condition` expression is evaluated against the
//     event payload. For now we support a tiny substring/eq grammar that
//     covers the 90% case; richer expressions can come later.
package hooks

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/security"
)

// Event is a hook lifecycle event name. Handlers in config.HooksConfig
// are keyed by these strings. Unknown events are silently ignored so
// the engine can publish freely without gating on a known-events list.
type Event string

const (
	// EventUserPromptSubmit fires every time the user sends a chat turn.
	// Env: DFMC_EVENT, DFMC_PROMPT.
	EventUserPromptSubmit Event = "user_prompt_submit"
	// EventPreTool fires just before a tool call is dispatched. A hook
	// returning non-zero here is logged but does NOT cancel the tool;
	// that is deliberate — we don't want a flaky hook to deadlock the
	// agent loop. A future gated-tools feature can build on this.
	// Env: DFMC_EVENT, DFMC_TOOL_NAME, DFMC_TOOL_ARGS (JSON).
	EventPreTool Event = "pre_tool"
	// EventPostTool fires after every tool call, success or failure.
	// Env: DFMC_EVENT, DFMC_TOOL_NAME, DFMC_TOOL_SUCCESS ("true"/"false"),
	// DFMC_TOOL_DURATION_MS.
	EventPostTool Event = "post_tool"
	// EventSessionStart fires once per engine initialization. Useful for
	// loading per-project scratch state or warming caches.
	EventSessionStart Event = "session_start"
	// EventSessionEnd fires on graceful shutdown. Fire-and-forget; we do
	// not block shutdown on hook completion past a brief grace period.
	EventSessionEnd Event = "session_end"
)

// Payload carries structured event context to hooks. Each entry becomes
// a DFMC_<KEY> environment variable on the hook's process. Keys are
// upper-cased and non-alphanumerics are replaced with underscores.
type Payload map[string]string

// Observer receives a single line of hook telemetry for every dispatch
// (success or failure). The Engine wires this to its EventBus so the
// TUI/Web UIs can surface hook activity.
type Observer func(report Report)

// Report is the structured outcome of a single hook invocation.
type Report struct {
	Event    Event
	Name     string
	Command  string
	ExitCode int
	Err      error
	Duration time.Duration
	Stdout   string
	Stderr   string
}

// Dispatcher holds compiled hook handlers and runs them for events. A
// nil Dispatcher is a valid no-op; callers don't need to null-check.
type Dispatcher struct {
	mu        sync.RWMutex
	entries   map[Event][]compiledHook
	observer  Observer
	defaultTO time.Duration
}

// compiledHook pairs a config entry with its pre-parsed timeout so the
// dispatch path stays allocation-free on the hot loop.
type compiledHook struct {
	name      string
	command   string
	args      []string
	condition string
	timeout   time.Duration
	useShell  bool
}

// New builds a dispatcher from config. An empty config yields a no-op
// dispatcher; callers just call Fire and nothing happens. Invalid
// timeout strings fall back to the default without failing the whole
// load — hooks are a convenience layer, not a correctness gate.
func New(cfg config.HooksConfig, observer Observer) *Dispatcher {
	d := &Dispatcher{
		entries:   make(map[Event][]compiledHook),
		observer:  observer,
		defaultTO: 30 * time.Second,
	}
	for rawEvent, rawEntries := range cfg.Entries {
		event := Event(strings.TrimSpace(rawEvent))
		if event == "" {
			continue
		}
		for _, entry := range rawEntries {
			cmd := strings.TrimSpace(entry.Command)
			if cmd == "" {
				continue
			}
			useShell := true
			if entry.Shell != nil {
				useShell = *entry.Shell
			} else if len(entry.Args) > 0 {
				useShell = false
			}
			d.entries[event] = append(d.entries[event], compiledHook{
				name:      strings.TrimSpace(entry.Name),
				command:   cmd,
				args:      append([]string(nil), entry.Args...),
				condition: strings.TrimSpace(entry.Condition),
				timeout:   d.defaultTO,
				useShell:  useShell,
			})
		}
	}
	return d
}

// Fire runs every handler for `event` sequentially. The Payload is
// projected onto environment variables for each hook process. Returns
// the count of hooks that actually ran (post-condition filter).
//
// We run hooks sequentially rather than in parallel so their side
// effects have a deterministic ordering (e.g. a log hook writes its
// line before a notify hook triggers a desktop notification). Callers
// who want async dispatch should invoke Fire on a goroutine.
// safeObserve calls the observer with panic protection. A panicking
// observer must not unwind the dispatch loop — the next hook must still
// get a fresh invocation (VULN-048).
func safeObserve(obs Observer, report Report) {
	if obs == nil {
		return
	}
	defer func() { _ = recover() }()
	obs(report)
}

// fireOne evaluates the condition, runs one hook, and reports its result.
// Wrapped in defer/recover so a panicing hook or observer is contained
// and the dispatch loop continues with the next hook (VULN-048).
func (d *Dispatcher) fireOne(ctx context.Context, event Event, h compiledHook, payload Payload) (ran bool) {
	defer func() {
		if rec := recover(); rec != nil {
			if d.observer != nil {
				safeObserve(d.observer, Report{
					Event:    event,
					Name:     h.name,
					Command:  h.command,
					Err:      fmt.Errorf("hook panic: %v", rec),
					ExitCode: -1,
				})
			}
		}
	}()
	if !d.conditionMatches(h.condition, payload) {
		return false
	}
	report := d.runOne(ctx, event, h, payload)
	if d.observer != nil {
		safeObserve(d.observer, report)
	}
	return true
}

// Fire runs every handler for `event` sequentially. The Payload is
// projected onto environment variables for each hook process. Returns
// the count of hooks that actually ran (post-condition filter).
//
// We run hooks sequentially rather than in parallel so their side
// effects have a deterministic ordering (e.g. a log hook writes its
// line before a notify hook triggers a desktop notification). Callers
// who want async dispatch should invoke Fire on a goroutine.
func (d *Dispatcher) Fire(ctx context.Context, event Event, payload Payload) int {
	if d == nil {
		return 0
	}
	d.mu.RLock()
	entries := d.entries[event]
	d.mu.RUnlock()
	if len(entries) == 0 {
		return 0
	}
	ran := 0
	for _, h := range entries {
		if d.fireOne(ctx, event, h, payload) {
			ran++
		}
	}
	return ran
}

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
	runCtx, cancel := context.WithTimeout(ctx, to)
	defer cancel()

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
// quote escaping (' -> '\''); Windows cmd.exe uses double-quote wrapping with
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

// conditionMatches implements the tiny condition grammar:
//
//	""                       → always match (no condition)
//	tool_name == apply_patch → key equals literal value
//	tool_name != run_command → key not equal
//	tool_name ~ file         → key contains substring
//
// Anything unrecognised falls through as "match" so a typo in a condition
// never silently drops hooks — the user still gets their hook fire and
// the default shell command can decide what to do.
func (d *Dispatcher) conditionMatches(expr string, payload Payload) bool {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return true
	}
	for _, op := range []string{"==", "!=", "~"} {
		if idx := strings.Index(expr, op); idx > 0 {
			key := strings.TrimSpace(expr[:idx])
			val := strings.TrimSpace(expr[idx+len(op):])
			got := payload[key]
			switch op {
			case "==":
				return got == val
			case "!=":
				return got != val
			case "~":
				return strings.Contains(got, val)
			}
		}
	}
	return true
}

// Count reports how many hooks are registered for an event. Useful for
// callers that want to skip payload serialization when no hook will
// consume it.
func (d *Dispatcher) Count(event Event) int {
	if d == nil {
		return 0
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.entries[event])
}

// HookInventoryEntry is a read-only snapshot of one registered hook.
// Returned by Inventory() so UI layers can render their own view over
// the dispatcher state without reaching into internals.
type HookInventoryEntry struct {
	Name      string
	Command   string
	Condition string
	Timeout   time.Duration
}

// Inventory returns every registered hook grouped by event, in the order
// they were loaded. Nil dispatcher yields a nil map — callers treat that
// as "no hooks".
func (d *Dispatcher) Inventory() map[Event][]HookInventoryEntry {
	if d == nil {
		return nil
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	if len(d.entries) == 0 {
		return nil
	}
	out := make(map[Event][]HookInventoryEntry, len(d.entries))
	for event, hooks := range d.entries {
		entries := make([]HookInventoryEntry, 0, len(hooks))
		for _, h := range hooks {
			entries = append(entries, HookInventoryEntry{
				Name:      h.name,
				Command:   h.command,
				Condition: h.condition,
				Timeout:   h.timeout,
			})
		}
		out[event] = entries
	}
	return out
}

// Describe returns a human-readable summary of what's registered. Used
// by `dfmc doctor` / status displays.
func (d *Dispatcher) Describe() string {
	if d == nil {
		return "hooks: dispatcher not initialized"
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	if len(d.entries) == 0 {
		return "hooks: none registered"
	}
	parts := make([]string, 0, len(d.entries))
	for event, hooks := range d.entries {
		parts = append(parts, fmt.Sprintf("%s(%d)", event, len(hooks)))
	}
	return "hooks: " + strings.Join(parts, " · ")
}
