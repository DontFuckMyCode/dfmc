// Per-iteration phase helpers extracted from runNativeToolLoop. Each
// helper handles one bounded phase of the loop body so the main loop
// reads as orchestration instead of a 400-line if-cascade. Splitting
// here is mechanical — every helper preserves the exact event payload,
// publish order, and side-effect sequence the loop had inline.
//
// Park sentinel pattern: budget gates return *nativeToolCompletion. When
// non-nil the caller MUST `return *parked, nil` from the loop. Every
// other return is "keep iterating with these updated values."

package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	ctxmgr "github.com/dontfuckmycode/dfmc/internal/context"
	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// preflightBudget runs the pre-round budget gate. If we'd consume more
// tokens than the headroom allows, try one auto-compact recovery; if
// that fails (or we're out of recoveries), park. Returns updated msgs,
// totalTokens, autoRecoveries, and a non-nil park sentinel only when
// the caller must return immediately.
func (e *Engine) preflightBudget(
	seed *parkedAgentState,
	msgs []provider.Message,
	traces []nativeToolTrace,
	chunks []types.ContextChunk,
	systemPrompt string,
	systemBlocks []provider.SystemBlock,
	descriptors []provider.ToolDescriptor,
	question, lastProvider, lastModel string,
	totalTokens, step, autoRecoveries int,
	lim agentLimits,
) ([]provider.Message, int, int, *nativeToolCompletion) {
	if lim.MaxTokens <= 0 {
		return msgs, totalTokens, autoRecoveries, nil
	}
	headroom := lim.MaxTokens / lim.BudgetHeadroomDivisor
	if totalTokens+headroom < lim.MaxTokens {
		return msgs, totalTokens, autoRecoveries, nil
	}
	if autoRecoveries < maxBudgetAutoRecoveries {
		if compacted, report := e.forceCompactNativeLoopHistory(msgs, systemPrompt, chunks); report != nil && report.MessagesRemoved > 0 {
			before := totalTokens
			autoRecoveries++
			e.publishAgentLoopEvent("agent:loop:auto_recover", map[string]any{
				"step":             step,
				"attempt":          autoRecoveries,
				"max_attempts":     maxBudgetAutoRecoveries,
				"tokens_before":    before,
				"rounds_collapsed": report.RoundsCollapsed,
				"messages_removed": report.MessagesRemoved,
				"reason":           "budget_headroom_preflight",
				"surface":          "native",
			})
			return compacted, 0, autoRecoveries, nil
		}
	}
	notice := formatBudgetExhaustedNotice(parkPhaseBefore, step, totalTokens, lim.MaxTokens, headroom, len(traces))
	parked := e.parkNativeToolLoop(question, seed, msgs, traces, chunks, systemPrompt, systemBlocks, descriptors, lastProvider, lastModel, totalTokens, step, notice, ParkReasonBudgetExhausted)
	return msgs, totalTokens, autoRecoveries, &parked
}

// postStepBudget runs the after-round budget gate. Same recovery-then-
// park pattern as preflightBudget but uses the parkPhaseAfter notice
// (no headroom mention because we already overshot).
func (e *Engine) postStepBudget(
	seed *parkedAgentState,
	msgs []provider.Message,
	traces []nativeToolTrace,
	chunks []types.ContextChunk,
	systemPrompt string,
	systemBlocks []provider.SystemBlock,
	descriptors []provider.ToolDescriptor,
	question, lastProvider, lastModel string,
	totalTokens, step, autoRecoveries int,
	lim agentLimits,
) ([]provider.Message, int, int, *nativeToolCompletion) {
	if lim.MaxTokens <= 0 || totalTokens < lim.MaxTokens {
		return msgs, totalTokens, autoRecoveries, nil
	}
	if autoRecoveries < maxBudgetAutoRecoveries {
		if compacted, report := e.forceCompactNativeLoopHistory(msgs, systemPrompt, chunks); report != nil && report.MessagesRemoved > 0 {
			before := totalTokens
			autoRecoveries++
			e.publishAgentLoopEvent("agent:loop:auto_recover", map[string]any{
				"step":             step,
				"attempt":          autoRecoveries,
				"max_attempts":     maxBudgetAutoRecoveries,
				"tokens_before":    before,
				"rounds_collapsed": report.RoundsCollapsed,
				"messages_removed": report.MessagesRemoved,
				"reason":           "budget_exhausted",
				"surface":          "native",
			})
			return compacted, 0, autoRecoveries, nil
		}
	}
	notice := formatBudgetExhaustedNotice(parkPhaseAfter, step, totalTokens, lim.MaxTokens, 0, len(traces))
	parked := e.parkNativeToolLoop(question, seed, msgs, traces, chunks, systemPrompt, systemBlocks, descriptors, lastProvider, lastModel, totalTokens, step, notice, ParkReasonBudgetExhausted)
	return msgs, totalTokens, autoRecoveries, &parked
}

