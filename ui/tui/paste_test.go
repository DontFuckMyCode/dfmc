package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestPasteBlockDetection(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0

	// Simulate pasting multi-line text via KeyRunes with \n
	pasteMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("line1\nline2\nline3")}

	next1, _ := m.Update(pasteMsg)
	m2, ok := next1.(Model)
	if !ok {
		t.Fatalf("expected Model, got %T", next1)
	}

	// Should have created a paste block
	if len(m2.chat.pasteBlocks) != 1 {
		t.Fatalf("expected 1 paste block, got %d", len(m2.chat.pasteBlocks))
	}

	// Input should contain the placeholder
	if !strings.Contains(m2.chat.input, "[pasted text #1") {
		t.Fatalf("expected placeholder in input, got %q", m2.chat.input)
	}

	// Notice should confirm paste
	if !strings.Contains(m2.notice, "PASTE") {
		t.Fatalf("expected paste notice, got %q", m2.notice)
	}
}

func TestPasteBlockEnterSubmitsOneMessage(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0

	// Paste multi-line
	pasteMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("line1\nline2\nline3")}
	next1, _ := m.Update(pasteMsg)
	m2, ok := next1.(Model)
	if !ok {
		t.Fatalf("expected Model, got %T", next1)
	}

	// Enter should submit immediately because a complete multi-line paste
	// does not extend the window.
	enterMsg := tea.KeyMsg{Type: tea.KeyEnter}
	next2, _ := m2.Update(enterMsg)
	m3, ok := next2.(Model)
	if !ok {
		t.Fatalf("expected Model, got %T", next2)
	}

	// pasteBlocks should be cleared
	if len(m3.chat.pasteBlocks) != 0 {
		t.Fatalf("expected paste blocks cleared, got %d", len(m3.chat.pasteBlocks))
	}

	// Transcript should have exactly ONE user message (not multiple)
	userCount := 0
	for _, line := range m3.chat.transcript {
		if line.Role == "user" {
			userCount++
		}
	}
	if userCount != 1 {
		t.Fatalf("expected exactly 1 user message, got %d", userCount)
	}

	// That message should contain all three lines
	found := false
	for _, line := range m3.chat.transcript {
		if line.Role == "user" && strings.Contains(line.Content, "line1") &&
			strings.Contains(line.Content, "line2") && strings.Contains(line.Content, "line3") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected user message with all three lines, got transcript: %v", m3.chat.transcript)
	}
}

// TestPasteBlockMultiplePastes verifies that two separate paste blocks can
// exist and that composeInput reconstructs them in the right order.
func TestPasteBlockMultiplePastes(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0

	// Paste first block
	next1, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("block1\nblock1b")})
	m2, ok := next1.(Model)
	if !ok {
		t.Fatalf("expected Model, got %T", next1)
	}
	if len(m2.chat.pasteBlocks) != 1 {
		t.Fatalf("expected 1 paste block, got %d", len(m2.chat.pasteBlocks))
	}

	// Simulate the paste window expiring, then paste a second block.
	m2.chat.pasteWindowEnd = time.Time{}
	next2, _ := m2.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("block2\nblock2b")})
	m3, ok := next2.(Model)
	if !ok {
		t.Fatalf("expected Model, got %T", next2)
	}

	if len(m3.chat.pasteBlocks) != 2 {
		t.Fatalf("expected 2 paste blocks, got %d", len(m3.chat.pasteBlocks))
	}

	// composeInput should reconstruct correctly
	full := m3.composeInput()
	if !strings.Contains(full, "block1") || !strings.Contains(full, "block2") {
		t.Fatalf("composeInput failed, got: %q", full)
	}
}

