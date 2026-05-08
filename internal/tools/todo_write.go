package tools

// todo_write.go — session todo-list tool that the model uses to plan
// multi-step work, plus the small ThinkTool reasoning scratch-pad.
// TodoWrite persists to the bbolt-backed task store when one is wired
// (see SetStore) and falls back to in-memory state otherwise. The
// LLM-facing status vocabulary ("pending" / "in_progress" / "completed"
// + aliases) is normalized onto canonical supervisor.TaskState values
// in todo_write_helpers.go (renderStore/renderTasksAsResult/renderMem
// + syncToStore + todoStatusToTaskState/taskStateToTodoStatus +
// parseTodoList all live there). Companion sibling glob.go owns the
// GlobTool + globMatch + doublestarMatch + matchSegments group.

import (
	"context"
	"fmt"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/taskstore"
)

// ThinkTool is a structured reasoning scratch-pad. It never touches state; it
// exists so the LLM can emit an explicit thought, have it logged in the trace,
// and proceed. No output back — the thought is recorded via Result.Data.
type ThinkTool struct{}

func NewThinkTool() *ThinkTool           { return &ThinkTool{} }
func (t *ThinkTool) Name() string        { return "think" }
func (t *ThinkTool) Description() string { return "Record a reasoning step for the trace." }

func (t *ThinkTool) Execute(_ context.Context, req Request) (Result, error) {
	thought := strings.TrimSpace(asString(req.Params, "thought", ""))
	if thought == "" {
		return Result{}, missingParamError("think", "thought", req.Params,
			`{"thought":"plan: read engine.go, then patch run loop"}`,
			`think is a scratch-pad — pass your reasoning step as the "thought" string. No side effects, just trace logging.`)
	}
	// Truncate very long thoughts so the trace stays readable. The LLM can
	// break its reasoning into multiple think calls if needed.
	if len(thought) > 2000 {
		thought = thought[:2000] + "…"
	}
	return Result{
		Output: "noted",
		Data: map[string]any{
			"thought": thought,
			"chars":   len(thought),
		},
	}, nil
}

// TodoWriteTool maintains a per-engine, in-memory todo list. The LLM calls it
// to plan multi-step work. When a task store is injected via SetStore, state
// is persisted to bbolt so todos survive process restarts. Without a store the
// tool falls back to the original in-memory behaviour.
type TodoWriteTool struct {
	state *todoState
	store *taskstore.Store
}

type todoState struct {
	items []todoItem
}

type todoItem struct {
	Content    string   `json:"content"`
	Status     string   `json:"status"`
	ActiveForm string   `json:"active_form,omitempty"`
	Priority   int      `json:"priority,omitempty"`
	Labels     []string `json:"labels,omitempty"`
	Detail     string   `json:"detail,omitempty"`
	ParentID   string   `json:"parent_id,omitempty"`
}

func NewTodoWriteTool() *TodoWriteTool {
	return &TodoWriteTool{state: &todoState{}}
}
func (t *TodoWriteTool) Name() string        { return "todo_write" }
func (t *TodoWriteTool) Description() string { return "Plan or update the session todo list." }

func (t *TodoWriteTool) SetStore(store *taskstore.Store) {
	t.store = store
}

func (t *TodoWriteTool) Execute(_ context.Context, req Request) (Result, error) {
	action := strings.ToLower(strings.TrimSpace(asString(req.Params, "action", "set")))
	switch action {
	case "list", "show":
		return t.renderStore(), nil
	case "clear", "reset":
		if t.store != nil {
			tasks, _ := t.store.ListTasks(taskstore.ListOptions{})
			for _, task := range tasks {
				if task.RunID == "" {
					_ = t.store.DeleteTask(task.ID)
				}
			}
		} else {
			t.state.items = t.state.items[:0]
		}
		return Result{Output: "todos cleared", Data: map[string]any{"count": 0}}, nil
	case "set", "replace", "update":
		raw, ok := req.Params["todos"]
		if !ok || raw == nil {
			return Result{}, missingParamError("todo_write", "todos", req.Params,
				`{"action":"set","todos":[{"content":"Read engine.go","status":"pending"},{"content":"Patch run loop","status":"in_progress"}]}`,
				`todos must be an array of {content, status} objects. Use status "pending" | "in_progress" | "completed".`)
		}
		items, err := parseTodoList(raw)
		if err != nil {
			return Result{}, err
		}
		if t.store != nil {
			if err := t.syncToStore(items); err != nil {
				return Result{}, fmt.Errorf("todo_write: persist failed: %w", err)
			}
		} else {
			t.state.items = items
		}
		return t.renderStore(), nil
	default:
		return Result{}, fmt.Errorf("todo_write: unknown action %q. Allowed: set | list | clear (aliases: replace/update for set; show for list; reset for clear). Example: {\"action\":\"set\",\"todos\":[...]}", action)
	}
}

// TodoItem is the exported projection of a single todo entry for consumers
// outside the tools package (agent loop, handoff brief, TUI).
type TodoItem struct {
	Content    string   `json:"content"`
	Status     string   `json:"status"`
	ActiveForm string   `json:"active_form,omitempty"`
	Priority   int      `json:"priority,omitempty"`
	Labels     []string `json:"labels,omitempty"`
	Detail     string   `json:"detail,omitempty"`
	ParentID   string   `json:"parent_id,omitempty"`
}

// Snapshot returns a defensive copy of the current todo list so callers
// can read it without racing with a concurrent todo_write call. Reads from
// the task store when available, falls back to in-memory state.
func (t *TodoWriteTool) Snapshot() []TodoItem {
	if t == nil {
		return nil
	}
	if t.store != nil {
		tasks, err := t.store.ListTasks(taskstore.ListOptions{})
		if err == nil {
			out := make([]TodoItem, len(tasks))
			for i, task := range tasks {
				out[i] = TodoItem{Content: task.Title, Detail: task.Detail, Status: string(task.State), Labels: task.Labels, ParentID: task.ParentID}
			}
			return out
		}
	}
	if t.state == nil {
		return nil
	}
	out := make([]TodoItem, len(t.state.items))
	for i, it := range t.state.items {
		out[i] = TodoItem(it)
	}
	return out
}
