package tui

import (
	"strings"
	"testing"
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

// TestShortcutsTab_ActivatedViaAltH — the new Alt+H key binding
// must land on the Shortcuts tab from any starting tab.
func TestShortcutsTab_ActivatedViaAltH(t *testing.T) {
	m := newCoverageModel(t)
	idx := m.activityTabIndex("Shortcuts")
	if idx < 0 {
		t.Fatal("Shortcuts tab not registered")
	}
	got := m.activateDiagnosticTab("Shortcuts")
	if got.activeTab != idx {
		t.Errorf("Alt+H landing tab wrong: got %d want %d", got.activeTab, idx)
	}
}

// TestSlashShortcuts_OpensTab — /shortcuts and aliases should jump
// to the Shortcuts tab, not fall into the Unknown fallback.
func TestSlashShortcuts_OpensTab(t *testing.T) {
	for _, alias := range []string{"/shortcuts", "/keys", "/cheatsheet"} {
		m := newCoverageModel(t)
		next, _, handled := m.executeChatCommand(alias)
		if !handled {
			t.Errorf("%s should be handled", alias)
		}
		nm := next.(Model)
		idx := nm.activityTabIndex("Shortcuts")
		if nm.activeTab != idx {
			t.Errorf("%s didn't switch tabs: active=%d expected=%d",
				alias, nm.activeTab, idx)
		}
	}
}
