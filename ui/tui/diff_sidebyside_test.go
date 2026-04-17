package tui

// Lock in the side-by-side diff parser shape. The Patch panel relies
// on parseUnifiedDiffSideBySide pairing removed lines (left) with
// added lines (right) row-by-row — a regression here would silently
// reshuffle hunks and the user would think the diff was wrong.

import (
	"strings"
	"testing"
)

func TestParseUnifiedDiff_PairsRemovedAndAddedRowByRow(t *testing.T) {
	diff := `--- a/main.go
+++ b/main.go
@@ -1,3 +1,3 @@
 package main
-fmt.Println("hello")
+fmt.Println("world")
 // unchanged
`
	rows := parseUnifiedDiffSideBySide(diff)
	if len(rows) < 5 {
		t.Fatalf("expected at least 5 rows (---, +++, @@, context, change, context); got %d:\n%+v", len(rows), rows)
	}
	// The change row must have removed text on the left and added text
	// on the right — paired, not stacked.
	var changeRow *diffSideRow
	for i := range rows {
		if rows[i].leftKind == diffRemove || rows[i].rightKind == diffAdd {
			changeRow = &rows[i]
			break
		}
	}
	if changeRow == nil {
		t.Fatal("no change row produced")
	}
	if !strings.Contains(changeRow.leftText, `"hello"`) {
		t.Fatalf("left side missing the removed line: %+v", changeRow)
	}
	if !strings.Contains(changeRow.rightText, `"world"`) {
		t.Fatalf("right side missing the added line: %+v", changeRow)
	}
	if changeRow.leftKind != diffRemove || changeRow.rightKind != diffAdd {
		t.Fatalf("kinds mismatch: left=%c right=%c", changeRow.leftKind, changeRow.rightKind)
	}
}

// Surplus-on-one-side hunks (e.g. pure deletion) must produce empty
// cells on the surplus column so vertical alignment is preserved.
// Otherwise the +++ marker for the next file would slide up against
// the last - line and the columns drift.
func TestParseUnifiedDiff_PadsSurplusWithEmpty(t *testing.T) {
	diff := `@@ -1,3 +1,1 @@
-deleted-1
-deleted-2
-deleted-3
+kept
`
	rows := parseUnifiedDiffSideBySide(diff)
	// Skip the @@ row; expect 3 paired rows (one with right text, two
	// with right empty).
	var changeRows []diffSideRow
	for _, r := range rows {
		if r.leftKind == diffRemove || r.rightKind == diffAdd {
			changeRows = append(changeRows, r)
		}
	}
	if len(changeRows) != 3 {
		t.Fatalf("expected 3 change rows, got %d: %+v", len(changeRows), changeRows)
	}
	if changeRows[0].rightKind != diffAdd {
		t.Fatalf("first row should pair with the +kept line; got %+v", changeRows[0])
	}
	for i := 1; i < 3; i++ {
		if changeRows[i].rightKind != diffEmpty {
			t.Fatalf("row %d should have empty right; got %+v", i, changeRows[i])
		}
		if changeRows[i].leftKind != diffRemove {
			t.Fatalf("row %d should still hold the deleted line on the left; got %+v", i, changeRows[i])
		}
	}
}

// Render output must contain the gutter glyphs `-` and `+` so users
// can identify direction even if the terminal strips colour (TTY
// recordings, screenshots that downsample, etc).
func TestRenderDiffSideBySide_IncludesPlusMinusGutters(t *testing.T) {
	diff := `@@ -1 +1 @@
-old
+new
`
	out := renderDiffSideBySide(diff, 80, 10)
	if !strings.Contains(out, "- old") {
		t.Fatalf("expected '- old' gutter on the left:\n%s", out)
	}
	if !strings.Contains(out, "+ new") {
		t.Fatalf("expected '+ new' gutter on the right:\n%s", out)
	}
	if !strings.Contains(out, "│") {
		t.Fatalf("expected vertical divider between columns:\n%s", out)
	}
}

func TestRenderDiffSideBySide_EmptyDiffShowsCleanMessage(t *testing.T) {
	out := renderDiffSideBySide("", 80, 10)
	if !strings.Contains(out, "Working tree is clean") {
		t.Fatalf("empty-diff message missing:\n%s", out)
	}
}

// Truncation marker must appear when the row count exceeds maxRows.
// Without this, large diffs would silently drop the tail and hide
// changes the user needs to review.
func TestRenderDiffSideBySide_AnnouncesTruncation(t *testing.T) {
	var b strings.Builder
	b.WriteString("@@ -1,30 +1,30 @@\n")
	for i := 0; i < 30; i++ {
		b.WriteString("-line\n+line\n")
	}
	out := renderDiffSideBySide(b.String(), 80, 5)
	if !strings.Contains(out, "truncated") {
		t.Fatalf("expected truncation marker:\n%s", out)
	}
}
