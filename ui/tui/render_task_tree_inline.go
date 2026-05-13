package tui

// render_task_tree_inline.go — `/tasks` slash command + the plain-text
// tree/list/detail formatters it prints below the chat. The flashy
// floating-panel renderer + tree builder + keyboard router live in
// render_task_tree.go. Companion sibling:
//
//   - render_task_tree.go  taskTreeRow + buildTaskTreeRows +
//                          renderTasksPanel + renderTasksPanelOverlay +
//                          handleTasksPanelKey

import (
	"fmt"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/supervisor"
	"github.com/dontfuckmycode/dfmc/internal/taskstore"
)

// tasksSlash is the /tasks slash command handler.
func (m Model) tasksSlash(args []string) (Model, string) {
	sub := ""
	if len(args) > 0 {
		sub = strings.ToLower(strings.TrimSpace(args[0]))
	}
	switch sub {
	case "":
		m.ui.showTasksPanel = !m.ui.showTasksPanel
		if m.ui.showTasksPanel {
			m.notice = "Tasks panel open - j/k navigate, enter/right expand, left collapse, esc closes."
			if m.tasksPanel.expanded == nil {
				m.tasksPanel.expanded = make(map[string]bool)
			}
		}
		if !m.ui.showTasksPanel {
			m.notice = "Tasks panel closed."
		}
		return m, ""
	case "open", "panel":
		m.ui.showTasksPanel = true
		m.notice = "Tasks panel open - j/k navigate, enter/right expand, left collapse, esc closes."
		if m.tasksPanel.expanded == nil {
			m.tasksPanel.expanded = make(map[string]bool)
		}
		return m, ""
	case "close", "hide":
		m.ui.showTasksPanel = false
		m.notice = "Tasks panel closed."
		return m, ""
	case "list":
		m.ui.showTasksPanel = false
		if m.eng == nil || m.eng.Tools == nil {
			return m, "Engine unavailable."
		}
		store := m.eng.Tools.TaskStore()
		if store == nil {
			return m, "Task store not initialized."
		}
		all, err := store.ListTasks(taskstore.ListOptions{})
		if err != nil {
			return m, "error: " + err.Error()
		}
		return m, renderTasksInlineList(all)
	case "tree":
		m.ui.showTasksPanel = false
		if m.eng == nil || m.eng.Tools == nil {
			return m, "Engine unavailable."
		}
		store := m.eng.Tools.TaskStore()
		if store == nil {
			return m, "Task store not initialized."
		}
		return m, renderTasksInlineTree(store, "")
	case "show":
		m.ui.showTasksPanel = false
		if len(args) < 2 {
			return m, "Usage: /tasks show <id>"
		}
		id := strings.TrimSpace(args[1])
		if m.eng == nil || m.eng.Tools == nil {
			return m, "Engine unavailable."
		}
		store := m.eng.Tools.TaskStore()
		if store == nil {
			return m, "Task store not initialized."
		}
		t, err := store.LoadTask(id)
		if err != nil {
			return m, "load error: " + err.Error()
		}
		if t == nil {
			return m, "task not found: " + id
		}
		return m, formatTaskDetailInline(t)
	case "roots":
		m.ui.showTasksPanel = false
		if m.eng == nil || m.eng.Tools == nil {
			return m, "Engine unavailable."
		}
		store := m.eng.Tools.TaskStore()
		if store == nil {
			return m, "Task store not initialized."
		}
		all, err := store.ListTasks(taskstore.ListOptions{})
		if err != nil {
			return m, "error: " + err.Error()
		}
		var roots []*supervisor.Task
		for _, t := range all {
			if t.ParentID == "" {
				roots = append(roots, t)
			}
		}
		return m, renderTasksInlineList(roots)
	case "clear", "reset":
		m.ui.showTasksPanel = false
		// Wipe every task in the store. Walks the list and DeleteTask's
		// each entry so the operation is observable through the same
		// path /api/v1/task uses, and so children of deleted parents
		// are explicitly removed (no implicit cascade in the store).
		// Drive-managed tasks (RunID != "") are kept — those are owned
		// by drive runs and removing them would orphan the run state.
		if m.eng == nil || m.eng.Tools == nil {
			return m, "Engine unavailable."
		}
		store := m.eng.Tools.TaskStore()
		if store == nil {
			return m, "Task store not initialized."
		}
		all, err := store.ListTasks(taskstore.ListOptions{})
		if err != nil {
			return m, "/tasks clear failed: " + err.Error()
		}
		if len(all) == 0 {
			return m, "/tasks clear: store is already empty."
		}
		deleted := 0
		skipped := 0
		var firstErr error
		for _, t := range all {
			if t.RunID != "" {
				skipped++
				continue
			}
			if delErr := store.DeleteTask(t.ID); delErr != nil {
				if firstErr == nil {
					firstErr = delErr
				}
				continue
			}
			deleted++
		}
		out := fmt.Sprintf("▸ Cleared %d task(s) from the store.", deleted)
		if skipped > 0 {
			out += fmt.Sprintf(" %d drive-owned task(s) kept (use /drive stop <id> to cancel a run).", skipped)
		}
		if firstErr != nil {
			out += "\n   First error: " + firstErr.Error()
		}
		return m, out
	default:
		return m, "tasks: unknown subcommand. Try: /tasks [list|tree|show <id>|roots|clear|open|close]"
	}
}

