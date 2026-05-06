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
	"time"
)

// headroomThresholds defines the (pct, bit, severity, hint) bands the
// pre-compact warning notifies on. bit indices match the bitmask in
// agentLoopState.headroomThresholdsHit so each band fires at most once
// per turn — a long loop that ticks 71→72→73 over many rounds only
// shows the 70% notification ONCE.
var headroomThresholds = []struct {
	pct      int
	bit      uint8
	status   string // chat-event Status (warn, error)
	headline string
	hint     string
}{
	// Hint copy reflects what actually reduces ENGINE context (not the
	// TUI-only /compact slash command, which only collapses visible
	// transcript lines without affecting the running loop's working
	// set). Auto-compact fires reactively at 0.7 ratio; /chat new
	// rotates to a fresh conversation; narrowing @file mentions and
	// dropping pinned files reduces the per-Ask context payload.
	{pct: 70, bit: 1 << 0, status: "warn", headline: "context 70% full", hint: "auto-compact will fire next round — or narrow @files / drop pins"},
	{pct: 85, bit: 1 << 1, status: "warn", headline: "context 85% full", hint: "tighten scope: drop @files, fewer pins, or /conv new"},
	{pct: 95, bit: 1 << 2, status: "error", headline: "context 95% full", hint: "next turn may park on budget — /conv new for a fresh window"},
}

