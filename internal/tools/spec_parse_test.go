package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSpecMarkdown_HeadingsAndTasks(t *testing.T) {
	body := `# Project Plan

Some intro text.

## Goals

- [ ] Ship feature A
- [x] Write the design doc
  - [ ] Sub-task indented two spaces

## Risks

- [ ] Migration is irreversible
- skipping this bullet (no checkbox)

### Compliance

Body for compliance section.

` + "```" + `
# This is fenced code, not a heading
- [ ] also fenced, not a task
` + "```" + `

## Goals

This second "Goals" must dedupe to goals-1.
`
	sections, tasks := parseSpecMarkdown(body, true)

	if len(sections) != 5 {
		t.Fatalf("want 5 sections, got %d: %#v", len(sections), sections)
	}
	if sections[0].Heading != "Project Plan" || sections[0].Level != 1 {
		t.Errorf("first section wrong: %+v", sections[0])
	}
	if sections[1].Heading != "Goals" || sections[1].Anchor != "goals" {
		t.Errorf("second section wrong: %+v", sections[1])
	}
	if sections[1].ParentAnchor != "project-plan" {
		t.Errorf("second section parent wrong: got %q", sections[1].ParentAnchor)
	}
	if sections[3].Heading != "Compliance" || sections[3].Level != 3 {
		t.Errorf("nested section wrong: %+v", sections[3])
	}
	if sections[3].ParentAnchor != "risks" {
		t.Errorf("nested section parent wrong: got %q", sections[3].ParentAnchor)
	}
	if sections[4].Anchor != "goals-1" {
		t.Errorf("dedupe failed: got anchor %q for second 'Goals'", sections[4].Anchor)
	}

	// Three tasks total — fenced ones must NOT be picked up.
	if len(tasks) != 4 {
		t.Fatalf("want 4 tasks, got %d: %#v", len(tasks), tasks)
	}
	if tasks[0].SectionAnchor != "goals" || tasks[0].Done {
		t.Errorf("first task wrong: %+v", tasks[0])
	}
	if !tasks[1].Done || !strings.Contains(tasks[1].Text, "design doc") {
		t.Errorf("second task wrong: %+v", tasks[1])
	}
	if tasks[2].Indent != 2 {
		t.Errorf("indented sub-task indent wrong: got %d", tasks[2].Indent)
	}
	if tasks[3].SectionAnchor != "risks" {
		t.Errorf("fourth task should attach to risks, got %q", tasks[3].SectionAnchor)
	}

	// Task counts must roll up to the section.
	if sections[1].TaskCount != 3 {
		t.Errorf("goals task_count: want 3, got %d", sections[1].TaskCount)
	}
	if sections[2].TaskCount != 1 {
		t.Errorf("risks task_count: want 1, got %d", sections[2].TaskCount)
	}
}

func TestParseSpecMarkdown_IncludeTasksFalseSkipsTaskList(t *testing.T) {
	body := "# Plan\n\n- [ ] do thing\n"
	sections, tasks := parseSpecMarkdown(body, false)
	if len(sections) != 1 {
		t.Fatalf("want 1 section, got %d", len(sections))
	}
	if len(tasks) != 0 {
		t.Errorf("want 0 tasks when include_tasks=false, got %d", len(tasks))
	}
	// task_count counter must also be zero — section state is untouched.
	if sections[0].TaskCount != 0 {
		t.Errorf("section task_count should remain 0 when tasks skipped, got %d", sections[0].TaskCount)
	}
}

func TestParseSpecMarkdown_LineRanges(t *testing.T) {
	body := strings.Join([]string{
		"# Title", // line 1
		"",        // 2
		"prose",   // 3
		"## Sub",  // 4
		"more",    // 5
		"## Sub2", // 6
		"trailer", // 7
	}, "\n")
	sections, _ := parseSpecMarkdown(body, true)
	if len(sections) != 3 {
		t.Fatalf("want 3 sections, got %d", len(sections))
	}
	if sections[0].LineStart != 1 || sections[0].LineEnd != 3 {
		t.Errorf("title range wrong: %+v", sections[0])
	}
	if sections[1].LineStart != 4 || sections[1].LineEnd != 5 {
		t.Errorf("sub range wrong: %+v", sections[1])
	}
	if sections[2].LineStart != 6 || sections[2].LineEnd < 7 {
		t.Errorf("sub2 range should reach EOF, got %+v", sections[2])
	}
}

func TestSlugifyHeading(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Hello World", "hello-world"},
		{"  Padded  ", "padded"},
		{"Foo: Bar / Baz", "foo-bar-baz"},
		{"v1.2.3 release", "v1-2-3-release"},
		{"Über setting", "über-setting"},
		{"___", "section"},
		{"!!!", "section"},
	}
	for _, c := range cases {
		if got := slugifyHeading(c.in); got != c.want {
			t.Errorf("slugifyHeading(%q): want %q, got %q", c.in, c.want, got)
		}
	}
}

func TestSpecParseTool_Execute_RealFile(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "PLAN.md")
	body := "# Plan\n\n## Items\n\n- [ ] alpha\n- [x] beta\n"
	if err := os.WriteFile(specPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	tool := NewSpecParseTool()
	res, err := tool.Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params:      map[string]any{"path": "PLAN.md"},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success, got %+v", res)
	}
	if got := res.Data["title"]; got != "Plan" {
		t.Errorf("title wrong: got %v", got)
	}
	if got := res.Data["task_count"]; got != 2 {
		t.Errorf("task_count wrong: got %v", got)
	}
}

func TestSpecParseTool_Execute_MissingPath(t *testing.T) {
	tool := NewSpecParseTool()
	_, err := tool.Execute(context.Background(), Request{ProjectRoot: t.TempDir(), Params: map[string]any{}})
	if err == nil {
		t.Fatalf("expected missing-param error")
	}
	if !strings.Contains(err.Error(), "path") {
		t.Errorf("error should mention path: %v", err)
	}
}

func TestSpecParseTool_Spec_HasRequiredSurface(t *testing.T) {
	spec := NewSpecParseTool().Spec()
	if spec.Name != "spec_parse" || spec.Risk != RiskRead {
		t.Errorf("spec metadata wrong: %+v", spec)
	}
	if len(spec.Args) == 0 || spec.Args[0].Name != "path" || !spec.Args[0].Required {
		t.Errorf("path arg must be first and required: %+v", spec.Args)
	}
}
