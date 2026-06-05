package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestActionMenu_OpensOnEnterAndClosesOnEsc is the core arrow-only
// contract: pressing Enter on a Files row pops the menu (not the
// preview directly), arrows pick, esc dismisses without firing.
func TestActionMenu_OpensOnEnterAndClosesOnEsc(t *testing.T) {
	m := newCoverageModel(t)
	m.filesView = filesViewState{
		entries: []string{"main.go"},
		index:   0,
	}

	// Enter on a Files row: menu opens, preview also loaded (cmd
	// returned for free since cursor moves preserve preview).
	got, _ := m.handleFilesKey(tea.KeyMsg{Type: tea.KeyEnter})
	gm := got.(Model)
	if !gm.actionMenu.open {
		t.Fatalf("expected action menu open after Enter, got closed")
	}
	if gm.actionMenu.owner != "Files" {
		t.Errorf("expected owner=Files, got %q", gm.actionMenu.owner)
	}
	if len(gm.actionMenu.actions) == 0 {
		t.Fatalf("expected menu actions, got none")
	}

	// Esc closes without firing.
	got2, _ := gm.handleFilesKey(tea.KeyMsg{Type: tea.KeyEsc})
	gm2 := got2.(Model)
	if gm2.actionMenu.open {
		t.Errorf("expected action menu closed after Esc, still open")
	}
}

// TestActionMenu_ArrowDownThenEnterRunsSecondAction — pressing down
// moves the cursor to the second action ("Pin to chat context"),
// Enter runs it. Verifies the arrow-only path actually changes state.
func TestActionMenu_ArrowDownThenEnterRunsSecondAction(t *testing.T) {
	m := newCoverageModel(t)
	m.filesView = filesViewState{
		entries: []string{"main.go"},
		index:   0,
	}

	got, _ := m.handleFilesKey(tea.KeyMsg{Type: tea.KeyEnter})
	gm := got.(Model)

	// Move cursor down once → "Pin to chat context".
	got2, _ := gm.handleFilesKey(tea.KeyMsg{Type: tea.KeyDown})
	gm2 := got2.(Model)
	if gm2.actionMenu.selected != 1 {
		t.Errorf("expected selected=1 after Down, got %d", gm2.actionMenu.selected)
	}

	// Enter runs the action and closes the menu.
	got3, _ := gm2.handleFilesKey(tea.KeyMsg{Type: tea.KeyEnter})
	gm3 := got3.(Model)
	if gm3.actionMenu.open {
		t.Errorf("expected menu closed after Enter, still open")
	}
	if gm3.filesView.pinned != "main.go" {
		t.Errorf("expected pinned=main.go after running pin action, got %q",
			gm3.filesView.pinned)
	}
}

// TestActionMenu_RendersActionLabels — the menu renders every action
// label so the user can read them with arrow keys instead of guessing
// the single-letter shortcuts.
func TestActionMenu_RendersActionLabels(t *testing.T) {
	m := newCoverageModel(t)
	m.filesView = filesViewState{entries: []string{"main.go"}, index: 0}
	got, _ := m.handleFilesKey(tea.KeyMsg{Type: tea.KeyEnter})
	gm := got.(Model)
	gm.activeTab = indexOfString(gm.tabs, "Files")

	// The menu is composited centrally now (overlayActionMenu in
	// renderActiveView), not appended inside the panel renderer.
	view := stripANSI(gm.renderActiveView(140, 30, paletteForTab("Files", false)))
	for _, want := range []string{
		"Open preview",
		"Pin to chat context",
		"Insert [[file:",
		"Explain this file",
		"Review this file",
		"Reload index",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("rendered menu missing %q. Got:\n%s", want, view)
		}
	}
}

func TestActiveViewLeavesRoomForActionMenu(t *testing.T) {
	m := newCoverageModel(t)
	m.filesView = filesViewState{entries: []string{"main.go"}, index: 0}
	got, _ := m.handleFilesKey(tea.KeyMsg{Type: tea.KeyEnter})
	gm := got.(Model)
	gm.activeTab = indexOfString(gm.tabs, "Files")

	view := stripANSI(gm.renderActiveView(140, 22, paletteForTab("Files", false)))
	if !strings.Contains(view, "Open preview") {
		t.Fatalf("active view should leave visible space for action menu, got:\n%s", view)
	}
}
