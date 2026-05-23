package engine

// agent_compact.go — offline (LLM-free) auto-compaction for the native tool
// loop's in-flight message list. The goal is token-miser behaviour: when the
// running conversation plus tool rounds approach the provider's context
// window, collapse the oldest completed rounds into a single summary
// message so subsequent provider calls stay cheap.
//
// Honours cfg.Agent.ContextLifecycle: fires only above the configured ratio,
// keeps the last N rounds verbatim (so the model still sees recent tool
// evidence), and never splits an assistant+tool_result pair (splitting would
// break Anthropic/OpenAI tool-turn invariants).
//
// Companion siblings (extracted to keep this file scannable):
//
//   - agent_compact_rounds.go  toolRound type + splitNativeLoopRounds +
//                              findNativeLoopPrefixEnd +
//                              patchUnresolvedToolUses orphan-id fixer
//   - agent_compact_summary.go terse offline summary text builders
//                              (summariseCollapsedRounds + per-round +
//                              per-tool-call + result excerpt)

import (
	"fmt"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/internal/tokens"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// compactionReport captures what maybeCompactNativeLoopHistory did so the
// caller can emit a telemetry event and so tests can assert the behaviour.
type compactionReport struct {
	BeforeTokens     int
	AfterTokens      int
	RoundsCollapsed  int
	MessagesRemoved  int
	ThresholdRatio   float64
	KeepRecentRounds int
}

// resolveContextLifecycle returns the effective lifecycle config for this
// engine, substituting safe defaults for zero values so yaml-missing fields
// behave predictably.
func (e *Engine) resolveContextLifecycle() config.ContextLifecycleConfig {
	out := config.ContextLifecycleConfig{
		Enabled:                   true,
		AutoCompactThresholdRatio: 0.7,
		// Keep the most-recent round verbatim; older rounds become a
		// one-line summary. Was 3 — dropped to 2 after real sessions
		// showed per-round tool_result payloads dominating the budget.
		// Two is still enough for the model to see what it just did
		// plus the setup round before it.
		KeepRecentRounds:          2,
		HandoffBriefMaxTokens:     500,
		AutoHandoffThresholdRatio: 0.9,
	}
	if e == nil || e.Config == nil {
		return out
	}
	cfg := e.Config.Agent.ContextLifecycle
	// Asymmetry note (REPORT.md #8): numeric fields use `> 0` as the
	// "unset" sentinel so a default-zero stays defaulted, but Enabled
	// is a bool — Go can't distinguish "unset" from "explicit false".
	// We rely on DefaultConfig() pre-seeding Enabled=true and YAML's
	// merge semantics preserving untouched fields, so the only paths
	// that yield cfg.Enabled==false are:
	//   (a) the user explicitly wrote `enabled: false` in YAML, or
	//   (b) a caller constructed ContextLifecycleConfig from a literal
	//       without copying defaults — a programmer error we won't
	//       paper over silently.
	// In both cases honouring cfg.Enabled is the correct behaviour.
	out.Enabled = cfg.Enabled
	if cfg.AutoCompactThresholdRatio > 0 {
		out.AutoCompactThresholdRatio = cfg.AutoCompactThresholdRatio
	}
	if cfg.KeepRecentRounds > 0 {
		out.KeepRecentRounds = cfg.KeepRecentRounds
	}
	if cfg.HandoffBriefMaxTokens > 0 {
		out.HandoffBriefMaxTokens = cfg.HandoffBriefMaxTokens
	}
	if cfg.AutoHandoffThresholdRatio > 0 {
		out.AutoHandoffThresholdRatio = cfg.AutoHandoffThresholdRatio
	}
	return out
}

// maybeCompactNativeLoopHistory checks whether the current in-loop message
// list plus the static context is approaching the provider's context window
// and, if so, collapses the oldest complete tool rounds into a summary
// message. Returns the (possibly rewritten) msgs and — when compaction
// fired — a report for event emission. Pure function otherwise: no side
// effects, no provider calls.
func (e *Engine) maybeCompactNativeLoopHistory(
	msgs []provider.Message,
	systemPrompt string,
	chunks []types.ContextChunk,
) ([]provider.Message, *compactionReport) {
	return e.maybeCompactNativeLoopHistoryForBudget(msgs, systemPrompt, chunks, 0)
}

// maybeCompactNativeLoopHistoryForBudget is the budget-aware variant.
// The compact threshold must sit BELOW the ceiling that actually parks
// the loop; otherwise we silently let the history drift until the
// park gate trips and it's too late. Callers pass the effective tool
// budget (cfg.Agent.MaxToolTokens or the elastic-scaled equivalent);
// compaction then fires at 0.7 × min(providerLimit, budget) — the
// binding constraint, not just the provider's hard window.
func (e *Engine) maybeCompactNativeLoopHistoryForBudget(
	msgs []provider.Message,
	systemPrompt string,
	chunks []types.ContextChunk,
	budgetTokens int,
) ([]provider.Message, *compactionReport) {
	lifecycle := e.resolveContextLifecycle()
	if !lifecycle.Enabled {
		return msgs, nil
	}

	providerLimit := e.providerMaxContext()
	if providerLimit <= 0 {
		providerLimit = defaultProviderContextTokens
	}
	// Reference is the smaller of the provider window and the tool
	// budget. With defaults (128k provider, 120k budget) this barely
	// shifts the threshold, but on a 1M-window provider with a 120k
	// tool budget the threshold was firing at 700k — past parking —
	// before this change.
	reference := providerLimit
	if budgetTokens > 0 && budgetTokens < reference {
		reference = budgetTokens
	}
	threshold := int(float64(reference) * lifecycle.AutoCompactThresholdRatio)
	if threshold <= 0 {
		return msgs, nil
	}

	current := estimateRequestTokens(systemPrompt, chunks, msgs)
	if current < threshold {
		return msgs, nil
	}
	return e.compactNativeLoopHistory(msgs, systemPrompt, chunks, current, lifecycle)
}

// proactiveCompactRatio is the budget ratio at which the proactive
// step-boundary compactor fires once the loop is past the soft round
// cap. Lower than the reactive AutoCompactThresholdRatio (default 0.7)
// because we'd rather collapse old rounds preemptively than wait for
// the budget to tip into emergency-park territory. 0.5 keeps headroom
// stable through long sustained loops without compacting unnecessarily
// early.
const proactiveCompactRatio = 0.5

// proactiveCompactNativeLoopHistory is a step-boundary trigger meant for
// long-running loops. Same compactor body as the reactive variant but
// the threshold is gentler (proactiveCompactRatio) so headroom never
// gets a chance to crash. The loop body calls this only after the soft
// round cap so short Q&A turns don't pay the (small) compaction cost.
func (e *Engine) proactiveCompactNativeLoopHistory(
	msgs []provider.Message,
	systemPrompt string,
	chunks []types.ContextChunk,
	budgetTokens int,
) ([]provider.Message, *compactionReport) {
	lifecycle := e.resolveContextLifecycle()
	if !lifecycle.Enabled {
		return msgs, nil
	}
	providerLimit := e.providerMaxContext()
	if providerLimit <= 0 {
		providerLimit = defaultProviderContextTokens
	}
	reference := providerLimit
	if budgetTokens > 0 && budgetTokens < reference {
		reference = budgetTokens
	}
	threshold := int(float64(reference) * proactiveCompactRatio)
	if threshold <= 0 {
		return msgs, nil
	}
	current := estimateRequestTokens(systemPrompt, chunks, msgs)
	if current < threshold {
		return msgs, nil
	}
	return e.compactNativeLoopHistory(msgs, systemPrompt, chunks, current, lifecycle)
}

// forceCompactNativeLoopHistory runs compaction unconditionally (no threshold
// gate). Used on the resume path where we already know the parked history is
// fat — the next provider call will trip budget unless we collapse first.
// Still honours KeepRecentRounds and the "compaction saved nothing" early-out.
func (e *Engine) forceCompactNativeLoopHistory(
	msgs []provider.Message,
	systemPrompt string,
	chunks []types.ContextChunk,
) ([]provider.Message, *compactionReport) {
	lifecycle := e.resolveContextLifecycle()
	if !lifecycle.Enabled {
		return msgs, nil
	}
	current := estimateRequestTokens(systemPrompt, chunks, msgs)
	return e.compactNativeLoopHistory(msgs, systemPrompt, chunks, current, lifecycle)
}

// compactNativeLoopHistory is the shared collapse routine: splits the
// post-prefix messages into tool rounds, keeps the last KeepRecentRounds
// verbatim, and replaces the older rounds with a single summary message.
func (e *Engine) compactNativeLoopHistory(
	msgs []provider.Message,
	systemPrompt string,
	chunks []types.ContextChunk,
	current int,
	lifecycle config.ContextLifecycleConfig,
) ([]provider.Message, *compactionReport) {
	prefixEnd := findNativeLoopPrefixEnd(msgs)
	rounds := splitNativeLoopRounds(msgs[prefixEnd:])
	if len(rounds) <= lifecycle.KeepRecentRounds {
		return msgs, nil
	}
	collapseCount := len(rounds) - lifecycle.KeepRecentRounds
	if collapseCount <= 0 {
		return msgs, nil
	}

	toCollapse := rounds[:collapseCount]
	keep := rounds[collapseCount:]

	summary := summariseCollapsedRounds(toCollapse, 220)
	if strings.TrimSpace(summary) == "" {
		return msgs, nil
	}

	// Patch any kept round that has unresolved tool_use IDs (assistant
	// emitted ToolCalls but the round has no matching tool_result for
	// some of them). This can happen when:
	//   - The loop was canceled / panicked between tool_use emit and the
	//     tool_result append, leaving a half-finished round on disk.
	//   - A tool was approval-denied and produced no result entry.
	// Either way, sending the next provider request without resolving the
	// orphan tool_uses fails on Anthropic with a "tool_use ID matched no
	// tool_result" 400. We inject a synthetic ToolError result instead so
	// the conversation stays well-formed. The model can still reason that
	// a prior tool call failed.
	keep = patchUnresolvedToolUses(keep)

	rebuilt := make([]provider.Message, 0, prefixEnd+1+totalRoundMessages(keep))
	rebuilt = append(rebuilt, msgs[:prefixEnd]...)
	rebuilt = append(rebuilt, provider.Message{
		Role:    types.RoleAssistant,
		Content: "[auto-compacted prior tool context]\n" + summary,
	})
	for _, r := range keep {
		rebuilt = append(rebuilt, r.Messages...)
	}

	after := estimateRequestTokens(systemPrompt, chunks, rebuilt)
	removed := len(msgs) - len(rebuilt)
	if removed <= 0 || after >= current {
		return msgs, nil
	}

	return rebuilt, &compactionReport{
		BeforeTokens:     current,
		AfterTokens:      after,
		RoundsCollapsed:  collapseCount,
		MessagesRemoved:  removed,
		ThresholdRatio:   lifecycle.AutoCompactThresholdRatio,
		KeepRecentRounds: lifecycle.KeepRecentRounds,
	}
}

// estimateRequestTokens gives a consistent token estimate used both by the
// compaction decision and the post-compaction delta so the report reflects a
// real saving.
func estimateRequestTokens(systemPrompt string, chunks []types.ContextChunk, msgs []provider.Message) int {
	total := tokens.Estimate(systemPrompt)
	for _, ch := range chunks {
		total += ch.TokenCount
	}
	// Per-message overhead: provider APIs add framing tokens for role
	// labels, message boundaries, and tool_result wrappers. The
	// HeuristicCounter uses 4 tokens/message but estimateRequestTokens
	// is a standalone path that needs its own floor. Empirically,
	// Anthropic's API costs ~6-8 framing tokens per message; OpenAI is
	// similar. Using 8 gives a small safety margin that prevents the
	// compaction threshold from firing too late.
	const perMessageOverhead = 8
	for _, m := range msgs {
		total += perMessageOverhead
		total += tokens.Estimate(string(m.Role))
		total += tokens.Estimate(m.Content)
		// Tool calls carry their name plus input as JSON. The previous
		// code counted individual key/value pairs but missed JSON
		// structural overhead (quotes, colons, commas, braces).
		// Adding a 15% JSON overhead factor to the input token sum
		// covers the framing without trying to marshal + re-count.
		const jsonOverheadFactor = 1.15
		for _, call := range m.ToolCalls {
			if call.Name != "" {
				total += tokens.Estimate(call.Name)
			}
			inputTokens := 0
			for k, v := range call.Input {
				inputTokens += tokens.Estimate(k) + tokens.Estimate(fmt.Sprint(v))
			}
			total += int(float64(inputTokens)*jsonOverheadFactor + 0.5)
		}
		// Tool result messages carry a tool_call_id wrapper that adds
		// a few framing tokens on top of the content. This is modest
		// but adds up across a 20-round tool loop.
		if m.ToolCallID != "" {
			total += tokens.Estimate(m.ToolCallID) + 2 // wrapper overhead
		}
	}
	return total
}
