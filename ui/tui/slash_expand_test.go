package tui

import (
	"context"
	"strings"
	"testing"
)

func longAssistantTranscript(lineCount int) []chatLine {
	body := strings.Repeat("line\n", lineCount)
	return []chatLine{
		{Role: chatRoleUser, Content: "give me lots of lines"},
		{Role: chatRoleAssistant, Content: strings.TrimRight(body, "\n")},
	}
}

func TestRenderChatView_LongAssistantTurnCollapses(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.transcript = longAssistantTranscript(120)
	view := m.renderChatView(140)
	if !strings.Contains(view, "hidden line(s)") {
		t.Fatalf("expected collapse footer, got:\n%s", view)
	}
	if !strings.Contains(view, "/expand 1") {
		t.Fatalf("expected /expand 1 hint in collapse footer, got:\n%s", view)
	}
}

func TestExpandSlash_OpensCollapsedTurn(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.transcript = longAssistantTranscript(120)
	next, _, handled := m.handleExpandSlash([]string{"1"})
	if !handled {
		t.Fatal("expected handler to flag handled")
	}
	mm := next.(Model)
	if !mm.chat.expandedAssistantTurns[1] {
		t.Fatalf("expected turn 1 marked expanded, got %v", mm.chat.expandedAssistantTurns)
	}
	view := mm.renderChatView(140)
	if strings.Contains(view, "hidden line(s)") {
		t.Fatalf("expanded turn should not show collapse footer:\n%s", view)
	}
}

func TestExpandSlash_OutOfRangeRejected(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.transcript = longAssistantTranscript(120)
	next, _, _ := m.handleExpandSlash([]string{"99"})
	mm := next.(Model)
	last := mm.chat.transcript[len(mm.chat.transcript)-1].Content
	if !strings.Contains(last, "out of range") {
		t.Fatalf("expected out-of-range notice, got %q", last)
	}
}

func TestExpandSlash_AllOpensEveryTurn(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.transcript = append(longAssistantTranscript(120),
		chatLine{Role: chatRoleUser, Content: "second q"},
		chatLine{Role: chatRoleAssistant, Content: strings.Repeat("x\n", 200)},
	)
	next, _, _ := m.handleExpandSlash([]string{"all"})
	mm := next.(Model)
	if !mm.chat.expandedAssistantTurns[1] || !mm.chat.expandedAssistantTurns[2] {
		t.Fatalf("expected both turns expanded, got %v", mm.chat.expandedAssistantTurns)
	}
}

func TestCollapseSlash_ReversesExpand(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.transcript = longAssistantTranscript(120)
	next, _, _ := m.handleExpandSlash([]string{"1"})
	next2, _, _ := next.(Model).handleCollapseSlash([]string{"1"})
	mm := next2.(Model)
	if mm.chat.expandedAssistantTurns[1] {
		t.Fatalf("expected turn 1 collapsed after /collapse, got expanded")
	}
}

func TestClearSlash_ResetsExpansionMap(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.transcript = longAssistantTranscript(120)
	next, _, _ := m.handleExpandSlash([]string{"1"})
	cleared, _, _ := next.(Model).handleClearSlash()
	mm := cleared.(Model)
	if len(mm.chat.expandedAssistantTurns) > 0 {
		t.Fatalf("expected /clear to wipe expansion map, got %v", mm.chat.expandedAssistantTurns)
	}
}
