package tui

// engine_event_adapters.go — wires the four existing per-domain
// helpers (handleAgentLoopEvent / handleToolEvent / handleSubagentEvent /
// handleDriveEvent in their respective engine_events_*.go files) into
// the engineEventRegistry. Adapter functions exist because the
// originals predate the registry and have heterogeneous signatures
// (some take event, some don't); the registry's contract is a single
// uniform engineEventHandler shape.

import (
	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func registerAgentLoopEventHandlers(r *engineEventRegistry) {
	h := func(m Model, eventType string, event engine.Event, payload map[string]any) (Model, string) {
		return m.handleAgentLoopEvent(eventType, payload)
	}
	r.registerMany([]string{
		"agent:loop:start", "agent:loop:thinking", "agent:loop:narration",
		"agent:loop:final", "agent:loop:max_steps", "agent:loop:error",
		"agent:loop:parked", "agent:loop:budget_exhausted",
		"agent:loop:auto_resume", "agent:loop:auto_resume_refused",
		"agent:loop:auto_recover", "agent:loop:tools_force_stop",
		"agent:loop:interrupted", "agent:loop:shutdown_parked",
		"agent:loop:resume", "agent:loop:resume_refused",
		"agent:loop:safety_bound", "agent:loop:empty_recovery",
		"agent:loop:empty_final", "agent:loop:stuck_force_stop",
	}, h)
}

func registerToolEventHandlers(r *engineEventRegistry) {
	h := func(m Model, eventType string, event engine.Event, payload map[string]any) (Model, string) {
		return m.handleToolEvent(eventType, event, payload)
	}
	r.registerMany([]string{
		"tool:call", "tool:result", "tool:error",
		"tool:reasoning", "tool:timeout", "tool:denied",
	}, h)
}

func registerSubagentEventHandlers(r *engineEventRegistry) {
	h := func(m Model, eventType string, event engine.Event, payload map[string]any) (Model, string) {
		return m.handleSubagentEvent(eventType, payload)
	}
	r.registerMany([]string{
		"agent:subagent:start", "agent:subagent:fallback", "agent:subagent:done",
		"agent:subagent:interrupted",
	}, h)
}

func registerDriveEventHandlers(r *engineEventRegistry) {
	h := func(m Model, eventType string, event engine.Event, payload map[string]any) (Model, string) {
		return m.handleDriveEvent(eventType, payload)
	}
	r.registerMany([]string{
		"drive:run:start", "drive:plan:done", "drive:plan:failed",
		"drive:todo:start", "drive:todo:done", "drive:todo:blocked",
		"drive:todo:skipped", "drive:todo:retry",
		"drive:run:warning", "drive:run:done", "drive:run:stopped", "drive:run:failed",
	}, h)
}
