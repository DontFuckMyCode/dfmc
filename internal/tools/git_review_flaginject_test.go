package tools

import (
	"strings"
	"testing"
)

// TestGitReviewRejectsFlagInjection locks in the VF-01 fix: an LLM-controlled
// `target` beginning with `-` must be refused before it can reach git, where
// `git diff --output=<path>` would otherwise be an arbitrary file-write
// primitive. The rejection fires inside getFileChanges/getCommits ahead of any
// exec, so this test needs no git repository.
func TestGitReviewRejectsFlagInjection(t *testing.T) {
	tool := NewGitReviewTool()
	malicious := []string{"--output=/tmp/pwned", "-O/tmp/x", "--upload-pack=/tmp/x.sh"}

	for _, target := range malicious {
		if _, err := tool.getFileChanges(target, ""); err == nil {
			t.Errorf("getFileChanges(%q) accepted a flag-shaped target; want rejection", target)
		} else if !strings.Contains(err.Error(), "flag injection") {
			t.Errorf("getFileChanges(%q) error = %v; want flag-injection refusal", target, err)
		}
		if _, err := tool.getCommits(target, "", 10); err == nil {
			t.Errorf("getCommits(%q) accepted a flag-shaped target; want rejection", target)
		} else if !strings.Contains(err.Error(), "flag injection") {
			t.Errorf("getCommits(%q) error = %v; want flag-injection refusal", target, err)
		}
	}
}

// TestChangelogRejectsLeadingDashTag locks in the VF-04 hardening: tag inputs
// may no longer begin with `-`, so they cannot be parsed as git options.
func TestChangelogRejectsLeadingDashTag(t *testing.T) {
	tool := NewChangelogGenerateTool()
	if _, err := tool.generateChangelog(".", "--output=x", "HEAD", ""); err == nil {
		t.Fatal("generateChangelog accepted a `-`-prefixed from_tag; want rejection")
	}
}
