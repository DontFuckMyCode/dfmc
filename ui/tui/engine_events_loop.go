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
	"time"
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
		// Fresh ask zeros the cumulative counters — the prior /continue
		// chain is over. Without this reset the runtime strip would keep
		// showing yesterday's 540/600 progress on tomorrow's first turn.
		m.agentLoop.cumulativeSteps = 0
		m.agentLoop.stepCeiling = 0
		m.agentLoop.cumulativeTokens = 0
		m.agentLoop.tokenCeiling = 0
		// And the unvalidated-edits ledger — a fresh ask gets a clean
		// slate. Edits the new turn produces will accumulate from zero.
		m.agentLoop.unvalidatedEdits = nil
		m.agentLoop.unvalidatedSinceStep = 0
		// Turn accumulators for the on-final summary card.
		m.agentLoop.turnStartedAt = time.Now()
		m.agentLoop.turnEditedFiles = nil
		m.agentLoop.turnValidationPasses = 0
		m.agentLoop.turnCoachInterventions = 0
		// Live token tracking — pick up the per-turn budget from the
		// start event so the strip can render "47k/250k" with the
		// right denominator. Cleared together with the rest.
		m.agentLoop.liveLoopTokens = 0
		m.agentLoop.liveLoopBudgetCap = payloadInt(payload, "max_tool_tokens", 0)
		// Reset the per-turn threshold-warnings tracker so the new ask
		// gets a fresh chance to show 70/85/95% notifications. Without
		// this reset, a long previous turn that hit 95% would suppress
		// the warning for the new turn until usage drops back below 70.
		m.agentLoop.headroomThresholdsHit = 0
		// Compacts-this-turn counter resets so the runtime badge shows
		// 0 again at the start of each ask. Without this a turn that
		// needed 3 compacts would carry the "×3" badge into the next
		// turn even though that turn might compact 0 times.
		m.agentLoop.compactsThisTurn = 0
		m.agentLoop.compactReclaimedTurn = 0
		// Cache hits also reset per-turn so the badge tells the user
		// "this turn saved N tool calls via cache" cleanly.
		m.agentLoop.cacheHitsThisTurn = 0
		// Tool error counter resets per-turn so the summary card reflects
		// just THIS turn's fragility, not a cumulative since-launch tally.
		m.agentLoop.toolErrorsThisTurn = 0
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
		// Live working-set token count, refreshed every round. The
		// engine reports the rolling conversation footprint here (NOT
		// a cumulative sum) so this can shrink when a force-compact
		// runs — a feature, not a bug: the strip should reflect the
		// real footprint, not a monotonic counter.
		if used := payloadInt(payload, "tokens_used", 0); used > 0 {
			m.agentLoop.liveLoopTokens = used
		}
		m.agentLoop.provider = payloadString(payload, "provider", m.agentLoop.provider)
		m.agentLoop.model = payloadString(payload, "model", m.agentLoop.model)
		// Pre-compact headroom warnings: when the running tokens_used
		// crosses 70/85/95% of the live loop budget for the FIRST
		// TIME this turn, push a chat-event notification. The
		// auto-compactor fires reactively at 0.7 ratio (and proactively
		// at 0.5 once past the soft-cap), so a 70% notification means
		// "compact is imminent next round"; 95% means "things are
		// getting urgent — narrow the question or /compact now."
		// Tracker is per-turn (cleared on agent:loop:start) so a
		// second turn after compact gets a clean band-crossing slate.
		m = m.maybeNotifyHeadroomThreshold()
		if m.agentLoop.step > 0 && m.agentLoop.maxToolStep > 0 {
			line = fmt.Sprintf("Agent thinking: step %d/%d", m.agentLoop.step, m.agentLoop.maxToolStep)
		} else {
			line = "Agent thinking..."
		}
	case "agent:loop:final":
		m.agentLoop.active = false
		m.agentLoop.phase = "finalizing"
		// Drop the live counter — the loop is done, the badge is
		// no longer meaningful. The /stats card preserves session-
		// scope token totals separately.
		m.agentLoop.liveLoopTokens = 0
		m.agentLoop.liveLoopBudgetCap = 0
		if rounds := payloadInt(payload, "tool_rounds", 0); rounds >= 0 {
			m.agentLoop.toolRounds = rounds
		}
		if step := payloadInt(payload, "step", 0); step > 0 {
			m.agentLoop.step = step
		}
		// Render the turn-summary card AS the final notice line so the
		// user lands on a multi-line recap of what just happened
		// instead of a generic "finalizing answer after N tool call(s)"
		// banner. Suppressed for trivial turns (zero edits, zero
		// validation, no coach interventions) where the answer itself
		// is the report and a card would be noise.
		todoTotal, todoDone, todoPending := todoCountsForSummary(m)
		if summary := buildTurnSummary(m.agentLoop, todoTotal, todoDone, todoPending); summary != "" {
			line = summary
		} else {
			line = fmt.Sprintf("Agent loop finalizing answer after %d tool call(s).", m.agentLoop.toolRounds)
		}
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
		// prompt) — otherwise the "press Ctrl+X to resume" affordance and
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
		line = fmt.Sprintf("Agent loop parked at step %d/%d - press Ctrl+X to resume, Esc to dismiss.", step, maxSteps)
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
