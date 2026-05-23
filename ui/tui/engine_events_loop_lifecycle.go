package tui

import (
	"fmt"
	"time"
)

func (m Model) handleAgentLoopStart(payload map[string]any) (Model, string) {
	m.agentLoop.active = true
	m.agentLoop.phase = "starting"
	m.agentLoop.step = 0
	m.agentLoop.maxToolStep = payloadInt(payload, "max_tool_steps", m.agentLoop.maxToolStep)
	m.agentLoop.toolRounds = payloadInt(payload, "tool_rounds", 0)
	m.agentLoop.provider = payloadString(payload, "provider", m.agentLoop.provider)
	m.agentLoop.model = payloadString(payload, "model", m.agentLoop.model)
	m.agentLoop.cumulativeSteps = 0
	m.agentLoop.stepCeiling = 0
	m.agentLoop.cumulativeTokens = 0
	m.agentLoop.tokenCeiling = 0
	m.agentLoop.unvalidatedEdits = nil
	m.agentLoop.unvalidatedSinceStep = 0
	m.agentLoop.turnStartedAt = time.Now()
	m.agentLoop.turnEditedFiles = nil
	m.agentLoop.turnValidationPasses = 0
	m.agentLoop.turnCoachInterventions = 0
	m.agentLoop.liveLoopTokens = 0
	m.agentLoop.liveLoopBudgetCap = payloadInt(payload, "max_tool_tokens", 0)
	m.agentLoop.headroomThresholdsHit = 0
	m.agentLoop.compactsThisTurn = 0
	m.agentLoop.compactReclaimedTurn = 0
	m.agentLoop.cacheHitsThisTurn = 0
	m.agentLoop.toolErrorsThisTurn = 0
	m.agentLoop.toolForceStopNotified = false
	m.agentLoop.unverifiedCoachLastCount = 0
	m.ui.resumePromptActive = false
	files := payloadInt(payload, "context_files", 0)
	tokens := payloadInt(payload, "context_tokens", 0)
	return m, fmt.Sprintf("Agent loop started: max_tools=%d context=%df/%dtok", m.agentLoop.maxToolStep, files, tokens)
}

func (m Model) handleAgentLoopThinking(payload map[string]any) (Model, string) {
	m.agentLoop.active = true
	m.agentLoop.phase = "thinking"
	step := payloadInt(payload, "step", 0)
	if step > 0 {
		m.agentLoop.step = step
	}
	maxSteps := payloadInt(payload, "max_tool_steps", 0)
	if maxSteps > 0 {
		m.agentLoop.maxToolStep = maxSteps
	}
	rounds := payloadInt(payload, "tool_rounds", 0)
	if rounds >= 0 {
		m.agentLoop.toolRounds = rounds
	}
	if used := payloadInt(payload, "tokens_used", 0); used > 0 {
		m.agentLoop.liveLoopTokens = used
	}
	m.agentLoop.provider = payloadString(payload, "provider", m.agentLoop.provider)
	m.agentLoop.model = payloadString(payload, "model", m.agentLoop.model)
	m = m.maybeNotifyHeadroomThreshold()
	if m.agentLoop.step > 0 && m.agentLoop.maxToolStep > 0 {
		return m, fmt.Sprintf("Agent thinking: step %d/%d", m.agentLoop.step, m.agentLoop.maxToolStep)
	}
	return m, "Agent thinking..."
}

func (m Model) handleAgentLoopFinal(payload map[string]any) (Model, string) {
	m.agentLoop.active = false
	m.agentLoop.phase = "finalizing"
	m.agentLoop.liveLoopTokens = 0
	m.agentLoop.liveLoopBudgetCap = 0
	if rounds := payloadInt(payload, "tool_rounds", 0); rounds >= 0 {
		m.agentLoop.toolRounds = rounds
	}
	if step := payloadInt(payload, "step", 0); step > 0 {
		m.agentLoop.step = step
	}
	todoTotal, todoDone, todoPending := todoCountsForSummary(m)
	if summary := buildTurnSummary(m.agentLoop, todoTotal, todoDone, todoPending); summary != "" {
		return m, summary
	}
	return m, fmt.Sprintf("Agent loop finalizing answer after %d tool call(s).", m.agentLoop.toolRounds)
}

func (m Model) handleAgentLoopParked(payload map[string]any) (Model, string) {
	if payloadBool(payload, "autonomous_pending", false) {
		return m, ""
	}
	m.agentLoop.phase = "parked"
	m.agentLoop.active = false
	step := payloadInt(payload, "step", m.agentLoop.step)
	maxSteps := payloadInt(payload, "max_tool_steps", m.agentLoop.maxToolStep)
	m.agentLoop.step = step
	if maxSteps > 0 {
		m.agentLoop.maxToolStep = maxSteps
	}
	m.ui.resumePromptActive = true
	if payloadString(payload, "reason", "") == "budget_exhausted" {
		return m, ""
	}
	return m, fmt.Sprintf("Agent loop parked at step %d/%d - press Enter to resume, Esc to dismiss.", step, maxSteps)
}
