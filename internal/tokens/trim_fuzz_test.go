package tokens

import (
	"strings"
	"testing"
)

// FuzzTrimToBudget pins TrimToBudget's core safety contract: the returned
// text's estimated token count must never exceed maxTokens. The context
// manager relies on this to keep "every token justified" under a hard
// budget — an over-budget return silently blows the window. The risk is
// real: TrimToBudget binary-searches on word count (a non-strictly-
// monotonic proxy for tokens) and then appends a truncation suffix, so the
// final estimate could in principle land above the requested cap.
func FuzzTrimToBudget(f *testing.F) {
	seeds := []struct {
		s   string
		max int
	}{
		{"", 10},
		{"hello world", 1},
		{"a b c d e f g h i j k l m n o p", 3},
		{strings.Repeat("word ", 200), 5},
		{strings.Repeat("{\"k\":\"v\"},", 100), 7},
		{"日本語 のテキスト を 含む 文字列", 2},
		{strings.Repeat("x", 5000), 4},
	}
	for _, s := range seeds {
		f.Add(s.s, s.max)
	}

	f.Fuzz(func(t *testing.T, content string, maxTokens int) {
		// Contract is only defined for a positive budget; the function
		// returns "" for maxTokens <= 0, which trivially fits.
		out := TrimToBudget(content, maxTokens, "... [truncated for token budget]")

		if maxTokens <= 0 {
			if out != "" {
				t.Fatalf("maxTokens=%d should yield empty, got %q", maxTokens, out)
			}
			return
		}

		got := EstimateDefault(out)
		if got > maxTokens {
			t.Fatalf("TrimToBudget returned %d-token text for a %d-token budget:\n  out=%q",
				got, maxTokens, out)
		}

		// The marker-free body must be a whitespace-aligned subset of the
		// input words — TrimToBudget never invents content.
		body, _, _ := strings.Cut(out, "\n... [truncated")
		for _, w := range strings.Fields(body) {
			if !strings.Contains(content, w) {
				t.Fatalf("TrimToBudget emitted word %q not present in input", w)
			}
		}
	})
}

// FuzzTrimToBudgetNoSuffixWithinBudget pins that the no-marker variant
// (suffix="") also honours the cap — this is the path used when callers
// don't want the "[truncated]" annotation polluting the snippet.
func FuzzTrimToBudgetNoSuffixWithinBudget(f *testing.F) {
	for _, s := range []string{"", "a b c", strings.Repeat("tok ", 100), strings.Repeat("y", 2000)} {
		f.Add(s, 4)
	}
	f.Fuzz(func(t *testing.T, content string, maxTokens int) {
		out := TrimToBudget(content, maxTokens, "")
		if maxTokens <= 0 {
			if out != "" {
				t.Fatalf("maxTokens=%d should yield empty, got %q", maxTokens, out)
			}
			return
		}
		if got := EstimateDefault(out); got > maxTokens {
			t.Fatalf("no-suffix TrimToBudget returned %d tokens for %d-token budget: %q", got, maxTokens, out)
		}
	})
}
