package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// Ctrl+B must toggle the panel switcher overlay open and closed.
// The overlay is the workaround for terminals that eat F-keys
// (F11 → fullscreen, F1 → terminal help, F4 → close-tab) so it must
// reach the user even when their terminal swallows function keys.
func TestPanelSwitcherCtrlBTogglesOpen(t *testing.T) {
	m := NewModel(context.Background(), nil)
	if m.panelSwitcher.active {
		t.Fatal("panel switcher should start closed")
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlB})
	mm := next.(Model)
	if !mm.panelSwitcher.active {
		t.Fatal("Ctrl+B should open the panel switcher")
	}

	// Second Ctrl+B closes.
	next2, _ := mm.Update(tea.KeyMsg{Type: tea.KeyCtrlB})
	mm2 := next2.(Model)
	if mm2.panelSwitcher.active {
		t.Fatal("second Ctrl+B should close the panel switcher")
	}
}

// Filtering must narrow the entry list. Typing "cont" filters down to
// Contexts/Conversations/Context — three entries that all carry the
// substring.
func TestPanelSwitcherFilterByQuery(t *testing.T) {
	all := filteredPanelSwitcherEntries("")
	if len(all) < 18 {
		t.Fatalf("expected at least 18 panels in canonical list, got %d", len(all))
	}
	contains := filteredPanelSwitcherEntries("cont")
	if len(contains) == 0 {
		t.Fatal("'cont' should match Contexts/Conversations/Context")
	}
	for _, e := range contains {
		hay := strings.ToLower(e.Label + " " + e.Hint + " " + e.KeyHint)
		if !strings.Contains(hay, "cont") {
			t.Errorf("entry %q matched but doesn't contain 'cont'", e.Label)
		}
	}
}

// Pressing enter on a filtered entry must close the switcher and
// route the activeTab/overlay to that panel. Test the full round-trip.
func TestPanelSwitcherEnterSwitchesToPanel(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m = m.openPanelSwitcher()
	// Type "cont" — first match should be Contexts.
	for _, r := range "context" {
		next, _, _ := m.handlePanelSwitcherKey(tea.KeyMsg{
			Type: tea.KeyRunes, Runes: []rune{r},
		})
		m = next.(Model)
	}
	// Enter to switch.
	next, _, handled := m.handlePanelSwitcherKey(tea.KeyMsg{Type: tea.KeyEnter})
	if !handled {
		t.Fatal("enter must be handled by the switcher")
	}
	mm := next.(Model)
	if mm.panelSwitcher.active {
		t.Fatal("enter should close the switcher")
	}
	// "context" filter matches Context overlay first (canonical order).
	if mm.ui.panelOverlayKind != "context" {
		t.Errorf("expected panelOverlayKind=context after switching, got %q", mm.ui.panelOverlayKind)
	}
}

// Esc must close without changing the active panel — purely a cancel.
func TestPanelSwitcherEscClosesWithoutSwitching(t *testing.T) {
	m := NewModel(context.Background(), nil)
	originalTab := m.activeTab
	originalKind := m.ui.panelOverlayKind
	m = m.openPanelSwitcher()
	next, _, _ := m.handlePanelSwitcherKey(tea.KeyMsg{Type: tea.KeyEsc})
	mm := next.(Model)
	if mm.panelSwitcher.active {
		t.Fatal("esc should close the switcher")
	}
	if mm.activeTab != originalTab {
		t.Errorf("esc must not change activeTab; was %d, now %d", originalTab, mm.activeTab)
	}
	if mm.ui.panelOverlayKind != originalKind {
		t.Errorf("esc must not change overlay kind; was %q, now %q", originalKind, mm.ui.panelOverlayKind)
	}
}

// Backspace deletes one rune from the query. Empty query is a no-op.
func TestPanelSwitcherBackspaceTrimsQuery(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m = m.openPanelSwitcher()
	for _, r := range "abc" {
		next, _, _ := m.handlePanelSwitcherKey(tea.KeyMsg{
			Type: tea.KeyRunes, Runes: []rune{r},
		})
		m = next.(Model)
	}
	if m.panelSwitcher.query != "abc" {
		t.Fatalf("expected query 'abc', got %q", m.panelSwitcher.query)
	}
	next, _, _ := m.handlePanelSwitcherKey(tea.KeyMsg{Type: tea.KeyBackspace})
	mm := next.(Model)
	if mm.panelSwitcher.query != "ab" {
		t.Errorf("backspace should leave 'ab', got %q", mm.panelSwitcher.query)
	}
}

// renderPanelSwitcher must render without panicking and surface the
// title + at least one entry on a default-width call.
func TestPanelSwitcherRendersWithoutPanic(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m = m.openPanelSwitcher()
	out := m.renderPanelSwitcher(80)
	if !strings.Contains(out, "Switch Panel") {
		t.Errorf("expected title 'Switch Panel' in render, got:\n%s", out)
	}
	if !strings.Contains(out, "Chat") {
		t.Errorf("expected at least one panel entry (Chat) in render, got:\n%s", out)
	}
}
