package tools

// todo_write_helpers.go — render/persist helpers + LLM-vocabulary
// status mapping + JSON-payload parser for TodoWriteTool. Split from
// todo_write.go so the core file stays the lifecycle surface (Execute
// / Snapshot / SetStore) and this sibling owns the formatting and
// persistence plumbing.
//
// renderStore / renderTasksAsResult / renderMem produce the same
// `[ ]` / `[~]` / `[x]` markdown checklist regardless of backend so
// the model sees a stable shape whether the SQLite task store is wired
// or the in-memory fallback is in use. todoStatusToTaskState and its
// inverse taskStateToTodoStatus translate the LLM-facing vocabulary
// (`pending` | `in_progress` | `completed` + aliases) to and from
// canonical supervisor.TaskState values so /api/v1/task and `dfmc
// task list --state running` find todos authored by the model.

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/supervisor"
	"github.com/dontfuckmycode/dfmc/internal/taskstore"
)

// renderStore renders todos from the task store when available, falling back
// to the in-memory state.
func (t *TodoWriteTool) renderStore() Result {
	if t.store != nil {
		tasks, err := t.store.ListTasks(taskstore.ListOptions{})
		if err == nil && len(tasks) == 0 {
			return Result{Output: "(no todos)", Data: map[string]any{"count": 0, "items": []any{}}}
		}
		if err == nil {
			return t.renderTasksAsResult(tasks)
		}
	}
	return t.renderMem()
}

// syncToStore clears session-only tasks (those with no RunID) and replaces
// them with the items parsed from the model payload.
func (t *TodoWriteTool) syncToStore(items []todoItem) error {
	existing, err := t.store.ListTasks(taskstore.ListOptions{})
	if err != nil {
		return err
	}
	for _, task := range existing {
		if task.RunID == "" {
			_ = t.store.DeleteTask(task.ID)
		}
	}
	for _, item := range items {
		task := supervisor.Task{
			ID:       taskstore.NewTaskID(),
			ParentID: item.ParentID,
			Title:    item.Content,
			Detail:   item.Detail,
			State:    todoStatusToTaskState(item.Status),
			Origin:   "todo_write",
			Labels:   item.Labels,
		}
		if err := t.store.SaveTask(&task); err != nil {
			return err
		}
	}
	return nil
}

func (t *TodoWriteTool) renderTasksAsResult(tasks []*supervisor.Task) Result {
	if len(tasks) == 0 {
		return Result{Output: "(no todos)", Data: map[string]any{"count": 0, "items": []any{}}}
	}
	var lines []string
	var pending, active, done int
	for i, task := range tasks {
		mark := "[ ]"
		switch strings.ToLower(string(task.State)) {
		case "running", "in_progress", "active", "doing":
			mark = "[~]"
			active++
		case "done", "completed":
			mark = "[x]"
			done++
		default:
			pending++
		}
		content := task.Title
		if task.Detail != "" {
			content = task.Detail
		}
		lines = append(lines, fmt.Sprintf("%s %d. %s", mark, i+1, content))
	}
	out := strings.Join(lines, "\n")
	items := make([]TodoItem, len(tasks))
	for i, task := range tasks {
		content := task.Title
		if task.Detail != "" {
			content = task.Detail
		}
		items[i] = TodoItem{
			Content:  content,
			Detail:   task.Detail,
			Status:   taskStateToTodoStatus(task.State),
			Priority: 0,
			Labels:   task.Labels,
			ParentID: task.ParentID,
		}
	}
	return Result{
		Output: out,
		Data: map[string]any{
			"count":       len(tasks),
			"pending":     pending,
			"in_progress": active,
			"completed":   done,
			"items":       items,
		},
	}
}

func (t *TodoWriteTool) renderMem() Result {
	if len(t.state.items) == 0 {
		return Result{Output: "(no todos)", Data: map[string]any{"count": 0, "items": []any{}}}
	}
	var lines []string
	var pending, active, done int
	for i, it := range t.state.items {
		mark := "[ ]"
		switch strings.ToLower(it.Status) {
		case "in_progress", "active", "doing":
			mark = "[~]"
			active++
		case "completed", "done":
			mark = "[x]"
			done++
		default:
			pending++
		}
		lines = append(lines, fmt.Sprintf("%s %d. %s", mark, i+1, it.Content))
	}
	out := strings.Join(lines, "\n")
	return Result{
		Output: out,
		Data: map[string]any{
			"count":       len(t.state.items),
			"pending":     pending,
			"in_progress": active,
			"completed":   done,
			"items":       t.state.items,
		},
	}
}

