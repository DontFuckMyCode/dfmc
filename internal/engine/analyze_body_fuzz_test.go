package engine

import (
	"strings"
	"testing"
)

// FuzzEndOfFunctionBody drives the complexity scorer's function-body walkers
// (endOfBraceBody + skipStringLiteral for brace languages, endOfPythonBody for
// Python) over arbitrary source and start indices. These run on every analyzed
// file and track stateful brace depth / string-literal / comment state across
// untrusted bytes — the same class of walker that produced the extractByBraces
// out-of-range panic. Contract: never panic, and the returned end is always in
// (start, len(lines)] for a valid start (so the caller's lines[start:end] slice
// is non-empty and in-bounds), or exactly len(lines) for an out-of-range start.
func FuzzEndOfFunctionBody(f *testing.F) {
	langs := []string{"go", "javascript", "typescript", "java", "python", "c", ""}
	seeds := []struct {
		src   string
		start int
		lang  uint8
	}{
		{"func f() {\n\treturn 1\n}", 0, 0},
		{"def f():\n    return 1\n", 0, 4},
		{"func f() { s := \"}\" ; return s }", 0, 0}, // brace inside string
		{"x := `raw\nstring } with brace`", 0, 0},    // multi-line raw string
		{"a := '}' // }\nb := 1", 0, 0},              // brace in rune + comment
		{"/* } */ func f() {}", 0, 0},                // brace in block comment
		{"func f() { \\", 0, 0},                      // trailing backslash
		{"\"unterminated", 0, 0},
		{"", 0, 0},
		{"}}}}", 0, 0},  // only closers
		{"{{{{", 0, 0},  // only openers
		{"line", 99, 0}, // start past EOF
		{"line", -3, 0}, // negative start
		{"  \tdef g():\n\t\tpass", 0, 4},
	}
	for _, s := range seeds {
		f.Add(s.src, s.start, s.lang)
	}

	f.Fuzz(func(t *testing.T, src string, start int, langSel uint8) {
		lines := strings.Split(src, "\n")
		lang := langs[int(langSel)%len(langs)]

		end := endOfFunctionBody(lines, start, lang) // must never panic

		if start < 0 || start >= len(lines) {
			if end != len(lines) {
				t.Fatalf("out-of-range start=%d returned end=%d, want len=%d", start, end, len(lines))
			}
			return
		}
		// Valid start: end must be a usable upper bound for lines[start:end].
		if end <= start || end > len(lines) {
			t.Fatalf("endOfFunctionBody(start=%d, lang=%q) = %d, want in (%d, %d]", start, lang, end, start, len(lines))
		}
		// The reported slice must be addressable without panicking.
		_ = lines[start:end]
	})
}
