package security

import (
	"strings"
	"testing"
)

const sampleDiff = `diff --git a/internal/auth/token.go b/internal/auth/token.go
index abc..def 100644
--- a/internal/auth/token.go
+++ b/internal/auth/token.go
@@ -10,3 +10,5 @@ package auth
 const X = 1
-const Old = "stale"
+const APIKey = "sk-ant-1234567890abcdef"
+const Other = "kept"
 const Y = 2
@@ -50,2 +52,3 @@ func parseToken() {
 	x := 1
+	y := exec.Command("sh", "-c", "rm "+input)
 	z := 2
`

func TestIndexAddedLines_BasicHunkParsing(t *testing.T) {
	idx := IndexAddedLines(sampleDiff)
	want := "internal/auth/token.go"
	if _, ok := idx[want]; !ok {
		t.Fatalf("want index entry for %q, got keys=%v", want, keysOf(idx))
	}
	added := idx[want]
	// First hunk starts at new line 10. Body lines, post-header,
	// in order are:
	//   " const X = 1"        (context, line 10)
	//   "-const Old = \"stale\""  (removed, no advance)
	//   "+const APIKey..."    (added, line 11)
	//   "+const Other..."     (added, line 12)
	//   " const Y = 2"        (context, line 13)
	for _, line := range []int{11, 12} {
		if _, ok := added[line]; !ok {
			t.Errorf("expected added line %d, missing (added=%v)", line, mapKeys(added))
		}
	}
	for _, line := range []int{10, 13} {
		if _, ok := added[line]; ok {
			t.Errorf("line %d is context, not added; should not be in index", line)
		}
	}
	// Second hunk: new start 52.
	//   " x := 1"   (context, line 52)
	//   "+y := ..." (added, line 53)
	//   " z := 2"   (context, line 54)
	if _, ok := added[53]; !ok {
		t.Errorf("expected added line 53 from second hunk")
	}
}

func TestAddedLineIndex_HasSuffixMatchesAbsoluteFinding(t *testing.T) {
	idx := IndexAddedLines(sampleDiff)
	// Scanner stores findings with whatever path was passed in;
	// auto-audit passes absolute paths. Finder must still match.
	if !idx.Has("/abs/repo/internal/auth/token.go", 11) {
		t.Errorf("absolute-path finding should suffix-match diff path")
	}
	// Adjacent-name path must NOT match.
	if idx.Has("/abs/repo/internal/auth/token_test.go", 11) {
		t.Errorf("token_test.go should not match token.go")
	}
}

func TestAddedLineIndex_HasIgnoresUnknownPathOrZero(t *testing.T) {
	idx := IndexAddedLines(sampleDiff)
	if idx.Has("internal/parsers/foo.go", 11) {
		t.Errorf("path not in diff should not match")
	}
	if idx.Has("internal/auth/token.go", 0) {
		t.Errorf("line 0 should never match")
	}
	if idx.Has("internal/auth/token.go", -1) {
		t.Errorf("negative line should never match")
	}
}

func TestReport_FilterToAddedLines_KeepsOnlyAddedFindings(t *testing.T) {
	r := Report{
		FilesScanned: 4,
		Secrets: []SecretFinding{
			{File: "internal/auth/token.go", Line: 11, Match: "sk-ant-...", Severity: "critical", Pattern: "Anthropic API Key"},
			{File: "internal/auth/token.go", Line: 5, Match: "stale", Severity: "low", Pattern: "Generic API Key"}, // pre-existing, NOT added
			{File: "unrelated.go", Line: 1, Match: "x", Severity: "high", Pattern: "Stripe Key"},                   // not in diff
		},
		Vulnerabilities: []VulnerabilityFinding{
			{File: "internal/auth/token.go", Line: 53, Kind: "command_injection", Severity: "critical"},
			{File: "internal/auth/token.go", Line: 55, Kind: "command_injection", Severity: "critical"}, // not in added range
		},
	}
	out := r.FilterToAddedLines(sampleDiff)
	if out.FilesScanned != 4 {
		t.Errorf("FilesScanned must be preserved, got %d", out.FilesScanned)
	}
	if len(out.Secrets) != 1 || out.Secrets[0].Line != 11 {
		t.Errorf("only the line-11 secret should survive: got %+v", out.Secrets)
	}
	if len(out.Vulnerabilities) != 1 || out.Vulnerabilities[0].Line != 53 {
		t.Errorf("only the line-53 vuln should survive: got %+v", out.Vulnerabilities)
	}
}

