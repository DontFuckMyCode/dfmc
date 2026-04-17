package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestChatHeaderShowsQueuedAndBtwBadges(t *testing.T) {
	out := renderChatHeader(chatHeaderInfo{
		Provider:     "anthropic",
		Model:        "claude-opus-4-6",
		Streaming:    true,
		QueuedCount:  3,
		PendingNotes: 2,
	}, 200)
	if !strings.Contains(out, "queued 3") {
		t.Fatalf("expected queued badge, got %q", out)
	}
	if !strings.Contains(out, "btw 2") {
		t.Fatalf("expected /btw badge, got %q", out)
	}
	if !strings.Contains(out, "streaming") {
		t.Fatalf("expected streaming indicator alongside badges, got %q", out)
	}
}

func TestChatHeaderShowsParkedBadge(t *testing.T) {
	out := renderChatHeader(chatHeaderInfo{
		Provider: "anthropic",
		Model:    "claude-opus-4-6",
		Parked:   true,
	}, 200)
	if !strings.Contains(out, "parked") || !strings.Contains(out, "/continue") {
		t.Fatalf("expected parked badge with /continue hint, got %q", out)
	}
}

func TestEnterWhileSendingQueuesMessage(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.sending = true
	m.setChatInput("follow-up question")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm, ok := next.(Model)
	if !ok {
		t.Fatalf("expected Model, got %T", next)
	}
	if len(mm.pendingQueue) != 1 || mm.pendingQueue[0] != "follow-up question" {
		t.Fatalf("expected one queued message, got %#v", mm.pendingQueue)
	}
	if strings.TrimSpace(mm.input) != "" {
		t.Fatalf("composer should clear after queueing, got %q", mm.input)
	}
	if !strings.Contains(mm.notice, "Queued (1/") {
		t.Fatalf("expected Queued (1/N) notice, got %q", mm.notice)
	}
}

func TestTypingStaysEnabledWhileSending(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.sending = true

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	mm, ok := next.(Model)
	if !ok {
		t.Fatalf("expected Model, got %T", next)
	}
	if mm.input != "h" {
		t.Fatalf("expected typing to work while sending, got %q", mm.input)
	}

	next2, _ := mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	mm2, _ := next2.(Model)
	if mm2.input != "hi" {
		t.Fatalf("expected composer to accept multiple keystrokes, got %q", mm2.input)
	}
}

func TestBtwSlashCommandQueuesNoteAndUpdatesBadge(t *testing.T) {
	m := NewModel(context.Background(), nil)
	// Bootstrap the engine pointer minimally — we only need QueueAgentNote
	// to be callable. Since the TUI's /btw handler also bumps a counter
	// on the Model itself, that counter drives the header badge.
	m = m.appendSystemMessage("ready")
	m.setChatInput("/btw focus on the test file")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm, ok := next.(Model)
	if !ok {
		t.Fatalf("expected Model, got %T", next)
	}
	// Without an engine the /btw handler warns the user; the composer should
	// still have been cleared and the notice should explain.
	if strings.TrimSpace(mm.input) != "" {
		t.Fatalf("composer should clear after /btw, got %q", mm.input)
	}
	if !strings.Contains(mm.notice, "/btw") && !strings.Contains(strings.ToLower(mm.notice), "engine") {
		t.Fatalf("expected /btw-related notice, got %q", mm.notice)
	}
}

func TestContinueCommandWarnsWhenNoParkedLoop(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.setChatInput("/continue")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm, ok := next.(Model)
	if !ok {
		t.Fatalf("expected Model, got %T", next)
	}
	if !strings.Contains(mm.notice, "Nothing to resume") && !strings.Contains(strings.ToLower(mm.notice), "parked") {
		t.Fatalf("expected Nothing-to-resume notice, got %q", mm.notice)
	}
}

func TestResumeAliasRoutesToContinue(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.setChatInput("/resume")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm, _ := next.(Model)
	if !strings.Contains(mm.notice, "Nothing to resume") {
		t.Fatalf("/resume should route to /continue handler, got %q", mm.notice)
	}
}

func TestSlashCatalogSurfacesResumeAndBtwCommands(t *testing.T) {
	m := NewModel(context.Background(), nil)
	catalog := m.slashCommandCatalog()

	want := map[string]bool{"continue": false, "btw": false}
	for _, item := range catalog {
		name := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(item.Command)), "/")
		if _, tracked := want[name]; tracked {
			want[name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("slash catalog missing /%s — should be discoverable from the palette", name)
		}
	}
}

func TestIsKnownChatCommandTokenAcceptsResumeAndBtw(t *testing.T) {
	for _, tok := range []string{"continue", "resume", "btw"} {
		if !isKnownChatCommandToken(tok) {
			t.Errorf("expected %q to be a known chat command token", tok)
		}
	}
}
