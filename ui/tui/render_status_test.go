package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

// TestStatusViewV2_RendersCoreCards — every always-on card must
// appear so a fresh-boot user sees the full layout. Conditional
// cards (Memory degraded, Context In, Subagents) are tested
// separately.
func TestStatusViewV2_RendersCoreCards(t *testing.T) {
	m := newCoverageModel(t)
	m.status = engine.Status{
		ProjectRoot: "/tmp/proj",
		Provider:    "anthropic",
		Model:       "claude-sonnet-4-6",
		ASTBackend:  "tree-sitter",
	}
	view := stripANSI(m.renderStatusViewV2(120))
	for _, want := range []string{
		"PROJECT", "PROVIDER", "AST", "CODEMAP",
		"Root:", "Provider:", "Backend:",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("status view missing %q. Got:\n%s", want, view)
		}
	}
}

// TestStatusViewV2_TopBannerReflectsHealth — the banner chip must
// flip OK / DEGRADED / NO PROVIDER / OFFLINE based on engine state.
func TestStatusViewV2_TopBannerReflectsHealth(t *testing.T) {
	cases := []struct {
		name   string
		status engine.Status
		want   string
	}{
		{"ready", engine.Status{Provider: "anthropic", Model: "x"}, "READY"},
		{"degraded", engine.Status{Provider: "anthropic", MemoryDegraded: true}, "DEGRADED"},
		{"no provider", engine.Status{}, "NO PROVIDER"},
		{"offline", engine.Status{Provider: "offline"}, "OFFLINE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newCoverageModel(t)
			m.status = tc.status
			view := stripANSI(m.renderStatusViewV2(120))
			if !strings.Contains(view, tc.want) {
				t.Errorf("expected banner %q. Got:\n%s", tc.want, view)
			}
		})
	}
}

// TestStatusViewV2_MemoryCardOnlyWhenDegraded — Memory card lives
// in the conditional section so a healthy session doesn't render
// a "Memory: ok" tile that adds noise.
func TestStatusViewV2_MemoryCardOnlyWhenDegraded(t *testing.T) {
	m := newCoverageModel(t)
	m.status = engine.Status{Provider: "anthropic"}
	view := stripANSI(m.renderStatusViewV2(120))
	if strings.Contains(view, "MEMORY") {
		t.Errorf("Memory card should be hidden when not degraded. Got:\n%s", view)
	}

	m.status.MemoryDegraded = true
	m.status.MemoryLoadErr = "bbolt: locked"
	view = stripANSI(m.renderStatusViewV2(120))
	if !strings.Contains(view, "MEMORY") {
		t.Errorf("Memory card missing under degraded state. Got:\n%s", view)
	}
	if !strings.Contains(view, "DEGRADED") {
		t.Errorf("DEGRADED chip missing. Got:\n%s", view)
	}
	if !strings.Contains(view, "bbolt: locked") {
		t.Errorf("reason missing. Got:\n%s", view)
	}
}

// TestStatusKey_ArrowNavigationMovesSelectedCard pins the new
// arrow-key navigation contract. Right/l moves forward, left/h
// moves back, home/end jump to bounds. A delta past the bounds
// clamps instead of wrapping (deliberate — wrap-around in a small
// card grid is disorienting).
func TestStatusKey_ArrowNavigationMovesSelectedCard(t *testing.T) {
	m := newCoverageModel(t)
	m.status = engine.Status{Provider: "anthropic", Model: "x", ASTBackend: "tree-sitter"}
	// Render once so cardCount is populated by the renderer.
	_ = m.renderStatusViewV2(120)
	if m.diagnosticPanelsState.statusPanel.cardCount < 4 {
		t.Fatalf("setup: expected at least 4 cards, got %d",
			m.diagnosticPanelsState.statusPanel.cardCount)
	}

	// right/l moves forward
	got, _ := m.handleStatusKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	gm := got.(Model)
	if gm.diagnosticPanelsState.statusPanel.selectedCard != 1 {
		t.Errorf("l: expected selectedCard=1, got %d",
			gm.diagnosticPanelsState.statusPanel.selectedCard)
	}

	// left/h moves back
	got, _ = gm.handleStatusKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	gm = got.(Model)
	if gm.diagnosticPanelsState.statusPanel.selectedCard != 0 {
		t.Errorf("h: expected selectedCard=0, got %d",
			gm.diagnosticPanelsState.statusPanel.selectedCard)
	}

	// G jumps to last
	got, _ = gm.handleStatusKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	gm = got.(Model)
	last := gm.diagnosticPanelsState.statusPanel.cardCount - 1
	if gm.diagnosticPanelsState.statusPanel.selectedCard != last {
		t.Errorf("G: expected selectedCard=%d, got %d",
			last, gm.diagnosticPanelsState.statusPanel.selectedCard)
	}

	// past-bounds delta clamps, doesn't wrap
	for range 10 {
		got, _ = gm.handleStatusKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
		gm = got.(Model)
	}
	if gm.diagnosticPanelsState.statusPanel.selectedCard != last {
		t.Errorf("clamp: expected selectedCard=%d after 10 forwards, got %d",
			last, gm.diagnosticPanelsState.statusPanel.selectedCard)
	}
}

// TestStatusKey_EnterJumpsToDetailTab pins the "press enter on a
// card to drill in" contract. Provider card → Providers tab.
func TestStatusKey_EnterJumpsToDetailTab(t *testing.T) {
	m := newCoverageModel(t)
	m.status = engine.Status{Provider: "anthropic", Model: "x", ASTBackend: "tree-sitter"}
	_ = m.renderStatusViewV2(120)

	// Card index 1 = Provider
	m.diagnosticPanelsState.statusPanel.selectedCard = 1
	got, _ := m.handleStatusKey(tea.KeyMsg{Type: tea.KeyEnter})
	gm := got.(Model)
	wantIdx := gm.activityTabIndex("Providers")
	if gm.activeTab != wantIdx {
		t.Errorf("enter on Provider: expected activeTab=%d (Providers), got %d",
			wantIdx, gm.activeTab)
	}
}
