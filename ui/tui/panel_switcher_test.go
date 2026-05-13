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

func TestCtrlAltTOpensToolStatus(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0

	next, _, handled := m.handleGlobalShortcuts(tea.KeyMsg{Type: tea.KeyCtrlT, Alt: true})
	if !handled {
		t.Fatal("Ctrl+Alt+T should be handled as the ToolStatus shortcut")
	}
	mm := next.(Model)
	if mm.ui.panelOverlayKind != "toolstatus" {
		t.Fatalf("Ctrl+Alt+T should open ToolStatus, got %q", mm.ui.panelOverlayKind)
	}
}

func TestAltTOpensToolStatusEvenWithChatInput(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0
	m.setChatInput("typing")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}, Alt: true})
	mm := next.(Model)
	if mm.ui.panelOverlayKind != "toolstatus" {
		t.Fatalf("Alt+T should open ToolStatus even while typing, got %q", mm.ui.panelOverlayKind)
	}
}

func TestToolStatusOverlayOwnsKeys(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.ui.panelOverlayKind = "toolstatus"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	mm := next.(Model)
	if mm.ui.panelOverlayKind != "" {
		t.Fatalf("Esc should close ToolStatus overlay, got %q", mm.ui.panelOverlayKind)
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

func TestPanelSwitcherProvidersOpensStatsProviderMode(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 2
	m = m.openPanelSwitcher()
	for _, r := range "providers" {
		next, _, _ := m.handlePanelSwitcherKey(tea.KeyMsg{
			Type: tea.KeyRunes, Runes: []rune{r},
		})
		m = next.(Model)
	}

	next, _, handled := m.handlePanelSwitcherKey(tea.KeyMsg{Type: tea.KeyEnter})
	if !handled {
		t.Fatal("enter must be handled by the switcher")
	}
	mm := next.(Model)
	if mm.activeTab != 0 {
		t.Fatalf("Providers switcher entry should return to Chat stats panel, got tab %d", mm.activeTab)
	}
	if mm.ui.panelOverlayKind != "" {
		t.Fatalf("Providers status should not leave a panel overlay open, got %q", mm.ui.panelOverlayKind)
	}
	if !mm.ui.showStatsPanel || mm.ui.statsPanelMode != statsPanelModeProviders {
		t.Fatalf("Providers switcher entry should open stats providers mode, show=%v mode=%q", mm.ui.showStatsPanel, mm.ui.statsPanelMode)
	}
}

func TestPanelSwitcherProviderConfigOpensProvidersTab(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m = m.openPanelSwitcher()
	for _, r := range "config" {
		next, _, _ := m.handlePanelSwitcherKey(tea.KeyMsg{
			Type: tea.KeyRunes, Runes: []rune{r},
		})
		m = next.(Model)
	}

	next, _, handled := m.handlePanelSwitcherKey(tea.KeyMsg{Type: tea.KeyEnter})
	if !handled {
		t.Fatal("enter must be handled by the switcher")
	}
	mm := next.(Model)
	if mm.activeTab != 7 {
		t.Fatalf("Provider Config should open the providers config tab, got tab %d", mm.activeTab)
	}
	if !mm.providers.loaded {
		t.Fatal("Provider Config should refresh/seed provider rows")
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
