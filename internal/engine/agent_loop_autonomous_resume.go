// agent_loop_autonomous_resume.go — user-driven /continue entry
// point for the native agent loop. Sibling of
// agent_loop_autonomous.go which keeps the autonomous wrapper
// (runNativeToolLoopAutonomous), the autonomy on/off gate
// (autonomousResumeEnabled), the auto-resume claim/compact/seed
// helper (attemptAutoResume), and the resume-multiplier knob
// (resumeMaxMultiplier + defaultResumeMaxMultiplier).
//
// Splitting ResumeAgent out keeps the wrapper file scoped to the
// "engine resumed itself" path while this file owns the
// "user typed /continue" path: optional note appendage, cumulative
// ceiling enforcement, force-compact before re-entry, and the
// hand-off back to the autonomous wrapper so a /continue that
// itself parks for budget keeps progressing inside the same call.

package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// ResumeAgent re-enters the native tool loop from a previously parked state.
// An optional note is appended as a user message before the next round-trip,
// e.g. /continue focus on the auth tests. Returns an error when there is no
// parked loop, or when the cumulative work ceiling has been hit.
func (e *Engine) ResumeAgent(ctx context.Context, note string, onDelta ...func(string)) (nativeToolCompletion, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	var deltaFn func(string)
	if len(onDelta) > 0 {
		deltaFn = onDelta[0]
	}

	seed := e.takeParkedAgent()
	if seed == nil {
		return nativeToolCompletion{}, ErrNoParkedAgent
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
	return e.runNativeToolLoopAutonomous(ctx, seed, lim, "resume", deltaFn)
}
