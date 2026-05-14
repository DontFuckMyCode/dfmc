package tui

// overlay_scroll_keys_test.go pins the keyboard contract for the two
// read-only reference overlays (Orchestrate, Shortcuts). Both are long
// digests that overflow stock terminal heights; the j/k/pgup/pgdn/g/G
// grammar is the only way the user can read past the first viewport.
// We also verify q closes any overlay — the close-hint in
// panel_overlay.go advertises "esc · q to close" and that promise was
// previously a lie for every demoted panel.

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestAdjustScrollOnlyOffset_StepsAndClamps(t *testing.T) {
	cases := []struct {
		key   string
		start int
		want  int
	}{
		{"j", 0, 1},
		{"down", 5, 6},
		{"k", 3, 2},
		{"up", 0, 0}, // already at top, must not go negative
		{"k", -2, 0}, // never returns negative
		{"pgdown", 0, 10},
		{"pgup", 5, 0}, // less than a page from top — snap to 0
		{"pgup", 25, 15},
		{"g", 999, 0},
		{"home", 42, 0},
		{"G", 0, 1 << 20},
		{"end", 0, 1 << 20},
		{"x", 7, 7}, // unrelated keys leave scroll untouched
	}
	for _, tc := range cases {
		got := adjustScrollOnlyOffset(tc.key, tc.start)
		if got != tc.want {
			t.Errorf("adjustScrollOnlyOffset(%q, %d) = %d, want %d", tc.key, tc.start, got, tc.want)
		}
	}
}

func TestHandleOrchestrateKey_AdjustsScroll(t *testing.T) {
	m := Model{diagnosticPanelsState: newDiagnosticPanelsState()}
	out, _ := m.handleOrchestrateKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if got := out.(Model).orchestrate.scroll; got != 1 {
		t.Fatalf("orchestrate scroll after j: got %d, want 1", got)
	}
}

func TestHandleShortcutsKey_AdjustsScroll(t *testing.T) {
	m := Model{diagnosticPanelsState: newDiagnosticPanelsState()}
	m.shortcuts.scroll = 5
	out, _ := m.handleShortcutsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	if got := out.(Model).shortcuts.scroll; got != 4 {
		t.Fatalf("shortcuts scroll after k: got %d, want 4", got)
	}
}

func TestFitPanelContentScrollable_AddsHintsAtEdges(t *testing.T) {
	body := "L1\nL2\nL3\nL4\nL5\nL6\nL7\nL8\nL9\nL10"
	out, _ := fitPanelContentScrollable(body, 4, 3)
	// Top hint must mention earlier content; bottom hint must mention more.
	if !strings.Contains(out, "earlier") {
		t.Fatalf("expected ↑ hint when scrolled past top: %q", out)
	}
	if !strings.Contains(out, "more") {
		t.Fatalf("expected ↓ hint when more content remains below: %q", out)
	}
}

func TestFitPanelContentScrollable_ClampsRunawayScroll(t *testing.T) {
	body := "A\nB\nC\nD\nE"
	out, clamped := fitPanelContentScrollable(body, 3, 100)
	// Whole content fits in 5 lines; clamped to maxScroll (5-3=2).
	if clamped != 2 {
		t.Fatalf("expected scroll clamped to 2, got %d", clamped)
	}
	// Last line should be the literal "E" (no truncation; we render the tail).
	if !strings.Contains(out, "E") {
		t.Fatalf("clamped scroll must reveal the tail line E: %q", out)
	}
}

// TestHelpOverlayRendersOnNonChatTab — pressing Ctrl+H on Files (or any
// non-Chat tab) used to silently set a flag with no visible effect
// because the help overlay was wired only inside the chat console
// widget. Now renderActiveView gives it the full body when a non-Chat
// tab is active. Regression guard.
func TestHelpOverlayRendersOnNonChatTab(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 1 // Files
	m.ui.showHelpOverlay = true
	m.width = 120
	m.height = 30
	out := m.View()
	// The help overlay always emits a section with the verbatim
	// "PANELS" label (defined in help.go's renderTUIHelp); easier to
	// pin the body content via the section title than to rely on the
	// per-tab quick-hint text which varies.
	if !strings.Contains(out, "PANELS") && !strings.Contains(out, "Keys") {
		t.Fatalf("help overlay missing on Files tab — View() output had no panel hotkey section:\n%s", out)
	}
}

