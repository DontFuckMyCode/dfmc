package tui

import (
	"strings"
	"testing"
)

func TestMutationImpactTimelineLineSummarizesDiffShape(t *testing.T) {
	payload := map[string]any{
		"changed_files": []string{"a.go", "b.go"},
		"added_lines":   12,
		"removed_lines": 4,
		"hunks_applied": 3,
		"replacements":  2,
	}
	got := mutationImpactTimelineLine(payload)
	for _, want := range []string{"2 files", "+12 -4 lines", "3 hunks", "2 replacements"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected impact summary %q in %q", want, got)
		}
	}
}

func TestMutationResultTimelineIncludesImpactBeforeRawPayloadHints(t *testing.T) {
	payload := map[string]any{
		"changed_files": []string{"internal/demo.go"},
		"added_lines":   2,
		"removed_lines": 1,
		"written_bytes": 128,
	}
	lines := toolResultTimelineLines("write_file", payload, "", true, 0)
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"impact: 1 file | +2 -1 lines", "payload: wrote 128 bytes; file content hidden", "review: /diff"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected timeline line %q, got:\n%s", want, joined)
		}
	}
}
