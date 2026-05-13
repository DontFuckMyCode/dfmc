// Per-iteration phase helpers extracted from runNativeToolLoop. Each
// helper handles one bounded phase of the loop body so the main loop
// reads as orchestration instead of a 400-line if-cascade. Splitting
// here is mechanical — every helper preserves the exact event payload,
// publish order, and side-effect sequence the loop had inline.
//
// Park sentinel pattern: budget gates return *nativeToolCompletion. When
// non-nil the caller MUST `return *parked, nil` from the loop. Every
// other return is "keep iterating with these updated values."
//
// This file owns the budget gates (preflightBudget / postStepBudget /
// tryBudgetAutoRecover) + the empty-turn recovery (handleEmptyTurn).
// The tool-batch execution + trajectory-hint phases
// (executeAndAppendToolBatch, injectTrajectoryHints, dedupTargetHint,
// clipPathsForEvent) live in agent_loop_phases_batch.go.

package engine

import (
	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// tryBudgetAutoRecover attempts one force-compact pass against the loop
// history. On success, mutates s.msgs and resets s.totalTokens to 0,
// bumps s.autoRecoveries, publishes an agent:loop:auto_recover event
// tagged with `reasonTag`, and returns true. Returns false when out of
// recoveries OR the compactor couldn't shrink the history further.
// Shared by preflightBudget and postStepBudget — the only difference
// between their recovery paths was the reason tag.
func (e *Engine) tryBudgetAutoRecover(s *loopRunState, reasonTag string) bool {
	if s.autoRecoveries >= maxBudgetAutoRecoveries {
		return false
	}
	compacted, report := e.forceCompactNativeLoopHistory(s.msgs, s.systemPrompt, s.chunks)
	if report == nil || report.MessagesRemoved == 0 {
		return false
	}
	before := s.totalTokens
	s.autoRecoveries++
	s.msgs = compacted
	s.totalTokens = 0
	e.publishAgentLoopEvent("agent:loop:auto_recover", map[string]any{
		"step":             s.step,
		"attempt":          s.autoRecoveries,
		"max_attempts":     maxBudgetAutoRecoveries,
		"tokens_before":    before,
		"rounds_collapsed": report.RoundsCollapsed,
		"messages_removed": report.MessagesRemoved,
		"reason":           reasonTag,
		"surface":          "native",
	})
	return true
}

// preflightBudget runs the pre-round budget gate. If we'd consume more
// tokens than the headroom allows, try one auto-compact recovery; if
// that fails (or we're out of recoveries), park. Mutates s.msgs and
// s.autoRecoveries on recovery (resets s.totalTokens to 0). Returns a
// non-nil park sentinel only when the caller must return immediately.
func (e *Engine) preflightBudget(s *loopRunState) *nativeToolCompletion {
	if s.lim.MaxTokens <= 0 {
		return nil
	}
	headroom := s.lim.MaxTokens / s.lim.BudgetHeadroomDivisor
	if s.totalTokens+headroom < s.lim.MaxTokens {
		return nil
	}
	if e.tryBudgetAutoRecover(s, "budget_headroom_preflight") {
		return nil
	}
	headline := formatBudgetExhaustedNotice(parkPhaseBefore, s.step, s.totalTokens, s.lim.MaxTokens, headroom, len(s.traces))
	notice := composeParkedNotice(headline, s.traces, "")
	parked := s.park(e, notice, ParkReasonBudgetExhausted)
	return &parked
}

// postStepBudget runs the after-round budget gate. Same recovery-then-
// park pattern as preflightBudget but uses the parkPhaseAfter notice
// (no headroom mention because we already overshot).
func (e *Engine) postStepBudget(s *loopRunState) *nativeToolCompletion {
	if s.lim.MaxTokens <= 0 || s.totalTokens < s.lim.MaxTokens {
		return nil
	}
	if e.tryBudgetAutoRecover(s, "budget_exhausted") {
		return nil
	}
	headline := formatBudgetExhaustedNotice(parkPhaseAfter, s.step, s.totalTokens, s.lim.MaxTokens, 0, len(s.traces))
	notice := composeParkedNotice(headline, s.traces, "")
	parked := s.park(e, notice, ParkReasonBudgetExhausted)
	return &parked
}

// handleEmptyTurn deals with the "model returned no tool calls AND no
// text" case. First time: push a synthesis nudge to s.msgs and return
// (true, nil) so the caller flips the recovery flag. Second time:
// build a visible failure completion and return it. Caller only
// invokes this when len(resp.ToolCalls)==0 && resp.Text=="". The
// returned completion is non-nil iff the loop must return now.
func (e *Engine) handleEmptyTurn(
	s *loopRunState,
	resp *provider.CompletionResponse,
	emptyRecoveryTried bool,
) (bool, *nativeToolCompletion) {
	if !emptyRecoveryTried {
		s.msgs = append(s.msgs, provider.Message{
			Role:      types.RoleAssistant,
			Content:   resp.Text,
			ToolCalls: resp.ToolCalls,
		})
		s.msgs = append(s.msgs, provider.Message{
			Role: types.RoleUser,
			Content: "[system] Your previous response was empty. Please provide a natural-language answer to the original question based on the context you've gathered. " +
				"If you genuinely cannot answer, say so explicitly — do not return an empty response.",
		})
		e.publishAgentLoopEvent("agent:loop:empty_recovery", map[string]any{
			"step":        s.step,
			"tool_rounds": len(s.traces),
			"tokens_used": s.totalTokens,
			"surface":     "native",
		})
		return true, nil
	}
	completion := nativeToolCompletion{
		Answer: "The model returned an empty response twice in a row even after an explicit synthesis nudge. " +
			"Try rephrasing the question or `/continue` with a narrower scope.",
		Provider:     s.lastProvider,
		Model:        s.lastModel,
		TokenCount:   s.totalTokens,
		Context:      s.chunks,
		ToolTraces:   s.traces,
		SystemPrompt: s.systemPrompt,
	}
	e.recordNativeAgentInteraction(s.question, completion)
	e.publishAgentLoopEvent("agent:loop:empty_final", map[string]any{
		"step":        s.step,
		"tool_rounds": len(s.traces),
		"tokens_used": s.totalTokens,
		"surface":     "native",
	})
	return false, &completion
}
