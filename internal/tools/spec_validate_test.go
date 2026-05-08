package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLintSpecLines_BrokenAnchor(t *testing.T) {
	body := `# Top

See [the goals](#goals) and [the roadmap](#roadmap-section).

## Goals

Some goals.
`
	sections, _ := parseSpecMarkdown(body, false)
	anchors := make(map[string]struct{})
	for _, s := range sections {
		anchors[s.Anchor] = struct{}{}
	}
	issues := lintSpecLines(body, anchors, "")
	// goals exists; roadmap-section does not.
	got := pickRule(issues, "broken_anchor")
	if len(got) != 1 {
		t.Fatalf("want 1 broken_anchor issue, got %d (all=%+v)", len(got), issues)
	}
	if !strings.Contains(got[0].Message, "roadmap-section") {
		t.Errorf("message wrong: %q", got[0].Message)
	}
}

func TestLintSpecLines_TaskSyntax(t *testing.T) {
	body := `# Plan

- [ ] good task
- [Y] bogus marker
- [ ]missing space
- [x] also good
`
	issues := lintSpecLines(body, map[string]struct{}{}, "")
	got := pickRule(issues, "task_syntax")
	if len(got) != 2 {
		t.Fatalf("want 2 task_syntax issues, got %d (all=%+v)", len(got), issues)
	}
	// First should mention marker; second should mention missing space.
	if !strings.Contains(got[0].Message, "marker") {
		t.Errorf("first issue should mention marker: %q", got[0].Message)
	}
	if !strings.Contains(got[1].Message, "missing space") {
		t.Errorf("second issue should mention missing space: %q", got[1].Message)
	}
}

func TestLintSpecLines_HeadingSkip(t *testing.T) {
	body := "# Top\n\n### Skipped\n\n## Back\n"
	issues := lintSpecLines(body, map[string]struct{}{}, "")
	got := pickRule(issues, "heading_skip")
	if len(got) != 1 {
		t.Fatalf("want 1 heading_skip, got %d", len(got))
	}
	if !strings.Contains(got[0].Message, "1 to level 3") {
		t.Errorf("message wrong: %q", got[0].Message)
	}
}

func TestLintSpecLines_FencedTasksIgnored(t *testing.T) {
	body := "# Top\n\n```\n- [Y] this is in fenced code, must NOT be flagged\n```\n"
	issues := lintSpecLines(body, map[string]struct{}{"top": {}}, "")
	if len(pickRule(issues, "task_syntax")) != 0 {
		t.Errorf("fenced lines should not be linted: %+v", issues)
	}
}

func TestLintSpecLines_BrokenLink_OnDisk(t *testing.T) {
	dir := t.TempDir()
	exists := filepath.Join(dir, "exists.md")
	if err := os.WriteFile(exists, []byte("hi"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	specPath := filepath.Join(dir, "spec.md")
	body := `# Top

See [exists](exists.md) and [missing](nope.md) and [absolute](/totally/missing) too.
`
	if err := os.WriteFile(specPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	issues := lintSpecLines(body, map[string]struct{}{"top": {}}, specPath)
	got := pickRule(issues, "broken_link")
	// nope.md missing → warn. The /totally/missing path also resolves
	// off-disk so it's another broken_link.
	if len(got) < 1 {
		t.Fatalf("want at least 1 broken_link, got 0 (all=%+v)", issues)
	}
	foundNope := false
	for _, iss := range got {
		if strings.Contains(iss.Message, "nope.md") {
			foundNope = true
		}
	}
	if !foundNope {
		t.Errorf("missing nope.md report: %+v", got)
	}
}

func TestLintSpecLines_ProtocolLinksSkipped(t *testing.T) {
	body := "# Top\n\nSee [home](https://example.com) or mail [me](mailto:x@y.z).\n"
	issues := lintSpecLines(body, map[string]struct{}{"top": {}}, "")
	if len(issues) != 0 {
		t.Errorf("protocol links should not produce issues, got %+v", issues)
	}
}

func TestSpecValidateTool_Execute_RealFile(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "spec.md")
	body := "# Top\n\nLink to [missing](#ghost).\n"
	if err := os.WriteFile(specPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	res, err := NewSpecValidateTool().Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params:      map[string]any{"path": "spec.md"},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Data["valid"] != false {
		t.Errorf("valid should be false when broken_anchor present: %+v", res.Data)
	}
	bs := res.Data["by_severity"].(map[string]int)
	if bs["error"] != 1 {
		t.Errorf("expected 1 error, got %d", bs["error"])
	}
}

func TestSpecValidateTool_MissingPath(t *testing.T) {
	_, err := NewSpecValidateTool().Execute(context.Background(), Request{
		ProjectRoot: t.TempDir(),
		Params:      map[string]any{},
	})
	if err == nil {
		t.Fatalf("expected missing-param error")
	}
}

func TestSpecValidateTool_Spec_HasRequiredSurface(t *testing.T) {
	spec := NewSpecValidateTool().Spec()
	if spec.Name != "spec_validate" || spec.Risk != RiskRead {
		t.Errorf("spec metadata wrong: %+v", spec)
	}
}

// pickRule returns issues whose Rule matches name. Test helper.
func pickRule(issues []specIssue, name string) []specIssue {
	out := make([]specIssue, 0)
	for _, iss := range issues {
		if iss.Rule == name {
			out = append(out, iss)
		}
	}
	return out
}
