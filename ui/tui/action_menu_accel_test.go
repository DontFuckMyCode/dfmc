package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// action_menu_accel_test.go pins the action-menu's direct-fire
// accelerator feature: when the menu is open, pressing the letter
// shown in [brackets] next to an action fires that action without
// requiring arrow-then-enter. The feature is already supported by
// handleActionMenuKey (lines 107-116) but was invisible in the menu
// hint — users wouldn't try pressing `t` because they didn't know
// it would do anything.

func TestActionMenu_HintAdvertisesDirectAccel(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m = m.openActionMenu("test", "test menu", []panelAction{
		{Label: "Do thing", Accel: "x"},
	})
	out := ansi.Strip(m.renderActionMenu(80))
	if !strings.Contains(out, "[letter] direct") {
		t.Errorf("action menu hint must advertise the [letter]-direct affordance, got:\n%s", out)
	}
	// And the rendered row must show the bracketed accel so the
	// hint's promise matches the affordance the user sees.
	if !strings.Contains(out, "[x]") {
		t.Errorf("action menu must render the [accel] bracket next to actions, got:\n%s", out)
	}
}

func TestActionMenu_AccelDirectFireWorks(t *testing.T) {
	fired := ""
	m := NewModel(context.Background(), nil)
	m = m.openActionMenu("test", "test menu", []panelAction{
		{Label: "Action A", Accel: "a", Handler: func(m Model) (Model, tea.Cmd) {
			fired = "a"
			return m, nil
		}},
		{Label: "Action B", Accel: "b", Handler: func(m Model) (Model, tea.Cmd) {
			fired = "b"
			return m, nil
		}},
	})
	// Pressing the second action's accel (`b`) must fire it and close
	// the menu — even though the cursor is on the first row.
	if m.actionMenu.selected != 0 {
		t.Fatalf("test fixture: expected cursor at 0, got %d", m.actionMenu.selected)
	}
	nm, _, handled := m.handleActionMenuKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	if !handled {
		t.Fatal("accel key should be marked handled by the menu")
	}
	if fired != "b" {
		t.Errorf("expected action B to fire on direct-press of `b`, got fired=%q", fired)
	}
	if nm.actionMenu.open {
		t.Errorf("menu should close after firing the action via direct-accel")
	}
}

func TestActionMenu_RendersAllAccelsInBrackets(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m = m.openActionMenu("test", "menu", []panelAction{
		{Label: "Run", Accel: "r"},
		{Label: "Edit", Accel: "e"},
		{Label: "Quit"}, // no Accel
	})
	out := ansi.Strip(m.renderActionMenu(80))
	for _, want := range []string{"[r]", "[e]"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected accel %q in rendered menu, got:\n%s", want, out)
		}
	}
	// Action with no Accel must NOT render an empty `[]` chip.
	if strings.Contains(out, "[]") {
		t.Errorf("action with no Accel should not render empty brackets, got:\n%s", out)
	}
}
