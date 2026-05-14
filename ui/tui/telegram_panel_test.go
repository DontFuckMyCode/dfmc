package tui

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestTelegramOverlayRoutesSetupKeys(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.ui.panelOverlayKind = "telegram"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	mm := modelValue(t, next)
	if mm.telegram.setupSelected != 1 {
		t.Fatalf("telegram setup down key should select allowed-users row, got %d", mm.telegram.setupSelected)
	}

	next, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm = modelValue(t, next)
	if !mm.telegram.formActive {
		t.Fatal("telegram setup enter should open config form")
	}
	if !mm.telegram.editingUsers || mm.telegram.editingToken {
		t.Fatalf("enter on selected users row should edit users, token=%v users=%v", mm.telegram.editingToken, mm.telegram.editingUsers)
	}
}

func modelValue(t *testing.T, got tea.Model) Model {
	t.Helper()
	switch v := got.(type) {
	case Model:
		return v
	case *Model:
		return *v
	default:
		t.Fatalf("expected TUI model, got %T", got)
		return Model{}
	}
}

// Regression: pressing 'q' while in the Telegram token form must
// NOT close the panel — it must be typed into the token field.
// Bug: panel overlay q-handler at update_keypress.go:145 would intercept
// 'q' before routing to handleTelegramFormKey.
func TestTelegramFormAcceptsQLetter(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.ui.panelOverlayKind = "telegram"

	// Open form in token-edit mode
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = modelValue(t, next)
	if !m.telegram.formActive {
		t.Fatal("form should be active after enter")
	}
	if !m.telegram.editingToken {
		t.Fatal("should be editing token after opening from empty-state setup")
	}

	// Type 'q' — it must land in tokenInput, not close the panel
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = modelValue(t, next)

	if m.telegram.formActive == false {
		t.Fatal("BUG: 'q' closed the form — panel overlay q-handler intercepted it")
	}
	if m.telegram.tokenInput != "q" {
		t.Fatalf("expected tokenInput='q', got %q", m.telegram.tokenInput)
	}
	if m.ui.panelOverlayKind != "telegram" {
		t.Fatal("panel should still be open")
	}
}
