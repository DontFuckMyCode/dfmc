package context

import (
	"strings"
	"testing"
)

// FuzzExtractSnippetLineRange pins extractSnippet's reported line range
// against the snippet it returns. The context manager surfaces LineStart/
// LineEnd to the model (and to read_file follow-ups), so an off-by-one here
// feeds wrong line numbers into the prompt. The invariants:
//   - 1 <= lineStart <= lineEnd <= total lines in content,
//   - the returned snippet is exactly lines[lineStart-1 : lineEnd] rejoined,
//   - the snippet's own line count equals lineEnd-lineStart+1.
func FuzzExtractSnippetLineRange(f *testing.F) {
	seeds := []struct {
		content  string
		term     string
		maxLines int
	}{
		{"line one\nline two\nline three", "two", 60},
		{"", "x", 10},
		{"single", "single", 1},
		{"a\nb\nc\nd\ne\nf\ng\nh", "e", 4},
		{strings.Repeat("x\n", 100), "nomatch", 30},
		{"alpha\nbeta\ngamma", "", 2},
		{"head\nNEEDLE\ntail", "needle", 0},
	}
	for _, s := range seeds {
		f.Add(s.content, s.term, s.maxLines)
	}

	f.Fuzz(func(t *testing.T, content, term string, maxLines int) {
		snippet, lineStart, lineEnd := extractSnippet(content, []string{term}, maxLines)

		total := len(strings.Split(content, "\n"))

		if lineStart < 1 {
			t.Fatalf("lineStart=%d < 1", lineStart)
		}
		if lineEnd < lineStart {
			t.Fatalf("lineEnd=%d < lineStart=%d", lineEnd, lineStart)
		}
		if lineEnd > total {
			t.Fatalf("lineEnd=%d > total lines=%d", lineEnd, total)
		}

		// The snippet must be exactly the claimed slice of the source.
		lines := strings.Split(content, "\n")
		want := strings.Join(lines[lineStart-1:lineEnd], "\n")
		if snippet != want {
			t.Fatalf("snippet does not match reported range [%d,%d]:\n  got=%q\n want=%q",
				lineStart, lineEnd, snippet, want)
		}

		// Line count consistency: a non-empty snippet must have exactly
		// lineEnd-lineStart+1 lines.
		gotLines := strings.Count(snippet, "\n") + 1
		if wantLines := lineEnd - lineStart + 1; gotLines != wantLines {
			t.Fatalf("snippet has %d lines, range [%d,%d] claims %d", gotLines, lineStart, lineEnd, wantLines)
		}
	})
}
