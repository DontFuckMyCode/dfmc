// Context-budget advisory surface. Derived from ContextBudgetPreview:
// ContextRecommendations emits human-readable warn/info codes, and
// ContextTuningSuggestions emits concrete key/value patches the caller
// can apply to .dfmc/config.yaml. Both run on the same heuristic table
// so their outputs stay consistent — recommendations and suggestions
// must never disagree about whether the budget is over or under-sized.

package engine

import (
	"math"
	"strings"

	ctxmgr "github.com/dontfuckmycode/dfmc/internal/context"
)

func (e *Engine) ContextRecommendations(question string) []ContextRecommendation {
	return e.ContextRecommendationsWithRuntime(question, ctxmgr.PromptRuntime{})
}

func (e *Engine) ContextRecommendationsWithRuntime(question string, overrides ctxmgr.PromptRuntime) []ContextRecommendation {
	preview := e.ContextBudgetPreviewWithRuntime(question, overrides)
	recs := make([]ContextRecommendation, 0, 6)
	add := func(severity, code, message string) {
		recs = append(recs, ContextRecommendation{
			Severity: strings.TrimSpace(strings.ToLower(severity)),
			Code:     strings.TrimSpace(strings.ToLower(code)),
			Message:  strings.TrimSpace(message),
		})
	}

	available := preview.ContextAvailableTokens
	if available <= 0 {
		available = minContextTotalBudgetTokens
	}
	utilization := float64(preview.MaxTokensTotal) / float64(available)

	if utilization >= 0.92 {
		add("warn", "near_context_cap", "Context budget is near provider limit. Reduce max_files, lower max_tokens_per_file, or use [[file:...]] markers.")
	}
	if preview.ReserveHistoryTokens > available/3 {
		add("warn", "history_reserve_high", "History reserve is large relative to available context. Lower context.max_history_tokens for deeper code context.")
	}
	if preview.ExplicitFileMentions == 0 {
		add("info", "use_file_markers", "No explicit file markers detected. Add [[file:path#Lx-Ly]] to focus retrieval and reduce token waste.")
	}
	if (preview.Task == "security" || preview.Task == "review" || preview.Task == "debug") && preview.MaxTokensPerFile < 320 {
		add("warn", "shallow_file_slices", "Per-file token budget is shallow for this task type. Consider increasing context.max_tokens_per_file.")
	}
	if (preview.Task == "security" || preview.Task == "review") && utilization < 0.55 {
		add("info", "headroom_available", "There is context headroom for deeper inspection. You can increase context.max_tokens_total for richer evidence.")
	}
	if len(recs) == 0 {
		add("info", "balanced_budget", "Current context budget looks balanced for this query.")
	}
	return recs
}

func (e *Engine) ContextTuningSuggestions(question string) []ContextTuningSuggestion {
	return e.ContextTuningSuggestionsWithRuntime(question, ctxmgr.PromptRuntime{})
}

func (e *Engine) ContextTuningSuggestionsWithRuntime(question string, overrides ctxmgr.PromptRuntime) []ContextTuningSuggestion {
	preview := e.ContextBudgetPreviewWithRuntime(question, overrides)
	suggestions := make([]ContextTuningSuggestion, 0, 6)
	add := func(priority, key string, value any, reason string) {
		suggestions = append(suggestions, ContextTuningSuggestion{
			Priority: strings.TrimSpace(strings.ToLower(priority)),
			Key:      strings.TrimSpace(key),
			Value:    value,
			Reason:   strings.TrimSpace(reason),
		})
	}

	available := preview.ContextAvailableTokens
	if available <= 0 {
		available = minContextTotalBudgetTokens
	}
	utilization := float64(preview.MaxTokensTotal) / float64(available)

	if utilization >= 0.92 {
		targetTotal := int(math.Round(float64(available) * 0.78))
		if targetTotal < minContextTotalBudgetTokens {
			targetTotal = minContextTotalBudgetTokens
		}
		add("high", "context.max_tokens_total", targetTotal, "Current budget is near context cap; lowering total budget reduces truncation risk.")
	}
	if preview.ReserveHistoryTokens > available/3 {
		targetHistory := available / 4
		if targetHistory < minContextPerFileTokens {
			targetHistory = minContextPerFileTokens
		}
		if targetHistory > maxHistoryBudgetTokens {
			targetHistory = maxHistoryBudgetTokens
		}
		add("high", "context.max_history_tokens", targetHistory, "History reserve is large relative to available context; reducing it increases code context headroom.")
	}
	if (preview.Task == "security" || preview.Task == "review" || preview.Task == "debug") && preview.MaxTokensPerFile < 320 {
		perFile := 320
		if capPerFile := preview.MaxTokensTotal / maxInt(1, preview.MaxFiles); capPerFile > 0 && capPerFile < perFile {
			perFile = capPerFile
		}
		if perFile < minContextPerFileTokens {
			perFile = minContextPerFileTokens
		}
		add("medium", "context.max_tokens_per_file", perFile, "Task type benefits from deeper per-file slices for evidence quality.")
	}
	if preview.ProviderMaxContext <= 12000 && preview.Compression != "aggressive" {
		add("medium", "context.compression", "aggressive", "Tight runtime context benefits from aggressive compression to preserve critical context.")
	}
	if preview.ProviderMaxContext <= 8000 && preview.IncludeDocs {
		add("low", "context.include_docs", false, "Disabling docs frees tokens for code context in very tight windows.")
	}
	if len(suggestions) == 0 {
		add("low", "context.profile", "balanced", "No urgent tuning required for current query/runtime profile.")
	}
	return suggestions
}
