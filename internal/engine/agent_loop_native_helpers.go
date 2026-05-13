package engine

// agent_loop_native_helpers.go — small per-round helpers for the
// native-tool agent loop. Sibling to agent_loop_native.go which owns
// runNativeToolLoop itself; phase helpers (preflightBudget /
// postStepBudget / executeAndAppendToolBatch / handleEmptyTurn /
// injectTrajectoryHints) live in agent_loop_phases.go.
//
//   - applyNativeBudgetCompaction   reactive (0.7) + proactive (0.5)
//                                   compaction of s.msgs, with
//                                   context:lifecycle:* events.
//   - computeNativeToolChoice       picks "auto" vs "none" for the
//                                   next provider round (round-cap
//                                   ceiling + stuck-streak hardstop).
//   - buildNativeLoopRequest        assembles the CompletionRequest
//                                   carrying last-known provider/model
//                                   so a router fallback sticks.
//   - updateNativeTokenFootprint    replaces s.totalTokens with the
//                                   rolling per-round footprint, NOT
//                                   a cumulative sum (would double-
//                                   count the growing history).

import (
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/provider"
)

// applyNativeBudgetCompaction runs the reactive (0.7 ratio) and the
// proactive (0.5 ratio, only past the soft round cap) compactors on the
// loop's running message buffer, replacing s.msgs in place when either
// one collapsed history. Publishes context:lifecycle:* events with the
// before/after token snapshot so the TUI lifecycle ribbon stays in sync.
func (e *Engine) applyNativeBudgetCompaction(s *loopRunState, step int) {
	if compacted, report := e.maybeCompactNativeLoopHistoryForBudget(s.msgs, s.systemPrompt, s.chunks, s.lim.MaxTokens); report != nil {
		s.msgs = compacted
		e.publishAgentLoopEvent("context:lifecycle:compacted", map[string]any{
			"step":             step,
			"before_tokens":    report.BeforeTokens,
			"after_tokens":     report.AfterTokens,
			"rounds_collapsed": report.RoundsCollapsed,
			"messages_removed": report.MessagesRemoved,
			"threshold_ratio":  report.ThresholdRatio,
			"keep_recent":      report.KeepRecentRounds,
			"surface":          "native",
		})
	}

	// Proactive step-boundary compaction. Once we're past the soft round
	// cap (15 by default), drop the threshold so old rounds get collapsed
	// before headroom crashes. The reactive compactor above uses 0.7;
	// this one uses 0.5 — fires earlier so a long sustained loop never
	// has to pay an emergency park. No-op when the loop is short or the
	// lifecycle is disabled.
	if step > s.lim.RoundSoftCap {
		if compacted, report := e.proactiveCompactNativeLoopHistory(s.msgs, s.systemPrompt, s.chunks, s.lim.MaxTokens); report != nil {
			s.msgs = compacted
			e.publishAgentLoopEvent("context:lifecycle:proactive_compacted", map[string]any{
				"step":             step,
				"before_tokens":    report.BeforeTokens,
				"after_tokens":     report.AfterTokens,
				"rounds_collapsed": report.RoundsCollapsed,
				"messages_removed": report.MessagesRemoved,
				"threshold_ratio":  proactiveCompactRatio,
				"surface":          "native",
			})
		}
	}
}

// computeNativeToolChoice picks the tool_choice string for the next
// provider round. Defaults to "auto"; switches to "none" once the
// hard round cap is reached, OR when the trajectory layer has flagged
// the same failure pattern for stuckStreakHardstopThreshold consecutive
// rounds (the model is ignoring the switch-tactic hint and grinding on
// the same broken approach — force a text response so it explains the
// blocker or hands back).
func (e *Engine) computeNativeToolChoice(s *loopRunState, step int) string {
	if len(s.traces) >= s.lim.RoundHardCap {
		e.publishAgentLoopEvent("agent:loop:tools_force_stop", map[string]any{
			"step":        step,
			"tool_rounds": len(s.traces),
			"hard_cap":    s.lim.RoundHardCap,
			"surface":     "native",
		})
		return "none"
	}
	if s.stuckStreak >= stuckStreakHardstopThreshold {
		e.publishAgentLoopEvent("agent:loop:stuck_force_stop", map[string]any{
			"step":         step,
			"stuck_streak": s.stuckStreak,
			"threshold":    stuckStreakHardstopThreshold,
			"surface":      "native",
		})
		return "none"
	}
	return "auto"
}

// buildNativeLoopRequest assembles a CompletionRequest for the next
// provider round. Picks last-known provider/model when set (so a
// router fallback sticks) and falls back to engine defaults otherwise.
func (e *Engine) buildNativeLoopRequest(s *loopRunState, toolChoice string) provider.CompletionRequest {
	reqProvider := strings.TrimSpace(s.lastProvider)
	if reqProvider == "" {
		reqProvider = e.provider()
	}
	reqModel := strings.TrimSpace(s.lastModel)
	if reqModel == "" {
		reqModel = strings.TrimSpace(e.modelForProvider(reqProvider))
	}
	if reqModel == "" && e.Providers != nil {
		if selected, ok := e.Providers.Get(reqProvider); ok && selected != nil {
			reqModel = strings.TrimSpace(selected.Model())
		}
	}
	return provider.CompletionRequest{
		Provider:     reqProvider,
		Model:        reqModel,
		System:       s.systemPrompt,
		SystemBlocks: s.systemBlocks,
		Context:      s.chunks,
		Messages:     s.msgs,
		Tools:        s.descriptors,
		ToolChoice:   toolChoice,
	}
}

// updateNativeTokenFootprint replaces s.totalTokens with the latest
// per-round footprint reported by the provider. totalTokens is the
// rolling conversation footprint as seen by the provider, NOT a
// cumulative sum across rounds. Summing per-round Usage.TotalTokens
// double-counted the growing history every iteration — a modest 20-
// round loop that never left a 25k working set would trip a 250k
// "budget" purely from re-counting the same prompt tokens. Using the
// latest InputTokens+OutputTokens makes the metric track real
// footprint and correctly shrink after auto-compact trims history.
// Falls back to TotalTokens for OpenAI-compatible endpoints that drop
// the per-direction split.
func updateNativeTokenFootprint(s *loopRunState, resp *provider.CompletionResponse) {
	if resp == nil {
		return
	}
	if footprint := resp.Usage.InputTokens + resp.Usage.OutputTokens; footprint > 0 {
		s.totalTokens = footprint
	} else if resp.Usage.TotalTokens > 0 {
		s.totalTokens = resp.Usage.TotalTokens
	}
}
