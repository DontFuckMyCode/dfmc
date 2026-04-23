// agent_loop_autonomous.go — autonomous-resume wrapper and the public
// ResumeAgent entry point for the native agent loop.
//
//   - runNativeToolLoopAutonomous: wraps runNativeToolLoop with the
//     park→compact→resume cycle so a budget park becomes a transparent
//     continuation rather than a user-visible stop. Step-cap parks,
//     shutdown parks, and errors still short-circuit so the user stays
//     in the loop for genuine blockers.
//   - autonomousResumeEnabled / resumeMaxMultiplier: read the agent
//     config knobs that gate the wrapper. Both default ON/10× so the
//     user gets continuous progress by default and a reasonable ceiling
//     on runaway models.
//   - attemptAutoResume: claims the parked state, accumulates the
//     cumulative counters, refuses on ceiling, force-compacts history,
//     and returns the fresh seed ready for another loop attempt.
//   - ResumeAgent: user-driven /continue entry point. Shares the
//     ceiling / compact / reset machinery with the autonomous path and
//     re-enters through runNativeToolLoopAutonomous so a manual resume
//     that itself parks for budget keeps progressing inside one call.
//
// Extracted from agent_loop_native.go to keep the main loop file
// focused on the round-by-round control flow.

package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// runNativeToolLoopAutonomous wraps runNativeToolLoop with the
// autonomous park→compact→resume cycle. When the loop parks for budget
// reasons and autonomy is enabled (and the cumulative ceiling hasn't
// been hit), we transparently re-enter the loop without returning to
// the caller — the user sees one continuous response instead of having
// to type /continue between every park. Step caps, shutdown parks, and
// errors short-circuit the autonomy and return immediately so the
// user can intervene.
//
// Source label ("ask" / "resume") is recorded on the auto_resume event
// so observability can tell apart the autonomous progression from a
// user-driven /continue chain.
func (e *Engine) runNativeToolLoopAutonomous(ctx context.Context, seed *parkedAgentState, lim agentLimits, source string) (nativeToolCompletion, error) {
	const safetyBound = 64 // belt-and-braces; cumulative ceiling is the real cap
	for attempt := 0; attempt < safetyBound; attempt++ {
		if ctx.Err() != nil {
			return nativeToolCompletion{}, ctx.Err()
		}
		completion, err := e.runNativeToolLoop(ctx, seed, lim)
		if err != nil || !completion.Parked {
			return completion, err
		}
		if completion.ParkedReason != ParkReasonBudgetExhausted {
			// Step cap and shutdown parks deliberately surface to the
			// user — step cap means the model isn't converging, shutdown
			// means the engine is going away. Auto-resume would mask
			// both signals.
			return completion, err
		}
		if !e.autonomousResumeEnabled() {
			return completion, err
		}
		nextSeed, ok := e.attemptAutoResume(source)
		if !ok {
			return completion, err
		}
		seed = nextSeed
	}
	// Hit the safety bound — extremely unlikely given the cumulative
	// ceiling kicks in well before this. Park whatever's current so the
	// user can /continue manually if they want.
	e.publishAgentLoopEvent("agent:loop:safety_bound", map[string]any{
		"safety_bound": safetyBound,
		"source":       source,
		"surface":      "native",
		"provider":     seed.LastProvider,
		"model":        seed.LastModel,
	})
	res, err := e.runNativeToolLoop(ctx, seed, lim)
	return res, err
}

// autonomousResumeEnabled reports whether the engine's config opts in
// to auto-resume on budget park. Default ON; explicit "off"/"false"/"no"/"0"
// disables. The opt-out exists for CI runs that must hard-stop after one
// budget without manual intervention.
func (e *Engine) autonomousResumeEnabled() bool {
	if e == nil || e.Config == nil {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(e.Config.Agent.AutonomousResume)) {
	case "off", "false", "no", "0", "manual":
		return false
	}
	return true
}

