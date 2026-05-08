package tools

// spec_to_todo.go — convert the GFM task list inside a markdown spec
// into a list of Drive-compatible TODO entries. Companion to
// spec_parse.go (which provides the structural read).
//
// Why a separate tool rather than just letting the planner read the
// spec: the planner LLM is opinionated — it merges tasks, adds
// dependencies, splits items it considers too big. That's right when
// the spec is a brain-dump; it's wrong when the spec is already a
// plan and the user wants those exact tasks executed in order. This
// tool gives the model the "literal interpretation" path: each
// `- [ ]` becomes one Todo, no merging, no inference of dependencies
// the user didn't write down.
//
// Output is a Drive-compatible Todo shape but emitted as
// map[string]any rather than internal/drive.Todo to avoid a tools →
// drive package dependency. Drive's planner ingestion path can json-
// round-trip this into its own type when we wire spec-to-drive.

import (
	"context"
	"fmt"
	"os"
	"strings"
)

type SpecToTodoTool struct{}

func NewSpecToTodoTool() *SpecToTodoTool { return &SpecToTodoTool{} }
func (t *SpecToTodoTool) Name() string   { return "spec_to_todo" }
func (t *SpecToTodoTool) Description() string {
	return "Turn the checklist items in a markdown spec into Drive-compatible TODO entries."
}

// classifySpecTask runs a tiny keyword classifier on the task text to
// pick a kind/worker/provider triple. The heuristic is intentionally
// shallow: the model can override any of these by setting fields on
// the returned Todos before passing them to Drive, and a misfire here
// just means "wrong default" — never an outright wrong execution.
func classifySpecTask(text string) (kind, workerClass, providerTag string, readOnly bool) {
	lower := strings.ToLower(text)
	switch {
	case containsAny(lower, "verify", "check that", "ensure that", "audit", "review "):
		return "review", "reviewer", "review", true
	case containsAny(lower, "research", "investigate", "explore", "survey"):
		return "research", "researcher", "research", true
	case containsAny(lower, "write test", "add test", "test that", "unit test", "integration test"):
		return "test", "tester", "test", false
	case containsAny(lower, "document ", "write doc", "update doc", "documentation"):
		return "docs", "scribe", "code", false
	case containsAny(lower, "design ", "spec ", "plan "):
		return "plan", "architect", "plan", true
	default:
		return "code", "coder", "code", false
	}
}

func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}

func (t *SpecToTodoTool) Execute(ctx context.Context, req Request) (Result, error) {
	path := strings.TrimSpace(asString(req.Params, "path", ""))
	if path == "" {
		return Result{}, missingParamError("spec_to_todo", "path", req.Params,
			`{"path":".project/PLAN.md"}`,
			`path is the markdown spec file. Optional: section (anchor filter), include_done (default false), max_todos (default 0 = no cap).`)
	}
	abs, err := EnsureWithinRoot(req.ProjectRoot, path)
	if err != nil {
		return Result{}, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return Result{}, fmt.Errorf("read spec: %w", err)
	}
	sectionFilter := strings.TrimSpace(strings.ToLower(asString(req.Params, "section", "")))
	includeDone := false
	if v, ok := req.Params["include_done"].(bool); ok {
		includeDone = v
	}
	maxTodos := asInt(req.Params, "max_todos", 0)

	sections, tasks := parseSpecMarkdown(string(data), true)
	headingByAnchor := make(map[string]string, len(sections))
	for _, s := range sections {
		headingByAnchor[s.Anchor] = s.Heading
	}

	// id generator counts per section so re-parses produce stable IDs
	// when only unrelated sections change.
	idxBySection := make(map[string]int)
	todos := make([]map[string]any, 0, len(tasks))
	skippedDone := 0
	for _, task := range tasks {
		if !includeDone && task.Done {
			skippedDone++
			continue
		}
		if sectionFilter != "" && task.SectionAnchor != sectionFilter {
			continue
		}
		anchor := task.SectionAnchor
		if anchor == "" {
			anchor = "root"
		}
		idx := idxBySection[anchor]
		idxBySection[anchor] = idx + 1
		id := fmt.Sprintf("%s-%d", anchor, idx)
		kind, worker, providerTag, readOnly := classifySpecTask(task.Text)
		title := task.Text
		if len(title) > 200 {
			title = title[:200] + "..."
		}
		detail := buildSpecTodoDetail(path, task, headingByAnchor)
		todo := map[string]any{
			"id":             id,
			"title":          title,
			"detail":         detail,
			"kind":           kind,
			"worker_class":   worker,
			"provider_tag":   providerTag,
			"read_only":      readOnly,
			"status":         "pending",
			"source_section": task.SectionAnchor,
			"source_line":    task.Line,
		}
		if task.Done {
			todo["status"] = "done"
		}
		todos = append(todos, todo)
		if maxTodos > 0 && len(todos) >= maxTodos {
			break
		}
	}

	out := map[string]any{
		"path":           path,
		"todo_count":     len(todos),
		"skipped_done":   skippedDone,
		"section_filter": sectionFilter,
		"todos":          todos,
	}
	return Result{Success: true, Data: out}, nil
}

