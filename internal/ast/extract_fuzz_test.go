package ast

import (
	"strings"
	"testing"
)

// FuzzExtractSymbolsNoPanic drives the regex symbol extractor (the CGO-off
// fallback that runs whenever tree-sitter is unavailable) over arbitrary
// source bytes for every language branch. The extractor does
// `m := re.FindStringSubmatch(line); add(..., m[1], ...)` per branch, so a
// regex that matches but lacks capture group 1 would panic with an index-out-
// of-range — on attacker-influenceable file content. The invariants:
//   - extractSymbols never panics for any (lang, content),
//   - every emitted symbol has a non-empty name and a 1-based line within the
//     file (1 <= Line <= number of lines),
//   - the symbol's recorded language matches the requested one.
func FuzzExtractSymbolsNoPanic(f *testing.F) {
	seeds := []string{
		"func Foo() {}\ntype Bar struct{}",
		"export function baz() {}\nclass Qux {}",
		"def hello():\n    pass",
		"public class A { void m() {} }",
		"fn main() {}\nstruct S;",
		"interface I {}\nenum E { A, B }",
		"",
		"\n\n\n",
		"{[(<>)]}",
		strings.Repeat("function ", 200),
	}
	for _, s := range seeds {
		f.Add(s)
	}

	// Every language the extractor switches on (plus a couple that fall
	// through to default) so each regex branch is exercised.
	langs := []string{
		"go", "javascript", "typescript", "tsx", "jsx",
		"python", "java", "rust", "c", "cpp", "csharp", "ruby", "unknown",
	}

	f.Fuzz(func(t *testing.T, content string) {
		lineCount := len(strings.Split(content, "\n"))
		for _, lang := range langs {
			// The call itself must not panic — that's the primary guard.
			syms := extractSymbols("fuzz/input.src", lang, []byte(content))
			for _, s := range syms {
				if strings.TrimSpace(s.Name) == "" {
					t.Fatalf("lang=%q produced a symbol with empty name", lang)
				}
				if s.Line < 1 || s.Line > lineCount {
					t.Fatalf("lang=%q symbol %q has out-of-range line %d (file has %d lines)",
						lang, s.Name, s.Line, lineCount)
				}
				if s.Language != lang {
					t.Fatalf("symbol language %q != requested %q", s.Language, lang)
				}
			}
		}
	})
}
