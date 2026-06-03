package tools

import (
	"strings"
	"testing"
)

// FuzzExtractScopeEnd drives every per-language scope walker over arbitrary
// content and start lines. find_symbol feeds these an AST-reported start line
// and the file's lines; a bad index must never panic and the returned end must
// stay within the file. extractByIndent/extractRubyScope guard startLine<1, but
// extractByBraces did not — its loop reads lines[startLine-1], so startLine<=0
// indexed lines[-1].
func FuzzExtractScopeEnd(f *testing.F) {
	langs := []string{"go", "python", "yaml", "ruby", "bash", "shell", "javascript", "rust", ""}
	seeds := []struct {
		content string
		start   int
		lang    uint8
	}{
		{"func f() {\n\treturn\n}", 1, 0},
		{"def f():\n    pass", 1, 1},
		{"class C\n  def m\n  end\nend", 1, 3},
		{"a\nb\nc", 0, 0},   // startLine 0 — the brace-walker panic
		{"a\nb\nc", -5, 0},  // negative
		{"x", 99, 0},        // past EOF
		{"", 1, 0},          // empty content
		{"{\n{\n{\n", 1, 0}, // unbalanced opens
		{"}}}\n", 1, 0},     // unbalanced closes
	}
	for _, s := range seeds {
		f.Add(s.content, s.start, s.lang)
	}

	f.Fuzz(func(t *testing.T, content string, startLine int, langSel uint8) {
		lines := strings.Split(content, "\n")
		lang := langs[int(langSel)%len(langs)]

		end := extractScopeEnd(lang, lines, startLine) // must never panic (the core guarantee)

		// For an out-of-contract startLine (the walkers return it unchanged by
		// design), only the no-panic guarantee applies. For a VALID startLine
		// the end must be a sane 1-based line within the file and at/after the
		// header.
		if startLine >= 1 && startLine <= len(lines) {
			if end < startLine {
				t.Fatalf("extractScopeEnd(%q, start=%d) = %d before the header", lang, startLine, end)
			}
			if end > len(lines) {
				t.Fatalf("extractScopeEnd(%q, start=%d) = %d exceeds %d lines", lang, startLine, end, len(lines))
			}
		}
	})
}