// handleEmptyTurn deals with the "model returned no tool calls AND no
// text" case. First time: push a synthesis nudge and signal retry.
// Second time: build a visible failure completion and return it. Caller
// only invokes this when len(resp.ToolCalls)==0 && resp.Text=="". The
// returned completion is non-nil iff the loop must return now.
func (e *Engine) handleEmptyTurn(
	question string,
	msgs []provider.Message,
	traces []nativeToolTrace,
	resp *provider.CompletionResponse,
	chunks []types.ContextChunk,
	systemPrompt string,
	lastProvider, lastModel string,
	step, totalTokens int,
	emptyRecoveryTried bool,
) ([]provider.Message, bool, *nativeToolCompletion) {
	if !emptyRecoveryTried {
		msgs = append(msgs, provider.Message{
			Role:      types.RoleAssistant,
			Content:   resp.Text,
			ToolCalls: resp.ToolCalls,
		})
		msgs = append(msgs, provider.Message{
			Role: types.RoleUser,
			Content: "[system] Your previous response was empty. Please provide a natural-language answer to the original question based on the context you've gathered. " +
				"If you genuinely cannot answer, say so explicitly — do not return an empty response.",
		})
		e.publishAgentLoopEvent("agent:loop:empty_recovery", map[string]any{
			"step":        step,
			"tool_rounds": len(traces),
			"tokens_used": totalTokens,
			"surface":     "native",
		})
		return msgs, true, nil
	}
	completion := nativeToolCompletion{
		Answer: "The model returned an empty response twice in a row even after an explicit synthesis nudge. " +
			"Try rephrasing the question or `/continue` with a narrower scope.",
		Provider:     lastProvider,
		Model:        lastModel,
		TokenCount:   totalTokens,
		Context:      chunks,
		ToolTraces:   traces,
		SystemPrompt: systemPrompt,
	}
	e.recordNativeAgentInteraction(question, completion)
	e.publishAgentLoopEvent("agent:loop:empty_final", map[string]any{
		"step":        step,
		"tool_rounds": len(traces),
		"tokens_used": totalTokens,
		"surface":     "native",
	})
	return msgs, false, &completion
}

// executeAndAppendToolBatch runs every tool call from the assistant's
// turn (in parallel when safe), formats results within the elastic
// char caps, dedupes prior identical results, and appends both the
// assistant message and tool_result messages to msgs. Returns the
// updated msgs, traces, and the index in traces where this round's
// new entries start (so the trajectory-hints helper can scope to the
// just-run batch).
func (e *Engine) executeAndAppendToolBatch(
	ctx context.Context,
	resp *provider.CompletionResponse,
	msgs []provider.Message,
	traces []nativeToolTrace,
	lastProvider, lastModel string,
	step, totalTokens int,
	lim agentLimits,
) ([]provider.Message, []nativeToolTrace, int) {
	msgs = append(msgs, provider.Message{
		Role:      types.RoleAssistant,
		Content:   resp.Text,
		ToolCalls: resp.ToolCalls,
	})
	freshStart := len(traces)

	stepTraces := make([]nativeToolTrace, len(resp.ToolCalls))
	for i, call := range resp.ToolCalls {
		stepTraces[i] = nativeToolTrace{
			Call:       call,
			Provider:   lastProvider,
			Model:      lastModel,
			Step:       step,
			OccurredAt: time.Now(),
		}
		e.publishNativeToolCall(stepTraces[i])
	}

	batchSize := 1
	if allParallelSafe(resp.ToolCalls) {
		batchSize = e.parallelBatchSize()
	}
	results := e.executeToolCallsParallel(ctx, resp.ToolCalls, batchSize)

	// When we're already deep in the budget, halve the per-tool char
	// caps so new results don't accelerate bloat.
	effectiveMaxResult := lim.MaxResultChars
	effectiveMaxData := lim.MaxDataChars
	if lim.MaxTokens > 0 && totalTokens*2 >= lim.MaxTokens {
		if effectiveMaxResult > 0 {
			effectiveMaxResult /= 2
		}
		if effectiveMaxData > 0 {
			effectiveMaxData /= 2
		}
	}

	for i, call := range resp.ToolCalls {
		r := results[i]
		trace := stepTraces[i]
		if r.Err != nil {
			trace.Err = r.Err.Error()
		} else {
			trace.Result = r.Result
		}

		content, isErr := formatNativeToolResultPayloadWithLimits(r.Result, r.Err, effectiveMaxResult, effectiveMaxData)
		e.publishNativeToolResultWithPayload(trace, content)
		traces = append(traces, trace)

		// Cross-round dedup: replace any prior identical (name, input)
		// tool_result with a one-line stub. ToolCallID chains must stay
		// intact, so we shrink Content rather than removing the message.
		if prev := findPriorIdenticalToolResult(msgs, call, call.ID); prev >= 0 {
			if len(msgs[prev].Content) > toolResultDedupStubBytes {
				msgs[prev].Content = fmt.Sprintf(
					"[deduped — identical %s call appears again in a later round; see that result for the current payload]",
					call.Name,
				)
			}
		}

		msgs = append(msgs, provider.Message{
			Role:       types.RoleUser,
			Content:    content,
			ToolCallID: call.ID,
			ToolName:   call.Name,
			ToolError:  isErr,
		})
	}

	return msgs, traces, freshStart
}

// injectTrajectoryHints derives coach hints from the just-run batch and
// appends them as a system-tagged user note. Updates seed.RecentCoachHints
// in place so the same hint can't fire twice in a single run.
func (e *Engine) injectTrajectoryHints(
	seed *parkedAgentState,
	msgs []provider.Message,
	traces []nativeToolTrace,
	freshStart, step int,
) []provider.Message {
	hints := buildTrajectoryHints(traces[freshStart:], traces, seed.RecentCoachHints)
	if len(hints) == 0 {
		return msgs
	}
	block := ctxmgr.FormatTrajectoryHints(hints)
	if strings.TrimSpace(block) == "" {
		return msgs
	}
	msgs = append(msgs, provider.Message{
		Role:    types.RoleUser,
		Content: block,
	})
	seed.RecentCoachHints = appendRecentHints(seed.RecentCoachHints, hints)
	e.publishAgentLoopEvent("agent:coach:hint", map[string]any{
		"step":  step,
		"hints": hints,
	})
	return msgs
}
