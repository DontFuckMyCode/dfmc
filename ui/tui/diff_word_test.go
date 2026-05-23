package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestComputeWordDiff_IdenticalStrings(t *testing.T) {
	lp, rp := computeWordDiff("hello world", "hello world")
	if lp.middle != "" || rp.middle != "" {
		t.Fatalf("identical strings should produce empty middles, got left=%q right=%q",
			lp.middle, rp.middle)
	}
	if lp.prefix != "hello world" {
		t.Errorf("identical: left prefix should hold full string, got %q", lp.prefix)
	}
}

func TestComputeWordDiff_TrailingAddition(t *testing.T) {
	lp, rp := computeWordDiff(
		"func Handle(w ResponseWriter) {",
		"func Handle(w ResponseWriter) error {",
	)
	if lp.middle != "" {
		t.Errorf("trailing-add: removed side has no middle, got %q", lp.middle)
	}
	if !strings.Contains(rp.middle, "error") {
		t.Errorf("trailing-add: added side middle should hold 'error', got %q", rp.middle)
	}
	if lp.prefix != rp.prefix {
		t.Errorf("trailing-add: prefixes should match, got %q vs %q", lp.prefix, rp.prefix)
	}
}

func TestComputeWordDiff_MiddleReplacement(t *testing.T) {
	lp, rp := computeWordDiff("var foo = 42", "var bar = 42")
	if lp.middle != "foo" {
		t.Errorf("middle replacement (remove): expected 'foo', got %q", lp.middle)
	}
	if rp.middle != "bar" {
		t.Errorf("middle replacement (add): expected 'bar', got %q", rp.middle)
	}
	if lp.prefix != "var " || rp.prefix != "var " {
		t.Errorf("middle replacement: prefix should be 'var ', got %q / %q", lp.prefix, rp.prefix)
	}
	if lp.suffix != " = 42" || rp.suffix != " = 42" {
		t.Errorf("middle replacement: suffix should be ' = 42', got %q / %q", lp.suffix, rp.suffix)
	}
}

func TestComputeWordDiff_NoCommonChars(t *testing.T) {
	lp, rp := computeWordDiff("abc", "xyz")
	if lp.middle != "abc" || rp.middle != "xyz" {
		t.Errorf("no-overlap: expected full strings as middle, got %q / %q",
			lp.middle, rp.middle)
	}
	if lp.prefix != "" || lp.suffix != "" {
		t.Errorf("no-overlap: prefix/suffix should be empty, got %q / %q",
			lp.prefix, lp.suffix)
	}
}

func TestComputeWordDiff_MultibyteSafe(t *testing.T) {
	// "café" → "cafe": the é (0xC3 0xA9) and e (0x65) differ. The walker
	// must not split the multibyte rune; the middle should hold "é" on
	// the left and "e" on the right.
	lp, rp := computeWordDiff("café", "cafe")
	if lp.middle != "é" || rp.middle != "e" {
		t.Errorf("multibyte: expected middles 'é'/'e', got %q / %q", lp.middle, rp.middle)
	}
	if lp.prefix != "caf" || rp.prefix != "caf" {
		t.Errorf("multibyte: prefix should be 'caf', got %q / %q", lp.prefix, rp.prefix)
	}
}

func TestRenderDiffSideBySide_HighlightsPairedChange(t *testing.T) {
	diff := strings.Join([]string{
		"--- a/foo.go",
		"+++ b/foo.go",
		"@@ -1,1 +1,1 @@",
		"-var foo = 42",
		"+var bar = 42",
	}, "\n")
	out := renderDiffSideBySide(diff, 120, 30)
	stripped := ansi.Strip(out)
	// The literal "var", "foo", "bar", "= 42" must all survive.
	for _, want := range []string{"var", "foo", "bar", "= 42"} {
		if !strings.Contains(stripped, want) {
			t.Errorf("expected %q in output, got:\n%s", want, stripped)
		}
	}
}

func TestRenderDiffSideBySide_UnpairedAdditionFallsBack(t *testing.T) {
	// A pure addition (no peer) should not trip the word-diff path.
	// It should render through the plain cell renderer same as before.
	diff := strings.Join([]string{
		"--- a/foo.go",
		"+++ b/foo.go",
		"@@ -1,1 +1,2 @@",
		" keep",
		"+inserted",
	}, "\n")
	out := renderDiffSideBySide(diff, 120, 30)
	if !strings.Contains(ansi.Strip(out), "inserted") {
		t.Fatalf("unpaired add lost from output:\n%s", out)
	}
}