// attemptAutoResume runs the same preflight ResumeAgent does — claim
// the parked state, accumulate cumulative counters, refuse on ceiling,
// force-compact history, reset per-attempt counters — and returns the
// fresh seed if and only if another loop attempt is allowed. When it
// returns ok=false the caller must let the park stand: either the
// cumulative ceiling hit (re-parked seed already saved by this fn) or
// there's no parked state (race / cleared).
//
// Emits agent:loop:auto_resume so the TUI can render a one-line "↻
// auto-resuming after compact" indicator instead of the disruptive
// "park / SYS resume / park" trio that breaks the user's reading flow.
func (e *Engine) attemptAutoResume(source string) (*parkedAgentState, bool) {
	seed := e.takeParkedAgent()
	if seed == nil {
		return nil, false
	}
	lim := e.agentLimits()
	seed.CumulativeSteps += seed.Step
	seed.CumulativeTokens += seed.TotalTokens

	mult := e.resumeMaxMultiplier()
	stepCeiling := lim.MaxSteps * mult
	tokenCeiling := lim.MaxTokens * mult
	if lim.MaxSteps > 0 && seed.CumulativeSteps >= stepCeiling {
		e.saveParkedAgent(seed)
		e.publishAgentLoopEvent("agent:loop:auto_resume_refused", map[string]any{
			"reason":            "cumulative_steps_ceiling",
			"cumulative_steps":  seed.CumulativeSteps,
			"ceiling":           stepCeiling,
			"max_steps_per_run": lim.MaxSteps,
			"multiplier":        mult,
			"source":            source,
		})
		return nil, false
	}
	if lim.MaxTokens > 0 && seed.CumulativeTokens >= tokenCeiling {
		e.saveParkedAgent(seed)
		e.publishAgentLoopEvent("agent:loop:auto_resume_refused", map[string]any{
			"reason":             "cumulative_tokens_ceiling",
			"cumulative_tokens":  seed.CumulativeTokens,
			"ceiling":            tokenCeiling,
			"max_tokens_per_run": lim.MaxTokens,
			"multiplier":         mult,
			"source":             source,
		})
		return nil, false
	}

	priorTokens := seed.TotalTokens
	beforeMsgs := len(seed.Messages)
	if compacted, report := e.forceCompactNativeLoopHistory(seed.Messages, seed.SystemPrompt, seed.Chunks); report != nil {
		seed.Messages = compacted
		e.publishAgentLoopEvent("context:lifecycle:compacted", map[string]any{
			"step":             0,
			"before_tokens":    report.BeforeTokens,
			"after_tokens":     report.AfterTokens,
			"rounds_collapsed": report.RoundsCollapsed,
			"messages_removed": report.MessagesRemoved,
			"threshold_ratio":  report.ThresholdRatio,
			"keep_recent":      report.KeepRecentRounds,
			"surface":          "native",
			"phase":            "auto_resume",
		})
	}

	e.publishAgentLoopEvent("agent:loop:auto_resume", map[string]any{
		"resumed_from_step": seed.Step,
		"prior_tokens":      priorTokens,
		"messages_before":   beforeMsgs,
		"messages_after":    len(seed.Messages),
		"cumulative_steps":  seed.CumulativeSteps,
		"cumulative_tokens": seed.CumulativeTokens,
		"step_ceiling":      stepCeiling,
		"token_ceiling":     tokenCeiling,
		"resumes_remaining": stepCeiling - seed.CumulativeSteps,
		"source":            source,
		"surface":           "native",
	})

	seed.Step = 0
	seed.TotalTokens = 0
	return seed, true
}

// defaultResumeMaxMultiplier is the outer ceiling on cumulative agent
// work across every /continue of a single root ask. Each resume gets a
// fresh MaxSteps budget so /continue actually progresses instead of
// instantly re-parking; this multiplier caps the total work one user
// question can spawn. Bumped from 3→10 to support hours-long sustained
// orchestration: with MaxToolSteps=60 default that's 600 cumulative
// steps and ~2.5M cumulative tokens before the engine refuses, which
// fits the "read-edit-verify, repeat for hours" workload without
// letting a runaway model burn unbounded budget. Override per-project
// via cfg.Agent.ResumeMaxMultiplier when even more headroom is needed.
const defaultResumeMaxMultiplier = 10

// resumeMaxMultiplier resolves the active ceiling. Cfg=0 falls back to
// the default; explicit values let high-trust environments lift the cap
// (e.g. an unattended overnight refactor) or tighten it (CI runs that
// must hard-stop after 1 budget).
func (e *Engine) resumeMaxMultiplier() int {
	if e == nil || e.Config == nil || e.Config.Agent.ResumeMaxMultiplier <= 0 {
		return defaultResumeMaxMultiplier
	}
	return e.Config.Agent.ResumeMaxMultiplier
}

