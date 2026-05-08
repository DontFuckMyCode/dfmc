// Verbose event stream for `dfmc ask -v` and `dfmc chat -v`. Subscribes
// to a curated allowlist of high-signal engine events and prints
// compact one-line summaries to stderr while the CLI command runs.
// The allowlist mirrors the failure/visibility events the TUI / web
// surface so a user running the CLI gets the same signal coverage —
// without flooding stderr with per-tool tool:call/result chatter that
// would drown out the answer.

package cli

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

// streamVerboseEvents subscribes a one-line stderr printer to the
// engine's bus, filtered to the verbose allowlist. Returns a stop
// function the caller defers to drain and unsubscribe before exit.
//
// Event payloads are typically map[string]any from EventBus.Publish;
// we tolerate other shapes (string for context:error) without crashing.
func streamVerboseEvents(eng *engine.Engine) func() {
	if eng == nil || eng.EventBus == nil {
		return func() {}
	}
	ch := eng.EventBus.Subscribe("*")
	var wg sync.WaitGroup
	done := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			case ev, ok := <-ch:
				if !ok {
					return
				}
				line := formatVerboseEvent(ev)
				if line != "" {
					fmt.Fprintln(os.Stderr, line)
				}
			}
		}
	}()
	return func() {
		close(done)
		eng.EventBus.Unsubscribe("*", ch)
		wg.Wait()
	}
}

// verboseAllowlist is the set of event types the -v stream surfaces.
// Tool calls and provider heartbeats are NOT included — they're too
// frequent and would obscure the answer. Failure / state-change /
// guard events ARE included so a verbose run doubles as a debug aid.
var verboseAllowlist = map[string]struct{}{
	// Memory + history visibility
	"history:trimmed":           {},
	"intent:decision":           {},
	"context:error":             {},
	"context:lifecycle:handoff": {},
	// Reliability
	"conversation:save:error":     {},
	"engine:shutdown_error":       {},
	"runtime:panic":               {},
	"tool:panicked":               {},
	"security:config_permissions": {},
	"memory:degraded":             {},
	// Approval gate + tool execution faults
	"tool:denied":  {},
	"tool:timeout": {}, // structural: per-tool deadline gate fired
	// Hooks (only failures, filtered below)
	"hook:run": {},
	// Loop guards
	"agent:loop:budget_exhausted":    {},
	"agent:loop:auto_resume":         {},
	"agent:loop:auto_resume_refused": {},
	"agent:loop:tools_force_stop":    {},
	"agent:loop:stuck_force_stop":    {},
	"agent:loop:interrupted":         {},
	"agent:loop:shutdown_parked":     {},
	"agent:loop:resume":              {},
	"agent:loop:resume_refused":      {},
	"agent:loop:safety_bound":        {},
	"agent:loop:empty_recovery":      {},
	"agent:loop:empty_final":         {},
	"agent:loop:max_steps":           {},
	"agent:loop:error":               {},
	"agent:loop:parked":              {},
	// Coach
	"agent:coach:stuck":      {},
	"agent:coach:unverified": {},
	// Provider issues (failures only)
	"provider:throttle:retry": {},
	"provider:race:failed":    {},
	"provider:fallback":       {},
}

func formatVerboseEvent(ev engine.Event) string {
	if _, ok := verboseAllowlist[ev.Type]; !ok {
		return ""
	}
	payload, _ := ev.Payload.(map[string]any)

	// hook:run is on the allowlist but only failures are interesting
	// — successful hook runs would flood stderr.
	if ev.Type == "hook:run" {
		exit, _ := payload["exit_code"].(int)
		errStr := strings.TrimSpace(verboseStringField(payload, "err"))
		if exit == 0 && errStr == "" {
			return ""
		}
		return fmt.Sprintf("[%s] %s/%s exit=%d %s",
			ev.Type,
			verboseStringField(payload, "name"),
			verboseStringField(payload, "event"),
			exit,
			errStr,
		)
	}

	// context:error has a plain-string payload, not a map.
	if ev.Type == "context:error" {
		if s, ok := ev.Payload.(string); ok {
			return fmt.Sprintf("[%s] %s", ev.Type, strings.TrimSpace(s))
		}
	}

	// Default formatter: compact key fields when present.
	parts := []string{fmt.Sprintf("[%s]", ev.Type)}
	for _, key := range []string{"reason", "name", "tool", "stage", "error", "tokens_used", "step", "tool_rounds"} {
		if val := verboseStringField(payload, key); val != "" {
			parts = append(parts, key+"="+val)
		}
	}
	return strings.Join(parts, " ")
}

func verboseStringField(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	v, ok := payload[key]
	if !ok || v == nil {
		return ""
	}
	switch vv := v.(type) {
	case string:
		return strings.TrimSpace(vv)
	case int:
		return fmt.Sprintf("%d", vv)
	case int64:
		return fmt.Sprintf("%d", vv)
	case float64:
		return fmt.Sprintf("%v", vv)
	case bool:
		return fmt.Sprintf("%t", vv)
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}
