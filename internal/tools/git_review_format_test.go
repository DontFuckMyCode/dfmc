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
