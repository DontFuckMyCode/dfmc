package tui

import (
	"strings"
	"testing"
	"time"
)

// TestRenderTimelineEventMessage_HeaderLineThenIndentedBody pins the
// "header on top, body indented 2 chars" layout. Earlier the first
// content row rode on the same line as the badge+pill+header which
// produced a deep right-aligned column when content wrapped — the
// "her tool callın baş kısmında boşluk fazla" complaint.
func TestRenderTimelineEventMessage_HeaderLineThenIndentedBody(t *testing.T) {
	item := chatLine{
		Role:       chatRoleTool,
		Timestamp:  time.Date(2026, 5, 6, 10, 49, 20, 0, time.Local),
		Content:    "done: batch [3: glob, grep_codebase, read_file]\nsummary: 3 calls | 3 parallel | 3 ok\ncalls:",
		EventLines: []chatEventLine{{Status: "done", Step: 6, ToolName: "tool_batch_call"}},
	}
	header := "10:49:20  |  58 tok"
	rows := renderTimelineEventMessage(item, header, 200)
	if len(rows) < 4 {
		t.Fatalf("expected at least 4 rows (header + 3 body), got %d:\n%v", len(rows), rows)
	}
	// First row is the header — must contain the timestamp/token header
	// AND the status pill, but NOT the body content.
	if !strings.Contains(rows[0], "10:49:20") {
		t.Errorf("header row missing timestamp: %q", rows[0])
	}
	if !strings.Contains(rows[0], "58 tok") {
		t.Errorf("header row missing token count: %q", rows[0])
	}
	if strings.Contains(rows[0], "done: batch") {
		t.Errorf("header row should NOT carry body content, got %q", rows[0])
	}
	// Body rows must start with exactly 2 spaces of visible indent
	// (lipgloss styles wrap the spaces but the rendered string still
	// starts with the literal two spaces).
	for i := 1; i < len(rows); i++ {
		stripped := stripANSI(rows[i])
		if !strings.HasPrefix(stripped, "  ") {
			t.Errorf("body row %d should be indented 2 chars, got %q", i, stripped)
		}
		// And NOT 3+ spaces — we rejected the deep right-aligned column.
		if strings.HasPrefix(stripped, "   ") {
			t.Errorf("body row %d should NOT be indented more than 2 chars, got %q", i, stripped)
		}
	}
}


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