// ResumeAgent re-enters the native tool loop from a previously parked state.
// An optional note is appended as a user message before the next round-trip,
// e.g. /continue focus on the auth tests. Returns an error when there is no
// parked loop, or when the cumulative work ceiling has been hit.
func (e *Engine) ResumeAgent(ctx context.Context, note string) (nativeToolCompletion, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	seed := e.takeParkedAgent()
	if seed == nil {
		return nativeToolCompletion{}, fmt.Errorf("no parked agent loop to resume")
	}
	if trimmed := strings.TrimSpace(note); trimmed != "" {
		seed.Messages = append(seed.Messages, provider.Message{
			Role:    types.RoleUser,
			Content: trimmed,
		})
	}
	lim := e.agentLimits()

	// Accumulate cumulative counters from the just-parked run BEFORE we
	// zero out the per-attempt values below. Without this the outer
	// ceiling never engages: every resume starts fresh and the model
	// can park indefinitely.
	seed.CumulativeSteps += seed.Step
	seed.CumulativeTokens += seed.TotalTokens

	// Hard ceiling — refuse further resumes when the model has already
	// burned resumeMaxMultiplier full budgets. We re-park so the user
	// can still inspect the state, but we won't run another round.
	mult := e.resumeMaxMultiplier()
	stepCeiling := lim.MaxSteps * mult
	tokenCeiling := lim.MaxTokens * mult
	if lim.MaxSteps > 0 && seed.CumulativeSteps >= stepCeiling {
		e.saveParkedAgent(seed)
		e.publishAgentLoopEvent("agent:loop:resume_refused", map[string]any{
			"reason":            "cumulative_steps_ceiling",
			"cumulative_steps":  seed.CumulativeSteps,
			"ceiling":           stepCeiling,
			"max_steps_per_run": lim.MaxSteps,
			"multiplier":        mult,
			"surface":           "native",
		})
		return nativeToolCompletion{}, fmt.Errorf(
			"resume refused: cumulative agent steps %d hit ceiling %d (%d x MaxSteps=%d). The model has already had %d full budgets on this question — start a new ask with refined scope, or raise agent.resume_max_multiplier in config",
			seed.CumulativeSteps, stepCeiling, mult, lim.MaxSteps, mult)
	}
	if lim.MaxTokens > 0 && seed.CumulativeTokens >= tokenCeiling {
		e.saveParkedAgent(seed)
		e.publishAgentLoopEvent("agent:loop:resume_refused", map[string]any{
			"reason":             "cumulative_tokens_ceiling",
			"cumulative_tokens":  seed.CumulativeTokens,
			"ceiling":            tokenCeiling,
			"max_tokens_per_run": lim.MaxTokens,
			"multiplier":         mult,
			"surface":            "native",
		})
		return nativeToolCompletion{}, fmt.Errorf(
			"resume refused: cumulative agent tokens %d hit ceiling %d (%d x MaxTokens=%d). The model has already spent %d full token budgets on this question — start a new ask, or raise agent.resume_max_multiplier in config",
			seed.CumulativeTokens, tokenCeiling, mult, lim.MaxTokens, mult)
	}

	// Force-compact the parked history before the next provider round-trip.
	// Without this, resume ships the full fat tool_result transcript back to
	// the provider, which balloons Usage.TotalTokens past the budget on step
	// 1 and re-parks us in a cycle.
	priorTokens := seed.TotalTokens
	if compacted, report := e.forceCompactNativeLoopHistory(seed.Messages, seed.SystemPrompt, seed.Chunks); report != nil {
		seed.Messages = compacted
		e.publishAgentLoopEvent("context:lifecycle:compacted", map[string]any{
			"step":             0,
			"before_tokens":    report.BeforeTokens,
			"after_tokens":     report.AfterTokens,
			"rounds_collapsed": report.RoundsCollapsed,
			"messages_removed": report.MessagesRemoved,
			"threshold_ratio":  report.ThresholdRatio,
			"keep_recent":      report.KeepRecentRounds,
			"surface":          "native",
			"phase":            "resume",
		})
	}

	e.publishAgentLoopEvent("agent:loop:resume", map[string]any{
		"resumed_from_step": seed.Step,
		"max_tool_steps":    lim.MaxSteps,
		"tool_rounds":       len(seed.Traces),
		"tokens_used":       priorTokens,
		"cumulative_steps":  seed.CumulativeSteps,
		"cumulative_tokens": seed.CumulativeTokens,
		"step_ceiling":      stepCeiling,
		"token_ceiling":     tokenCeiling,
		"surface":           "native",
	})
	// Give the resumed loop a fresh cap of MaxSteps *additional* iterations
	// on top of whatever it already consumed. That's what the user typed
	// /continue for — they want more work, not another instant park. Reset
	// TotalTokens too: the budget meters per resume attempt, not cumulative
	// — otherwise every /continue trips MaxTokens on step 1. The outer
	// ceiling above still bounds the total.
	seed.Step = 0
	seed.TotalTokens = 0
	// Use the autonomous wrapper so a /continue that itself parks for
	// budget keeps progressing inside the same call instead of forcing
	// the user to type /continue twice. The cumulative ceiling above
	// still caps total work.
	return e.runNativeToolLoopAutonomous(ctx, seed, lim, "resume")
}
