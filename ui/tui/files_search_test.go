package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestHandleFilesSearchKey_SlashOpensLiveBox(t *testing.T) {
	m := newCoverageModel(t)
	m.filesView = filesViewState{entries: []string{"a.go", "b.go"}}
	got, _ := m.handleFilesKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	mm, ok := got.(Model)
	if !ok {
		t.Fatalf("handleFilesKey did not return Model")
	}
	if !mm.filesView.searchActive {
		t.Errorf("/ should set searchActive=true")
	}
}

func TestHandleFilesSearchKey_RunesAppendToQuery(t *testing.T) {
	m := newCoverageModel(t)
	m.filesView = filesViewState{entries: []string{"a.go"}, searchActive: true}
	got, _ := m.handleFilesKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	mm := got.(Model)
	got2, _ := mm.handleFilesKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	mm2 := got2.(Model)
	if mm2.filesView.query != "fo" {
		t.Errorf("expected query 'fo', got %q", mm2.filesView.query)
	}
}

func TestHandleFilesSearchKey_EnterCommits(t *testing.T) {
	m := newCoverageModel(t)
	m.filesView = filesViewState{searchActive: true, query: "main"}
	got, _ := m.handleFilesKey(tea.KeyMsg{Type: tea.KeyEnter})
	mm := got.(Model)
	if mm.filesView.searchActive {
		t.Errorf("enter should drop out of searchActive")
	}
	if mm.filesView.query != "main" {
		t.Errorf("enter should keep the query; got %q", mm.filesView.query)
	}
}

func TestHandleFilesSearchKey_EscKeepsQuery(t *testing.T) {
	m := newCoverageModel(t)
	m.filesView = filesViewState{searchActive: true, query: "main"}
	got, _ := m.handleFilesKey(tea.KeyMsg{Type: tea.KeyEsc})
	mm := got.(Model)
	if mm.filesView.searchActive {
		t.Errorf("esc should drop out of searchActive")
	}
	if mm.filesView.query != "main" {
		t.Errorf("esc keeps the query intact; got %q", mm.filesView.query)
	}
}

func TestHandleFilesKey_CClearsQuery(t *testing.T) {
	m := newCoverageModel(t)
	m.filesView = filesViewState{entries: []string{"a.go"}, query: "foo", index: 3}
	got, _ := m.handleFilesKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	mm := got.(Model)
	if mm.filesView.query != "" {
		t.Errorf("c should clear query, got %q", mm.filesView.query)
	}
	if mm.filesView.index != 0 {
		t.Errorf("c should reset index to 0, got %d", mm.filesView.index)
	}
}

func TestRenderFilesView_SurfacesLiveSearchBox(t *testing.T) {
	m := newCoverageModel(t)
	m.filesView = filesViewState{
		entries:      []string{"cmd/dfmc/main.go", "internal/engine/engine.go"},
		searchActive: true,
		query:        "eng",
	}
	view := ansi.Strip(m.renderFilesViewV2(140, 30))
	if !strings.Contains(view, "Search:") {
		t.Errorf("expected live search box, got:\n%s", view)
	}
	if !strings.Contains(view, "enter commit") {
		t.Errorf("expected commit hint, got:\n%s", view)
	}
}

func TestRenderFilesView_ZeroMatchesGuidesUser(t *testing.T) {
	m := newCoverageModel(t)
	m.filesView = filesViewState{
		entries: []string{"cmd/dfmc/main.go"},
		query:   "nonexistent-xyz",
	}
	view := ansi.Strip(m.renderFilesViewV2(140, 30))
	if !strings.Contains(view, "No matches") {
		t.Errorf("expected no-match warning, got:\n%s", view)
	}
	if !strings.Contains(view, "Press c to clear") {
		t.Errorf("expected clear hint, got:\n%s", view)
	}
}
