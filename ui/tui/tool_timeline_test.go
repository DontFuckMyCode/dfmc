package tui

import (
	"strings"
	"testing"
	"time"
)

// TestRenderTimelineEventMessage_SingleLineLayout pins the compact
// 1-line-per-event layout: badge + header + body content all on one
// line so a running+done pair occupies 2 lines instead of 4.
func TestRenderTimelineEventMessage_SingleLineLayout(t *testing.T) {
	item := chatLine{
		Role:       chatRoleTool,
		Timestamp:  time.Date(2026, 5, 6, 10, 49, 20, 0, time.Local),
		Content:    "done: batch [3: glob, grep_codebase, read_file]\nsummary: 3 calls | 3 parallel | 3 ok\ncalls:",
		EventLines: []chatEventLine{{Status: "done", Step: 6, ToolName: "tool_batch_call"}},
	}
	header := "10:49:20  |  58 tok"
	rows := renderTimelineEventMessage(item, header, 200)
	if len(rows) != 1 {
		t.Fatalf("expected exactly 1 row (inline layout), got %d:\n%v", len(rows), rows)
	}
	// Single row must contain header info AND body content
	if !strings.Contains(rows[0], "10:49:20") {
		t.Errorf("row missing timestamp: %q", rows[0])
	}
	if !strings.Contains(rows[0], "58 tok") {
		t.Errorf("row missing token count: %q", rows[0])
	}
	if !strings.Contains(rows[0], "done: batch") {
		t.Errorf("row should carry body content inline, got %q", rows[0])
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
