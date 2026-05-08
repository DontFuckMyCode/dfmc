package tui

import (
	"context"
	"strings"
	"testing"
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