func renderTasksInlineTree(store *taskstore.Store, rootID string) string {
	all, err := store.ListTasks(taskstore.ListOptions{})
	if err != nil {
		return "error: " + err.Error()
	}
	var roots []*supervisor.Task
	if rootID != "" {
		for _, t := range all {
			if t.ID == rootID {
				roots = []*supervisor.Task{t}
				break
			}
		}
		if roots == nil {
			return "task not found: " + rootID
		}
	} else {
		for _, t := range all {
			if t.ParentID == "" {
				roots = append(roots, t)
			}
		}
	}
	if len(roots) == 0 {
		return "(no tasks)"
	}
	var b strings.Builder
	for i, root := range roots {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(renderTaskTreeNodeDepth(root, 0))
		b.WriteString("\n")
		addChildren(&b, all, root.ID, 1)
	}
	return b.String()
}

func addChildren(b *strings.Builder, all []*supervisor.Task, parentID string, depth int) {
	for _, t := range all {
		if t.ParentID == parentID {
			b.WriteString(renderTaskTreeNodeDepth(t, depth))
			b.WriteString("\n")
			addChildren(b, all, t.ID, depth+1)
		}
	}
}

func renderTasksInlineList(tasks []*supervisor.Task) string {
	if len(tasks) == 0 {
		return "(no tasks)"
	}
	var b strings.Builder
	for _, t := range tasks {
		b.WriteString(renderTaskTreeNode(t))
		b.WriteString("\n")
	}
	return b.String()
}

func renderTaskTreeNode(t *supervisor.Task) string {
	icon := taskStateIconInline(t.State)
	return fmt.Sprintf("%s %s", icon, t.Title)
}

// renderTaskTreeNodeDepth renders a task node with explicit depth (used by inline tree builder).
func renderTaskTreeNodeDepth(t *supervisor.Task, depth int) string {
	indent := strings.Repeat("  ", depth)
	icon := taskStateIconInline(t.State)
	return fmt.Sprintf("%s%s %s", indent, icon, t.Title)
}

func taskStateIconInline(state supervisor.TaskState) string {
	switch state {
	case supervisor.TaskDone:
		return "✓"
	case supervisor.TaskRunning:
		return "…"
	case supervisor.TaskBlocked:
		return "✗"
	case supervisor.TaskSkipped:
		return "⤳"
	case supervisor.TaskWaiting:
		return "⧖"
	case supervisor.TaskExternalReview:
		return "⚠"
	default:
		return "○"
	}
}

func formatTaskDetailInline(t *supervisor.Task) string {
	var b strings.Builder
	fmt.Fprintf(&b, "▸ %s  [%s]\n", t.Title, t.State)
	if t.Detail != "" {
		fmt.Fprintf(&b, "  detail:   %s\n", t.Detail)
	}
	if t.ParentID != "" {
		fmt.Fprintf(&b, "  parent:   %s\n", t.ParentID)
	}
	if len(t.DependsOn) > 0 {
		fmt.Fprintf(&b, "  depends:  %s\n", strings.Join(t.DependsOn, ", "))
	}
	if t.BlockedReason != "" {
		fmt.Fprintf(&b, "  blocked:  %s\n", t.BlockedReason)
	}
	if t.WorkerClass != "" {
		fmt.Fprintf(&b, "  worker:   %s\n", t.WorkerClass)
	}
	if len(t.Labels) > 0 {
		fmt.Fprintf(&b, "  labels:   %s\n", strings.Join(t.Labels, ", "))
	}
	if t.Verification != "" {
		fmt.Fprintf(&b, "  verify:   %s\n", t.Verification)
	}
	if t.Confidence > 0 {
		fmt.Fprintf(&b, "  conf:     %.0f%%\n", t.Confidence*100)
	}
	if t.Summary != "" {
		fmt.Fprintf(&b, "  summary:  %s\n", t.Summary)
	}
	if t.Error != "" {
		fmt.Fprintf(&b, "  error:    %s\n", t.Error)
	}
	if !t.StartedAt.IsZero() {
		fmt.Fprintf(&b, "  started:  %s\n", t.StartedAt.Format("2006-01-02 15:04:05"))
	}
	if !t.EndedAt.IsZero() {
		fmt.Fprintf(&b, "  ended:    %s\n", t.EndedAt.Format("2006-01-02 15:04:05"))
	}
	return strings.TrimRight(b.String(), "\n")
}