// maybeNotifyHeadroomThreshold pushes a chat-event line when the live
// loop tokens cross a 70/85/95 band for the first time this turn.
// Uses the live loop budget when available (max_tool_tokens), falling
// back to MaxContext from the live context snapshot when not — that
// way a non-loop Ask still gets a warning if the request itself fills
// the window. Caller is the agent:loop:thinking handler so this fires
// once per round at most; the bitmask dedupes within the turn.
func (m Model) maybeNotifyHeadroomThreshold() Model {
	used := m.agentLoop.liveLoopTokens
	cap := m.agentLoop.liveLoopBudgetCap
	if cap <= 0 {
		// Fall back to provider context window when the loop didn't
		// report a budget — better than nothing for non-tool-loop asks.
		if live := m.liveContextSnapshot(); live.ok && live.maxContext > 0 {
			cap = live.maxContext
		}
	}
	if used <= 0 || cap <= 0 {
		return m
	}
	pct := int((int64(used) * 100) / int64(cap))
	for _, band := range headroomThresholds {
		if pct < band.pct {
			continue
		}
		if m.agentLoop.headroomThresholdsHit&band.bit != 0 {
			continue // already fired this turn
		}
		m.agentLoop.headroomThresholdsHit |= band.bit
		m.upsertStreamingChatEvent(chatEventLine{
			Key:    fmt.Sprintf("context:headroom:%d", band.pct),
			Kind:   "context",
			Status: band.status,
			Title:  band.headline,
			Detail: fmt.Sprintf("%d / %d tokens · %s", used, cap, band.hint),
		})
	}
	return m
}

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
		cumTokens := payloadInt(payload, "cumulative_tokens", 0)
		tokenCeiling := payloadInt(payload, "token_ceiling", 0)
		// Persist the counters on agentLoopState so the runtime strip can
		// render a continuous "auto · 240/600" badge between resumes,
		// instead of the user only seeing it for one frame on each chip.
		m.agentLoop.cumulativeSteps = cumSteps
		m.agentLoop.stepCeiling = stepCeiling
		m.agentLoop.cumulativeTokens = cumTokens
		m.agentLoop.tokenCeiling = tokenCeiling
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
	case "agent:loop:resume":
		// Manual /continue (or successful auto-resume) just re-entered
		// the loop with prior seed state. Quiet info-level line so the
		// user sees the resume actually took effect, especially on long
		// runs where they typed /continue a while back and forgot.
		fromStep := payloadInt(payload, "resumed_from_step", 0)
		rounds := payloadInt(payload, "tool_rounds", 0)
		tokens := payloadInt(payload, "tokens_used", 0)
		line = fmt.Sprintf("Loop resumed from step %d (prior: %d rounds, ~%d tokens).", fromStep, rounds, tokens)
	case "agent:loop:tools_force_stop":
		// Hard round-cap fired — distinct from stuck_force_stop (which
		// is the trajectory-detector guard) and from max_steps. Surface
		// it so the user knows the loop cap is what cut off tool
		// access, not a model decision.
		rounds := payloadInt(payload, "tool_rounds", 0)
		hardCap := payloadInt(payload, "hard_cap", 0)
		m.pushToolChip(toolChip{
			Name:    "force-stop",
			Status:  "warn",
			Preview: fmt.Sprintf("%d rounds · hard cap %d", rounds, hardCap),
		})
		line = fmt.Sprintf("Tool round hard cap reached (%d/%d) — forcing text-only reply. Refine scope or raise agent.tool_round_hard_cap.", rounds, hardCap)
	case "agent:loop:interrupted":
		// User cancelled mid-loop (Ctrl-C / parent ctx cancelled). The
		// loop parked the work; surface a distinct line so the user
		// knows the interrupt landed cleanly vs. having to guess from
		// "agent: parked".
		rounds := payloadInt(payload, "tool_rounds", 0)
		errText := strings.TrimSpace(payloadString(payload, "error", ""))
		m.pushToolChip(toolChip{
			Name:    "interrupted",
			Status:  "warn",
			Preview: fmt.Sprintf("%d rounds · cancelled", rounds),
		})
		if errText != "" {
			line = fmt.Sprintf("Loop interrupted (%d rounds, %s) — work parked; /continue to resume.", rounds, errText)
		} else {
			line = fmt.Sprintf("Loop interrupted (%d rounds) — work parked; /continue to resume.", rounds)
		}
	case "agent:loop:shutdown_parked":
		// Engine is shutting down mid-loop. Different from "interrupted"
		// — this is engine-initiated, not user-initiated. The work is
		// saved; user must restart dfmc and resume.
		step := payloadInt(payload, "step", 0)
		m.pushToolChip(toolChip{
			Name:    "shutdown-park",
			Status:  "warn",
			Preview: fmt.Sprintf("step %d", step),
		})
		line = fmt.Sprintf("Engine shutting down — loop parked at step %d. Restart dfmc to resume.", step)
	case "agent:loop:resume_refused":
		// Manual /continue was rejected because cumulative ceiling has
		// been reached or some other resume guard fired. Distinct from
		// auto_resume_refused (which is the autonomous-mode equivalent)
		// because the user explicitly asked this time and deserves a
		// clear "no, here's why" instead of a quiet refusal.
		reason := payloadString(payload, "reason", "ceiling")
		m.pushToolChip(toolChip{
			Name:    "resume",
			Status:  "failed",
			Preview: "refused: " + reason,
		})
		line = fmt.Sprintf("Resume refused: %s. Refine the question or start a fresh /ask.", reason)
	case "agent:loop:safety_bound":
		// Outer safety net — should fire approximately never given the
		// cumulative ceiling kicks in earlier. If it DOES fire it means
		// the autonomous loop reached its absolute upper bound and the
		// user must intervene. High-prominence so they don't miss it.
		bound := payloadInt(payload, "safety_bound", 0)
		source := payloadString(payload, "source", "")
		m.pushToolChip(toolChip{
			Name:    "safety-bound",
			Status:  "failed",
			Preview: fmt.Sprintf("hit %d", bound),
		})
		line = fmt.Sprintf("Safety bound reached (%d, source=%s) — autonomous loop forced to stop. This should be extremely rare.", bound, source)
	case "agent:loop:empty_recovery":
		// Model returned an empty response; the loop sent a synthesis
		// nudge ("please answer in natural language") and is retrying.
		// Quiet info-level line; only surfaces because users wonder
		// what's happening when the assistant pauses with no output.
		rounds := payloadInt(payload, "tool_rounds", 0)
		line = fmt.Sprintf("Empty response detected (%d rounds) — sending synthesis nudge and retrying.", rounds)
	case "agent:loop:empty_final":
		// Model returned empty twice in a row. The loop gives up with a
		// canned message; user must rephrase or scope down. Distinct
		// from auto_recover because there's no compaction to retry —
		// the model just isn't answering.
		rounds := payloadInt(payload, "tool_rounds", 0)
		m.pushToolChip(toolChip{
			Name:    "empty-final",
			Status:  "failed",
			Preview: fmt.Sprintf("%d rounds", rounds),
		})
		line = fmt.Sprintf("Empty response twice in a row after %d rounds — giving up. Try rephrasing or narrowing scope.", rounds)
	case "agent:loop:stuck_force_stop":
		// Stuck-streak guard fired — the loop forced tool_choice="none"
		// for the next call because the same failure pattern persisted
		// for N consecutive rounds and the "switch tactic" hint went
		// unheeded. Surface a distinct chip + warn line so the user can
		// see WHY the model suddenly switches to text-only output.
		streak := payloadInt(payload, "stuck_streak", 0)
		threshold := payloadInt(payload, "threshold", 0)
		preview := fmt.Sprintf("%d rounds stuck · forcing text reply", streak)
		m.pushToolChip(toolChip{
			Name:    "force-stop",
			Status:  "warn",
			Preview: preview,
		})
		line = fmt.Sprintf(
			"Loop stuck for %d consecutive rounds (threshold %d) — forcing the next reply to be text-only so you can redirect.",
			streak, threshold,
		)
	}
	return m, line
}
