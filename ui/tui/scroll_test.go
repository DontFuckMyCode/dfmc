package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func makeTranscript(n int) []chatLine {
	out := make([]chatLine, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, chatLine{Role: "user", Content: "msg " + itoaSmall(i)})
	}
	return out
}

func itoaSmall(i int) string {
	if i < 10 {
		return string(rune('0' + i))
	}
	return string(rune('0'+i/10)) + string(rune('0'+i%10))
}

func TestScrollTranscriptClampsAndPages(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.transcript = makeTranscript(40)

	m.scrollTranscript(-8)
	if m.chat.scrollback != 8 {
		t.Fatalf("PageUp once should scroll 8, got %d", m.chat.scrollback)
	}

	// scrollTranscript uses a line-based ceiling now — each 1-line message
	// is estimated at ~2 rendered lines (header bar + content bar), so the
	// upper bound for a 40-message transcript is generous (~80 lines).
	// We just check monotonic scrollback and that the notice mentions the
	// new line-centric copy.
	for i := 0; i < 15; i++ {
		m.scrollTranscript(-8)
	}
	if m.chat.scrollback == 0 {
		t.Fatalf("expected chatScrollback > 0 after many PageUps, got 0")
	}
	ceiling := estimateTranscriptLines(m.chat.transcript)
	if m.chat.scrollback > ceiling {
		t.Fatalf("scrollback %d should not exceed estimate ceiling %d", m.chat.scrollback, ceiling)
	}

	// Further PageUps clamp at the top — notice should say so.
	before := m.chat.scrollback
	m.scrollTranscript(-8)
	if m.chat.scrollback != before {
		t.Fatalf("already at top, further PageUp should not change scrollback, got %d (was %d)", m.chat.scrollback, before)
	}
	if !strings.Contains(m.notice, "top") {
		t.Fatalf("expected top-of-history notice, got %q", m.notice)
	}

	m.scrollTranscript(8)
	if m.chat.scrollback != before-8 {
		t.Fatalf("PageDown should subtract 8, got %d (was %d)", m.chat.scrollback, before)
	}
}

func TestPageUpPageDownKeysScrollTranscript(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.transcript = makeTranscript(30)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	mm, ok := next.(Model)
	if !ok {
		t.Fatalf("expected Model after PageUp, got %T", next)
	}
	if mm.chat.scrollback != 8 {
		t.Fatalf("PageUp should scroll 8 back, got %d", mm.chat.scrollback)
	}

	next2, _ := mm.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	mm2, ok := next2.(Model)
	if !ok {
		t.Fatalf("expected Model after PageDown, got %T", next2)
	}
	if mm2.chat.scrollback != 0 {
		t.Fatalf("PageDown should return to latest, got %d", mm2.chat.scrollback)
	}
}

func TestShiftAndCtrlArrowKeysScrollTranscript(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.transcript = makeTranscript(30)

	for _, keyType := range []tea.KeyType{tea.KeyShiftUp, tea.KeyCtrlUp} {
		next, _ := m.Update(tea.KeyMsg{Type: keyType})
		mm, ok := next.(Model)
		if !ok {
			t.Fatalf("expected Model, got %T", next)
		}
		if mm.chat.scrollback != 3 {
			t.Fatalf("%v should scroll back 3 lines, got %d", keyType, mm.chat.scrollback)
		}
		// Reset for next iteration.
		m.chat.scrollback = 0
	}

	// Now scroll back then test the "down" variants bring us forward.
	m.chat.scrollback = 9
	for _, keyType := range []tea.KeyType{tea.KeyShiftDown, tea.KeyCtrlDown} {
		m.chat.scrollback = 9
		next, _ := m.Update(tea.KeyMsg{Type: keyType})
		mm, ok := next.(Model)
		if !ok {
			t.Fatalf("expected Model, got %T", next)
		}
		if mm.chat.scrollback != 6 {
			t.Fatalf("%v should scroll forward 3 lines, got %d", keyType, mm.chat.scrollback)
		}
	}
}

func TestMouseWheelScrollsTranscriptOnChatTab(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.transcript = makeTranscript(30)

	next, _ := m.Update(tea.MouseMsg{
		Button: tea.MouseButtonWheelUp,
		Action: tea.MouseActionPress,
	})
	mm, ok := next.(Model)
	if !ok {
		t.Fatalf("expected Model after wheel up, got %T", next)
	}
	if mm.chat.scrollback != mouseWheelStep {
		t.Fatalf("wheel up should scroll back %d lines, got %d", mouseWheelStep, mm.chat.scrollback)
	}

	next2, _ := mm.Update(tea.MouseMsg{
		Button: tea.MouseButtonWheelDown,
		Action: tea.MouseActionPress,
	})
	mm2, ok := next2.(Model)
	if !ok {
		t.Fatalf("expected Model after wheel down, got %T", next2)
	}
	if mm2.chat.scrollback != 0 {
		t.Fatalf("wheel down should return toward latest, got %d", mm2.chat.scrollback)
	}
}

