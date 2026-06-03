// Package tokens provides token count estimation for prompts, context chunks,
// and tool results. The heuristic counter is zero-dependency and model-
// agnostic; encoding-based counters (tiktoken) can be selected via
// DetectFamily + NewCounter.
package tokens

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Counter estimates token counts for text and message sequences. Implementations
// must be safe for concurrent use.
type Counter interface {
	Count(text string) int
	CountMessages(msgs []Message) int
}

// Message is a minimal role+content pair used for framing-aware counting.
type Message struct {
	Role    string
	Content string
}

// EstimateDefault returns a token estimate for text using the default
// heuristic counter. This is a drop-in for code that previously used the
// global defaultCounter via loadDefault().
func EstimateDefault(text string) int {
	return NewHeuristic().Count(text)
}

// Estimate is a backward-compatible alias for EstimateDefault.
func Estimate(text string) int {
	return EstimateDefault(text)
}

// EstimateForModel auto-detects the model family and returns the most
// appropriate token counter's estimate for text.
func EstimateForModel(model, text string) int {
	return CountForModel(model, text)
}

// HeuristicCounter estimates token counts using character-based math adjusted
// by symbol density. Empirical alignment targets (cl100k_base / Claude tokenizer):
//
//	prose           ~4.2 chars/token
//	mixed code+doc  ~3.7 chars/token
//	dense code      ~3.3 chars/token
//	JSON / minified ~2.8 chars/token
//
// Accuracy target: within ±15% of the real tokenizer on representative code
// samples. Plenty good enough for budget decisions; replace with a real
// tokenizer when we need exact accounting for a paid API call.
type HeuristicCounter struct {
	// PerMessageOverhead is added to every message when counting sequences
	// (role/framing tokens). 4 is a reasonable cross-provider default.
	PerMessageOverhead int
	// PerSequenceOverhead is added once per message sequence.
	PerSequenceOverhead int
}

// NewHeuristic returns a Counter with sensible defaults.
func NewHeuristic() *HeuristicCounter {
	return &HeuristicCounter{
		PerMessageOverhead:  4,
		PerSequenceOverhead: 2,
	}
}

// Count returns an estimated token count for text. Safe for empty input.
func (h *HeuristicCounter) Count(text string) int {
	if strings.TrimSpace(text) == "" {
		return 0
	}
	chars := utf8.RuneCountInString(text)
	if chars == 0 {
		return 0
	}

	symbolCount := 0
	wordCount := 0
	inWord := false
	for _, r := range text {
		if unicode.IsSpace(r) {
			inWord = false
			continue
		}
		if !inWord {
			wordCount++
			inWord = true
		}
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			symbolCount++
		}
	}

	density := float64(symbolCount) / float64(chars)
	divisor := 4.2 // prose
	switch {
	case density > 0.22:
		divisor = 2.8 // JSON, minified, symbol-heavy
	case density > 0.12:
		divisor = 3.3 // source code
	case density > 0.06:
		divisor = 3.7 // mixed prose + code
	}

	est := int(float64(chars)/divisor + 0.5)

	// Floor: every non-empty text should cost at least 1 token. Also, if
	// the text has whitespace-separated words, tokenizers rarely emit
	// fewer tokens than words — use word count as a lower bound. The
	// previous "whitespace runs + 1" formula overcounted by the number
	// of leading + trailing whitespace runs (e.g. " a b c " → runs=4 →
	// floor=5 even though there are only 3 words). The space->non-space
	// transition counter above gives the exact word count regardless of
	// boundary whitespace.
	if est < 1 {
		est = 1
	}
	if est < wordCount {
		est = wordCount
	}
	return est
}

// TrimToBudget returns the largest whitespace-aligned prefix of content whose
// estimated token count (via the default counter) fits within maxTokens. When
// content is already within budget it is returned unchanged (trimmed). When
// content must be cut, suffix is appended if there is budget room for it;
// pass "" to disable the marker.
//
// The implementation binary-searches on word count — word count is a
// monotonically increasing proxy for token count, so the largest prefix that
// fits can be found in O(log N · count_cost) regardless of how far off
// word-count and token-count are.
func TrimToBudget(content string, maxTokens int, suffix string) string {
	if maxTokens <= 0 {
		return ""
	}
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return ""
	}
	if EstimateDefault(trimmed) <= maxTokens {
		return trimmed
	}

	words := strings.Fields(content)
	if len(words) == 0 {
		return ""
	}

	// candidate builds the full output for the first n words, including the
	// truncation marker when requested. The budget check estimates the WHOLE
	// output (body + marker) against maxTokens rather than subtracting the
	// marker's standalone estimate from the budget: the heuristic counter is
	// density-sensitive and NON-additive, so a symbol-dense body concatenated
	// with the prose marker can estimate higher than est(body)+est(marker).
	// The old "budget = maxTokens - suffixTokens" math let that overflow leak
	// through — a fuzzer found a 24-token budget returning 25 tokens.
	candidate := func(n int, withSuffix bool) string {
		body := strings.Join(words[:n], " ")
		if withSuffix {
			return body + "\n" + suffix
		}
		return body
	}
	// search returns the largest word-prefix whose full output fits. Word
	// count is a non-strictly-monotonic proxy for tokens, so binary search
	// may return fewer words than the true maximum — but it NEVER returns an
	// n whose output exceeds maxTokens, which is the contract that matters.
	search := func(withSuffix bool) int {
		lo, hi, best := 0, len(words), 0
		for lo <= hi {
			mid := (lo + hi) / 2
			if mid == 0 {
				lo = mid + 1
				continue
			}
			if EstimateDefault(candidate(mid, withSuffix)) <= maxTokens {
				best = mid
				lo = mid + 1
			} else {
				hi = mid - 1
			}
		}
		return best
	}

	if suffix != "" {
		if best := search(true); best > 0 {
			return candidate(best, true)
		}
		// Even one word plus the marker overflows the budget — drop the
		// marker rather than return nothing, so the caller still gets as much
		// real content as fits.
	}
	if best := search(false); best > 0 {
		return candidate(best, false)
	}
	return ""
}

// CountMessages estimates tokens for a message sequence, including framing
// overhead. Empty sequences return 0.
func (h *HeuristicCounter) CountMessages(msgs []Message) int {
	if len(msgs) == 0 {
		return 0
	}
	total := h.PerSequenceOverhead
	for _, m := range msgs {
		total += h.PerMessageOverhead
		total += h.Count(m.Role)
		total += h.Count(m.Content)
	}
	return total
}