// todoStatusToTaskState normalizes the LLM-facing vocabulary ("pending" |
// "in_progress" | "completed", plus common aliases) onto the canonical
// supervisor.TaskState values the task store filters on. Without this
// mapping a todo saved with status "in_progress" would never match a
// ListTasks(State:"running") query — /api/v1/task and `dfmc task list
// --state running` would silently skip every model-authored todo.
func todoStatusToTaskState(status string) supervisor.TaskState {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "pending", "todo", "queued":
		return supervisor.TaskPending
	case "in_progress", "in-progress", "running", "active", "doing", "working":
		return supervisor.TaskRunning
	case "completed", "complete", "done", "finished":
		return supervisor.TaskDone
	case "blocked":
		return supervisor.TaskBlocked
	case "skipped":
		return supervisor.TaskSkipped
	case "waiting":
		return supervisor.TaskWaiting
	case "external_review":
		return supervisor.TaskExternalReview
	case "verifying":
		return supervisor.TaskVerifying
	}
	return supervisor.TaskPending
}

// taskStateToTodoStatus is the inverse of todoStatusToTaskState: it
// renders the canonical state back into the LLM-facing vocabulary so a
// todo_write "list" call shows the status text the model originally
// sent (e.g. "in_progress" not "running") on tasks persisted via the
// task store.
func taskStateToTodoStatus(state supervisor.TaskState) string {
	switch state {
	case supervisor.TaskRunning:
		return "in_progress"
	case supervisor.TaskDone:
		return "completed"
	case supervisor.TaskBlocked:
		return "blocked"
	case supervisor.TaskSkipped:
		return "skipped"
	case supervisor.TaskWaiting:
		return "waiting"
	case supervisor.TaskExternalReview:
		return "external_review"
	case supervisor.TaskVerifying:
		return "verifying"
	}
	return string(state)
}

func parseTodoList(raw any) ([]todoItem, error) {
	arr, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf(`todos must be a JSON array of {content, status} objects, got %T. Example: [{"content":"Read engine.go","status":"pending"}]`, raw)
	}
	out := make([]todoItem, 0, len(arr))
	for i, entry := range arr {
		m, ok := entry.(map[string]any)
		if !ok {
			return nil, fmt.Errorf(`todos[%d] must be an object like {"content":"...","status":"pending"}, got %T`, i, entry)
		}
		content := strings.TrimSpace(asString(m, "content", ""))
		if content == "" {
			return nil, fmt.Errorf(`todos[%d].content is required (a non-empty string describing the task). Example entry: {"content":"Read engine.go","status":"pending","parent_id":"tsk-abc123"}`, i)
		}
		status := strings.ToLower(strings.TrimSpace(asString(m, "status", "pending")))
		if status == "" {
			status = "pending"
		}
		var labels []string
		if raw := m["labels"]; raw != nil {
			switch v := raw.(type) {
			case []any:
				for _, x := range v {
					if s := strings.TrimSpace(fmt.Sprint(x)); s != "" {
						labels = append(labels, s)
					}
				}
			case []string:
				for _, s := range v {
					if s = strings.TrimSpace(s); s != "" {
						labels = append(labels, s)
					}
				}
			}
		}
		priority := 0
		if raw := m["priority"]; raw != nil {
			switch v := raw.(type) {
			case int:
				priority = v
			case float64:
				priority = int(v)
			case string:
				if p, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
					priority = p
				}
			}
		}
		out = append(out, todoItem{
			Content:    content,
			Status:     status,
			ActiveForm: strings.TrimSpace(asString(m, "active_form", "")),
			Priority:   priority,
			Labels:     labels,
			Detail:     strings.TrimSpace(asString(m, "detail", "")),
			ParentID:   strings.TrimSpace(asString(m, "parent_id", "")),
		})
	}
	return out, nil
}
