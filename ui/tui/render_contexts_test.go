package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// renderContextsView must render every section without panicking, even
// when the engine is nil and no run is active. The Active Contexts panel
// is reachable from any tab via Shift+F6, so a panic here would
// instantly crash the TUI on a fresh start.
func TestRenderContextsViewSurvivesNilEngine(t *testing.T) {
	m := NewModel(context.Background(), nil)
	out := m.renderContextsView(120)
	for _, want := range []string{
		"Active Contexts",
		"MAIN AGENT",
		"PARKED AGENT",
		"SUB-AGENTS",
		"DRIVE",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected section %q in Contexts panel, got:\n%s", want, out)
		}
	}
}

// Shift+F6 / F18 must route through activateDiagnosticTab("Contexts")
// onto the panelOverlayKind = "contexts" branch. The shortcut handler
// code path is exercised here via demotedPanelKinds so we don't need a
// full key-event simulation.
func TestContextsOverlayKindWiredCorrectly(t *testing.T) {
	kind, ok := demotedPanelKinds["Contexts"]
	if !ok {
		t.Fatal("Contexts label missing from demotedPanelKinds — Shift+F6 wiring broken")
	}
	if kind != "contexts" {
		t.Fatalf("expected overlay kind 'contexts', got %q", kind)
	}
	// helpOverlayTabHints must surface the panel so users find the keymap.
	hints := helpOverlayTabHints("contexts")
	if len(hints) == 0 {
		t.Fatal("contexts panel has no help hints")
	}
	joined := strings.Join(hints, " ")
	if !strings.Contains(joined, "shift+f6") {
		t.Errorf("contexts hint should mention shift+f6, got: %s", joined)
	}
}

// Up/down on the Contexts overlay must move the section cursor, not
// just scroll the body. The renderer prefixes the selected section
// title with "▶" so the user can see which one Enter would act on.
func TestContextsOverlayKey_UpDownMovesSection(t *testing.T) {
	m := NewModel(context.Background(), nil)
	// Start at MAIN (index 0) → down should land on PARKED.
	out, _ := m.handleContextsOverlayKey(tea.KeyMsg{Type: tea.KeyDown})
	if got := out.(Model).contexts.selectedSection; got != contextsSectionParked {
		t.Fatalf("down: selectedSection got %d, want %d", got, contextsSectionParked)
	}
	// Down again → SUB-AGENT.
	out, _ = out.(Model).handleContextsOverlayKey(tea.KeyMsg{Type: tea.KeyDown})
	if got := out.(Model).contexts.selectedSection; got != contextsSectionSubagent {
		t.Fatalf("down x2: selectedSection got %d, want %d", got, contextsSectionSubagent)
	}
	// Up → PARKED.
	out, _ = out.(Model).handleContextsOverlayKey(tea.KeyMsg{Type: tea.KeyUp})
	if got := out.(Model).contexts.selectedSection; got != contextsSectionParked {
		t.Fatalf("up: selectedSection got %d, want %d", got, contextsSectionParked)
	}
	// Past-end / past-start are clamped — down on DRIVE stays on DRIVE.
	m2 := NewModel(context.Background(), nil)
	m2.contexts.selectedSection = contextsSectionDrive
	out, _ = m2.handleContextsOverlayKey(tea.KeyMsg{Type: tea.KeyDown})
	if got := out.(Model).contexts.selectedSection; got != contextsSectionDrive {
		t.Fatalf("down on last section should clamp: got %d, want %d", got, contextsSectionDrive)
	}
	m3 := NewModel(context.Background(), nil)
	out, _ = m3.handleContextsOverlayKey(tea.KeyMsg{Type: tea.KeyUp})
	if got := out.(Model).contexts.selectedSection; got != contextsSectionMain {
		t.Fatalf("up on first section should clamp: got %d, want %d", got, contextsSectionMain)
	}
}

// Right/Enter on the Contexts overlay must open an action menu. The
// menu must be owned by the panel (so close-routing is correct) and
// must always have at least one action — opening an empty menu is the
// "press enter, nothing visibly happens" bug the action-menu audit was
// designed to catch.
func TestContextsOverlayKey_RightOpensActionMenu(t *testing.T) {
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRight},
		{Type: tea.KeyEnter},
	} {
		m := NewModel(context.Background(), nil)
		out, _ := m.handleContextsOverlayKey(key)
		mm := out.(Model)
		if !mm.actionMenu.open {
			t.Fatalf("key %v should open action menu", key)
		}
		if mm.actionMenu.owner != "Contexts" {
			t.Errorf("action menu owner got %q, want %q", mm.actionMenu.owner, "Contexts")
		}
		if len(mm.actionMenu.actions) == 0 {
			t.Errorf("action menu opened with 0 actions — at least 1 should be available")
		}
	}
}

// renderContextsView must surface a cursor (▶) on the selected section
// title so the user can see which section Enter would act on.
func TestRenderContextsView_HighlightsSelectedSection(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.contexts.selectedSection = contextsSectionParked
	out := m.renderContextsView(120)
	// The cursor marker appears on the PARKED section line.
	parkedIdx := strings.Index(out, "PARKED AGENT")
	if parkedIdx < 0 {
		t.Fatal("PARKED AGENT section missing from render")
	}
	// Look back at most ~30 bytes for the cursor glyph (lipgloss may add
	// ANSI escapes around it).
	headStart := max(parkedIdx-40, 0)
	head := out[headStart:parkedIdx]
	if !strings.Contains(head, "▶") {
		t.Errorf("expected ▶ cursor on PARKED section title, head:\n%s", head)
	}
}
