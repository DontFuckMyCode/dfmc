// engine_context_helpers_reserve.go — provider context lookup and
// reserve breakdown accounting. Sibling of engine_context_helpers.go
// which keeps path normalization, query-term analysis, explicit
// [[file:...]] marker parsing, task profiles, and the small clampInt
// helper.
//
// Splitting the reserve breakdown out keeps the main helpers file
// scoped to "given a question, what should the budget look like" while
// this file owns "given the runtime, how much do we reserve for system
// prompt + response + history + tools, and how do we squeeze that
// shape onto a tight context window without starving retrieval."
// Compression-level comparisons live here too because they share the
// "stronger of two reserved-budget hints" pattern with the breakdown.

package engine

import (
	"math"
	"strings"

	ctxmgr "github.com/dontfuckmycode/dfmc/internal/context"
	"github.com/dontfuckmycode/dfmc/internal/tokens"
)

func (e *Engine) providerMaxContext() int {
	if e.Providers == nil {
		return 0
	}
	p, ok := e.Providers.Get(e.provider())
	if !ok || p == nil {
		return 0
	}
	return p.MaxContext()
}

func (e *Engine) providerMaxContextForRuntime(runtime ctxmgr.PromptRuntime) int {
	if runtime.MaxContext > 0 {
		return runtime.MaxContext
	}
	providerName := strings.TrimSpace(runtime.Provider)
	if providerName == "" || strings.EqualFold(providerName, e.provider()) {
		return e.providerMaxContext()
	}
	if e.Providers == nil {
		return 0
	}
	p, ok := e.Providers.Get(providerName)
	if !ok || p == nil {
		return 0
	}
	if max := p.MaxContext(); max > 0 {
		return max
	}
	return p.Hints().MaxContext
}

func normalizeContextCompression(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "none", "standard", "aggressive":
		return strings.ToLower(strings.TrimSpace(v))
	default:
		return "standard"
	}
}

func strongerContextCompression(current, desired string) string {
	cur := normalizeContextCompression(current)
	des := normalizeContextCompression(desired)
	if contextCompressionRank(des) > contextCompressionRank(cur) {
		return des
	}
	return cur
}

func contextCompressionRank(level string) int {
	switch normalizeContextCompression(level) {
	case "none":
		return 0
	case "standard":
		return 1
	case "aggressive":
		return 2
	default:
		return 1
	}
}

func (e *Engine) contextReserveBreakdownWithRuntime(question string, runtime ctxmgr.PromptRuntime) contextReserveBreakdown {
	promptReserve := maxInt(basePromptReserveTokens, tokens.Estimate(question)*3)
	responseReserve := defaultResponseReserveTokens
	providerName := strings.TrimSpace(runtime.Provider)
	if providerName == "" {
		providerName = e.provider()
	}
	if prof, ok := e.Config.Providers.Profiles[providerName]; ok && prof.MaxTokens > 0 {
		responseReserve = prof.MaxTokens
	}
	if responseReserve > maxResponseReserveTokens {
		responseReserve = maxResponseReserveTokens
	}
	if responseReserve < minContextPerFileTokens {
		responseReserve = minContextPerFileTokens
	}
	historyReserve := e.conversationHistoryBudget()
	toolReserve := baseToolReserveTokens
	providerLimit := e.providerMaxContextForRuntime(runtime)
	if providerLimit <= 0 {
		providerLimit = defaultProviderContextTokens
	}

	// Tight context windows require proportionally smaller reserve buckets to avoid
	// starving the retrieval budget.
	if providerLimit <= 24000 {
		promptReserve = minInt(promptReserve, maxInt(minContextPerFileTokens*2, providerLimit/5))
		responseReserve = minInt(responseReserve, maxInt(minContextPerFileTokens, providerLimit/4))
		historyReserve = minInt(historyReserve, maxInt(minContextPerFileTokens, providerLimit/6))
		toolReserve = minInt(toolReserve, maxInt(minContextPerFileTokens/2, providerLimit/8))
	}

	// Keep reserve total bounded so context has meaningful headroom even on small windows.
	maxTotalReserve := providerLimit - minContextTotalBudgetTokens
	if maxTotalReserve < minContextPerFileTokens {
		maxTotalReserve = minContextPerFileTokens
	}
	total := promptReserve + responseReserve + toolReserve + historyReserve
	if total > maxTotalReserve {
		scale := float64(maxTotalReserve) / float64(total)
		promptReserve = maxInt(minContextPerFileTokens, int(math.Round(float64(promptReserve)*scale)))
		responseReserve = maxInt(minContextPerFileTokens, int(math.Round(float64(responseReserve)*scale)))
		historyReserve = maxInt(minContextPerFileTokens, int(math.Round(float64(historyReserve)*scale)))
		toolReserve = maxInt(minContextPerFileTokens/2, int(math.Round(float64(toolReserve)*scale)))

		total = promptReserve + responseReserve + toolReserve + historyReserve
		overflow := total - maxTotalReserve
		if overflow > 0 {
			cut := minInt(overflow, maxInt(0, responseReserve-minContextPerFileTokens))
			responseReserve -= cut
			overflow -= cut
		}
		if overflow > 0 {
			cut := minInt(overflow, maxInt(0, historyReserve-minContextPerFileTokens))
			historyReserve -= cut
			overflow -= cut
		}
		if overflow > 0 {
			cut := minInt(overflow, maxInt(0, toolReserve-(minContextPerFileTokens/2)))
			toolReserve -= cut
			overflow -= cut
		}
		if overflow > 0 {
			cut := minInt(overflow, maxInt(0, promptReserve-minContextPerFileTokens))
			promptReserve -= cut
		}
	}
	total = promptReserve + responseReserve + toolReserve + historyReserve
	return contextReserveBreakdown{
		Prompt:   promptReserve,
		History:  historyReserve,
		Response: responseReserve,
		Tool:     toolReserve,
		Total:    total,
	}
}
