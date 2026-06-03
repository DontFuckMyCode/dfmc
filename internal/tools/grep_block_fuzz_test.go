package tools

import (
	"strings"
	"testing"
)

// FuzzFormatGrepBlock drives the grep context-window formatter over arbitrary
// line buffers and match/before/after offsets. grep_codebase calls this per
// match with caller-controlled context sizes (before/after, capped at 50) and
// a match index; an out-of-range index or huge context must never panic, and
// every line number it emits must be a real 1-based line in the buffer.
func FuzzFormatGrepBlock(f *testing.F) {
	seeds := []struct {
		content            string
		idx, before, after int
	}{
		{"a\nb\nc\nd\ne", 2, 1, 1},
		{"a\nb\nc", 0, 5, 5}, // before runs past the top
		{"a\nb\nc", 2, 0, 9}, // after runs past the bottom
		{"only", 0, 0, 0},
		{"", 0, 0, 0},          // empty buffer
		{"a\nb", 99, 3, 3},     // idx past EOF
		{"a\nb", -4, 3, 3},     // negative idx
		{"x\ny\nz", 1, 50, 50}, // max context
	}
	for _, s := range seeds {
		f.Add(s.content, s.idx, s.before, s.after)
	}

	f.Fuzz(func(t *testing.T, content string, idx, before, after int) {
		lines := strings.Split(content, "\n")

		out := formatGrepBlock("file.go", lines, idx, before, after) // must never panic

		// Every emitted reference "file.go:N:" or "file.go-N-" must name a
		// real 1-based line. Parse the leading "file.go<sep>N<sep>" of each row.
		for _, row := range strings.Split(out, "\n") {
			if row == "" {
				continue
			}
			rest, ok := strings.CutPrefix(row, "file.go")
			if !ok || rest == "" {
				t.Fatalf("row %q lacks the file prefix", row)
			}
			sep := rest[0]
			if sep != ':' && sep != '-' {
				t.Fatalf("row %q has unexpected separator %q", row, sep)
			}
			numStr, _, _ := strings.Cut(rest[1:], string(sep))
			n := 0
			for _, c := range numStr {
				if c < '0' || c > '9' {
					t.Fatalf("row %q has a non-numeric line field %q", row, numStr)
				}
				n = n*10 + int(c-'0')
			}
			if n < 1 || n > len(lines) {
				t.Fatalf("row %q references line %d outside [1,%d]", row, n, len(lines))
			}
		}
	})
}
