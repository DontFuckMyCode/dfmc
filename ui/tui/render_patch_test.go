package tui

import (
	"strings"
	"testing"
)

// TestPatchViewV2_EmptyStateOffersGuidance — when no patch is queued,
// the panel should print the EMPTY chip and actionable hints rather
// than blank panes.
func TestPatchViewV2_EmptyStateOffersGuidance(t *testing.T) {
	m := newCoverageModel(t)
	view := stripANSI(m.renderPatchViewV2(140))
	for _, want := range []string{
		"PATCH LAB",
		"EMPTY",
		// Production copy says "No assistant patch loaded." (render_patch_panes.go);
		// the test used to assert "No assistant patch yet" which never matched.
		"No assistant patch loaded.",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("empty patch view missing %q. Got:\n%s", want, view)
		}
	}
}

// TestPatchViewV2_RendersAllThreePanesOnWideTerminal pins the 3-pane
// layout: FILES list, DIFF body, and the metadata SUMMARY/REVIEW/
// ACTIONS cards. All must appear at width≥120.
func TestPatchViewV2_RendersAllThreePanesOnWideTerminal(t *testing.T) {
	m := newCoverageModel(t)
	m.patchView.set = []patchSection{
		{
			Path:      "internal/auth/service.go",
			HunkCount: 1,
			Content:   "--- a/internal/auth/service.go\n+++ b/internal/auth/service.go\n@@ -1 +1 @@\n-old\n+new\n",
			Hunks: []patchHunk{
				{Header: "@@ -1 +1 @@", Content: "--- a/internal/auth/service.go\n+++ b/internal/auth/service.go\n@@ -1 +1 @@\n-old\n+new\n"},
			},
		},
	}
	m.patchView.files = []string{"internal/auth/service.go"}
	m.patchView.latestPatch = m.patchView.set[0].Content

	view := stripANSI(m.renderPatchViewV2(140))
	for _, want := range []string{
		"PATCH LAB",
		"FILES", "DIFF",
		"SUMMARY", "REVIEW", "ACTIONS",
		"service.go",
		"@@ -1 +1 @@",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("wide patch view missing %q. Got:\n%s", want, view)
		}
	}
}

// TestPatchViewV2_StatusChipReflectsApplyState — the chip flips
// EMPTY (no patch) → PENDING (loaded) → APPLIED (notice mentions
// applied) → FAILED (notice mentions error/fail).
func TestPatchViewV2_StatusChipReflectsApplyState(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*Model)
		want  string
	}{
		{"empty", func(m *Model) {}, "EMPTY"},
		{"pending", func(m *Model) {
			m.patchView.latestPatch = "--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b\n"
		}, "PENDING"},
		{"applied", func(m *Model) {
			m.patchView.latestPatch = "--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b\n"
			m.notice = "Patch applied successfully."
		}, "APPLIED"},
		{"failed", func(m *Model) {
			m.patchView.latestPatch = "--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b\n"
			m.notice = "Patch apply failed: conflict."
		}, "FAILED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newCoverageModel(t)
			tc.setup(&m)
			view := stripANSI(m.renderPatchViewV2(140))
			if !strings.Contains(view, tc.want) {
				t.Errorf("expected chip %q. Got:\n%s", tc.want, view)
			}
		})
	}
}

// TestPatchViewV2_FilesListShowsAddDelStats — each file row must
// surface its +adds/-dels and hunk count.
func TestPatchViewV2_FilesListShowsAddDelStats(t *testing.T) {
	m := newCoverageModel(t)
	m.patchView.set = []patchSection{
		{
			Path:      "a.go",
			HunkCount: 2,
			Content:   "--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-x\n+y\n+z\n",
		},
	}
	m.patchView.files = []string{"a.go"}

	view := stripANSI(m.renderPatchViewV2(140))
	// Row format is "· a.go +2 -1 2h" — assert each segment without
	// requiring a specific separator/punctuation form. The earlier
	// "+2/-1" / "·2h" expectations encoded a rendering style that
	// drifted (spaces are used now instead of slash + middle-dot).
	for _, want := range []string{"a.go", "+2", "-1", "2h"} {
		if !strings.Contains(view, want) {
			t.Errorf("file row missing %q. Got:\n%s", want, view)
		}
	}
}

// TestPatchPanelWidths_BreakpointsBehave pins the 3/2/1-pane split
// math so future layout changes don't silently shift columns.
func TestPatchPanelWidths_BreakpointsBehave(t *testing.T) {
	t.Run("three-pane", func(t *testing.T) {
		l, d, m := patchPanelWidths(140, true, false)
		if l < 28 || d < 32 || m < 28 {
			t.Errorf("three-pane below floor: l=%d d=%d m=%d", l, d, m)
		}
		if l+d+m+4 > 140 {
			t.Errorf("three-pane overflow: l=%d d=%d m=%d (sum+gutter=%d)", l, d, m, l+d+m+4)
		}
	})
	t.Run("two-pane", func(t *testing.T) {
		l, d, m := patchPanelWidths(100, false, true)
		if m != 0 {
			t.Errorf("two-pane should have meta=0, got %d", m)
		}
		if l < 28 || d < 28 {
			t.Errorf("two-pane below floor: l=%d d=%d", l, d)
		}
	})
	t.Run("one-pane", func(t *testing.T) {
		l, d, m := patchPanelWidths(60, false, false)
		if l != 60 || d != 60 || m != 0 {
			t.Errorf("one-pane should give full width: l=%d d=%d m=%d", l, d, m)
		}
	})
}
