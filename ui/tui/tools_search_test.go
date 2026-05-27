package tui

import (
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestHandleToolsKey_SlashOpensSearch(t *testing.T) {
	m := newCoverageModel(t)
	got, _ := m.handleToolsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	mm := got.(Model)
	if !mm.toolView.searchActive {
		t.Errorf("/ should set searchActive=true")
	}
}

func TestHandleToolsSearchKey_RunesAppendToQuery(t *testing.T) {
	m := newCoverageModel(t)
	m.toolView.searchActive = true
	got, _ := m.handleToolsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	mm := got.(Model)
	got2, _ := mm.handleToolsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	mm2 := got2.(Model)
	if mm2.toolView.query != "re" {
		t.Errorf("expected query 're', got %q", mm2.toolView.query)
	}
}

func TestHandleToolsSearchKey_EnterCommitsKeepsQuery(t *testing.T) {
	m := newCoverageModel(t)
	m.toolView.searchActive = true
	m.toolView.query = "read"
	got, _ := m.handleToolsKey(tea.KeyMsg{Type: tea.KeyEnter})
	mm := got.(Model)
	if mm.toolView.searchActive {
		t.Errorf("enter should drop out of searchActive")
	}
	if mm.toolView.query != "read" {
		t.Errorf("enter should keep the query; got %q", mm.toolView.query)
	}
}

func TestHandleToolsKey_CClearsQuery(t *testing.T) {
	m := newCoverageModel(t)
	m.toolView.query = "read"
	m.toolView.index = 5
	got, _ := m.handleToolsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	mm := got.(Model)
	if mm.toolView.query != "" {
		t.Errorf("c should clear query, got %q", mm.toolView.query)
	}
	if mm.toolView.index != 0 {
		t.Errorf("c should reset index, got %d", mm.toolView.index)
	}
}

func TestVisibleTools_FiltersByNameSubstring(t *testing.T) {
	m := newCoverageModel(t)
	all := m.availableTools()
	if len(all) == 0 {
		t.Skip("no tools registered in this test env")
	}
	// Pick the first tool, use its first 3 chars as a query → expect at
	// least that tool to survive.
	target := all[0]
	if len(target) < 3 {
		t.Skip("tool name too short for substring test")
	}
	m.toolView.query = strings.ToUpper(target[:3])
	visible := m.visibleTools()
	if len(visible) == 0 {
		t.Errorf("expected at least one tool to match prefix %q of %q", target[:3], target)
	}
	if !slices.Contains(visible, target) {
		t.Errorf("expected %q in filtered set, got %v", target, visible)
	}
}

func TestVisibleTools_EmptyQueryReturnsAll(t *testing.T) {
	m := newCoverageModel(t)
	m.toolView.query = ""
	visible := m.visibleTools()
	all := m.availableTools()
	if len(visible) != len(all) {
		t.Errorf("empty query should return all %d tools, got %d", len(all), len(visible))
	}
}

func TestRenderToolsView_SurfacesLiveSearchBox(t *testing.T) {
	m := newCoverageModel(t)
	m.toolView.searchActive = true
	m.toolView.query = "rea"
	view := ansi.Strip(m.renderToolsViewSized(140, 30))
	if !strings.Contains(view, "Search:") {
		t.Errorf("expected live search box, got:\n%s", view)
	}
	if !strings.Contains(view, "enter commit") {
		t.Errorf("expected commit hint, got:\n%s", view)
	}
}

func TestRenderToolsView_StaticFilterHintWhenNotActive(t *testing.T) {
	m := newCoverageModel(t)
	m.toolView.query = "read"
	view := ansi.Strip(m.renderToolsViewSized(140, 30))
	if !strings.Contains(view, "filter") {
		t.Errorf("expected static filter line, got:\n%s", view)
	}
	if !strings.Contains(view, "press c to clear") {
		t.Errorf("expected clear hint, got:\n%s", view)
	}
}

func TestRenderToolsView_NoMatchEmptyState(t *testing.T) {
	m := newCoverageModel(t)
	if len(m.availableTools()) == 0 {
		t.Skip("no tools registered in this test env")
	}
	m.toolView.query = "definitely-not-a-tool-name-xyz"
	view := ansi.Strip(m.renderToolsViewSized(140, 30))
	if !strings.Contains(view, "No tool matches") {
		t.Errorf("expected no-match warning, got:\n%s", view)
	}
}
