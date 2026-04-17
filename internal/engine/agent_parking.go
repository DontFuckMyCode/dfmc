// Park-related types, constants, helpers, and the parkNativeToolLoop
// freeze function. Extracted from agent_loop_native.go to keep the loop
// body focused on iteration and to give park semantics one home.
//
// The native loop has three exit shapes:
//   - finished: returned a natural-language answer
//   - errored: provider/tool error bubbled up
//   - parked: budget or step cap hit, state saved for /continue resume
// Everything in this file deals with the third case.

package engine

import (
	"fmt"

	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// parkPhase distinguishes the two contexts in which the loop reaches
// the budget-exhausted park notice. "before" runs in the preflight
// gate — we know we WOULD overflow if we ran the next round, so the
// notice mentions the headroom we needed but didn't have. "after"
// runs once we've already executed and seen the totalTokens go past
// MaxTokens; headroom is moot at that point.
type parkPhase int

const (
	parkPhaseBefore parkPhase = iota
	parkPhaseAfter
)

// ParkReason discriminates why the native tool loop parked. Stringly-typed
// because it crosses the EventBus and TUI boundary as a payload field — the
// constants below are the canonical set.
type ParkReason string

const (
	ParkReasonStepCap         ParkReason = "step_cap"
	ParkReasonBudgetExhausted ParkReason = "budget_exhausted"
)

// formatBudgetExhaustedNotice renders the "Agent loop parked … tool
// budget exhausted" message that the engine emits when the native
// loop can't run another tool round without busting MaxTokens. The
// same string template appeared in 4 places (preflight headroom-fail
// happy path, preflight headroom-fail catch-all, post-step compact-
// failed path, post-step no-compact path) — fixing the wording in
// one without the others was a regression magnet flagged in the
// REPORT.md walk. headroom is ignored when phase == parkPhaseAfter.
func formatBudgetExhaustedNotice(phase parkPhase, step, tokens, maxTokens, headroom, rounds int) string {
	suffix := "Type /continue to resume with fresh headroom, or add a note to narrow focus — e.g. /continue just finish the test file."
	switch phase {
	case parkPhaseBefore:
		return fmt.Sprintf(
			"Agent loop parked before step %d — tool budget exhausted (~%d/%d tokens, need ~%d headroom, %d rounds). %s",
			step, tokens, maxTokens, headroom, rounds, suffix,
		)
	default:
		return fmt.Sprintf(
			"Agent loop parked after step %d — tool budget exhausted (~%d/%d tokens, %d rounds). %s",
			step, tokens, maxTokens, rounds, suffix,
		)
	}
}

// parkNativeToolLoop freezes the running loop state under a `parkedAgentState`,
// publishes the `agent:loop:parked` event (plus `agent:loop:budget_exhausted`
// when the token budget tripped), and returns a `nativeToolCompletion` whose
// Answer is a friendly notice telling the user to /continue. The helper is
// shared by the two exit paths that used to inline this logic — MaxSteps cap
// and MaxTokens budget — so they behave identically from the UI's point of
// view. The only difference is `reason`, which lands in both events so the
// TUI can distinguish "worked until step cap" from "ran out of headroom".
func (e *Engine) parkNativeToolLoop(
	question string,
	seed *parkedAgentState,
	msgs []provider.Message,
	traces []nativeToolTrace,
	chunks []types.ContextChunk,
	systemPrompt string,
	systemBlocks []provider.SystemBlock,
	descriptors []provider.ToolDescriptor,
	lastProvider, lastModel string,
	totalTokens, step int,
	notice string,
	reason ParkReason,
) nativeToolCompletion {
	lim := e.agentLimits()
	parked := &parkedAgentState{
		Question:         question,
		Messages:         msgs,
		Traces:           traces,
		Chunks:           chunks,
		SystemPrompt:     systemPrompt,
		SystemBlocks:     systemBlocks,
		Descriptors:      descriptors,
		ContextTokens:    seed.ContextTokens,
		TotalTokens:      totalTokens,
		Step:             step,
		LastProvider:     lastProvider,
		LastModel:        lastModel,
		RecentCoachHints: seed.RecentCoachHints,
		// Carry cumulative counters forward across park→resume→park
		// cycles. Without this the ceiling in ResumeAgent never trips:
		// each park rebuilds a fresh parkedAgentState and wipes the
		// cumulative totals that track total work done on this ask.
		CumulativeSteps:  seed.CumulativeSteps,
		CumulativeTokens: seed.CumulativeTokens,
	}
	e.saveParkedAgent(parked)

	if reason == ParkReasonBudgetExhausted {
		// Keep the dedicated telemetry event firing so operators grepping
		// for budget trips still see them. The old behavior returned an
		// error here; parking is strictly more graceful but shouldn't cost
		// us the observability.
		e.publishAgentLoopEvent("agent:loop:budget_exhausted", map[string]any{
			"step":            step,
			"max_tool_steps":  lim.MaxSteps,
			"max_tool_tokens": lim.MaxTokens,
			"tokens_used":     totalTokens,
			"tool_rounds":     len(traces),
			"surface":         "native",
		})
	}
	e.publishAgentLoopEvent("agent:loop:parked", map[string]any{
		"step":            step,
		"max_tool_steps":  lim.MaxSteps,
		"max_tool_tokens": lim.MaxTokens,
		"tool_rounds":     len(traces),
		"tokens_used":     totalTokens,
		"reason":          string(reason),
		"surface":         "native",
	})
	return nativeToolCompletion{
		Answer:       notice,
		Provider:     lastProvider,
		Model:        lastModel,
		TokenCount:   totalTokens,
		Context:      chunks,
		ToolTraces:   traces,
		SystemPrompt: systemPrompt,
		Parked:       true,
		ParkedAtStep: step,
		ParkedReason: reason,
	}
}
