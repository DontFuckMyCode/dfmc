package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/dontfuckmycode/dfmc/internal/codemap"
)

func TestNodeMatchesCodemapQuery(t *testing.T) {
	n := codemap.Node{ID: "pkg.Foo", Name: "Foo", Path: "pkg/foo.go"}
	if !nodeMatchesCodemapQuery(n, "") {
		t.Errorf("empty query should match everything")
	}
	if !nodeMatchesCodemapQuery(n, "foo") {
		t.Errorf("expected match on name")
	}
	if !nodeMatchesCodemapQuery(n, "pkg/") {
		t.Errorf("expected match on path")
	}
	if !nodeMatchesCodemapQuery(n, "pkg.f") {
		t.Errorf("expected match on ID")
	}
	if nodeMatchesCodemapQuery(n, "zzz") {
		t.Errorf("did not expect zzz to match")
	}
}

func TestCodemapHitsChip(t *testing.T) {
	if !strings.Contains(ansi.Strip(codemapHitsChip(0)), "0 hits") {
		t.Errorf("zero-hit chip lost label")
	}
	if !strings.Contains(ansi.Strip(codemapHitsChip(3)), "3 hits") {
		t.Errorf("nonzero chip should report count")
	}
}

func TestCountCodemapHits(t *testing.T) {
	snap := sampleCodemapSnap()
	if got := countCodemapHits(codemapViewHotspots, snap, ""); got != 0 {
		t.Errorf("empty query should return 0 (callers skip the chip), got %d", got)
	}
	if got := countCodemapHits(codemapViewHotspots, snap, "a"); got != 1 {
		t.Errorf("expected 1 hotspot match for 'a', got %d", got)
	}
	if got := countCodemapHits(codemapViewOrphans, snap, "lost"); got != 1 {
		t.Errorf("expected 1 orphan match for 'lost', got %d", got)
	}
	if got := countCodemapHits(codemapViewCycles, snap, "pkg.a"); got != 1 {
		t.Errorf("expected cycle to match on label, got %d", got)
	}
	if got := countCodemapHits(codemapViewVisual, snap, "a"); got != 0 {
		t.Errorf("visual view should never count hits, got %d", got)
	}
	if got := countCodemapHits(codemapViewOverview, snap, "a"); got != 0 {
		t.Errorf("overview view should never count hits, got %d", got)
	}
}

func TestRenderCodemapView_SurfacesLiveSearchAndHitChip(t *testing.T) {
	m := newCodemapTestModel()
	m.codemap.snap = sampleCodemapSnap()
	m.codemap.view = codemapViewHotspots
	m.codemap.searchActive = true
	m.codemap.query = "a"
	view := m.renderCodemapView(140)
	stripped := ansi.Strip(view)
	if !strings.Contains(stripped, "Search:") {
		t.Errorf("expected live search box, got:\n%s", stripped)
	}
	if !strings.Contains(stripped, "1 hits") {
		t.Errorf("expected 1-hit chip, got:\n%s", stripped)
	}
}

func TestRenderCodemapView_ZeroHitChipOnMiss(t *testing.T) {
	m := newCodemapTestModel()
	m.codemap.snap = sampleCodemapSnap()
	m.codemap.view = codemapViewHotspots
	m.codemap.query = "nonexistent-symbol"
	view := m.renderCodemapView(140)
	if !strings.Contains(ansi.Strip(view), "0 hits") {
		t.Errorf("expected 0-hit chip on miss, got:\n%s", view)
	}
}

func TestRenderCodemapView_SearchDisabledInVisualView(t *testing.T) {
	m := newCodemapTestModel()
	m.codemap.snap = sampleCodemapSnap()
	m.codemap.view = codemapViewVisual
	m.codemap.query = "a"
	view := m.renderCodemapView(140)
	stripped := ansi.Strip(view)
	if !strings.Contains(stripped, "search disabled in this view") {
		t.Errorf("expected disabled-in-view notice, got:\n%s", stripped)
	}
}
