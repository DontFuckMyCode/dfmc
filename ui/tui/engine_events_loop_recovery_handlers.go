package tui

import "fmt"

func (m Model) handleLoopBudgetExhausted(payload map[string]any) (Model, string) {
	m.agentLoop.phase = "budget-exhausted"
	used := payloadInt(payload, "tokens_used", 0)
	budget := payloadInt(payload, "max_tool_tokens", 0)
	m.pushToolChip(toolChip{Name: "token-budget", Status: "budget", Preview: fmt.Sprintf("%d/%d tok", used, budget)})
	return m, fmt.Sprintf("Agent loop exhausted token budget (%d/%d).", used, budget)
}

func (m Model) handleLoopAutoResume(payload map[string]any) (Model, string) {
	m.ui.resumePromptActive = false
	m.agentLoop.active = true
	m.agentLoop.phase = "auto-resuming"
	cumSteps := payloadInt(payload, "cumulative_steps", 0)
	stepCeiling := payloadInt(payload, "step_ceiling", 0)
	cumTokens := payloadInt(payload, "cumulative_tokens", 0)
	tokenCeiling := payloadInt(payload, "token_ceiling", 0)
	m.agentLoop.cumulativeSteps = cumSteps
	m.agentLoop.stepCeiling = stepCeiling
	m.agentLoop.cumulativeTokens = cumTokens
	m.agentLoop.tokenCeiling = tokenCeiling
	preview := "compacted, continuing"
	if stepCeiling > 0 {
		preview = fmt.Sprintf("compacted, continuing · %d/%d steps", cumSteps, stepCeiling)
	}
	m.pushToolChip(toolChip{Name: "auto-resume", Status: "running", Preview: preview})
	return m, ""
}

func (m Model) handleLoopAutoResumeRefused(payload map[string]any) (Model, string) {
	reason := payloadString(payload, "reason", "ceiling")
	m.pushToolChip(toolChip{Name: "auto-resume", Status: "failed", Preview: "ceiling: " + reason})
	return m, "Autonomous resume stopped — cumulative work ceiling reached. Type /continue to override or refine the question."
}

func (m Model) handleLoopAutoRecover(payload map[string]any) (Model, string) {
	before := payloadInt(payload, "before_tokens", 0)
	after := payloadInt(payload, "after_tokens", 0)
	collapsed := payloadInt(payload, "rounds_collapsed", 0)
	m.pushToolChip(toolChip{Name: "auto-recover", Status: "recover", Preview: fmt.Sprintf("%d→%d tok · %d rounds", before, after, collapsed)})
	if collapsed > 0 {
		return m, fmt.Sprintf("Auto-recover: budget trip, compacted %d→%d tokens (%d rounds). Retrying.", before, after, collapsed)
	}
	return m, "Auto-recover: budget trip, transcript slimmed. Retrying."
}

func (m Model) handleLoopResume(payload map[string]any) (Model, string) {
	fromStep := payloadInt(payload, "resumed_from_step", 0)
	rounds := payloadInt(payload, "tool_rounds", 0)
	tokens := payloadInt(payload, "tokens_used", 0)
	return m, fmt.Sprintf("Loop resumed from step %d (prior: %d rounds, ~%d tokens).", fromStep, rounds, tokens)
}

func (m Model) handleLoopToolsForceStop(payload map[string]any) (Model, string) {
	rounds := payloadInt(payload, "tool_rounds", 0)
	hardCap := payloadInt(payload, "hard_cap", 0)
	if rounds > m.agentLoop.toolRounds {
		m.agentLoop.toolRounds = rounds
	}
	if m.agentLoop.toolForceStopNotified {
		m.notice = fmt.Sprintf("Tool round cap still active (%d/%d). Chat history is collapsed; details remain in Activity/ToolStatus.", rounds, hardCap)
		return m, ""
	}
	m.agentLoop.toolForceStopNotified = true
	m.ui.toolStripExpanded = false
	m.pushToolChip(toolChip{Name: "force-stop", Status: "warn", Preview: fmt.Sprintf("%d rounds · hard cap %d", rounds, hardCap)})
	return m, fmt.Sprintf("Tool round hard cap reached (%d/%d) — forcing text-only reply. Refine scope or raise agent.tool_round_hard_cap.", rounds, hardCap)
}

func (m Model) handleLoopInterrupted(payload map[string]any) (Model, string) {
	rounds := payloadInt(payload, "tool_rounds", 0)
	errText := payloadString(payload, "error", "")
	m.pushToolChip(toolChip{Name: "interrupted", Status: "warn", Preview: fmt.Sprintf("%d rounds · cancelled", rounds)})
	if errText != "" {
		return m, fmt.Sprintf("Loop interrupted (%d rounds, %s) — work parked; /continue to resume.", rounds, errText)
	}
	return m, fmt.Sprintf("Loop interrupted (%d rounds) — work parked; /continue to resume.", rounds)
}

func (m Model) handleLoopShutdownParked(payload map[string]any) (Model, string) {
	step := payloadInt(payload, "step", 0)
	m.pushToolChip(toolChip{Name: "shutdown-park", Status: "warn", Preview: fmt.Sprintf("step %d", step)})
	return m, fmt.Sprintf("Engine shutting down — loop parked at step %d. Restart dfmc to resume.", step)
}

func (m Model) handleLoopResumeRefused(payload map[string]any) (Model, string) {
	reason := payloadString(payload, "reason", "ceiling")
	m.pushToolChip(toolChip{Name: "resume", Status: "failed", Preview: "refused: " + reason})
	return m, fmt.Sprintf("Resume refused: %s. Refine the question or start a fresh /ask.", reason)
}

func (m Model) handleLoopSafetyBound(payload map[string]any) (Model, string) {
	bound := payloadInt(payload, "safety_bound", 0)
	source := payloadString(payload, "source", "")
	m.pushToolChip(toolChip{Name: "safety-bound", Status: "failed", Preview: fmt.Sprintf("hit %d", bound)})
	return m, fmt.Sprintf("Safety bound reached (%d, source=%s) — autonomous loop forced to stop. This should be extremely rare.", bound, source)
}

func (m Model) handleLoopEmptyRecovery(payload map[string]any) (Model, string) {
	rounds := payloadInt(payload, "tool_rounds", 0)
	return m, fmt.Sprintf("Empty response detected (%d rounds) — sending synthesis nudge and retrying.", rounds)
}

func (m Model) handleLoopEmptyFinal(payload map[string]any) (Model, string) {
	rounds := payloadInt(payload, "tool_rounds", 0)
	m.pushToolChip(toolChip{Name: "empty-final", Status: "failed", Preview: fmt.Sprintf("%d rounds", rounds)})
	return m, fmt.Sprintf("Empty response twice in a row after %d rounds — giving up. Try rephrasing or narrowing scope.", rounds)
}

func (m Model) handleLoopStuckForceStop(payload map[string]any) (Model, string) {
	streak := payloadInt(payload, "stuck_streak", 0)
	threshold := payloadInt(payload, "threshold", 0)
	m.pushToolChip(toolChip{Name: "force-stop", Status: "warn", Preview: fmt.Sprintf("%d rounds stuck · forcing text reply", streak)})
	return m, fmt.Sprintf(
		"Loop stuck for %d consecutive rounds (threshold %d) — forcing the next reply to be text-only so you can redirect.",
		streak, threshold,
	)
}
