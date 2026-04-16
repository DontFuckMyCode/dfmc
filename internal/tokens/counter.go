// Package tokens provides token count estimation for prompts, context chunks,
// and tool results. The default heuristic counter is zero-dependency and
// model-agnostic; provider-aware counters (Anthropic count_tokens API,
// tiktoken, etc.) can be registered later via the Counter interface.
package tokens

import (
	"strings"
	"sync"
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

// Estimate returns the default counter's token estimate for text. This is the
// drop-in replacement for the old word-count estimateTokens helpers.
func Estimate(text string) int {
	return defaultCounter.Count(text)
}

// EstimateMessages returns the default counter's framing-aware estimate.
func EstimateMessages(msgs []Message) int {
	return defaultCounter.CountMessages(msgs)
}

// Default returns the process-wide default Counter.
func Default() Counter {
	return defaultCounter
}

// SetDefault swaps the default counter. Intended for tests or for wiring a
// provider-aware counter (e.g. Anthropic count_tokens, tiktoken).
func SetDefault(c Counter) {
	if c == nil {
		return
	}
	defaultMu.Lock()
	defaultCounter = c
	defaultMu.Unlock()
}

var (
	defaultMu      sync.RWMutex
	defaultCounter Counter = NewHeuristic()
)

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
	whitespaceRuns := 0
	prevWasSpace := false
	for _, r := range text {
		switch {
		case unicode.IsSpace(r):
			if !prevWasSpace {
				whitespaceRuns++
			}
			prevWasSpace = true
		default:
			prevWasSpace = false
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
				symbolCount++
			}
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

	// Floor: every non-empty text should cost at least 1 token. Also, if the
	// text has many whitespace-separated words, tokenizers rarely emit fewer
	// tokens than words — use word count as a lower bound.
	if est < 1 && strings.TrimSpace(text) != "" {
		est = 1
	}
	if words := whitespaceRuns + 1; est < words {
		est = words
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
	if Estimate(trimmed) <= maxTokens {
		return trimmed
	}

	suffixTokens := 0
	includeSuffix := suffix != ""
	if includeSuffix {
		suffixTokens = Estimate(suffix)
	}
	budget := maxTokens - suffixTokens
	if budget <= 0 {
		budget = maxTokens
		includeSuffix = false
	}

	words := strings.Fields(content)
	if len(words) == 0 {
		return ""
	}
	lo, hi := 0, len(words)
	best := 0
	for lo <= hi {
		mid := (lo + hi) / 2
		if mid == 0 {
			lo = mid + 1
			continue
		}
		if Estimate(strings.Join(words[:mid], " ")) <= budget {
			best = mid
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	if best == 0 {
		return ""
	}
	out := strings.Join(words[:best], " ")
	if includeSuffix {
		return out + "\n" + suffix
	}
	return out
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
