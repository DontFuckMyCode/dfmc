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

import (
	"fmt"
	"strings"
)

func (m Model) handleAgentLoopRecoveryEvent(eventType string, payload map[string]any) (Model, string, bool) {
	line := ""
	switch eventType {
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
		if rounds > m.agentLoop.toolRounds {
			m.agentLoop.toolRounds = rounds
		}
		if m.agentLoop.toolForceStopNotified {
			m.notice = fmt.Sprintf("Tool round cap still active (%d/%d). Chat history is collapsed; details remain in Activity/ToolStatus.", rounds, hardCap)
			return m, "", true
		}
		m.agentLoop.toolForceStopNotified = true
		m.ui.toolStripExpanded = false
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
	default:
		return m, "", false
	}
	return m, line, true
}
