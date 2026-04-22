package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/codemap"
)

func newCodemapTestModel() Model {
	return Model{
		tabs:                  []string{"Chat", "Status", "Files", "Patch", "Workflow", "Tools", "Activity", "Memory", "CodeMap"},
		activeTab:             8,
		diagnosticPanelsState: newDiagnosticPanelsState(),
	}
}

func sampleCodemapSnap() codemapSnapshot {
	return codemapSnapshot{
		Nodes:     4,
		Edges:     3,
		Languages: map[string]int{"go": 3, "js": 1},
		Kinds:     map[string]int{"function": 3, "struct": 1},
		Hotspots: []codemap.Node{
			{ID: "pkg.A", Name: "A", Kind: "function", Language: "go", Path: "pkg/a.go"},
			{ID: "pkg.B", Name: "B", Kind: "struct", Language: "go", Path: "pkg/b.go"},
		},
		Orphans: []codemap.Node{
			{ID: "pkg.Lost", Name: "Lost", Kind: "function", Language: "go", Path: "pkg/lost.go"},
		},
		Cycles: [][]string{
			{"pkg.A", "pkg.B", "pkg.A"},
		},
	}
}

func TestNextCodemapViewCycles(t *testing.T) {
	want := []string{
		codemapViewHotspots,
		codemapViewOrphans,
		codemapViewCycles,
		codemapViewOverview,
	}
	got := make([]string, 0, len(want))
	v := codemapViewOverview
	for i := 0; i < len(want); i++ {
		v = nextCodemapView(v)
		got = append(got, v)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("step %d: want %q got %q", i, want[i], got[i])
		}
	}
}

func TestApplyScrollClamps(t *testing.T) {
	rows := []string{"a", "b", "c", "d"}
	if out := applyScroll(rows, 0); len(out) != 4 {
		t.Fatalf("scroll 0 should return all, got %d", len(out))
	}
	if out := applyScroll(rows, 2); len(out) != 2 || out[0] != "c" {
		t.Fatalf("scroll 2 should clip to [c d], got %v", out)
	}
	// Scrolling past the tail should hold at last row, not disappear.
	if out := applyScroll(rows, 99); len(out) != 1 || out[0] != "d" {
		t.Fatalf("over-scroll should hold at tail, got %v", out)
	}
	if out := applyScroll([]string{}, 3); len(out) != 0 {
		t.Fatalf("empty rows should stay empty, got %v", out)
	}
}

func TestFormatCountMapSortsByCountDesc(t *testing.T) {
	m := map[string]int{"go": 3, "py": 1, "js": 2}
	out := formatCountMap(m, 10, 0)
	if len(out) != 3 {
		t.Fatalf("want 3 rows, got %d: %v", len(out), out)
	}
	// Highest count first.
	if !strings.Contains(out[0], "go") || !strings.HasSuffix(strings.TrimRight(out[0], " "), "3") {
		t.Fatalf("first row should be go=3, got %q", out[0])
	}
	if !strings.Contains(out[1], "js") {
		t.Fatalf("second row should be js, got %q", out[1])
	}
	if !strings.Contains(out[2], "py") {
		t.Fatalf("third row should be py, got %q", out[2])
	}
}

func TestFormatCountMapEmptyShowsNone(t *testing.T) {
	out := formatCountMap(map[string]int{}, 10, 0)
	if len(out) != 1 || !strings.Contains(out[0], "(none)") {
		t.Fatalf("empty map should yield (none), got %v", out)
	}
}

func TestFormatCodemapNodeRowIncludesNameTagsPath(t *testing.T) {
	n := codemap.Node{Name: "doThing", Kind: "function", Language: "go", Path: "pkg/thing.go"}
	row := formatCodemapNodeRow(n)
	for _, want := range []string{"doThing", "function", "go", "pkg/thing.go"} {
		if !strings.Contains(row, want) {
			t.Errorf("row missing %q: %s", want, row)
		}
	}
}

func TestFormatCodemapNodeRowFallsBackToID(t *testing.T) {
	n := codemap.Node{ID: "orphan.id", Name: "", Kind: "", Language: ""}
	row := formatCodemapNodeRow(n)
	if !strings.Contains(row, "orphan.id") {
		t.Fatalf("row should fall back to ID when Name is blank, got %q", row)
	}
}

