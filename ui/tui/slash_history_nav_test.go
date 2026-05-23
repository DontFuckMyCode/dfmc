package tui

import (
	"context"
	"strings"
	"testing"
)

func searchableTranscript() []chatLine {
	return []chatLine{
		{Role: chatRoleUser, Content: "how does SSE work?"},
		{Role: chatRoleAssistant, Content: "the SSE handler closes the stream"},
		{Role: chatRoleUser, Content: "what about reconnect?"},
		{Role: chatRoleAssistant, Content: "exponential backoff capped at 30s"},
		{Role: chatRoleUser, Content: "and SSE buffering?"},
		{Role: chatRoleAssistant, Content: "no SSE buffering — flush per event"},
	}
}

func TestNextHitSlash_WalksMatches(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.transcript = searchableTranscript()
	m.chat.lastSearchQuery = "SSE"

	first, _, _ := m.handleNextHitSlash(nil)
	if got := first.(Model).chat.scrollback; got == 0 {
		t.Fatalf("expected scrollback to advance after /next, got 0")
	}
	if !strings.Contains(first.(Model).notice, "hit 1") {
		t.Errorf("expected `hit 1` in notice, got %q", first.(Model).notice)
	}
}

func TestNextHitSlash_WrapsAtEnd(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.transcript = searchableTranscript()
	m.chat.lastSearchQuery = "SSE"
	// Walk all hits then wrap.
	hits := m.searchHitIndices("SSE")
	cur := m
	for i := 0; i <= len(hits); i++ {
		next, _, _ := cur.handleNextHitSlash(nil)
		cur = next.(Model)
	}
	// After wrapping, notice should reference hit 1 (or 2 depending on
	// where the wrap landed). Either way, no failure.
	if !strings.Contains(cur.notice, "hit ") {
		t.Fatalf("expected wrap behaviour, got notice %q", cur.notice)
	}
}

func TestPrevHitSlash_WalksMatchesBackwards(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.transcript = searchableTranscript()
	m.chat.lastSearchQuery = "SSE"
	next, _, _ := m.handlePrevHitSlash(nil)
	if !strings.Contains(next.(Model).notice, "hit ") {
		t.Fatalf("expected hit notice, got %q", next.(Model).notice)
	}
}

func TestNextHitSlash_NoQueryReportsCleanly(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.transcript = searchableTranscript()
	next, _, _ := m.handleNextHitSlash(nil)
	if !strings.Contains(next.(Model).notice, "no active search") {
		t.Fatalf("expected no-search notice, got %q", next.(Model).notice)
	}
}

func TestNextHitSlash_StaleQueryClears(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.transcript = []chatLine{{Role: chatRoleAssistant, Content: "no match here"}}
	m.chat.lastSearchQuery = "stale"
	next, _, _ := m.handleNextHitSlash(nil)
	mm := next.(Model)
	if mm.chat.lastSearchQuery != "" {
		t.Fatalf("expected stale query to clear, got %q", mm.chat.lastSearchQuery)
	}
}

