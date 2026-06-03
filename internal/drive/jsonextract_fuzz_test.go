package drive

import (
	"strings"
	"testing"
)

// FuzzExtractJSONObjectCandidates exercises the JSON-envelope extractor on
// arbitrary input. It runs against raw LLM output — effectively untrusted,
// adversarial bytes — so its hard requirement is robustness: never panic, and
// every candidate it hands back must be a well-formed brace-delimited slice of
// the input. A slice-boundary bug here would either crash the planner or feed
// the JSON decoder garbage.
func FuzzExtractJSONObjectCandidates(f *testing.F) {
	seeds := []string{
		`{"todos":[]}`,
		"prose before {\"a\":1} prose after",
		`{"a":{"b":{"c":1}}}`,
		`{"s":"a}b{c"}`,           // braces inside a string must not unbalance
		`{"esc":"a\"}"}`,          // escaped quote inside string
		"{unbalanced",             // no closing brace
		"}{}{",                    // stray/again
		"```json\n{\"x\":1}\n```", // fenced
		"",
		"{}{}",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		// Must not panic on any input.
		candidates := extractJSONObjectCandidates(raw)

		for _, c := range candidates {
			if c == "" {
				t.Fatalf("empty candidate from %q", raw)
			}
			if c[0] != '{' {
				t.Fatalf("candidate %q does not start with '{'", c)
			}
			if c[len(c)-1] != '}' {
				t.Fatalf("candidate %q does not end with '}'", c)
			}
			if !strings.Contains(raw, c) {
				t.Fatalf("candidate %q is not a substring of the input", c)
			}
			// The candidate must be brace-balanced when braces inside string
			// literals are ignored — the same property findBalancedJSONObjectEnd
			// promises. Re-derive it independently here.
			if !braceBalancedOutsideStrings(c) {
				t.Fatalf("candidate %q is not brace-balanced outside strings", c)
			}
		}
	})
}

// braceBalancedOutsideStrings is an independent check that a string is a single
// brace-delimited object: depth starts at the leading '{', never returns to 0
// before the final byte, and ends at exactly 0 — with braces inside JSON string
// literals (respecting backslash escapes) ignored.
func braceBalancedOutsideStrings(s string) bool {
	depth := 0
	inString := false
	escaped := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case ch == '\\':
				escaped = true
			case ch == '"':
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 && i != len(s)-1 {
				return false // closed early — not a single object
			}
			if depth < 0 {
				return false
			}
		}
	}
	return depth == 0
}