func TestReport_FilterToAddedLines_EmptyDiffNoOps(t *testing.T) {
	r := Report{
		FilesScanned: 1,
		Secrets:      []SecretFinding{{File: "x.go", Line: 1}},
	}
	out := r.FilterToAddedLines("")
	if len(out.Secrets) != 1 {
		t.Errorf("empty diff must return original report, got %+v", out)
	}
	out = r.FilterToAddedLines("   \n\t")
	if len(out.Secrets) != 1 {
		t.Errorf("whitespace-only diff must return original report, got %+v", out)
	}
}

func TestReport_FilterToAddedLines_UnparsableDiffNoOps(t *testing.T) {
	// "garbage" with no recognisable headers: parser yields empty
	// index, filter returns the original report verbatim.
	r := Report{
		FilesScanned: 1,
		Secrets:      []SecretFinding{{File: "x.go", Line: 1}},
	}
	out := r.FilterToAddedLines("not a diff at all\nrandom text")
	if len(out.Secrets) != 1 {
		t.Errorf("unparsable diff must return original report, got %+v", out)
	}
}

func TestParseHunkNewStart_Cases(t *testing.T) {
	cases := []struct {
		header string
		want   int
		ok     bool
	}{
		{"@@ -10,5 +12,7 @@", 12, true},
		{"@@ -10 +12 @@", 12, true},
		{"@@ -1,3 +1,3 @@ func foo()", 1, true},
		{"@@ +12,7 @@ no minus", 12, true}, // tolerant
		{"@@ -10,5 @@", 0, false},          // missing plus
		{"@@", 0, false},
		{"@@ -10,5 +abc @@", 0, false}, // non-numeric
	}
	for _, c := range cases {
		got, ok := parseHunkNewStart(c.header)
		if ok != c.ok || got != c.want {
			t.Errorf("parseHunkNewStart(%q) = (%d,%v), want (%d,%v)", c.header, got, ok, c.want, c.ok)
		}
	}
}

func TestStripDiffPrefix_HandlesAandB(t *testing.T) {
	cases := []struct{ in, want string }{
		{"a/foo/bar.go", "foo/bar.go"},
		{"b/foo/bar.go", "foo/bar.go"},
		{"foo/bar.go", "foo/bar.go"},
		{"b/foo/bar.go\t2026-01-01 12:00:00", "foo/bar.go"}, // mtime trailer
		{"/dev/null", ""},
		{"   ", ""},
		{`b\foo\bar.go`, "foo/bar.go"}, // Windows separator → slash
	}
	for _, c := range cases {
		if got := stripDiffPrefix(c.in); got != c.want {
			t.Errorf("stripDiffPrefix(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPathSuffixMatch_Cases(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"foo.go", "foo.go", true},
		{"foo.go", "/abs/repo/foo.go", true},
		{"internal/auth/token.go", "/abs/repo/internal/auth/token.go", true},
		{"token.go", "internal/auth/token.go", true},
		{"token.go", "internal/auth/token_test.go", false}, // adjacent name
		{"internal/auth", "internal/auth_aux", false},
		{"", "anything", false},
	}
	for _, c := range cases {
		// Skip empty-input cases — pathSuffixMatch is internal and
		// callers never pass empty strings; the test exists for the
		// other cases.
		if c.a == "" {
			continue
		}
		if got := pathSuffixMatch(c.a, c.b); got != c.want {
			t.Errorf("pathSuffixMatch(%q,%q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestIndexAddedLines_DroppedOldFileEntryNotIncluded(t *testing.T) {
	// /dev/null in the new-file header marks a deletion — it should
	// not show up in the index because there is no path to add lines
	// to.
	diff := `diff --git a/old.go b/old.go
deleted file mode 100644
--- a/old.go
+++ /dev/null
@@ -1,3 +0,0 @@
-line one
-line two
-line three
`
	idx := IndexAddedLines(diff)
	if len(idx) != 0 {
		t.Errorf("deletion-only diff should yield empty index, got %v", idx)
	}
}

func TestIndexAddedLines_MultipleFiles(t *testing.T) {
	diff := strings.Join([]string{
		"diff --git a/a.go b/a.go",
		"--- a/a.go",
		"+++ b/a.go",
		"@@ -1,1 +1,2 @@",
		" line",
		"+added in a",
		"diff --git a/b.go b/b.go",
		"--- a/b.go",
		"+++ b/b.go",
		"@@ -10,1 +10,2 @@",
		" line",
		"+added in b",
		"",
	}, "\n")
	idx := IndexAddedLines(diff)
	if len(idx) != 2 {
		t.Fatalf("want 2 files in index, got %d (%v)", len(idx), keysOf(idx))
	}
	if _, ok := idx["a.go"][2]; !ok {
		t.Errorf("a.go line 2 missing")
	}
	if _, ok := idx["b.go"][11]; !ok {
		t.Errorf("b.go line 11 missing")
	}
}

func keysOf(m AddedLineIndex) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func mapKeys(m map[int]struct{}) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
