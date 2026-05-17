package tui

// render_task_tree_inline.go — `/tasks` slash command. The shared
// plain-text tree/list/detail formatters live in internal/taskview so CLI
// and TUI render the same task store view. The flashy floating-panel
// renderer + tree builder + keyboard router live in render_task_tree.go.
// Companion sibling:
//
//   - render_task_tree.go  taskTreeRow + buildTaskTreeRows +
//                          renderTasksPanel + renderTasksPanelOverlay +
//                          handleTasksPanelKey

import (
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/taskstore"
	"github.com/dontfuckmycode/dfmc/internal/taskview"
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
		return m, taskview.List(store)
	case "tree":
		m.ui.showTasksPanel = false
		if m.eng == nil || m.eng.Tools == nil {
			return m, "Engine unavailable."
		}
		store := m.eng.Tools.TaskStore()
		if store == nil {
			return m, "Task store not initialized."
		}
		return m, taskview.Tree(store, "")
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
		return m, taskview.Detail(store, id)
	case "roots":
		m.ui.showTasksPanel = false
		if m.eng == nil || m.eng.Tools == nil {
			return m, "Engine unavailable."
		}
		store := m.eng.Tools.TaskStore()
		if store == nil {
			return m, "Task store not initialized."
		}
		return m, taskview.Roots(store)
	case "clear", "reset":
		m.ui.showTasksPanel = false
		if m.eng == nil || m.eng.Tools == nil {
			return m, "Engine unavailable."
		}
		store := m.eng.Tools.TaskStore()
		if store == nil {
			return m, "Task store not initialized."
		}
		return m, taskview.ClearNonDrive(store)
	default:
		return m, taskview.UnknownSubcommandHelp
	}
}

func renderTasksInlineTree(store *taskstore.Store, rootID string) string {
	return taskview.Tree(store, rootID)
}
