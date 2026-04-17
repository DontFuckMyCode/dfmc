// Unit + integration tests for the rolling-hash duplication detector.
// Each positive case asserts we find the clone; each negative case
// asserts we don't flag dissimilar or overlapping-in-one-file code
// as a multi-location clone.

package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTempFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return p
}

// --- normalizeForDuplication + collapseWhitespace -----------------

// collapseWhitespace operates on a single line (the caller splits on
// '\n' before calling it), so we only test single-line inputs. The
// function's job is to strip leading WS, collapse runs of spaces /
// tabs / carriage returns, and drop the trailing space.
func TestCollapseWhitespace(t *testing.T) {
	cases := map[string]string{
		"":                   "",
		"   ":                "",
		"\t\t  ":             "",
		"foo bar":            "foo bar",
		"  foo   bar  baz  ": "foo bar baz",
		"foo\tbar":           "foo bar",
		"foo\rbar":           "foo bar",
	}
	for in, want := range cases {
		if got := collapseWhitespace(in); got != want {
			t.Errorf("collapseWhitespace(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeForDuplication_DropsBlanks(t *testing.T) {
	src := "foo\n\n  \nbar\n   \nbaz\n"
	got := normalizeForDuplication(src)
	if len(got) != 3 {
		t.Fatalf("blank lines should be dropped: got %d entries (%+v)", len(got), got)
	}
	// Verify origLine still points at the real source line number.
	if got[0].origLine != 1 || got[1].origLine != 4 || got[2].origLine != 6 {
		t.Fatalf("origLine preservation failed: %+v", got)
	}
}

// --- hashNormalizedWindow -----------------------------------------

func TestHashNormalizedWindow_MatchesOnNormalizedTextOnly(t *testing.T) {
	a := []normalizedLine{{1, "x"}, {2, "y"}, {3, "z"}}
	// Same text, totally different line numbers — must hash identically.
	b := []normalizedLine{{10, "x"}, {20, "y"}, {30, "z"}}
	c := []normalizedLine{{1, "x"}, {2, "y"}, {3, "W"}}
	if hashNormalizedWindow(a) != hashNormalizedWindow(b) {
		t.Fatal("origLine must not influence hash")
	}
	if hashNormalizedWindow(a) == hashNormalizedWindow(c) {
		t.Fatal("different content must hash differently")
	}
}

// --- detectDuplication: positive + negative cases -----------------

// Two files with an identical six-line block — must be flagged.
func TestDetectDuplication_FindsCrossFileClone(t *testing.T) {
	tmp := t.TempDir()
	block := `process(a)
process(b)
process(c)
process(d)
process(e)
process(f)
`
	a := writeTempFile(t, tmp, "a.go", "package main\nfunc A() {\n"+block+"}\n")
	b := writeTempFile(t, tmp, "b.go", "package main\nfunc B() {\n"+block+"}\n")

	rep := detectDuplication([]string{a, b}, 6)
	if len(rep.Groups) == 0 {
		t.Fatalf("expected at least one duplication group, got 0 (windows=%d)", rep.WindowsHashed)
	}
	// Check both files appear in at least one group.
	var sawA, sawB bool
	for _, g := range rep.Groups {
		for _, loc := range g.Locations {
			if strings.HasSuffix(loc.File, "a.go") {
				sawA = true
			}
			if strings.HasSuffix(loc.File, "b.go") {
				sawB = true
			}
		}
	}
	if !sawA || !sawB {
		t.Fatalf("both files should show up as clone locations: a=%v b=%v", sawA, sawB)
	}
}

// Two dissimilar files: no clones.
func TestDetectDuplication_NoFalsePositiveOnDifferentCode(t *testing.T) {
	tmp := t.TempDir()
	a := writeTempFile(t, tmp, "a.go", `package main
func A() {
	alpha()
	beta()
	gamma()
	delta()
	epsilon()
	zeta()
}
`)
	b := writeTempFile(t, tmp, "b.go", `package main
func B() {
	plum()
	cherry()
	apple()
	kiwi()
	mango()
	pear()
}
`)
	rep := detectDuplication([]string{a, b}, 6)
	if len(rep.Groups) > 0 {
		t.Fatalf("expected no clones, got %+v", rep.Groups)
	}
}

// A file that repeats the same block twice inside itself counts as a
// clone — two locations in one file is valid (it's still copy-paste).
func TestDetectDuplication_FindsIntraFileClone(t *testing.T) {
	tmp := t.TempDir()
	block := `doA()
doB()
doC()
doD()
doE()
doF()
`
	src := "package main\nfunc Twice() {\n" + block + "\n\n" + block + "}\n"
	p := writeTempFile(t, tmp, "twice.go", src)

	rep := detectDuplication([]string{p}, 6)
	if len(rep.Groups) == 0 {
		t.Fatal("expected intra-file clone to be detected")
	}
	// Exactly one group with two distinct spans in the same file.
	for _, g := range rep.Groups {
		seenStarts := map[int]bool{}
		for _, loc := range g.Locations {
			seenStarts[loc.StartLine] = true
		}
		if len(seenStarts) < 2 {
			t.Fatalf("expected at least 2 distinct start lines in the clone group, got %+v", g.Locations)
		}
	}
}

// Comments that are byte-identical across files must NOT produce a
// clone (stripStringsAndComments should erase them).
func TestDetectDuplication_CommentsDoNotCount(t *testing.T) {
	tmp := t.TempDir()
	comments := `// line one copied across files
// line two copied across files
// line three copied across files
// line four copied across files
// line five copied across files
// line six copied across files
`
	a := writeTempFile(t, tmp, "a.go", "package main\n"+comments+"func A() {}\n")
	b := writeTempFile(t, tmp, "b.go", "package main\n"+comments+"func B() {}\n")

	rep := detectDuplication([]string{a, b}, 6)
	if len(rep.Groups) > 0 {
		t.Fatalf("identical comment blocks must not be flagged: %+v", rep.Groups)
	}
}

// Length reported should reflect the span of source lines, at least
// `minLines` long.
func TestDetectDuplication_LengthAtLeastMin(t *testing.T) {
	tmp := t.TempDir()
	block := strings.Repeat("foo()\n", 8)
	a := writeTempFile(t, tmp, "a.go", "package main\nfunc A() {\n"+block+"}\n")
	b := writeTempFile(t, tmp, "b.go", "package main\nfunc B() {\n"+block+"}\n")

	rep := detectDuplication([]string{a, b}, 6)
	if len(rep.Groups) == 0 {
		t.Fatal("expected a clone group")
	}
	for _, g := range rep.Groups {
		if g.Length < 6 {
			t.Fatalf("group length must be >= minLines (6), got %d", g.Length)
		}
	}
}

// A short file (fewer non-blank lines than minLines) must be ignored,
// not crash.
func TestDetectDuplication_ShortFilesSkippedSafely(t *testing.T) {
	tmp := t.TempDir()
	a := writeTempFile(t, tmp, "a.go", "package a\nfunc A() {}\n")
	b := writeTempFile(t, tmp, "b.go", "package b\nfunc B() {}\n")

	rep := detectDuplication([]string{a, b}, 6)
	if len(rep.Groups) > 0 {
		t.Fatalf("short files produced unexpected groups: %+v", rep.Groups)
	}
	if rep.FilesScanned != 2 {
		t.Fatalf("both files should still be counted as scanned: got %d", rep.FilesScanned)
	}
}

// --- End-to-end: Engine AnalyzeWithOptions surfaces Duplication ----

func TestAnalyzeWithOptions_DuplicationPassReaches(t *testing.T) {
	// Direct detector call from the analyze wiring, without bringing up
	// a full Engine (that flow is exercised in analyze_test.go).
	tmp := t.TempDir()
	block := strings.Repeat("q()\n", 7)
	a := writeTempFile(t, tmp, "a.go", "package main\nfunc A() {\n"+block+"}\n")
	b := writeTempFile(t, tmp, "b.go", "package main\nfunc B() {\n"+block+"}\n")

	rep := detectDuplication([]string{a, b}, duplicationMinLines)
	if rep.MinLines != duplicationMinLines {
		t.Fatalf("MinLines not recorded: %d", rep.MinLines)
	}
	if len(rep.Groups) == 0 {
		t.Fatal("expected a clone group via the default minLines")
	}
	if rep.DuplicatedLines == 0 {
		t.Fatal("DuplicatedLines should be > 0 when clones exist")
	}
}