// TestHelpOverlayQClosesOnNonChatTab — q is the universal "close this
// overlay" key. While help overlay is taking the body on a non-Chat
// tab, q must close it just like esc does.
func TestHelpOverlayQClosesOnNonChatTab(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 1 // Files
	m.ui.showHelpOverlay = true
	out, _ := m.routeKeyByActiveTab(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if out.(Model).ui.showHelpOverlay {
		t.Fatalf("q must close help overlay on non-Chat tab")
	}
}

// TestTabSwitchClosesActionMenu — when an action menu is open in one
// panel (the menu's owner field pins it there), switching to another
// tab must close it. Without this, the menu carries cross-tab to a
// panel that has no idea what its actions mean — pressing Enter then
// fires a Files action while the user thinks they're on Patch.
func TestTabSwitchClosesActionMenu(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m = m.openActionMenu("files", "Files actions", []panelAction{{
		Label: "Pin",
		Accel: "p",
	}})
	if !m.actionMenu.open {
		t.Fatalf("setup: action menu didn't open")
	}
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyF3}) // jump to Patch
	if out.(Model).actionMenu.open {
		t.Fatalf("F3 left action menu open; owner=%q would now fire on Patch", out.(Model).actionMenu.owner)
	}
}

// TestActivateDiagnosticTabClosesActionMenu — the same expectation for
// activateDiagnosticTab, the single entry point used by F9..F12 +
// Shift+F1..F5 + Alt+letter aliases.
func TestActivateDiagnosticTabClosesActionMenu(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m = m.openActionMenu("files", "Files", []panelAction{{Label: "Pin"}})
	out := m.activateDiagnosticTab("Status")
	if out.actionMenu.open {
		t.Fatalf("activateDiagnosticTab left action menu open across panel switch")
	}
}

// TestTabSwitchResetsToolsEditing — the Tools panel's param editor
// captures every keystroke into a draft buffer (m.toolView.editing).
// Leaving Tools while editing without this reset stranded the user in
// editing mode on next visit; their next keystrokes silently fed into
// a stale draft. Centralised in resetTabSwitchAffordances so every
// tab-switch path triggers it.
func TestTabSwitchResetsToolsEditing(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.toolView.editing = true
	m.toolView.draft = "param=value"
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyF2}) // Files
	next := out.(Model)
	if next.toolView.editing {
		t.Errorf("Tools editing mode persisted across F2; would silently capture next keystrokes")
	}
	if next.toolView.draft != "" {
		t.Errorf("Tools draft persisted: %q", next.toolView.draft)
	}
}

// TestTabSwitchClearsSelectionMode — selection mode is a Chat-only
// drag-select-transcript feature; renderActiveView paints a top
// divider on every tab while it's set. Leaving Chat with selection
// mode on would clutter Files / Patch / etc. with an unexplained line.
// Centralised cleanup must clear it on every tab switch.
func TestTabSwitchClearsSelectionMode(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.ui.selectionModeActive = true
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyF2})
	if out.(Model).ui.selectionModeActive {
		t.Fatalf("F2 left selection mode active; would render a stray divider on Files")
	}
}

// TestStatsPanelVisibleOnHundredColTerminal — pins the threshold lowered
// to 88 cols. Previous 120-col threshold meant a stock 100×30 terminal
// (Windows Terminal default, most SSH sessions) never saw the panel,
// and users assumed it had been deleted. Without this regression guard
// a future bump back to 120+ would silently re-break the same surface.
func TestStatsPanelVisibleOnHundredColTerminal(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.ui.showStatsPanel = true
	// width 100 → contentWidth = 100 - 6 = 94 in renderActiveView, well
	// above the new 88 floor. statsPanelVisible takes contentWidth so we
	// pass that directly.
	if !m.statsPanelVisible(94) {
		t.Fatalf("stats panel hidden at contentWidth=94; threshold regression — adjust StatsPanelMinContentWidth in theme/stats_panel.go")
	}
}

