package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestParseHunkStart_StandardForm(t *testing.T) {
	left, right := parseHunkStart("@@ -42,7 +50,8 @@")
	if left != 42 || right != 50 {
		t.Errorf("expected (42,50), got (%d,%d)", left, right)
	}
}

func TestParseHunkStart_SingleLineCounts(t *testing.T) {
	left, right := parseHunkStart("@@ -1 +1 @@")
	if left != 1 || right != 1 {
		t.Errorf("single-line hunks: expected (1,1), got (%d,%d)", left, right)
	}
}

func TestParseHunkStart_WithContextSuffix(t *testing.T) {
	left, right := parseHunkStart("@@ -10,3 +10,5 @@ func Greet(name string) {")
	if left != 10 || right != 10 {
		t.Errorf("expected (10,10) ignoring suffix, got (%d,%d)", left, right)
	}
}

func TestParseHunkStart_Malformed(t *testing.T) {
	left, right := parseHunkStart("@@ garbage @@")
	if left != 0 || right != 0 {
		t.Errorf("expected (0,0) for malformed header, got (%d,%d)", left, right)
	}
}

func TestParseUnifiedDiffSideBySide_AssignsLineNumbers(t *testing.T) {
	diff := strings.Join([]string{
		"--- a/foo.go",
		"+++ b/foo.go",
		"@@ -10,3 +10,4 @@",
		" context one",
		"-removed-line",
		"+added-line",
		"+brand-new",
		" context two",
	}, "\n")
	rows := parseUnifiedDiffSideBySide(diff)

	// Walk the rows looking for the first content row past the hunk
	// header — it should be line 10 on both sides since the hunk
	// starts at -10/+10.
	var first *diffSideRow
	for i := range rows {
		if rows[i].leftKind == diffContext || rows[i].leftKind == diffRemove ||
			rows[i].rightKind == diffAdd {
			first = &rows[i]
			break
		}
	}
	if first == nil {
		t.Fatal("expected a non-header row")
	}
	if first.leftLine != 10 || first.rightLine != 10 {
		t.Errorf("first content row should start at 10/10, got %d/%d",
			first.leftLine, first.rightLine)
	}

	// Find the row that paired "removed-line" with "added-line":
	// left should be 11 (the line right after the leading context),
	// right should also be 11. Brand-new should be on a later row
	// with rightLine=12 and an empty left cell.
	var paired, brandNew *diffSideRow
	for i := range rows {
		if rows[i].leftKind == diffRemove && rows[i].leftText == "removed-line" {
			paired = &rows[i]
		}
		if rows[i].rightKind == diffAdd && rows[i].rightText == "brand-new" {
			brandNew = &rows[i]
		}
	}
	if paired == nil {
		t.Fatal("expected a paired remove/add row")
	}
	if paired.leftLine != 11 || paired.rightLine != 11 {
		t.Errorf("paired row should be at 11/11, got %d/%d",
			paired.leftLine, paired.rightLine)
	}
	if brandNew == nil {
		t.Fatal("expected a brand-new add row")
	}
	if brandNew.leftLine != 0 || brandNew.rightLine != 12 {
		t.Errorf("brand-new add should be 0/12, got %d/%d",
			brandNew.leftLine, brandNew.rightLine)
	}
}

func TestRenderDiffSideBySide_IncludesLineNumberColumn(t *testing.T) {
	diff := strings.Join([]string{
		"--- a/foo.go",
		"+++ b/foo.go",
		"@@ -10,2 +10,3 @@",
		" keep",
		"+inserted",
		" tail",
	}, "\n")
	out := renderDiffSideBySide(diff, 120, 40)
	stripped := ansi.Strip(out)
	// Each numeric line number should appear at least once.
	for _, want := range []string{" 10 ", " 11 ", " 12 "} {
		if !strings.Contains(stripped, want) {
			t.Errorf("expected line number marker %q in stripped output:\n%s", want, stripped)
		}
	}
}
