package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func TestCountMemoryEntriesByTier(t *testing.T) {
	entries := []types.MemoryEntry{
		{Tier: types.MemoryEpisodic, ID: "e1"},
		{Tier: types.MemoryEpisodic, ID: "e2"},
		{Tier: types.MemorySemantic, ID: "s1"},
		{Tier: "", ID: "u1"}, // unknown / working — buckets to episodic
	}
	ep, sem := countMemoryEntriesByTier(entries)
	if ep != 3 {
		t.Errorf("expected 3 episodic (incl. unknown bucket), got %d", ep)
	}
	if sem != 1 {
		t.Errorf("expected 1 semantic, got %d", sem)
	}
}

func TestMemoryHitsChip(t *testing.T) {
	out := ansi.Strip(memoryHitsChip(0))
	if !strings.Contains(out, "0 hits") {
		t.Errorf("zero-hit chip lost label, got %q", out)
	}
	out = ansi.Strip(memoryHitsChip(7))
	if !strings.Contains(out, "7 hits") {
		t.Errorf("nonzero chip should report count, got %q", out)
	}
}

func TestRenderMemoryView_BannerSurfacesTierCounts(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.memory.entries = []types.MemoryEntry{
		{Tier: types.MemoryEpisodic, ID: "e1", Value: "alpha"},
		{Tier: types.MemoryEpisodic, ID: "e2", Value: "beta"},
		{Tier: types.MemorySemantic, ID: "s1", Value: "gamma"},
	}
	view := m.renderMemoryView(140)
	stripped := ansi.Strip(view)
	for _, want := range []string{"episodic 2", "semantic 1"} {
		if !strings.Contains(stripped, want) {
			t.Errorf("expected %q in banner, got:\n%s", want, stripped)
		}
	}
}

func TestRenderMemoryView_LiveSearchBoxAndHitChip(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.memory.entries = []types.MemoryEntry{
		{Tier: types.MemoryEpisodic, ID: "e1", Value: "alpha"},
		{Tier: types.MemoryEpisodic, ID: "e2", Value: "beta"},
		{Tier: types.MemorySemantic, ID: "s1", Value: "alpha-too"},
	}
	m.memory.searchActive = true
	m.memory.query = "alpha"
	view := m.renderMemoryView(140)
	stripped := ansi.Strip(view)
	if !strings.Contains(stripped, "Search:") {
		t.Errorf("expected live search box, got:\n%s", stripped)
	}
	if !strings.Contains(stripped, "alpha") {
		t.Errorf("expected query inside search box, got:\n%s", stripped)
	}
	if !strings.Contains(stripped, "2 hits") {
		t.Errorf("expected 2-hit chip in tier line, got:\n%s", stripped)
	}
}

func TestRenderMemoryView_ZeroHitsChip(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.memory.entries = []types.MemoryEntry{
		{Tier: types.MemoryEpisodic, ID: "e1", Value: "alpha"},
	}
	m.memory.query = "nonexistent"
	view := m.renderMemoryView(140)
	stripped := ansi.Strip(view)
	if !strings.Contains(stripped, "0 hits") {
		t.Errorf("expected 0-hit chip when query misses, got:\n%s", stripped)
	}
}