// Shift+wheel should jump a half-page, not a single tick. Pin so the
// power-user shortcut survives a future "simplify mouse handler" pass.
func TestShiftMouseWheelJumpsPage(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.transcript = makeTranscript(60)
	m.activeTab = 0

	next, _ := m.Update(tea.MouseMsg{
		Button: tea.MouseButtonWheelUp,
		Action: tea.MouseActionPress,
		Shift:  true,
	})
	mm, ok := next.(Model)
	if !ok {
		t.Fatalf("expected Model after shift+wheel, got %T", next)
	}
	if mm.chat.scrollback != mouseWheelPageStep {
		t.Fatalf("shift+wheel should jump %d lines, got %d", mouseWheelPageStep, mm.chat.scrollback)
	}
}

func TestMouseWheelIgnoredOffChatTab(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.transcript = makeTranscript(30)
	m.activeTab = 1 // Status

	next, _ := m.Update(tea.MouseMsg{
		Button: tea.MouseButtonWheelUp,
		Action: tea.MouseActionPress,
	})
	mm, ok := next.(Model)
	if !ok {
		t.Fatalf("expected Model, got %T", next)
	}
	if mm.chat.scrollback != 0 {
		t.Fatalf("wheel events should be ignored off the chat tab, got %d", mm.chat.scrollback)
	}
}

func TestNewMessageSnapsScrollbackToZero(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.transcript = makeTranscript(30)
	m.chat.scrollback = 10

	m = m.appendSystemMessage("system event")
	if m.chat.scrollback != 0 {
		t.Fatalf("new message should snap scrollback to 0, got %d", m.chat.scrollback)
	}
}

func TestFitChatBodyKeepsTailAlwaysVisible(t *testing.T) {
	head := strings.Join([]string{
		"H1", "H2", "H3", "H4", "H5", "H6", "H7", "H8", "H9", "H10",
	}, "\n")
	tail := strings.Join([]string{"TAIL-1", "TAIL-2", "TAIL-3"}, "\n")
	out := fitChatBody(head, tail, 7, 0)
	// Tail must be present in full, even when head is much longer than
	// the remaining budget.
	for _, want := range []string{"TAIL-1", "TAIL-2", "TAIL-3"} {
		if !strings.Contains(out, want) {
			t.Fatalf("fitChatBody dropped tail line %q, got:\n%s", want, out)
		}
	}
	// Last head line should be visible; earliest should be clipped.
	if !strings.Contains(out, "H10") {
		t.Fatalf("fitChatBody should keep the tail of the head, got:\n%s", out)
	}
	if strings.Contains(out, "H1\n") {
		t.Fatalf("fitChatBody should clip the top of the head, got:\n%s", out)
	}
}

func TestFitChatBodyScrollbackShiftsWindow(t *testing.T) {
	headLines := []string{}
	for i := 1; i <= 20; i++ {
		headLines = append(headLines, "L"+itoaSmall(i))
	}
	head := strings.Join(headLines, "\n")
	tail := "INPUT"
	// 5 head lines visible (maxLines 6 - 1 tail).
	outTop := fitChatBody(head, tail, 6, 0)
	if !strings.Contains(outTop, "L20") {
		t.Fatalf("no scrollback should show tail of head, got:\n%s", outTop)
	}
	outBack := fitChatBody(head, tail, 6, 6)
	// Scrollback of 6 on a 20-line head with 5-line window surfaces
	// roughly L10..L14 with the first/last line replaced by hint markers,
	// so L11..L13 are definitely visible.
	if !strings.Contains(outBack, "L11") {
		t.Fatalf("scrollback should surface older lines, got:\n%s", outBack)
	}
	if strings.Contains(outBack, "L20") {
		t.Fatalf("scrollback should hide newest lines, got:\n%s", outBack)
	}
	if !strings.Contains(outBack, "earlier lines") || !strings.Contains(outBack, "newer lines") {
		t.Fatalf("expected both scrollback hints, got:\n%s", outBack)
	}
}

func TestRenderChatViewShowsScrollHintsWhenScrolledBack(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.transcript = makeTranscript(30)
	m.chat.scrollback = 10
	m.width = 120
	m.height = 20

	// fitChatBody is what decorates the scrollback hints; trigger through
	// renderActiveView so the real layout path runs.
	view := m.renderActiveView(120, 20, paletteForTab("Chat", false))
	if !strings.Contains(view, "earlier lines") {
		t.Fatalf("expected scroll-up hint when scrolled back, got:\n%s", view)
	}
}

func TestEndKeyJumpsToLatestWhenScrolledBack(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.transcript = makeTranscript(30)
	m.chat.scrollback = 10

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	mm, ok := next.(Model)
	if !ok {
		t.Fatalf("expected Model, got %T", next)
	}
	if mm.chat.scrollback != 0 {
		t.Fatalf("End should snap scrollback to 0 first, got %d", mm.chat.scrollback)
	}
}
