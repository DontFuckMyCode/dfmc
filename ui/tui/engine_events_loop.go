package tui

// Agent-loop branch of the engine-event router. Owns all ten
// agent:loop:* cases: start, thinking, final, max_steps, error,
// parked (with autonomous-resume suppression), plus the recovery
// chorus — budget_exhausted, auto_resume, auto_resume_refused,
// auto_recover. engine_events.go dispatches prefix-matched events
// here. Returning an empty line short-circuits the parent's notice
// append; that's how the parked autonomous_pending path stays silent.

import (
	"fmt"
	"strings"
)

func (m Model) handleAgentLoopEvent(eventType string, payload map[string]any) (Model, string) {
	line := ""
	switch eventType {
	case "agent:loop:start":
		m, line = m.handleAgentLoopStart(payload)
	case "agent:loop:thinking":
		m, line = m.handleAgentLoopThinking(payload)
	case "agent:loop:narration":
		if text := strings.TrimSpace(payloadString(payload, "text", "")); text != "" {
			m = m.appendAssistantNarration(text)
		}
	case "agent:loop:final":
		m, line = m.handleAgentLoopFinal(payload)
	case "agent:loop:max_steps":
		m.agentLoop.active = false
		m.agentLoop.phase = "max-steps"
		maxSteps := payloadInt(payload, "max_tool_steps", m.agentLoop.maxToolStep)
		if maxSteps > 0 {
			m.agentLoop.maxToolStep = maxSteps
		}
		line = fmt.Sprintf("Agent loop reached max tool steps (%d).", m.agentLoop.maxToolStep)
	case "agent:loop:error":
		m.agentLoop.active = false
		m.agentLoop.phase = "error"
		errText := payloadString(payload, "error", "unknown error")
		line = "Agent loop error: " + errText
	case "agent:loop:parked":
		m, line = m.handleAgentLoopParked(payload)
	default:
		// Recovery cases (budget_exhausted, auto_resume, auto_recover,
		// resume, tools_force_stop, interrupted, shutdown_parked,
		// resume_refused, safety_bound, empty_recovery, empty_final,
		// stuck_force_stop) live in engine_events_loop_recovery.go.
		next, recoveryLine, handled := m.handleAgentLoopRecoveryEvent(eventType, payload)
		if handled {
			return next, recoveryLine
		}
	}
	return m, line
}
