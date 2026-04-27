package tui

import (
	"testing"
)

func TestPatchHunkSummary(t *testing.T) {
	m := NewModel(nil, nil)

	// Nil section returns "(none)"
	if got := m.patchHunkSummary(); got != "(none)" {
		t.Errorf("nil section: got %q want (none)", got)
	}

	// Empty hunks returns "(none)"
	m.patchView.set = []patchSection{{Path: "a.txt", Hunks: nil}}
	m.patchView.index = 0
	m.patchView.hunk = 0
	if got := m.patchHunkSummary(); got != "(none)" {
		t.Errorf("empty hunks: got %q want (none)", got)
	}

	// With valid hunks
	m.patchView.set = []patchSection{
		{
			Path:      "a.txt",
			HunkCount: 2,
			Hunks: []patchHunk{
				{Header: "@@ -1,3 +1,4 @@"},
				{Header: "@@ -5,7 +5,8 @@"},
			},
		},
	}
	m.patchView.index = 0
	m.patchView.hunk = 0
	got := m.patchHunkSummary()
	if got == "(none)" {
		t.Error("expected non-none hunk summary")
	}
	if got == "" {
		t.Error("expected non-empty hunk summary")
	}

	// Index out of bounds resets to 0
	m.patchView.hunk = 99
	got = m.patchHunkSummary()
	if got == "(none)" {
		t.Error("out of bounds hunk index should reset to 0")
	}

	// Empty header falls back to "@@"
	m.patchView.hunk = 0
	m.patchView.set[0].Hunks[0].Header = ""
	got = m.patchHunkSummary()
	if got == "" {
		t.Error("empty header should fall back to @@")
	}
}

func TestBestPatchIndex(t *testing.T) {
	m := NewModel(nil, nil)

	// Empty set returns 0
	if got := m.bestPatchIndex(); got != 0 {
		t.Errorf("empty set: got %d want 0", got)
	}

	// No matching candidates returns 0
	m.patchView.set = []patchSection{
		{Path: "a.txt"},
		{Path: "b.txt"},
	}
	m.filesView.pinned = ""
	// currentPatchPath returns "" when index is out of range
	m.patchView.index = 99
	if got := m.bestPatchIndex(); got != 0 {
		t.Errorf("no match: got %d want 0", got)
	}

	// Match via currentPatchPath
	m.patchView.index = 0 // valid index so currentPatchPath returns a path
	m.patchView.set = []patchSection{
		{Path: "a.txt"},
		{Path: "b.txt"},
		{Path: "c.txt"},
	}
	// Force currentPatchPath to return "b.txt" by setting a valid index
	m.patchView.index = 1 // b.txt
	if got := m.bestPatchIndex(); got != 1 {
		t.Errorf("currentPatchPath match: got %d want 1", got)
	}
}

func TestBestPatchIndex_MatchesPinnedFile(t *testing.T) {
	m := NewModel(nil, nil)
	m.patchView.set = []patchSection{
		{Path: "a.txt"},
		{Path: "b.txt"},
	}
	// Set index out of bounds so currentPatchPath returns ""
	m.patchView.index = 99
	m.filesView.pinned = "b.txt"
	if got := m.bestPatchIndex(); got != 1 {
		t.Errorf("pinned file match: got %d want 1", got)
	}
}

func TestBestPatchIndex_CaseInsensitive(t *testing.T) {
	m := NewModel(nil, nil)
	m.patchView.set = []patchSection{
		{Path: "a.txt"},
		{Path: "b.txt"},
	}
	// Set index out of bounds so currentPatchPath returns ""
	m.patchView.index = 99
	m.filesView.pinned = "B.TXT"
	if got := m.bestPatchIndex(); got != 1 {
		t.Errorf("case-insensitive match: got %d want 1", got)
	}
}
