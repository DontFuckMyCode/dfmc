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

// File layout: this file owns the dispatcher lifecycle (types, Event
// constants, New, Fire / fireOne, condition matching, Count, Inventory,
// Describe). The execution path (runOne, hookCommand / shellCommand /
// hookEnv with secret-scrubbing + injection-safe quoting + process-
// group cleanup, CheckConfigPermissions) lives in hooks_run.go.

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
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

// compiledHook pairs a config entry with its pre-parsed per-hook timeout
// override so the dispatch path stays allocation-free on the hot loop. A
// zero timeout means "no per-hook override" — runOne then resolves the
// LIVE dispatcher default (d.defaultTO) at call time. We deliberately do
// NOT snapshot d.defaultTO here: doing so froze the timeout at New() time,
// so a later change to d.defaultTO (the only timeout knob, and what tests
// set) had no effect and every hook silently ran with the 30s default.
type compiledHook struct {
	name           string
	command        string
	args           []string
	condition      string
	timeout        time.Duration
	useShell       bool
	disabledReason string
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
			disabledReason := ""
			if useShell {
				if err := validateShellHookCommand(cmd); err != nil {
					disabledReason = err.Error()
				}
			}
			d.entries[event] = append(d.entries[event], compiledHook{
				name:      strings.TrimSpace(entry.Name),
				command:   cmd,
				args:      append([]string(nil), entry.Args...),
				condition: strings.TrimSpace(entry.Condition),
				// 0 == no per-hook override; runOne and Inventory resolve the
				// live d.defaultTO. A malformed duration is treated as "no
				// override" rather than failing the load.
				timeout:        parseHookTimeout(entry.Timeout),
				useShell:       useShell,
				disabledReason: disabledReason,
			})
		}
	}
	return d
}

// parseHookTimeout parses a per-hook timeout override. An empty or
// malformed duration yields 0, meaning "no override — use the dispatcher
// default". Negative/zero durations are rejected for the same reason: a
// hook with no enforceable budget is never what the operator meant.
func parseHookTimeout(raw string) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0
	}
	return d
}

// effectiveTimeout resolves the budget actually applied to a hook: its
// per-hook override when set, otherwise the LIVE dispatcher default. This
// is the single source of truth for both enforcement (runOne) and display
// (Inventory) so a hook can never be shown one budget and run under
// another.
func (d *Dispatcher) effectiveTimeout(h compiledHook) time.Duration {
	if h.timeout > 0 {
		return h.timeout
	}
	return d.defaultTO
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
	defer func() {
		if p := recover(); p != nil {
			buf := make([]byte, 4096)
			n := runtime.Stack(buf, false)
			fmt.Fprintf(os.Stderr, "[dfmc-hook-panic] source=safeObserve panic=%v stack=%s\n", p, string(buf[:n]))
		}
	}()
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
				Timeout:   d.effectiveTimeout(h),
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
