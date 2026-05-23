package tui

// Sub-agent branch of the engine-event router. Owns the
// agent:subagent:* cases so the chip ribbon, active-subagent counter,
// and notice line share one home. engine_events.go dispatches
// prefix-matched events here.

import "time"

func (m Model) handleSubagentEvent(eventType string, payload map[string]any) (Model, string) {
	now := time.Now()
	switch eventType {
	case "agent:subagent:start":
		return m.handleSubagentStart(payload, now)
	case "agent:subagent:fallback":
		return m.handleSubagentFallback(payload, now)
	case "agent:subagent:done":
		return m.handleSubagentDone(payload, now)
	case "agent:subagent:interrupted":
		return m.handleSubagentInterrupted(payload, now)
	default:
		return m, ""
	}
}
