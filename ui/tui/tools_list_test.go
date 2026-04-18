package tui

import (
	"context"
	"strings"
	"testing"
)

func TestSlashTools_ListsWithSummaries(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)

	// /tools (no args) now toggles the tool-strip collapse flag — the
	// catalog moved to `/tools list` to match the user-requested
	// collapse-by-default UX.
	next, _, handled := m.executeChatCommand("/tools list")
	if !handled {
		t.Fatal("/tools list must be handled")
	}
	nm := next.(Model)
	last := nm.chat.transcript[len(nm.chat.transcript)-1].Content
	if !strings.Contains(last, "Tools (") {
		t.Fatalf("expected heading 'Tools (N)', got:\n%s", last)
	}
	if !strings.Contains(last, "read_file") {
		t.Fatalf("/tools must include read_file, got:\n%s", last)
	}
	// At least one tool must render with a summary (two fields on a
	// line). If the Specer layer regresses to name-only, operators lose
	// the at-a-glance view.
	sawSummary := false
	for _, line := range strings.Split(last, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "read_file") && len(strings.Fields(trimmed)) > 1 {
			sawSummary = true
			break
		}
	}
	if !sawSummary {
		t.Fatalf("expected at least one summarized line, got:\n%s", last)
	}
}
