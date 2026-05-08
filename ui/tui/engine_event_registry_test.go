package tui

import (
	"sort"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

// expectedEngineEventTypes pins the complete catalog the TUI knows
// how to render. If a new event type is introduced on the engine bus
// and the TUI should react to it, add a handler to the appropriate
// engine_events_*.go and add the type here. If an event is genuinely
// "do nothing" in the TUI, that's fine — leave it out and dispatch
// will silently drop it.
//
// The list is the contract; the test below asserts every entry has a
// registered handler and the registry has no extra phantom entries.
var expectedEngineEventTypes = []string{
	// agent loop lifecycle
	"agent:loop:start", "agent:loop:thinking", "agent:loop:final",
	"agent:loop:max_steps", "agent:loop:error", "agent:loop:parked",
	"agent:loop:budget_exhausted", "agent:loop:auto_resume",
	"agent:loop:auto_resume_refused", "agent:loop:auto_recover",
	"agent:loop:tools_force_stop", "agent:loop:interrupted",
	"agent:loop:shutdown_parked", "agent:loop:resume",
	"agent:loop:resume_refused", "agent:loop:safety_bound",
	"agent:loop:empty_recovery", "agent:loop:empty_final",
	"agent:loop:stuck_force_stop",
	// tool dispatch
	"tool:call", "tool:result", "tool:error",
	"tool:reasoning", "tool:timeout", "tool:denied",
	// subagent
	"agent:subagent:start", "agent:subagent:fallback", "agent:subagent:done",
	// drive
	"drive:run:start", "drive:plan:done", "drive:plan:failed",
	"drive:todo:start", "drive:todo:done", "drive:todo:blocked",
	"drive:todo:skipped", "drive:todo:retry",
	"drive:run:warning", "drive:run:done", "drive:run:stopped", "drive:run:failed",
	// provider
	"provider:complete", "provider:stream:start", "provider:throttle:retry",
	"provider:circuit:open", "provider:circuit:closed",
	"provider:stream:recovered", "provider:race:complete",
	"provider:race:failed", "provider:fallback",
	// context
	"context:built", "context:error",
	"context:lifecycle:compacted", "context:lifecycle:proactive_compacted",
	"context:lifecycle:handoff", "context:cleanup", "history:trimmed",
	// coach / intent / autonomy / assistant
	"coach:note", "intent:decision",
	"agent:coach:hint", "agent:coach:stuck", "agent:coach:unverified",
	"agent:autonomy:plan", "agent:autonomy:kickoff",
	"agent:tool:cache_hit",
	"assistant:next_actions", "assistant:auto_continue",
	// system / health
	"config:reload:auto", "config:reload:auto_failed",
	"engine:shutdown_error", "runtime:panic", "tool:panicked",
	"security:config_permissions", "memory:degraded",
	"hook:run", "conversation:save:error", "index:error",
	"agent:note:queued",
}

// TestEngineEventRegistryCoverage pins the dispatch table against the
// expected catalog. A failure here means either:
//   - a new engine event was added but no TUI handler exists yet
//     (add one in the right engine_events_*.go), OR
//   - a handler was removed without updating expectedEngineEventTypes.
func TestEngineEventRegistryCoverage(t *testing.T) {
	r := newEngineEventRegistry()
	got := r.types()
	sort.Strings(got)
	want := append([]string(nil), expectedEngineEventTypes...)
	sort.Strings(want)

	missing := setDiff(want, got)
	extra := setDiff(got, want)
	if len(missing) > 0 {
		t.Errorf("registry is missing handlers for: %v", missing)
	}
	if len(extra) > 0 {
		t.Errorf("registry has unexpected handlers for: %v", extra)
	}
}

// TestEngineEventRegistryDispatchHit confirms a registered handler
// runs and its return values flow through dispatch.
func TestEngineEventRegistryDispatchHit(t *testing.T) {
	r := &engineEventRegistry{handlers: map[string]engineEventHandler{}}
	called := 0
	r.register("test:hit", func(m Model, et string, ev engine.Event, p map[string]any) (Model, string) {
		called++
		return m, "ok"
	})
	_, line, ok := r.dispatch(Model{}, engine.Event{Type: "test:hit"}, nil)
	if !ok {
		t.Fatalf("dispatch reported ok=false for registered type")
	}
	if line != "ok" {
		t.Fatalf("expected line=ok, got %q", line)
	}
	if called != 1 {
		t.Fatalf("expected handler called once, got %d", called)
	}
}

// TestEngineEventRegistryDispatchMiss confirms an unknown type is
// silently dropped and never blocks the caller.
func TestEngineEventRegistryDispatchMiss(t *testing.T) {
	r := &engineEventRegistry{handlers: map[string]engineEventHandler{}}
	_, line, ok := r.dispatch(Model{}, engine.Event{Type: "nope:never"}, nil)
	if ok {
		t.Fatalf("dispatch reported ok=true for unknown type")
	}
	if line != "" {
		t.Fatalf("expected empty line on miss, got %q", line)
	}
}

// TestEngineEventRegistryDispatchEmptyType handles the defensive
// empty-string branch that the production handleEngineEvent also
// short-circuits on.
func TestEngineEventRegistryDispatchEmptyType(t *testing.T) {
	r := newEngineEventRegistry()
	_, _, ok := r.dispatch(Model{}, engine.Event{Type: ""}, nil)
	if ok {
		t.Fatalf("dispatch should refuse empty event type")
	}
}

// TestEngineEventRegistryNormalization confirms case + whitespace are
// folded — same contract handleEngineEvent has had since day one.
func TestEngineEventRegistryNormalization(t *testing.T) {
	r := newEngineEventRegistry()
	if !r.has("Tool:Call") {
		t.Fatalf("registry should normalize case for known type")
	}
	if !r.has("  tool:call  ") {
		t.Fatalf("registry should trim whitespace for known type")
	}
}

func setDiff(a, b []string) []string {
	bset := make(map[string]struct{}, len(b))
	for _, s := range b {
		bset[s] = struct{}{}
	}
	var out []string
	for _, s := range a {
		if _, ok := bset[s]; !ok {
			out = append(out, s)
		}
	}
	return out
}
