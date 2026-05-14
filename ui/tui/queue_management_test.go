package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestImmediateSlashDoesNotQueueWhileStreaming(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.sending = true
	m.setChatInput("/tools")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm := next.(Model)
	if len(mm.chat.pendingQueue) != 0 {
		t.Fatalf("immediate slash command should not queue, got %#v", mm.chat.pendingQueue)
	}
	if strings.TrimSpace(mm.chat.input) != "" {
		t.Fatalf("composer should clear after immediate slash, got %q", mm.chat.input)
	}
	if len(mm.chat.transcript) == 0 {
		t.Fatal("expected transcript feedback for immediate slash")
	}
	last := mm.chat.transcript[len(mm.chat.transcript)-1].Content
	if !strings.Contains(strings.ToLower(last), "tool-call strip") {
		t.Fatalf("expected /tools response, got %q", last)
	}
}

func TestTrimmedClearSlashExecutesInsteadOfSending(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.transcript = []chatLine{
		newChatLine(chatRoleUser, "hello"),
		newChatLine(chatRoleAssistant, "hi"),
	}
	m.setChatInput("  /clear  ")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm := next.(Model)
	if mm.chat.sending {
		t.Fatal("trimmed /clear should execute locally, not start a chat send")
	}
	if len(mm.chat.pendingQueue) != 0 {
		t.Fatalf("trimmed /clear should not queue, got %#v", mm.chat.pendingQueue)
	}
	if strings.TrimSpace(mm.chat.input) != "" {
		t.Fatalf("composer should clear after /clear, got %q", mm.chat.input)
	}
	if len(mm.chat.transcript) != 1 || mm.chat.transcript[0].Role != chatRoleSystem {
		t.Fatalf("/clear should replace transcript with one system confirmation, got %#v", mm.chat.transcript)
	}
	if strings.Contains(mm.chat.transcript[0].Content, "/clear") {
		t.Fatalf("/clear was echoed as message content: %#v", mm.chat.transcript)
	}
}

func TestClearSlashDoesNotQueueWhileStreaming(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.sending = true
	m.setChatInput("  /clear  ")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm := next.(Model)
	if mm.chat.sending {
		t.Fatal("/clear should cancel the active stream")
	}
	if len(mm.chat.pendingQueue) != 0 {
		t.Fatalf("/clear should execute immediately while streaming, got queue %#v", mm.chat.pendingQueue)
	}
	if strings.TrimSpace(mm.chat.input) != "" {
		t.Fatalf("composer should clear after streaming /clear, got %q", mm.chat.input)
	}
}

func TestWorkSlashStillQueuesWhileStreaming(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.sending = true
	m.setChatInput("/review [[file:ui/tui/tui.go]]")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm := next.(Model)
	if len(mm.chat.pendingQueue) != 1 {
		t.Fatalf("work slash should still queue while streaming, got %#v", mm.chat.pendingQueue)
	}
	if !strings.HasPrefix(mm.chat.pendingQueue[0], "/review") {
		t.Fatalf("expected /review to remain queued verbatim, got %#v", mm.chat.pendingQueue)
	}
}

func TestQueueSlashShowsEntries(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.pendingQueue = []string{"first follow-up", "/review [[file:ui/tui/tui.go]]"}

	next, _, handled := m.executeChatCommand("/queue")
	if !handled {
		t.Fatal("/queue must be handled")
	}
	mm := next.(Model)
	last := mm.chat.transcript[len(mm.chat.transcript)-1].Content
	for _, want := range []string{"Pending chat queue", "1. first follow-up", "2. /review"} {
		if !strings.Contains(last, want) {
			t.Fatalf("/queue output should contain %q, got:\n%s", want, last)
		}
	}
}

func TestQueueSlashClearRemovesEntries(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.pendingQueue = []string{"one", "two"}

	next, _, handled := m.executeChatCommand("/queue clear")
	if !handled {
		t.Fatal("/queue clear must be handled")
	}
	mm := next.(Model)
	if len(mm.chat.pendingQueue) != 0 {
		t.Fatalf("/queue clear should empty queue, got %#v", mm.chat.pendingQueue)
	}
	last := mm.chat.transcript[len(mm.chat.transcript)-1].Content
	if !strings.Contains(last, "Cleared 2 queued message(s).") {
		t.Fatalf("expected clear confirmation, got %q", last)
	}
}

func TestQueueSlashDropRemovesOneEntry(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.pendingQueue = []string{"one", "two", "three"}

	next, _, handled := m.executeChatCommand("/queue drop 2")
	if !handled {
		t.Fatal("/queue drop must be handled")
	}
	mm := next.(Model)
	if got := strings.Join(mm.chat.pendingQueue, ","); got != "one,three" {
		t.Fatalf("expected middle queue item removed, got %q", got)
	}
	last := mm.chat.transcript[len(mm.chat.transcript)-1].Content
	if !strings.Contains(last, "Dropped queued #2: two") {
		t.Fatalf("expected drop confirmation, got %q", last)
	}
}
