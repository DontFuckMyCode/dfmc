package tui

import (
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func TestTierLabel(t *testing.T) {
	cases := []struct {
		t    types.MemoryTier
		want string
	}{
		{"", "all-tier"},
		{types.MemorySemantic, "semantic"},
		{types.MemoryEpisodic, "episodic"},
		{types.MemoryWorking, "working"},
	}
	for _, c := range cases {
		got := tierLabel(c.t)
		if got != c.want {
			t.Errorf("tierLabel(%q) = %q want %q", c.t, got, c.want)
		}
	}
}

func TestFormatMemoryEntries_Empty(t *testing.T) {
	got := formatMemoryEntries(nil, types.MemorySemantic)
	if got == "" {
		t.Error("expected non-empty output for nil entries")
	}
}

func TestFormatMemoryEntries_WithEntries(t *testing.T) {
	entries := []types.MemoryEntry{
		{Tier: types.MemorySemantic, Key: "key1", Value: "val1"},
		{Tier: types.MemoryEpisodic, Key: "key2", Value: "val2 longer"},
	}
	got := formatMemoryEntries(entries, types.MemorySemantic)
	if got == "" {
		t.Error("expected non-empty output")
	}
	if !strings.Contains(got, "Memory (") {
		t.Error("expected 'Memory (' prefix")
	}
}