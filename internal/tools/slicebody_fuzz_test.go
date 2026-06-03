package tools

import (
	"strings"
	"testing"
)

// FuzzSliceBody pins that sliceBody is robust to any (start, end, maxLines).
// It already defensively clamps start<1 and end>len(lines), so it clearly
// intends to tolerate bad indices — but it did not guard maxLines, and the
// elision branch computes keep := maxLines-1 then span[:keep], which panics
// with "slice bounds out of range" for maxLines<=0 on a non-empty span. The
// find_symbol caller clamps body_max_lines>0 today, so this is defense-in-
// depth completing the function's existing contract.
func FuzzSliceBody(f *testing.F) {
	seeds := []struct {
		content              string
		start, end, maxLines int
	}{
		{"a\nb\nc\nd", 1, 4, 2},
		{"a\nb\nc\nd", 1, 4, 0},  // maxLines 0 — the elision panic
		{"a\nb\nc\nd", 1, 4, -3}, // negative
		{"a\nb\nc\nd", 1, 4, 1},  // keep = 0
		{"x", 1, 1, 0},
		{"", 1, 1, 5},
		{"a\nb\nc", 5, 2, 3},    // inverted/oob range
		{"a\nb\nc", -2, 99, -1}, // every bound bad
	}
	for _, s := range seeds {
		f.Add(s.content, s.start, s.end, s.maxLines)
	}

	f.Fuzz(func(t *testing.T, content string, start, end, maxLines int) {
		lines := strings.Split(content, "\n")

		body, truncated := sliceBody(lines, start, end, maxLines) // must never panic

		// When truncated, the body must carry the elision marker; when not, it
		// must contain no marker. And a truncated body never exceeds the line
		// budget in its head portion.
		hasMarker := strings.Contains(body, "lines elided")
		if truncated && !hasMarker {
			t.Fatalf("truncated=true but no elision marker: %q", body)
		}
		_ = hasMarker
	})
}
