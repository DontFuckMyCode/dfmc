package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClassifySpecTask_Heuristics(t *testing.T) {
	cases := []struct {
		text   string
		kind   string
		worker string
		ro     bool
	}{
		{"Verify the migration is reversible", "review", "reviewer", true},
		{"Review the patch for race conditions", "review", "reviewer", true},
		{"Investigate why the cache misses", "research", "researcher", true},
		{"Add tests for the SSE handler", "test", "tester", false},
		{"Write unit test for parser", "test", "tester", false},
		{"Document the intent layer flow", "docs", "scribe", false},
		{"Design the new auth surface", "plan", "architect", true},
		{"Refactor the engine constructor", "code", "coder", false}, // default fallback
	}
	for _, c := range cases {
		k, w, _, ro := classifySpecTask(c.text)
		if k != c.kind || w != c.worker || ro != c.ro {
			t.Errorf("classify(%q): got kind=%s worker=%s ro=%v; want kind=%s worker=%s ro=%v",
				c.text, k, w, ro, c.kind, c.worker, c.ro)
		}
	}
}

func TestSpecToTodoTool_Execute_BasicSpec(t *testing.T) {
	dir := t.TempDir()
	body := strings.Join([]string{
		"# Plan",
		"",
		"## Phase 1",
		"",
		"- [ ] Add the parser",
		"- [x] Write unit test for parser",
		"- [ ] Verify migration works",
		"",
		"## Phase 2",
		"",
		"- [ ] Document the API",
	}, "\n")
	specPath := filepath.Join(dir, "PLAN.md")
	if err := os.WriteFile(specPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	tool := NewSpecToTodoTool()
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
	// Done item must be skipped by default.
	if got := res.Data["todo_count"]; got != 3 {
		t.Errorf("todo_count: want 3 (done item skipped), got %v", got)
	}
	if got := res.Data["skipped_done"]; got != 1 {
		t.Errorf("skipped_done: want 1, got %v", got)
	}
	todos, ok := res.Data["todos"].([]map[string]any)
	if !ok {
		t.Fatalf("todos shape wrong: %T", res.Data["todos"])
	}
	if len(todos) != 3 {
		t.Fatalf("todos len: want 3, got %d", len(todos))
	}
	// First TODO should be the parser one — kind=code (default).
	if todos[0]["kind"] != "code" {
		t.Errorf("first todo kind: want code, got %v", todos[0]["kind"])
	}
	// Verify task should classify as review with read_only=true.
	if todos[1]["kind"] != "review" || todos[1]["read_only"] != true {
		t.Errorf("verify task should be review/read_only: got %+v", todos[1])
	}
	// Detail must include the source path + line.
	detail, _ := todos[0]["detail"].(string)
	if !strings.Contains(detail, "PLAN.md:5") {
		t.Errorf("detail missing source breadcrumb: %q", detail)
	}
	// IDs are stable per section: phase-1-0, phase-1-1, phase-2-0
	if todos[0]["id"] != "phase-1-0" {
		t.Errorf("first id: want phase-1-0, got %v", todos[0]["id"])
	}
	if todos[2]["id"] != "phase-2-0" {
		t.Errorf("third id (different section): want phase-2-0, got %v", todos[2]["id"])
	}
}

func TestSpecToTodoTool_Execute_IncludeDoneAndSection(t *testing.T) {
	dir := t.TempDir()
	body := strings.Join([]string{
		"# Plan",
		"## Phase 1",
		"- [ ] alpha",
		"- [x] beta",
		"## Phase 2",
		"- [ ] gamma",
	}, "\n")
	specPath := filepath.Join(dir, "PLAN.md")
	if err := os.WriteFile(specPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	tool := NewSpecToTodoTool()
	res, err := tool.Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params: map[string]any{
			"path":         "PLAN.md",
			"include_done": true,
			"section":      "phase-1",
		},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	// Filter to phase-1 + include_done = 2 items (alpha pending + beta done).
	if got := res.Data["todo_count"]; got != 2 {
		t.Errorf("todo_count with section filter: want 2, got %v", got)
	}
	todos := res.Data["todos"].([]map[string]any)
	if todos[1]["status"] != "done" {
		t.Errorf("done item should keep status=done: %+v", todos[1])
	}
}

func TestSpecToTodoTool_Execute_MaxTodosCap(t *testing.T) {
	dir := t.TempDir()
	body := "# Plan\n## Items\n- [ ] one\n- [ ] two\n- [ ] three\n- [ ] four\n"
	if err := os.WriteFile(filepath.Join(dir, "x.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	tool := NewSpecToTodoTool()
	res, err := tool.Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params:      map[string]any{"path": "x.md", "max_todos": 2},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := res.Data["todo_count"]; got != 2 {
		t.Errorf("max_todos cap not honored: got %v", got)
	}
}

func TestSpecToTodoTool_MissingPath(t *testing.T) {
	_, err := NewSpecToTodoTool().Execute(context.Background(), Request{
		ProjectRoot: t.TempDir(),
		Params:      map[string]any{},
	})
	if err == nil {
		t.Fatalf("expected missing-param error")
	}
}

func TestSpecToTodoTool_Spec_HasRequiredSurface(t *testing.T) {
	spec := NewSpecToTodoTool().Spec()
	if spec.Name != "spec_to_todo" || spec.Risk != RiskRead {
		t.Errorf("spec metadata wrong: %+v", spec)
	}
}
