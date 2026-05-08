package engine

// engine_ask_history_summary.go — composes the "[History summary]"
// block that replaces older conversation turns when the trim cuts
// past the budget. Per-field caps scale with the overall summary
// budget so a 1k-token reserve carries roughly a paragraph each for
// primary turn / progress / open questions, while the legacy tight
// floors still hold for ~120-token tests. Companion siblings:
//
//   - engine_ask_history.go      trim-window machinery
//                                (publishHistoryTrimmedEvent +
//                                conversationHistoryBudget /
//                                MaxMessages + trimmedConversation
//                                Messages + historyBudgetForRequest +
//                                trimToTokenBudget)
//   - engine_ask_history_tail.go renderHistoricalToolTail +
//                                compactToolParamHint +
//                                compactToolResultHint

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// scaleSummaryCap linearly interpolates a per-field cap between lo
// (legacy tight floor for ~512-token summary budgets) and hi (rich
// floor for >=1024-token budgets). Returns lo for budget <= 512 so
// the legacy "RespectsTokenLimit" contract still holds, and hi once
// the summary budget gives us real headroom.
func scaleSummaryCap(budget, lo, hi int) int {
	if budget <= 512 {
		return lo
	}
	if budget >= 1024 {
		return hi
	}
	return lo + (hi-lo)*(budget-512)/512
}

func buildHistorySummary(omitted []types.Message, maxTokens int) string {
	if maxTokens <= 0 || len(omitted) == 0 {
		return ""
	}
	userN := 0
	assistantN := 0
	for _, m := range omitted {
		if m.Role == types.RoleUser {
			userN++
		}
		if m.Role == types.RoleAssistant {
			assistantN++
		}
	}
	// Per-field caps scale with the overall summary budget so larger
	// budgets carry more semantic detail. Legacy ~120-token tests pin
	// the lo end; the post-2025 1024-token regime now keeps roughly a
	// short paragraph for primary/progress instead of a sentence
	// fragment, which is what made the assistant feel amnesiac after
	// trimming.
	primaryCap := scaleSummaryCap(maxTokens, 12, 96)
	progressCap := scaleSummaryCap(maxTokens, 12, 96)
	openCap := scaleSummaryCap(maxTokens, 10, 32)
	openLimit := scaleSummaryCap(maxTokens, 1, 3)
	topicLimit := scaleSummaryCap(maxTokens, 3, 6)
	fileLimit := scaleSummaryCap(maxTokens, 2, 5)

	terms := topTermsFromMessages(omitted, topicLimit)
	files := topFileMentions(omitted, fileLimit)
	primary := latestOmittedByRole(omitted, types.RoleUser, primaryCap)
	progress := latestOmittedByRole(omitted, types.RoleAssistant, progressCap)
	openItems := recentUserQuestions(omitted, openLimit, openCap)

	var b strings.Builder
	fmt.Fprintf(&b, "[History summary] Scope=%d msgs (%dU/%dA).", len(omitted), userN, assistantN)
	if primary != "" {
		b.WriteString(" Primary=")
		b.WriteString(primary)
		b.WriteString(".")
	}
	if progress != "" {
		b.WriteString(" Progress=")
		b.WriteString(progress)
		b.WriteString(".")
	}
	if len(terms) > 0 {
		b.WriteString(" Topics=")
		b.WriteString(strings.Join(terms, ", "))
		b.WriteString(".")
	}
	if len(files) > 0 {
		b.WriteString(" Files=")
		b.WriteString(strings.Join(files, ", "))
		b.WriteString(".")
	}
	if len(openItems) > 0 {
		b.WriteString(" Open=")
		b.WriteString(strings.Join(openItems, " | "))
		b.WriteString(".")
	}
	return trimToTokenBudget(b.String(), maxTokens)
}

func latestOmittedByRole(messages []types.Message, role types.MessageRole, maxTokens int) string {
	if maxTokens <= 0 {
		return ""
	}
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != role {
			continue
		}
		s := trimToTokenBudget(strings.TrimSpace(messages[i].Content), maxTokens)
		if s != "" {
			return s
		}
	}
	return ""
}

func recentUserQuestions(messages []types.Message, maxItems, maxTokensPerItem int) []string {
	if maxItems <= 0 {
		return nil
	}
	out := make([]string, 0, maxItems)
	for i := len(messages) - 1; i >= 0 && len(out) < maxItems; i-- {
		msg := messages[i]
		if msg.Role != types.RoleUser {
			continue
		}
		text := strings.TrimSpace(msg.Content)
		if !strings.Contains(text, "?") {
			continue
		}
		s := trimToTokenBudget(text, maxTokensPerItem)
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func topTermsFromMessages(messages []types.Message, limit int) []string {
	if limit <= 0 {
		return nil
	}
	stop := map[string]struct{}{
		"the": {}, "and": {}, "for": {}, "with": {}, "this": {}, "that": {}, "from": {}, "into": {}, "your": {}, "you": {},
		"about": {}, "also": {}, "just": {}, "when": {}, "then": {}, "than": {}, "what": {}, "which": {}, "where": {}, "while": {},
		"code": {}, "file": {}, "line": {}, "tool": {}, "message": {}, "messages": {}, "user": {}, "assistant": {},
	}
	counts := map[string]int{}
	for _, msg := range messages {
		for _, tok := range tokenizeForSummary(msg.Content) {
			if _, blocked := stop[tok]; blocked {
				continue
			}
			counts[tok]++
		}
	}
	type kv struct {
		Key   string
		Count int
	}
	ranked := make([]kv, 0, len(counts))
	for k, c := range counts {
		ranked = append(ranked, kv{Key: k, Count: c})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].Count == ranked[j].Count {
			return ranked[i].Key < ranked[j].Key
		}
		return ranked[i].Count > ranked[j].Count
	})
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	out := make([]string, 0, len(ranked))
	for _, item := range ranked {
		out = append(out, item.Key)
	}
	return out
}

func tokenizeForSummary(text string) []string {
	parts := strings.FieldsFunc(strings.ToLower(strings.TrimSpace(text)), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if len(p) < 3 {
			continue
		}
		out = append(out, p)
	}
	return out
}

func topFileMentions(messages []types.Message, limit int) []string {
	if limit <= 0 {
		return nil
	}
	counts := map[string]int{}
	for _, msg := range messages {
		matches := fileMentionRe.FindAllString(strings.TrimSpace(msg.Content), -1)
		for _, m := range matches {
			key := strings.ToLower(strings.TrimSpace(strings.Trim(m, ".,;:()[]{}\"'`")))
			if key == "" {
				continue
			}
			counts[key]++
		}
	}
	type kv struct {
		Key   string
		Count int
	}
	ranked := make([]kv, 0, len(counts))
	for k, c := range counts {
		ranked = append(ranked, kv{Key: k, Count: c})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].Count == ranked[j].Count {
			return ranked[i].Key < ranked[j].Key
		}
		return ranked[i].Count > ranked[j].Count
	})
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	out := make([]string, 0, len(ranked))
	for _, item := range ranked {
		out = append(out, item.Key)
	}
	return out
}
