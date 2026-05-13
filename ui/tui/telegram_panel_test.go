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
