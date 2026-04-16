package tools

// task_split.go — exposes the deterministic task decomposer in
// internal/planning as a cheap, offline tool. The model calls it before a
// tool_batch_call(delegate_task) to check if a broad request actually has
// parallel research units worth fanning out. No LLM involved on this path.

import (
	"context"
	"fmt"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/planning"
)

type TaskSplitTool struct{}

func NewTaskSplitTool() *TaskSplitTool       { return &TaskSplitTool{} }
func (t *TaskSplitTool) Name() string        { return "task_split" }
func (t *TaskSplitTool) Description() string { return "Decompose a free-text task into parallel or sequential subtasks." }

func (t *TaskSplitTool) Execute(_ context.Context, req Request) (Result, error) {
	query := strings.TrimSpace(asString(req.Params, "task", ""))
	if query == "" {
		return Result{}, fmt.Errorf("task is required")
	}
	plan := planning.SplitTask(query)
	subs := make([]map[string]string, 0, len(plan.Subtasks))
	for _, s := range plan.Subtasks {
		subs = append(subs, map[string]string{
			"title":       s.Title,
			"description": s.Description,
			"hint":        s.Hint,
		})
	}
	var lines []string
	for i, s := range plan.Subtasks {
		lines = append(lines, fmt.Sprintf("%d. [%s] %s", i+1, s.Hint, s.Title))
	}
	return Result{
		Output: strings.Join(lines, "\n"),
		Data: map[string]any{
			"subtasks":   subs,
			"parallel":   plan.Parallel,
			"confidence": plan.Confidence,
			"count":      len(plan.Subtasks),
			"original":   plan.Original,
		},
	}, nil
}

func (t *TaskSplitTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "task_split",
		Title:   "Split task",
		Summary: "Decompose a task into subtasks the model can fan out to sub-agents.",
		Purpose: "Check whether a broad request has parallel research units before round-tripping.",
		Prompt: `Deterministic, offline splitter. No LLM call — just pattern matching on numbered lists, stage markers ("first/then"), and multi-conjunction enumerations.

When to use:
- User asked for something that *might* be multiple tasks ("survey A, B, and C and document each"). Call this first; if it returns multiple subtasks with ` + "`parallel=true`" + `, fan out via ` + "`tool_batch_call(delegate_task)`" + ` — each sub-agent gets one subtask.
- User gave an explicit numbered list. Split it so each item gets focused attention.
- ` + "`count=1`" + ` or ` + "`confidence<0.4`" + ` → treat as a single task; do NOT fan out.

When NOT to use:
- Single, focused questions ("fix the parser in token.go"). The splitter will return ` + "`count=1`" + ` and waste a round-trip.
- Code-edit requests. Fan-out shines for research, not mutation.

Rules:
- ` + "`parallel=false`" + ` means the subtasks are sequential ("first X, then Y"). Run them one after another, feeding results forward.
- ` + "`parallel=true`" + ` means the subtasks are independent — safe to run in parallel via ` + "`tool_batch_call`" + `.
- Confidence ≥ 0.7 is a strong split signal; ≥ 0.4 is worth considering; below that, don't fan out.`,
		Risk: RiskRead,
		Tags: []string{"meta", "planning"},
		Args: []Arg{
			{Name: "task", Type: ArgString, Required: true, Description: "Free-text task to decompose."},
		},
		Returns:    "{subtasks[{title,description,hint}], parallel, confidence, count, original}.",
		Examples:   []string{`{"task":"survey engine.go, map the router, and document the manager"}`},
		Idempotent: true,
		CostHint:   "cheap",
	}
}
