// agent_loop_autonomous.go — autonomous-resume wrapper for the
// native agent loop. Sibling: agent_loop_autonomous_resume.go owns
// the public user-driven /continue entry point (ResumeAgent).
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
//
// Extracted from agent_loop_native.go to keep the main loop file
// focused on the round-by-round control flow.

package engine

import (
	"context"
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
func (e *Engine) runNativeToolLoopAutonomous(ctx context.Context, seed *parkedAgentState, lim agentLimits, source string, onDelta func(string)) (nativeToolCompletion, error) {
	const safetyBound = 64 // belt-and-braces; cumulative ceiling is the real cap
	for attempt := 0; attempt < safetyBound; attempt++ {
		if ctx.Err() != nil {
			return nativeToolCompletion{}, ctx.Err()
		}
		completion, err := e.runNativeToolLoop(ctx, seed, lim, onDelta)
		if err != nil || !completion.Parked {
			return completion, err
		}
		if completion.ParkedReason != ParkReasonBudgetExhausted && completion.ParkedReason != ParkReasonStepCap {
			// Shutdown and interrupted parks deliberately surface to the
			// user: shutdown means the engine is going away, interrupted
			// means a caller cancelled this run. Budget and step caps are
			// local loop ceilings, so autonomous mode can compact and
			// continue until the cumulative guard trips.
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
	res, err := e.runNativeToolLoop(ctx, seed, lim, onDelta)
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

	resumePrompt := e.buildAutonomousResumePrompt(seed)
	seed.Messages = append(seed.Messages, provider.Message{
		Role:    types.RoleUser,
		Content: resumePrompt,
	})

	e.publishAgentLoopEvent("agent:loop:auto_resume", map[string]any{
		"resumed_from_step":   seed.Step,
		"prior_tokens":        priorTokens,
		"messages_before":     beforeMsgs,
		"messages_after":      len(seed.Messages),
		"cumulative_steps":    seed.CumulativeSteps,
		"cumulative_tokens":   seed.CumulativeTokens,
		"step_ceiling":        stepCeiling,
		"token_ceiling":       tokenCeiling,
		"resumes_remaining":   stepCeiling - seed.CumulativeSteps,
		"source":              source,
		"surface":             "native",
		"continuation_prompt": resumePrompt,
	})

	seed.Step = 0
	seed.TotalTokens = 0
	return seed, true
}

// buildAutonomousResumePrompt generates a continuation user message from the
// parked state so the model has clear direction after compaction, rather than
// guessing from compressed history alone.
func (e *Engine) buildAutonomousResumePrompt(seed *parkedAgentState) string {
	var b strings.Builder
	b.WriteString("[DFMC autonomous continuation]\n")
	b.WriteString("The agent loop was paused due to budget limits. Continue the task from where you left off.\n\n")
	if q := strings.TrimSpace(seed.Question); q != "" {
		b.WriteString("Original task: ")
		b.WriteString(truncateRunesWithMarker(q, 300, "…"))
		b.WriteString("\n")
	}
	if summary := summarizeTraces(seed.Traces); summary != "" {
		b.WriteString("Progress so far: ")
		b.WriteString(summary)
		b.WriteString("\n")
	}
	b.WriteString("\nContinue using tools as needed. End with [done: true] when the task is complete.\n")
	return b.String()
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

// ResumeAgent lives in agent_loop_autonomous_resume.go.