// TestHelpOverlayScrollOnNonChatTab — when help overlay covers the body
// on a non-Chat tab, j/k/pgup/pgdn/g/G adjust the scroll offset (the
// content can be 80+ rows tall — without scroll the bottom is unreachable).
// On Chat tab the help is the inline composer-filtered widget, so this
// path is bypassed.
func TestHelpOverlayScrollOnNonChatTab(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 1 // Files
	m.ui.showHelpOverlay = true

	out, _ := m.routeKeyByActiveTab(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if got := out.(Model).helpOverlay.scroll; got != 1 {
		t.Errorf("j on help overlay: scroll=%d, want 1", got)
	}

	m2 := out.(Model)
	out2, _ := m2.routeKeyByActiveTab(tea.KeyMsg{Type: tea.KeyPgDown})
	if got := out2.(Model).helpOverlay.scroll; got != 1+scrollOnlyOverlayPageStep {
		t.Errorf("pgdown on help overlay: scroll=%d, want %d", got, 1+scrollOnlyOverlayPageStep)
	}
}

// TestFKeyClosesHelpOverlay — pressing a panel hotkey while help is up
// is the user explicitly leaving the help context. Without closing it
// the new tab renders with the help overlay frozen on top, identical
// to the stale-panel-overlay regression at TestFKeyClearsStaleOverlay
// and equally confusing.
func TestFKeyClosesHelpOverlay(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.ui.showHelpOverlay = true
	m.chat.input = "patch"
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyF3}) // Patch
	next := out.(Model)
	if next.ui.showHelpOverlay {
		t.Errorf("F3 left help overlay open across tab switch")
	}
}

// TestEscOnHelpOverlay_HonoursTitleHint — the help overlay title
// advertises "esc to clear" (filtered) and "ctrl+h to close" (unfiltered).
// Both promises must hold: with a filter active esc clears it; without
// a filter esc closes the overlay. Regression guard for 2026-05-08 —
// previously esc fell through and did neither, contradicting the hint.
func TestEscOnHelpOverlay_HonoursTitleHint(t *testing.T) {
	// Case 1: filter active → esc clears the filter, overlay stays open.
	m := NewModel(context.Background(), nil)
	m.ui.showHelpOverlay = true
	m.chat.input = "patch"
	m.chat.cursor = len(m.chat.input)
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	next := out.(Model)
	if !next.ui.showHelpOverlay {
		t.Errorf("filter-active esc should keep overlay open, got closed")
	}
	if strings.TrimSpace(next.chat.input) != "" {
		t.Errorf("filter-active esc should clear chat input, got %q", next.chat.input)
	}

	// Case 2: no filter → esc closes the overlay.
	m2 := NewModel(context.Background(), nil)
	m2.ui.showHelpOverlay = true
	m2.chat.input = ""
	out2, _ := m2.Update(tea.KeyMsg{Type: tea.KeyEsc})
	next2 := out2.(Model)
	if next2.ui.showHelpOverlay {
		t.Errorf("empty-filter esc should close overlay, still open")
	}
}

// TestFKeyClearsStaleOverlay — pressing F1..F8 (a first-class tab key)
// while a panel overlay is open must close that overlay, otherwise the
// user lands on the new tab with the previous overlay frozen on top
// and rationally assumes the F-key is broken. Regression guard for
// 2026-05-08.
func TestFKeyClearsStaleOverlay(t *testing.T) {
	cases := []struct {
		key tea.KeyType
	}{
		{tea.KeyF1}, {tea.KeyF2}, {tea.KeyF3}, {tea.KeyF4},
		{tea.KeyF5}, {tea.KeyF6}, {tea.KeyF7}, {tea.KeyF8},
	}
	for _, c := range cases {
		m := NewModel(context.Background(), nil)
		m.ui.panelOverlayKind = "codemap" // simulate stale overlay
		out, _ := m.Update(tea.KeyMsg{Type: c.key})
		next := out.(Model)
		if next.ui.panelOverlayKind != "" {
			t.Errorf("F-key %v left overlay open: %q", c.key, next.ui.panelOverlayKind)
		}
	}
}

// TestTabCycleClearsStaleOverlay — same expectation for Tab/Shift+Tab
// when cycling first-class tabs; otherwise the user has to press esc
// after every tab change just to see the body.
func TestTabCycleClearsStaleOverlay(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 1                  // Files (Tab from Chat is suppressed)
	m.ui.panelOverlayKind = "status" // stale
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	next := out.(Model)
	if next.ui.panelOverlayKind != "" {
		t.Fatalf("Tab left overlay %q open after tab switch", next.ui.panelOverlayKind)
	}
}

func TestRouteKeyByActiveTab_QClosesAnyOverlay(t *testing.T) {
	for _, kind := range []string{"status", "tools", "codemap", "prompts",
		"security", "plans", "context", "orchestrate", "shortcuts"} {
		m := Model{
			tabs:                  []string{"Chat", "Files"},
			diagnosticPanelsState: newDiagnosticPanelsState(),
		}
		m.ui.panelOverlayKind = kind
		out, _ := m.routeKeyByActiveTab(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
		if out.(Model).ui.panelOverlayKind != "" {
			t.Fatalf("kind=%q: q must close the overlay, still %q", kind, out.(Model).ui.panelOverlayKind)
		}
	}
}
