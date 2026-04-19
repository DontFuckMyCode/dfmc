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
	"sort"
	"strings"

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
	// ParkReasonShuttingDown fires when the loop detects the engine
	// transitioned to StateShuttingDown (or beyond) between rounds.
	// We park rather than racing teardown — bbolt close mid-write is a
	// panic, and the user gets to /continue from a fresh boot.
	// REPORT.md #9.
	ParkReasonShuttingDown ParkReason = "shutting_down"
)

// summarizeTraces walks the parked loop's tool traces and produces a
// short, signal-dense "Did:" line plus an "Open:" hint based on the
// last tool that ran. The point is to give the user — staring at a
// terse "parked at step N" notice — enough context to know whether to
// /continue, redirect, or abandon. Pure derivation: no extra LLM call,
// no events, just a scan of the in-memory traces we already have.
//
// Output shape (single line, joined with " · "):
//
//	"Did: read_file×4, edit_file×2, run_command×1 · Open: agent paused mid-edit_file"
//
// When traces is empty (loop parked before any tool ran — rare but
// possible from preflight budget gates) returns an empty string so
// the caller can skip the section without an awkward "Did: nothing".
func summarizeTraces(traces []nativeToolTrace) string {
	if len(traces) == 0 {
		return ""
	}
	counts := map[string]int{}
	for _, t := range traces {
		name := strings.TrimSpace(t.Call.Name)
		if name == "" {
			name = "unknown"
		}
		counts[name]++
	}
	type kv struct {
		name string
		n    int
	}
	rows := make([]kv, 0, len(counts))
	for k, v := range counts {
		rows = append(rows, kv{k, v})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].n != rows[j].n {
			return rows[i].n > rows[j].n
		}
		return rows[i].name < rows[j].name
	})
	parts := make([]string, 0, len(rows))
	for i, r := range rows {
		if i >= 4 {
			parts = append(parts, fmt.Sprintf("+%d more", len(rows)-4))
			break
		}
		if r.n == 1 {
			parts = append(parts, r.name)
		} else {
			parts = append(parts, fmt.Sprintf("%s×%d", r.name, r.n))
		}
	}
	did := "Did: " + strings.Join(parts, ", ")
	last := strings.TrimSpace(traces[len(traces)-1].Call.Name)
	open := "Open: paused after " + last
	if last == "" {
		open = "Open: paused after final tool call"
	}
	return did + " · " + open
}

// formatBudgetExhaustedNotice renders the "Agent loop parked … tool
// budget exhausted" message that the engine emits when the native
// loop can't run another tool round without busting MaxTokens. The
// same string template appeared in 4 places (preflight headroom-fail
// happy path, preflight headroom-fail catch-all, post-step compact-
// failed path, post-step no-compact path) — fixing the wording in
// one without the others was a regression magnet flagged in the
// REPORT.md walk. headroom is ignored when phase == parkPhaseAfter.
func formatBudgetExhaustedNotice(phase parkPhase, step, tokens, maxTokens, headroom, rounds int) string {
	suffix := `Type "devam" / "continue" or /continue to resume — add a note to narrow focus (e.g. "devam, just finish the test file").`
	switch phase {
	case parkPhaseBefore:
		return fmt.Sprintf(
			"Parked before step %d — tool budget exhausted (~%d/%d tokens, need ~%d headroom, %d rounds). %s",
			step, tokens, maxTokens, headroom, rounds, suffix,
		)
	default:
		return fmt.Sprintf(
			"Parked after step %d — tool budget exhausted (~%d/%d tokens, %d rounds). %s",
			step, tokens, maxTokens, rounds, suffix,
		)
	}
}

// composeParkedNotice glues a one-line headline ("Parked at step N…") to
// the auto-derived trace summary ("Did: …") and a resume affordance.
// Used by the step-cap park path; budget-exhausted goes through
// formatBudgetExhaustedNotice but both end up calling this for the
// summary tail so the wording stays consistent.
func composeParkedNotice(headline string, traces []nativeToolTrace, resumeHint string) string {
	parts := []string{strings.TrimSpace(headline)}
	if summary := summarizeTraces(traces); summary != "" {
		parts = append(parts, summary)
	}
	if hint := strings.TrimSpace(resumeHint); hint != "" {
		parts = append(parts, hint)
	}
	return strings.Join(parts, "\n")
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
		ToolSource:       seed.ToolSource,
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
	// autonomous_pending tells the TUI whether the autonomous-resume wrapper
	// is about to immediately re-enter the loop. When true, the TUI should
	// NOT flip into the "parked, press Enter to resume" UI state — doing so
	// flashes a misleading prompt the user might act on before the wrapper
	// gets to the next round, producing the "No parked agent loop" race the
	// 2026-04-18 screenshot caught. Only budget-exhausted parks under an
	// enabled autonomous policy qualify; step-cap and shutdown parks always
	// surface to the user.
	autonomousPending := reason == ParkReasonBudgetExhausted && e.autonomousResumeEnabled()
	e.publishAgentLoopEvent("agent:loop:parked", map[string]any{
		"step":               step,
		"max_tool_steps":     lim.MaxSteps,
		"max_tool_tokens":    lim.MaxTokens,
		"tool_rounds":        len(traces),
		"tokens_used":        totalTokens,
		"reason":             string(reason),
		"surface":            "native",
		"autonomous_pending": autonomousPending,
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
