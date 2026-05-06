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

// executeAndAppendToolBatch runs every tool call from the assistant's
// turn (in parallel when safe), formats results within the elastic
// char caps, dedupes prior identical results, and appends both the
// assistant message and tool_result messages to s.msgs. Mutates s.msgs
// and s.traces in place. Returns the index in s.traces where this
// round's new entries start (so the trajectory-hints helper can scope
// to the just-run batch).
func (e *Engine) executeAndAppendToolBatch(
	ctx context.Context,
	s *loopRunState,
	resp *provider.CompletionResponse,
) int {
	cache := s.seed.LoopFileCache
	rangeIndex := s.seed.LoopReadRangeIndex

	s.msgs = append(s.msgs, provider.Message{
		Role:      types.RoleAssistant,
		Content:   resp.Text,
		ToolCalls: resp.ToolCalls,
	})
	freshStart := len(s.traces)

	stepTraces := make([]nativeToolTrace, len(resp.ToolCalls))
	for i, call := range resp.ToolCalls {
		stepTraces[i] = nativeToolTrace{
			Call:       call,
			Provider:   s.lastProvider,
			Model:      s.lastModel,
			Step:       s.step,
			OccurredAt: time.Now(),
		}
		e.publishNativeToolCall(stepTraces[i])
	}

	batchSize := 1
	if allParallelSafe(resp.ToolCalls) {
		batchSize = e.parallelBatchSize()
	}
	results := e.executeToolCallsParallel(ctx, resp.ToolCalls, batchSize, s.seed.ToolSource, cache, s.cacheMu, rangeIndex)

	// File cache invalidation. After a batch that includes successful
	// edit_file/write_file/apply_patch calls, drop any cached read_file/
	// list_dir/grep_codebase entries that touched the same path so the
	// next read in the loop sees fresh content. Without this, a sub-agent
	// edits foo.go, the parent re-reads foo.go, and gets the cached pre-
	// edit body — tracking down "why does the model think the file still
	// has the bug" wastes a turn or three. Only paths whose call returned
	// without error count, so a refused edit (read-gate, approval) doesn't
	// invalidate anything.
	if cache != nil {
		modified := make([]string, 0, len(resp.ToolCalls))
		for i, call := range resp.ToolCalls {
			if results[i].Err != nil {
				continue
			}
			if p := extractModifiedPath(call); p != "" {
				modified = append(modified, p)
			}
		}
		if len(modified) > 0 {
			invalidateCacheForFiles(cache, s.cacheMu, modified, rangeIndex)
		}
	}

	// When we're already deep in the budget, halve the per-tool char
	// caps so new results don't accelerate bloat.
	effectiveMaxResult := s.lim.MaxResultChars
	effectiveMaxData := s.lim.MaxDataChars
	if s.lim.MaxTokens > 0 && s.totalTokens*2 >= s.lim.MaxTokens {
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
		// Skill-scoped tool policy: when an active skill constrains this tool,
		// surface the guidance so the model can self-correct rather than guessing.
		// Prefers are soft nudges; Allowed-without-Allowed is a stronger signal.
		if policy := e.skillToolPolicy(call.Name); policy != "" {
			content = policy + "\n\n" + content
		}
		e.publishNativeToolResultWithPayload(trace, content)
		s.traces = append(s.traces, trace)

		// Cross-round dedup: replace any prior identical (name, input)
		// tool_result with a back-reference stub. ToolCallID chains
		// must stay intact, so we shrink Content rather than removing
		// the message.
		//
		// The stub names the re-call's target (path / pattern / etc)
		// when we can derive one — pre-fix the bare "[deduped — see
		// later result]" gave the model no anchor for what it had
		// originally read; it had to scan forward to find the same
		// call. With the target inlined the model recognises "this
		// was the read of foo.go I did earlier; the current result
		// is the same payload" without re-walking the transcript.
		if prev := findPriorIdenticalToolResult(s.msgs, call, call.ID); prev >= 0 {
			if len(s.msgs[prev].Content) > toolResultDedupStubBytes {
				target := dedupTargetHint(call)
				s.msgs[prev].Content = fmt.Sprintf(
					"[deduped — same %s%s call below; payload moved to the latest result so reasoning stays current]",
					call.Name, target,
				)
			}
		}

		s.msgs = append(s.msgs, provider.Message{
			Role:       types.RoleUser,
			Content:    content,
			ToolCallID: call.ID,
			ToolName:   call.Name,
			ToolError:  isErr,
		})
	}

	return freshStart
}

