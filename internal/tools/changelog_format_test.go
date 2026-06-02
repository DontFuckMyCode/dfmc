package tools

import (
	"strings"
	"testing"
)

// TestFormatChangelog covers the section grouping + the breaking-commit
// re-typing (a Breaking commit must surface under BREAKING CHANGES, once,
// instead of its original feat/fix bucket), plus scope and PR-link rendering.
func TestFormatChangelog(t *testing.T) {
	tool := NewChangelogGenerateTool()
	commits := []CommitEntry{
		{Type: "feat", Scope: "api", Message: "add endpoint", PRNumber: "12"},
		{Type: "fix", Message: "correct a bug"},
		{Type: "feat", Message: "remove old flag", Breaking: true},
	}
	out := tool.formatChangelog(commits)

	if !strings.Contains(out, "BREAKING CHANGES") {
		t.Errorf("expected a BREAKING CHANGES section:\n%s", out)
	}
	if !strings.Contains(out, "**(api)**") {
		t.Errorf("expected scope rendering:\n%s", out)
	}
	if !strings.Contains(out, "[#12]") {
		t.Errorf("expected PR link rendering:\n%s", out)
	}
	if !strings.Contains(out, "add endpoint") || !strings.Contains(out, "correct a bug") {
		t.Errorf("expected commit messages:\n%s", out)
	}
	// The breaking commit must appear exactly once (under breaking, not also
	// under its original feat bucket).
	if n := strings.Count(out, "remove old flag"); n != 1 {
		t.Errorf("breaking commit should appear once, got %d:\n%s", n, out)
	}
	// breaking section must come before the Features section.
	if bi, fi := strings.Index(out, "BREAKING CHANGES"), strings.Index(out, "add endpoint"); bi > fi {
		t.Errorf("BREAKING CHANGES should precede the feat entries (bi=%d fi=%d)", bi, fi)
	}
}
