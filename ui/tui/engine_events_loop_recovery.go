package tui

// engine_events_loop_recovery.go — recovery-side cases of the
// agent-loop event router: budget_exhausted, auto_resume,
// auto_resume_refused, auto_recover, resume, tools_force_stop,
// interrupted, shutdown_parked, resume_refused, safety_bound,
// empty_recovery, empty_final, stuck_force_stop.
//
// Sibling of engine_events_loop.go which keeps the lifecycle cases
// (start, thinking, final, max_steps, error, parked) and the
// dispatcher itself. The dispatcher delegates to handleAgentLoopRecoveryEvent
// for any case it doesn't match locally — returning (m, "", false) when
// the recovery handler doesn't recognise the event so the dispatcher
// can fall through cleanly.

func (m Model) handleAgentLoopRecoveryEvent(eventType string, payload map[string]any) (Model, string, bool) {
	line := ""
	switch eventType {
	case "agent:loop:budget_exhausted":
		m, line = m.handleLoopBudgetExhausted(payload)
	case "agent:loop:auto_resume":
		m, line = m.handleLoopAutoResume(payload)
	case "agent:loop:auto_resume_refused":
		m, line = m.handleLoopAutoResumeRefused(payload)
	case "agent:loop:auto_recover":
		m, line = m.handleLoopAutoRecover(payload)
	case "agent:loop:resume":
		m, line = m.handleLoopResume(payload)
	case "agent:loop:tools_force_stop":
		m, line = m.handleLoopToolsForceStop(payload)
	case "agent:loop:interrupted":
		m, line = m.handleLoopInterrupted(payload)
	case "agent:loop:shutdown_parked":
		m, line = m.handleLoopShutdownParked(payload)
	case "agent:loop:resume_refused":
		m, line = m.handleLoopResumeRefused(payload)
	case "agent:loop:safety_bound":
		m, line = m.handleLoopSafetyBound(payload)
	case "agent:loop:empty_recovery":
		m, line = m.handleLoopEmptyRecovery(payload)
	case "agent:loop:empty_final":
		m, line = m.handleLoopEmptyFinal(payload)
	case "agent:loop:stuck_force_stop":
		m, line = m.handleLoopStuckForceStop(payload)
	default:
		return m, "", false
	}
	return m, line, true
}