// clipPathsForEvent caps a path slice for event-payload inclusion. We
// don't want to ship 100 paths into every event when the count is
// large; subscribers only need a representative sample for "show me a
// few of them". The full list lives in the trajectory output struct.
func clipPathsForEvent(paths []string, max int) []string {
	if len(paths) <= max {
		out := make([]string, len(paths))
		copy(out, paths)
		return out
	}
	out := make([]string, 0, max)
	out = append(out, paths[:max]...)
	return out
}

// dedupTargetHint returns " (target)" — a short, paren-wrapped
// identifier for the deduped call so the back-reference stub can name
// the file/pattern/command instead of the opaque tool name alone.
// Reuses the same priority order as the live TUI batch-inner preview
// for cross-surface consistency. Empty string when no identifying arg
// is available — caller emits the bare tool name without parens.
func dedupTargetHint(call provider.ToolCall) string {
	input := call.Input
	if name, _ := input["name"].(string); name != "" {
		if inner, ok := input["args"].(map[string]any); ok {
			input = inner
		}
	}
	for _, key := range []string{"path", "pattern", "query", "command", "dir", "url"} {
		if raw, ok := input[key]; ok {
			value := strings.TrimSpace(fmt.Sprint(raw))
			if value == "" {
				continue
			}
			if len(value) > 60 {
				value = value[:57] + "..."
			}
			return " (" + value + ")"
		}
	}
	return ""
}

// injectTrajectoryHints derives coach hints from the just-run batch and
// appends them as a system-tagged user note. Mutates s.msgs and
// s.seed.RecentCoachHints in place so the same hint can't fire twice
// in a single run.
//
// Two events fan out from one detection pass:
//   - agent:coach:hint — list of advisory lines (silent when verbose=off)
//   - agent:coach:stuck — first-class loop-stall signal, fires whenever
//     the trajectory layer detected the repeated-failure pattern, even
//     when the textual hint was deduped out. The TUI / web feed surface
//     this one by default because it's the signal you want when staring
//     at a long autonomous run wondering "is it making progress?".
func (e *Engine) injectTrajectoryHints(s *loopRunState, freshStart int) {
	hints := buildTrajectoryHints(s.traces[freshStart:], s.traces, s.seed.RecentCoachHints)
	if hints == nil {
		return
	}
	if hints.StuckTool != "" {
		e.publishAgentLoopEvent("agent:coach:stuck", map[string]any{
			"step":              s.step,
			"tool":              hints.StuckTool,
			"failure_count":     hints.StuckCount,
			"error_class":       hints.StuckErrSample,
			"hint_text_emitted": len(hints.Hints) > 0,
			"surface":           "native",
		})
	}
	// Unverified-mutations escalation. The TUI's always-visible
	// "unverified: N" badge shows the count via its own tool:result
	// counting; this event correlates the badge with a transcript
	// notice the FIRST round Rule 2 takes its directive form, so the
	// user sees a matching warn line right when the engine has
	// effectively told the model "STOP editing, validate now".
	if hints.UnverifiedEscalated {
		e.publishAgentLoopEvent("agent:coach:unverified", map[string]any{
			"step":         s.step,
			"file_count":   hints.UnverifiedCount,
			"sample_paths": clipPathsForEvent(hints.UnverifiedPaths, 4),
			"surface":      "native",
		})
	}
	if len(hints.Hints) == 0 {
		return
	}
	block := ctxmgr.FormatTrajectoryHints(hints)
	if strings.TrimSpace(block) == "" {
		return
	}
	s.msgs = append(s.msgs, provider.Message{
		Role:    types.RoleUser,
		Content: block,
	})
	s.seed.RecentCoachHints = appendRecentHints(s.seed.RecentCoachHints, hints.Hints)
	e.publishAgentLoopEvent("agent:coach:hint", map[string]any{
		"step":  s.step,
		"hints": hints.Hints,
	})
}