func TestCodemapViewRowCountPerView(t *testing.T) {
	snap := sampleCodemapSnap()
	if got := codemapViewRowCount(codemapViewHotspots, snap); got != 2 {
		t.Fatalf("hotspots count, want 2 got %d", got)
	}
	if got := codemapViewRowCount(codemapViewOrphans, snap); got != 1 {
		t.Fatalf("orphans count, want 1 got %d", got)
	}
	if got := codemapViewRowCount(codemapViewCycles, snap); got != 1 {
		t.Fatalf("cycles count, want 1 got %d", got)
	}
	// Overview is fixed — no scrollable rows reported.
	if got := codemapViewRowCount(codemapViewOverview, snap); got != 0 {
		t.Fatalf("overview count, want 0 got %d", got)
	}
}

func TestRenderCodemapViewEmptyState(t *testing.T) {
	m := newCodemapTestModel()
	out := m.renderCodemapView(80)
	if !strings.Contains(out, "CodeMap") {
		t.Fatalf("empty view missing header:\n%s", out)
	}
	if !strings.Contains(out, "CodeMap is empty") {
		t.Fatalf("empty view missing empty copy:\n%s", out)
	}
}

func TestRenderCodemapViewErrorBanner(t *testing.T) {
	m := newCodemapTestModel()
	m.codemap.err = "graph read failed"
	out := m.renderCodemapView(80)
	if !strings.Contains(out, "error · graph read failed") {
		t.Fatalf("error banner missing:\n%s", out)
	}
}

func TestRenderCodemapViewWithSnapShowsSummary(t *testing.T) {
	m := newCodemapTestModel()
	m.codemap.snap = sampleCodemapSnap()
	out := m.renderCodemapView(120)
	if !strings.Contains(out, "4 nodes · 3 edges · 1 cycles · 1 orphans") {
		t.Fatalf("footer summary missing or wrong:\n%s", out)
	}
	// Overview view should surface the language breakdown.
	if !strings.Contains(out, "go") {
		t.Fatalf("language breakdown missing:\n%s", out)
	}
}

func TestHandleCodemapKeyCyclesViewAndResetsScroll(t *testing.T) {
	m := newCodemapTestModel()
	m.codemap.snap = sampleCodemapSnap()
	m.codemap.view = codemapViewHotspots
	m.codemap.scroll = 1

	m2, _ := m.handleCodemapKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m = m2.(Model)
	if m.codemap.view != codemapViewOrphans {
		t.Fatalf("v should advance view, got %q", m.codemap.view)
	}
	if m.codemap.scroll != 0 {
		t.Fatalf("v should reset scroll, got %d", m.codemap.scroll)
	}
}

func TestHandleCodemapKeyScrollBindings(t *testing.T) {
	m := newCodemapTestModel()
	m.codemap.snap = sampleCodemapSnap()
	m.codemap.view = codemapViewHotspots // 2 rows

	m2, _ := m.handleCodemapKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = m2.(Model)
	if m.codemap.scroll != 1 {
		t.Fatalf("j should advance, got %d", m.codemap.scroll)
	}
	// j at tail is clamped (total=2, scroll=1 already).
	m2, _ = m.handleCodemapKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = m2.(Model)
	if m.codemap.scroll != 1 {
		t.Fatalf("j at tail should clamp, got %d", m.codemap.scroll)
	}
	m2, _ = m.handleCodemapKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	m = m2.(Model)
	if m.codemap.scroll != 0 {
		t.Fatalf("g should jump to top, got %d", m.codemap.scroll)
	}
	m2, _ = m.handleCodemapKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	m = m2.(Model)
	if m.codemap.scroll != 1 {
		t.Fatalf("G should jump to last, got %d", m.codemap.scroll)
	}
}

func TestHandleCodemapKeyRefreshSetsLoading(t *testing.T) {
	m := newCodemapTestModel()
	m2, _ := m.handleCodemapKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	m = m2.(Model)
	if !m.codemap.loading {
		t.Fatalf("r should set loading=true")
	}
	if m.codemap.err != "" {
		t.Fatalf("r should clear error, got %q", m.codemap.err)
	}
}