// TestPasteWindowsTerminal simulates Windows Terminal where multi-line paste
// arrives as separate KeyRunes chunks (one per line) with KeyEnter events
// between them. Everything must accumulate into ONE paste block.
func TestPasteWindowsTerminal(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0

	// First line triggers paste detection (long enough).
	next1, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("this is the first line")})
	m2, _ := next1.(Model)
	if len(m2.chat.pasteBlocks) != 1 {
		t.Fatalf("expected 1 paste block, got %d", len(m2.chat.pasteBlocks))
	}

	// Terminal sends Enter as separate KeyEnter event.
	t.Logf("before Enter: pasteBlocks=%d windowEnd=%v", len(m2.chat.pasteBlocks), m2.chat.pasteWindowEnd)
	next2, _ := m2.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m3, _ := next2.(Model)
	t.Logf("after Enter: pasteBlocks=%d windowEnd=%v input=%q", len(m3.chat.pasteBlocks), m3.chat.pasteWindowEnd, m3.chat.input)
	if len(m3.chat.pasteBlocks) != 1 {
		t.Fatalf("expected 1 paste block after Enter, got %d", len(m3.chat.pasteBlocks))
	}
	if strings.Count(m3.chat.pasteBlocks[0].content, "\n") != 1 {
		t.Fatalf("expected 1 newline in block, got %q", m3.chat.pasteBlocks[0].content)
	}

	// Second line is short but inside paste window.
	next3, _ := m3.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("line two")})
	m4, _ := next3.(Model)
	if len(m4.chat.pasteBlocks) != 1 {
		t.Fatalf("expected 1 paste block, got %d", len(m4.chat.pasteBlocks))
	}
	if !strings.Contains(m4.chat.pasteBlocks[0].content, "line two") {
		t.Fatalf("expected second line accumulated, got %q", m4.chat.pasteBlocks[0].content)
	}

	// Another Enter.
	next4, _ := m4.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m5, _ := next4.(Model)
	if strings.Count(m5.chat.pasteBlocks[0].content, "\n") != 2 {
		t.Fatalf("expected 2 newlines, got %q", m5.chat.pasteBlocks[0].content)
	}

	// Third line.
	next5, _ := m5.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("line three")})
	m6, _ := next5.(Model)
	if !strings.Contains(m6.chat.pasteBlocks[0].content, "line three") {
		t.Fatalf("expected third line, got %q", m6.chat.pasteBlocks[0].content)
	}

	// Wait for paste window to close, then manual Enter submits as ONE message.
	t.Logf("before final Enter: pasteBlocks=%d windowEnd=%v now=%v", len(m6.chat.pasteBlocks), m6.chat.pasteWindowEnd, time.Now())
	time.Sleep(250 * time.Millisecond)
	t.Logf("after sleep: now=%v", time.Now())
	next6, _ := m6.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m7, _ := next6.(Model)
	t.Logf("after final Enter: pasteBlocks=%d windowEnd=%v input=%q sending=%v", len(m7.chat.pasteBlocks), m7.chat.pasteWindowEnd, m7.chat.input, m7.chat.sending)

	if len(m7.chat.pasteBlocks) != 0 {
		t.Fatalf("expected blocks cleared, got %d", len(m7.chat.pasteBlocks))
	}

	userCount := 0
	for _, line := range m7.chat.transcript {
		if line.Role == "user" {
			userCount++
		}
	}
	if userCount != 1 {
		t.Fatalf("expected 1 user message, got %d", userCount)
	}

	found := false
	for _, line := range m7.chat.transcript {
		if line.Role == "user" && strings.Contains(line.Content, "first line") &&
			strings.Contains(line.Content, "line two") && strings.Contains(line.Content, "line three") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected single message with all lines, got: %v", m7.chat.transcript)
	}
}

func TestPasteBlockCtrlCCancels(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0

	// Paste something
	next1, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("to cancel\nmore lines")})
	m2, ok := next1.(Model)
	if !ok {
		t.Fatalf("expected Model, got %T", next1)
	}

	if len(m2.chat.pasteBlocks) != 1 {
		t.Fatalf("expected 1 paste block, got %d", len(m2.chat.pasteBlocks))
	}

	// Ctrl+C should cancel
	next2, _ := m2.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m3, ok := next2.(Model)
	if !ok {
		t.Fatalf("expected Model, got %T", next2)
	}

	if len(m3.chat.pasteBlocks) != 0 {
		t.Fatalf("expected paste blocks cleared after ctrl+c, got %d", len(m3.chat.pasteBlocks))
	}
	if m3.chat.input != "" {
		t.Fatalf("expected input cleared, got %q", m3.chat.input)
	}
}

// TestPasteBracketedPaste simulates a terminal with bracketed paste mode
// enabled. The entire multi-line paste arrives as a single KeyMsg with
// Paste=true, newlines intact, and submits as ONE message.
func TestPasteBracketedPaste(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0

	// Bracketed paste delivers everything in one message.
	pasteMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("line1\nline2\nline3"), Paste: true}
	next1, _ := m.Update(pasteMsg)
	m2, ok := next1.(Model)
	if !ok {
		t.Fatalf("expected Model, got %T", next1)
	}

	if len(m2.chat.pasteBlocks) != 1 {
		t.Fatalf("expected 1 paste block, got %d", len(m2.chat.pasteBlocks))
	}
	if !strings.Contains(m2.chat.input, "[pasted text #1") {
		t.Fatalf("expected placeholder in input, got %q", m2.chat.input)
	}

	// Manual Enter submits everything as one message.
	next2, _ := m2.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m3, ok := next2.(Model)
	if !ok {
		t.Fatalf("expected Model, got %T", next2)
	}

	if len(m3.chat.pasteBlocks) != 0 {
		t.Fatalf("expected paste blocks cleared after submit, got %d", len(m3.chat.pasteBlocks))
	}

	userCount := 0
	for _, line := range m3.chat.transcript {
		if line.Role == "user" {
			userCount++
		}
	}
	if userCount != 1 {
		t.Fatalf("expected exactly 1 user message, got %d", userCount)
	}

	found := false
	for _, line := range m3.chat.transcript {
		if line.Role == "user" && strings.Contains(line.Content, "line1") &&
			strings.Contains(line.Content, "line2") && strings.Contains(line.Content, "line3") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected user message with all three lines, got transcript: %v", m3.chat.transcript)
	}
}