package tui

import (
	"strings"
	"testing"
)

const sampleMultiHunkPatch = `diff --git a/foo.go b/foo.go
index abc..def 100644
--- a/foo.go
+++ b/foo.go
@@ -1,3 +1,4 @@
 line a
+inserted near top
 line b
 line c
@@ -10,2 +11,3 @@
 line m
+inserted near middle
 line n
`

func newPatchTestModel(patch string) Model {
	m := Model{}
	m.diagnosticPanelsState = newDiagnosticPanelsState()
	m.patchView.latestPatch = patch
	m.patchView.set = parseUnifiedDiffSections(patch)
	m.patchView.index = 0
	m.patchView.hunk = 0
	return m
}

// TestCurrentHunkPatchFragmentBuildsSelfContainedDiff — Phase F item 1
// surface. Each hunk's Content already carries the file preamble (set
// up by extractPatchHunks), so picking just one hunk yields a complete
// applyable diff. Asserts the fragment includes the file headers, the
// targeted @@ block, and EXCLUDES the other hunk's @@ block / body.
func TestCurrentHunkPatchFragmentBuildsSelfContainedDiff(t *testing.T) {
	m := newPatchTestModel(sampleMultiHunkPatch)

	// Hunk 0 (top of file).
	frag, ok := m.currentHunkPatchFragment()
	if !ok {
		t.Fatal("expected fragment for hunk 0")
	}
	if !strings.Contains(frag, "diff --git a/foo.go b/foo.go") {
		t.Fatalf("fragment should carry file preamble, got:\n%s", frag)
	}
	if !strings.Contains(frag, "@@ -1,3 +1,4 @@") || !strings.Contains(frag, "+inserted near top") {
		t.Fatalf("fragment should carry hunk 0 body, got:\n%s", frag)
	}
	if strings.Contains(frag, "@@ -10,2 +11,3 @@") || strings.Contains(frag, "+inserted near middle") {
		t.Fatalf("fragment for hunk 0 should NOT include hunk 1 body, got:\n%s", frag)
	}
	if !strings.HasSuffix(frag, "\n") {
		t.Fatalf("fragment should end with a trailing newline (git apply expects one), got %q", frag[len(frag)-5:])
	}

	// Switch to hunk 1; the fragment's @@ header flips and hunk 0
	// disappears. Same preamble in both fragments because both belong
	// to the same file section.
	m.patchView.hunk = 1
	frag2, ok := m.currentHunkPatchFragment()
	if !ok {
		t.Fatal("expected fragment for hunk 1")
	}
	if !strings.Contains(frag2, "@@ -10,2 +11,3 @@") || !strings.Contains(frag2, "+inserted near middle") {
		t.Fatalf("fragment for hunk 1 should carry the second hunk, got:\n%s", frag2)
	}
	if strings.Contains(frag2, "@@ -1,3 +1,4 @@") {
		t.Fatalf("fragment for hunk 1 should NOT include hunk 0 header, got:\n%s", frag2)
	}
}

// TestCurrentHunkPatchFragmentRejectsEmptySelection — guard against
// the action menu dispatching an apply when the panel has no section
// or no hunk highlighted. ok=false flips the menu handler into a
// notice instead of feeding an empty patch through git apply.
func TestCurrentHunkPatchFragmentRejectsEmptySelection(t *testing.T) {
	m := Model{}
	m.diagnosticPanelsState = newDiagnosticPanelsState()
	if _, ok := m.currentHunkPatchFragment(); ok {
		t.Fatal("empty patch panel should yield ok=false")
	}

	m = newPatchTestModel(sampleMultiHunkPatch)
	m.patchView.hunk = 99
	if _, ok := m.currentHunkPatchFragment(); ok {
		t.Fatal("out-of-range hunk index should yield ok=false")
	}
	m.patchView.index = 99
	if _, ok := m.currentHunkPatchFragment(); ok {
		t.Fatal("out-of-range section index should yield ok=false")
	}
}
