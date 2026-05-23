package tui

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestToolShowSlash_PrintsFullDetail(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.transcript = []chatLine{
		{Role: chatRoleUser, Content: "go"},
		{
			Role:      chatRoleTool,
			Content:   "done: read_file\ntarget: foo/bar.go\nreturned: 120 lines",
			Timestamp: time.Now().Add(-time.Minute),
			EventLines: []chatEventLine{{
				ToolName:    "read_file",
				Status:      "done",
				Title:       "read foo/bar.go",
				DetailLines: []string{"line 1", "line 2"},
			}},
		},
	}
	next, _, handled := m.handleToolShowSlash([]string{"1"})
	if !handled {
		t.Fatal("expected handled=true")
	}
	mm := next.(Model)
	last := mm.chat.transcript[len(mm.chat.transcript)-1].Content
	for _, want := range []string{"read_file", "tool event 1/1", "line 1", "line 2"} {
		if !strings.Contains(last, want) {
			t.Fatalf("/toolshow output missing %q, got:\n%s", want, last)
		}
	}
}

func TestToolShowSlash_LastResolvesNewest(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.transcript = []chatLine{
		{Role: chatRoleTool, Content: "first", EventLines: []chatEventLine{{ToolName: "grep_codebase"}}},
		{Role: chatRoleTool, Content: "second", EventLines: []chatEventLine{{ToolName: "read_file"}}},
	}
	next, _, _ := m.handleToolShowSlash([]string{"last"})
	mm := next.(Model)
	last := mm.chat.transcript[len(mm.chat.transcript)-1].Content
	if !strings.Contains(last, "read_file") {
		t.Fatalf("/toolshow last must surface the newest event, got %q", last)
	}
}

func TestToolShowSlash_NoEventsReportsCleanly(t *testing.T) {
	m := NewModel(context.Background(), nil)
	next, _, _ := m.handleToolShowSlash([]string{"1"})
	mm := next.(Model)
	last := mm.chat.transcript[len(mm.chat.transcript)-1].Content
	if !strings.Contains(last, "no tool events") {
		t.Fatalf("expected empty-state notice, got %q", last)
	}
}

func TestToolShowSlash_OutOfRangeRejected(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.transcript = []chatLine{
		{Role: chatRoleTool, Content: "only", EventLines: []chatEventLine{{ToolName: "grep_codebase"}}},
	}
	next, _, _ := m.handleToolShowSlash([]string{"99"})
	mm := next.(Model)
	last := mm.chat.transcript[len(mm.chat.transcript)-1].Content
	if !strings.Contains(last, "out of range") {
		t.Fatalf("expected out-of-range notice, got %q", last)
	}
}