// buildSpecTodoDetail packs origin metadata into the detail string so
// Drive's planner-skip path keeps the breadcrumb back to the spec
// even after the JSON round-trip.
func buildSpecTodoDetail(path string, task specTask, headingByAnchor map[string]string) string {
	heading := headingByAnchor[task.SectionAnchor]
	if heading == "" {
		heading = "(no section)"
	}
	return fmt.Sprintf("Source: %s:%d · section: %s\n\n%s", path, task.Line, heading, task.Text)
}

func (t *SpecToTodoTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "spec_to_todo",
		Title:   "Spec → Drive TODOs",
		Summary: "Convert GFM checklist items in a markdown spec into Drive-compatible TODO objects.",
		Purpose: "Use when the spec already names the work as discrete tasks and you want to execute them literally — without the planner LLM merging or splitting items.",
		Prompt: `Reads a markdown spec, walks every ` + "`- [ ]`" + ` / ` + "`- [x]`" + ` checklist item, and emits one TODO per item with a kind/worker/provider classification derived from the task text.

Args:
- path (required): markdown spec relative to project root.
- section (optional): anchor of one section to filter on (matches the slug spec_parse emits, lowercase).
- include_done (optional, default false): when true, also emit done items as Todos with status=done.
- max_todos (optional, default 0 = unlimited): cap on returned TODOs.

Classification heuristic (keyword-based; coarse, override after the call when needed):
- "verify"/"check"/"audit"/"review " → kind=review, worker=reviewer, read_only=true
- "research"/"investigate"/"explore"/"survey" → kind=research, worker=researcher, read_only=true
- "test"/"unit test"/"integration test" → kind=test, worker=tester
- "document"/"docs" → kind=docs, worker=scribe
- "design "/"spec "/"plan " → kind=plan, worker=architect
- otherwise → kind=code, worker=coder

Output: {path, todo_count, skipped_done, section_filter, todos:[{id, title, detail, kind, worker_class, provider_tag, read_only, status, source_section, source_line}]}.

When to use:
- Spec is already a flat plan and you want literal execution.
- You want a starting set of TODOs to hand-edit before passing to Drive.

When NOT to use:
- The spec is prose without checklists — let the Drive planner LLM decompose it instead.`,
		Risk: RiskRead,
		Tags: []string{"read", "spec", "planning", "drive"},
		Args: []Arg{
			{Name: "path", Type: ArgString, Required: true, Description: "Markdown spec file relative to project root."},
			{Name: "section", Type: ArgString, Description: "Anchor of a single section to filter on (lowercase)."},
			{Name: "include_done", Type: ArgBoolean, Default: false, Description: "Include already-done items as status=done TODOs."},
			{Name: "max_todos", Type: ArgInteger, Default: 0, Description: "Cap on returned TODOs (0 = no cap)."},
		},
		Returns:    "{path, todo_count, skipped_done, section_filter, todos:[...]}.",
		Examples:   []string{`{"path":".project/TASKS.md"}`, `{"path":".project/PLAN.md","section":"phase-1","max_todos":10}`},
		Idempotent: true,
		CostHint:   "io-bound",
	}
}
