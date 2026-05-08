package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestShortcutsView_RendersAllSections — the cheat sheet must always
// surface every section so a user opening it for the first time
// sees the full taxonomy of "where can I find what" without scrolling.
func TestShortcutsView_RendersAllSections(t *testing.T) {
	m := newCoverageModel(t)
	view := stripANSI(m.renderShortcutsView(120))
	for _, want := range []string{
		"Shortcuts",
		"PANELS",
		"CHAT COMPOSER",
		"STATS PANEL",
		"CONTROL · STOP/CLEAR",
		"DIAGNOSTICS · INSPECT",
		"EVERYDAY SLASH COMMANDS",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("Shortcuts view missing section %q. Got:\n%s", want, view)
		}
	}
}

// TestShortcutsView_ListsEveryTab — pin that the panel catalog row
// list mentions every tab name we ship. A new tab added to NewModel
// without adding it here would mean a user scanning the cheat sheet
// could miss a panel entirely.
func TestShortcutsView_ListsEveryTab(t *testing.T) {
	m := newCoverageModel(t)
	view := stripANSI(m.renderShortcutsView(120))
	for _, tab := range m.tabs {
		if !strings.Contains(view, tab) {
			t.Errorf("PANELS section missing tab %q. Got:\n%s", tab, view)
		}
	}
}

// TestShortcutsView_ListsControlCommands — pin the stop/clear
// surface so a regression that drops one of the user-facing
// control commands fails this test instead of silently shipping.
func TestShortcutsView_ListsControlCommands(t *testing.T) {
	m := newCoverageModel(t)
	view := stripANSI(m.renderShortcutsView(120))
	for _, want := range []string{
		"Ctrl+C",
		"/cancel",
		"/drive stop",
		"/todos clear",
		"/tasks clear",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("CONTROL section missing %q. Got:\n%s", want, view)
		}
	}
}

// TestAltH_FlipsHelpOverlay — Phase K (help unification): Alt+H now
// flips the same Ctrl+H help overlay rather than opening the legacy
// Shortcuts panel. ctrl+h / alt+h / /help / /shortcuts all converge.
func TestAltH_FlipsHelpOverlay(t *testing.T) {
	m := newCoverageModel(t)
	if m.ui.showHelpOverlay {
		t.Fatal("help overlay should default to off")
	}
	next, _, _ := m.handleGlobalShortcuts(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}, Alt: true})
	nm := next.(Model)
	if !nm.ui.showHelpOverlay {
		t.Errorf("alt+h did not open the help overlay")
	}
	// Second alt+h closes it (toggle).
	again, _, _ := nm.handleGlobalShortcuts(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}, Alt: true})
	am := again.(Model)
	if am.ui.showHelpOverlay {
		t.Errorf("second alt+h did not close the help overlay")
	}
}

// TestSlashShortcuts_OpensHelpOverlay — Phase K (help unification):
// /shortcuts and aliases open the SAME Ctrl+H help overlay rather
// than the legacy Shortcuts panel, so there's a single place to look
// up keys.
func TestSlashShortcuts_OpensHelpOverlay(t *testing.T) {
	for _, alias := range []string{"/shortcuts", "/keys", "/cheatsheet"} {
		m := newCoverageModel(t)
		next, _, handled := m.executeChatCommand(alias)
		if !handled {
			t.Errorf("%s should be handled", alias)
		}
		nm := next.(Model)
		if !nm.ui.showHelpOverlay {
			t.Errorf("%s did not open the help overlay", alias)
		}
	}
}
