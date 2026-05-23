package tui

import (
	"context"
	"strings"
	"testing"
	"time"
)

func mkActivityEntry(at time.Time, kind activityKind, eventID, text string) activityEntry {
	return activityEntry{
		At:      at,
		Kind:    kind,
		EventID: eventID,
		Text:    text,
		Count:   1,
	}
}

func TestActivityEntryIcon_DistinguishesRunningAndDone(t *testing.T) {
	cases := []struct {
		entry activityEntry
		want  string
	}{
		{mkActivityEntry(time.Now(), activityKindTool, "tool:call", "running"), "⟳"},
		{mkActivityEntry(time.Now(), activityKindTool, "tool:done", "completed"), "✓"},
		{mkActivityEntry(time.Now(), activityKindTool, "tool:error", "failed"), "✗"},
		{mkActivityEntry(time.Now(), activityKindAgent, "agent:loop", "agent step"), "◈"},
	}
	for _, tc := range cases {
		out := activityEntryIcon(tc.entry)
		if !strings.Contains(out, tc.want) {
			t.Errorf("entry %s expected icon %q, got %q", tc.entry.EventID, tc.want, out)
		}
	}
}

func TestCountActivityQueryHits(t *testing.T) {
	entries := []activityEntry{
		{Kind: activityKindTool, Text: "tool call: read_file foo.go"},
		{Kind: activityKindTool, Text: "tool done: read_file foo.go"},
		{Kind: activityKindError, Text: "tool error: timeout"},
		{Kind: activityKindAgent, Text: "agent step 3"},
	}
	if n := countActivityQueryHits(entries, "read_file"); n != 2 {
		t.Errorf("expected 2 hits for read_file, got %d", n)
	}
	if n := countActivityQueryHits(entries, "agent"); n != 1 {
		t.Errorf("expected 1 hit for agent, got %d", n)
	}
	if n := countActivityQueryHits(entries, ""); n != 0 {
		t.Errorf("empty query must return 0, got %d", n)
	}
}

func TestActivitySearchView_RendersLiveBox(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activity.entries = []activityEntry{
		mkActivityEntry(time.Now(), activityKindTool, "tool:call", "read_file foo.go"),
	}
	m.activity.searchActive = true
	m.activity.query = "foo"
	view := m.renderActivityViewV2(120, 30)
	if !strings.Contains(view, "Search:") {
		t.Fatalf("expected live search box, got:\n%s", view)
	}
	if !strings.Contains(view, "foo") {
		t.Fatalf("expected query inside search box, got:\n%s", view)
	}
}

func TestActivitySearchView_RendersHitChip(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activity.entries = []activityEntry{
		mkActivityEntry(time.Now(), activityKindTool, "tool:call", "read_file foo.go"),
		mkActivityEntry(time.Now(), activityKindTool, "tool:done", "read_file foo.go"),
		mkActivityEntry(time.Now(), activityKindAgent, "agent:loop", "step 1"),
	}
	m.activity.query = "read_file"
	view := m.renderActivityViewV2(120, 30)
	if !strings.Contains(view, "2 hits") {
		t.Fatalf("expected 2-hit chip in banner, got:\n%s", view)
	}
}

func TestActivitySearchView_RendersZeroHitsChip(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activity.entries = []activityEntry{
		mkActivityEntry(time.Now(), activityKindTool, "tool:call", "read_file foo.go"),
	}
	m.activity.query = "absent"
	view := m.renderActivityViewV2(120, 30)
	if !strings.Contains(view, "0 hits") {
		t.Fatalf("expected 0-hit chip when query has no matches, got:\n%s", view)
	}
}

