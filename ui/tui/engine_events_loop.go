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
)

func (m Model) handleAgentLoopEvent(eventType string, payload map[string]any) (Model, string) {
	line := ""
	switch eventType {
	case "agent:loop:start":
		m.agentLoop.active = true
		m.agentLoop.phase = "starting"
		m.agentLoop.step = 0
		m.agentLoop.maxToolStep = payloadInt(payload, "max_tool_steps", m.agentLoop.maxToolStep)
		m.agentLoop.toolRounds = payloadInt(payload, "tool_rounds", 0)
		m.agentLoop.provider = payloadString(payload, "provider", m.agentLoop.provider)
		m.agentLoop.model = payloadString(payload, "model", m.agentLoop.model)
		// A fresh loop start means any previously parked banner is obsolete.
		m.ui.resumePromptActive = false
		files := payloadInt(payload, "context_files", 0)
		tokens := payloadInt(payload, "context_tokens", 0)
		line = fmt.Sprintf("Agent loop started: max_tools=%d context=%df/%dtok", m.agentLoop.maxToolStep, files, tokens)
	case "agent:loop:thinking":
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
		m.agentLoop.provider = payloadString(payload, "provider", m.agentLoop.provider)
		m.agentLoop.model = payloadString(payload, "model", m.agentLoop.model)
		if m.agentLoop.step > 0 && m.agentLoop.maxToolStep > 0 {
			line = fmt.Sprintf("Agent thinking: step %d/%d", m.agentLoop.step, m.agentLoop.maxToolStep)
		} else {
			line = "Agent thinking..."
		}
	case "agent:loop:final":
		m.agentLoop.active = false
		m.agentLoop.phase = "finalizing"
		if rounds := payloadInt(payload, "tool_rounds", 0); rounds >= 0 {
			m.agentLoop.toolRounds = rounds
		}
		if step := payloadInt(payload, "step", 0); step > 0 {
			m.agentLoop.step = step
		}
		line = fmt.Sprintf("Agent loop finalizing answer after %d tool call(s).", m.agentLoop.toolRounds)
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
		// autonomous_pending=true means the autonomous-resume wrapper will
		// immediately re-enter the loop after this park. In that case we
		// MUST NOT flip into the parked UI (no phase change, no resume
		// prompt) — otherwise the "press Enter to resume" affordance and
		// the spinner-stop flash through, the user types /continue, and by
		// the time it lands the wrapper has already cleared the park
		// state. The 2026-04-18 screenshot ("No parked agent loop"
		// immediately after a budget exhaust) was exactly this race.
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
		// budget_exhausted already surfaces its own "exhausted %d/%d"
		// transcript line with token counts; suppress the generic parked
		// line in that case so the scrollback reads once, not twice.
		if payloadString(payload, "reason", "") == "budget_exhausted" {
			return m, ""
		}
		line = fmt.Sprintf("Agent loop parked at step %d/%d - press Enter to resume, Esc to dismiss.", step, maxSteps)
	case "agent:loop:budget_exhausted":
		m.agentLoop.phase = "budget-exhausted"
		used := payloadInt(payload, "tokens_used", 0)
		budget := payloadInt(payload, "max_tool_tokens", 0)
		m.pushToolChip(toolChip{
			Name:    "token-budget",
			Status:  "budget",
			Preview: fmt.Sprintf("%d/%d tok", used, budget),
		})
		line = fmt.Sprintf("Agent loop exhausted token budget (%d/%d).", used, budget)
	case "agent:loop:auto_resume":
		// Autonomous park→compact→resume inside the same Ask call. We
		// render a compact in-flow chip instead of the disruptive
		// "park / SYS resume / park" sequence — the user wanted
		// hands-off continuation, so we make the continuation feel
		// like one fluent thought rather than three interrupting ones.
		// Belt-and-braces: clear any resume affordance the parked event
		// might have flipped on (it now suppresses itself when
		// autonomous_pending is set, but older engines or out-of-order
		// events shouldn't leave a stale prompt sitting on screen).
		m.ui.resumePromptActive = false
		m.agentLoop.active = true
		m.agentLoop.phase = "auto-resuming"
		cumSteps := payloadInt(payload, "cumulative_steps", 0)
		stepCeiling := payloadInt(payload, "step_ceiling", 0)
		preview := "compacted, continuing"
		if stepCeiling > 0 {
			preview = fmt.Sprintf("compacted, continuing · %d/%d steps", cumSteps, stepCeiling)
		}
		m.pushToolChip(toolChip{
			Name:    "auto-resume",
			Status:  "running",
			Preview: preview,
		})
		// No transcript line — the chip is enough signal. A line in
		// the chat would re-create the noisy "SYS resume" pattern the
		// autonomous loop is supposed to eliminate.
	case "agent:loop:auto_resume_refused":
		// Cumulative ceiling hit during autonomy — surface this so the
		// user knows the auto-progression bottomed out and a manual
		// /continue (or scope refinement) is needed.
		reason := payloadString(payload, "reason", "ceiling")
		m.pushToolChip(toolChip{
			Name:    "auto-resume",
			Status:  "failed",
			Preview: "ceiling: " + reason,
		})
		line = "Autonomous resume stopped — cumulative work ceiling reached. Type /continue to override or refine the question."
	case "agent:loop:auto_recover":
		before := payloadInt(payload, "before_tokens", 0)
		after := payloadInt(payload, "after_tokens", 0)
		collapsed := payloadInt(payload, "rounds_collapsed", 0)
		m.pushToolChip(toolChip{
			Name:    "auto-recover",
			Status:  "recover",
			Preview: fmt.Sprintf("%d→%d tok · %d rounds", before, after, collapsed),
		})
		if collapsed > 0 {
			line = fmt.Sprintf("Auto-recover: budget trip, compacted %d→%d tokens (%d rounds). Retrying.", before, after, collapsed)
		} else {
			line = "Auto-recover: budget trip, transcript slimmed. Retrying."
		}
	}
	return m, line
}
