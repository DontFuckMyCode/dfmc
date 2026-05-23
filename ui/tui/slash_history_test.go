package tui

import (
	"context"
	"strings"
	"testing"
)

func TestHistorySlash_SearchReportsMatches(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.transcript = []chatLine{
		{Role: chatRoleUser, Content: "how does SSE stream work?"},
		{Role: chatRoleAssistant, Content: "the SSE handler closes the stream after EOF"},
		{Role: chatRoleUser, Content: "what about reconnect?"},
		{Role: chatRoleAssistant, Content: "exponential backoff, capped at 30s"},
	}
	next, _, handled := m.handleHistorySlash([]string{"search", "SSE"})
	if !handled {
		t.Fatal("handler should have flagged itself handled=true")
	}
	mm := next.(Model)
	last := mm.chat.transcript[len(mm.chat.transcript)-1].Content
	if !strings.Contains(last, "2 match") {
		t.Fatalf("expected 2 matches, got %q", last)
	}
	if !strings.Contains(last, "/jump 1") {
		t.Fatalf("expected jump anchor #1, got %q", last)
	}
}

func TestHistorySlash_NoMatchesIsExplicit(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.transcript = []chatLine{
		{Role: chatRoleAssistant, Content: "first answer"},
	}
	next, _, _ := m.handleHistorySlash([]string{"search", "nonexistent"})
	mm := next.(Model)
	last := mm.chat.transcript[len(mm.chat.transcript)-1].Content
	if !strings.Contains(last, "0 matches") {
		t.Fatalf("expected 0 matches notice, got %q", last)
	}
}

func TestHistorySlash_ListEnumeratesAssistantTurns(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.transcript = []chatLine{
		{Role: chatRoleAssistant, Content: "first turn body line one\nsecond paragraph"},
		{Role: chatRoleAssistant, Content: "second turn"},
	}
	next, _, _ := m.handleHistorySlash([]string{"list"})
	mm := next.(Model)
	last := mm.chat.transcript[len(mm.chat.transcript)-1].Content
	for _, want := range []string{"#1", "#2", "first turn body", "second turn"} {
		if !strings.Contains(last, want) {
			t.Fatalf("/history list missing %q, got %q", want, last)
		}
	}
}

func TestJumpSlash_ScrollsAndRejectsOutOfRange(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.transcript = []chatLine{
		{Role: chatRoleUser, Content: "q1"},
		{Role: chatRoleAssistant, Content: "a1"},
		{Role: chatRoleUser, Content: "q2"},
		{Role: chatRoleAssistant, Content: "a2"},
	}
	// Valid jump.
	next, _, _ := m.handleJumpSlash([]string{"1"})
	mm := next.(Model)
	if mm.chat.scrollback <= 0 {
		t.Fatalf("expected scrollback to advance after jump, got %d", mm.chat.scrollback)
	}
	// Out of range.
	next2, _, _ := m.handleJumpSlash([]string{"99"})
	mm2 := next2.(Model)
	last := mm2.chat.transcript[len(mm2.chat.transcript)-1].Content
	if !strings.Contains(last, "out of range") {
		t.Fatalf("expected out-of-range notice, got %q", last)
	}
}

func TestJumpSlash_RejectsNonInteger(t *testing.T) {
	m := NewModel(context.Background(), nil)
	next, _, _ := m.handleJumpSlash([]string{"abc"})
	mm := next.(Model)
	last := mm.chat.transcript[len(mm.chat.transcript)-1].Content
	if !strings.Contains(last, "positive integer") {
		t.Fatalf("expected positive-integer notice, got %q", last)
	}
}
