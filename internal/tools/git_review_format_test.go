package tools

import (
	"strings"
	"testing"
)

// tableCellCount returns the number of |-delimited cells in a markdown
// table row, ignoring the leading/trailing pipe.
func tableCellCount(line string) int {
	line = strings.TrimSpace(line)
	line = strings.Trim(line, "|")
	if line == "" {
		return 0
	}
	return len(strings.Split(line, "|"))
}

// assertTableWellFormed finds the row whose trimmed text starts with
// headerPrefix and checks that the immediately-following separator row has
// the same number of cells. This catches both a wrong-width separator and a
// separator missing its trailing newline (which would glue a data row onto
// the separator line, inflating its cell count).
func assertTableWellFormed(t *testing.T, out, headerPrefix string) {
	t.Helper()
	lines := strings.Split(out, "\n")
	for i, ln := range lines {
		if strings.HasPrefix(strings.TrimSpace(ln), headerPrefix) {
			if i+1 >= len(lines) {
				t.Fatalf("header %q has no separator row", headerPrefix)
			}
			hdr := tableCellCount(ln)
			sep := tableCellCount(lines[i+1])
			if hdr != sep {
				t.Errorf("table %q: header has %d cells but separator has %d\n  header:    %q\n  separator: %q",
					headerPrefix, hdr, sep, ln, lines[i+1])
			}
			return
		}
	}
	t.Fatalf("header row %q not found in output:\n%s", headerPrefix, out)
}

func sampleReviewSummary() ReviewSummary {
	return ReviewSummary{
		TotalCommits:   3,
		TotalFiles:     2,
		TotalAdditions: 40,
		TotalDeletions: 5,
		TopFiles: []FileChange{
			{Path: "a.go", Additions: 30, Deletions: 2, Status: "modified"},
			{Path: "b.go", Additions: 10, Deletions: 3, Status: "added"},
		},
		Authors: []AuthorStats{
			{Name: "Ada", Commits: 2, Additions: 35, Deletions: 4},
			{Name: "Linus", Commits: 1, Additions: 5, Deletions: 1},
		},
	}
}

// TestGitReview_FormatTablesWellFormed guards the two markdown-table bugs:
// the "Most Changed Files" separator had 5 cells for a 4-column header, and
// the "Top Contributors" separator was missing its trailing newline.
func TestGitReview_FormatTablesWellFormed(t *testing.T) {
	tool := &GitReviewTool{}
	out := tool.formatOutput(sampleReviewSummary(), true)
	assertTableWellFormed(t, out, "| File |")
	assertTableWellFormed(t, out, "| Author |")
}

// TestGitReview_IncludeStatsToggle guards that include_stats actually
// controls the stats line (it used to be written unconditionally).
func TestGitReview_IncludeStatsToggle(t *testing.T) {
	tool := &GitReviewTool{}
	const statsMarker = "commits** ·"

	with := tool.formatOutput(sampleReviewSummary(), true)
	if !strings.Contains(with, statsMarker) {
		t.Errorf("include_stats=true should emit the stats line, got:\n%s", with)
	}

	without := tool.formatOutput(sampleReviewSummary(), false)
	if strings.Contains(without, statsMarker) {
		t.Errorf("include_stats=false must omit the stats line, got:\n%s", without)
	}
	// The tables themselves must still render regardless of the toggle.
	if !strings.Contains(without, "### Most Changed Files") {
		t.Errorf("tables should still render when stats are disabled:\n%s", without)
	}
}

// TestParseNumstat covers the numstat parser + the file_pattern substring
// filter (which used to be accepted and ignored) and the 3-field guard
// (a malformed 2-field line must be skipped, not indexed out of range).
func TestParseNumstat(t *testing.T) {
	const out = "30\t2\tinternal/a.go\n" +
		"10\t3\tinternal/b.go\n" +
		"0\t7\tdocs/old.md\n" +
		"5\t0\tinternal/c.go\n" +
		"-\t-\tbin/blob.png\n" + // binary file: counts are "-"
		"bogus-2-field-line\twith-tab\n" + // malformed: only 2 fields -> skipped
		"\n" // blank -> skipped

	// No filter: every well-formed line is parsed (6 well-formed, 1 malformed
	// skipped, 1 blank skipped).
	all, err := parseNumstat(out, "")
	if err != nil {
		t.Fatalf("parseNumstat: %v", err)
	}
	if len(all) != 5 {
		t.Fatalf("expected 5 parsed files, got %d: %+v", len(all), all)
	}

	// Status detection.
	byPath := map[string]FileChange{}
	for _, f := range all {
		byPath[f.Path] = f
	}
	if f := byPath["internal/a.go"]; f.Status != "modified" || f.Additions != 30 || f.Deletions != 2 {
		t.Errorf("a.go = %+v, want modified 30/2", f)
	}
	if f := byPath["docs/old.md"]; f.Status != "deleted" {
		t.Errorf("old.md status = %q, want deleted", f.Status)
	}
	if f := byPath["internal/c.go"]; f.Status != "added" {
		t.Errorf("c.go status = %q, want added", f.Status)
	}
	if f := byPath["bin/blob.png"]; f.Additions != 0 || f.Deletions != 0 {
		t.Errorf("binary blob should parse as 0/0, got %+v", f)
	}

	// Substring filter: only paths containing "internal/" survive.
	filtered, err := parseNumstat(out, "internal/")
	if err != nil {
		t.Fatalf("parseNumstat filtered: %v", err)
	}
	if len(filtered) != 3 {
		t.Fatalf("expected 3 internal/ files, got %d: %+v", len(filtered), filtered)
	}
	for _, f := range filtered {
		if !strings.Contains(f.Path, "internal/") {
			t.Errorf("filter leaked a non-matching path: %q", f.Path)
		}
	}

	// A pattern matching nothing yields an empty (non-nil-required) result.
	none, err := parseNumstat(out, "no-such-path")
	if err != nil {
		t.Fatalf("parseNumstat none: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("expected no matches, got %+v", none)
	}
}
