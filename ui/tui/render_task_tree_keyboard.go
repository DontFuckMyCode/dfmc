package tui

// render_task_tree_keyboard.go — flat-row builder, keyboard router,
// and side-by-side overlay frame for the floating Tasks panel.
// Sibling of render_task_tree.go which keeps the renderTasksPanel
// renderer (header + state-chip + per-row title + extras + footer
// hint) and the taskTreeRow shape. Companion sibling
// render_task_tree_inline.go owns the /tasks slash inline tree/list/
// detail formatters.

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/dontfuckmycode/dfmc/internal/supervisor"
	"github.com/dontfuckmycode/dfmc/internal/taskstore"
)

// buildTaskTreeRows builds flat list of taskTreeRow for the tasks panel.
// It uses expanded map to determine collapse state and selectedIndex for selection.
func buildTaskTreeRows(storeTasks []*supervisor.Task, expanded map[string]bool, selectedIndex int) []taskTreeRow {
	if len(storeTasks) == 0 {
		return nil
	}

	// Build parent-child map and root list
	children := make(map[string][]*supervisor.Task)
	var roots []*supervisor.Task
	for _, t := range storeTasks {
		if t.ParentID == "" {
			roots = append(roots, t)
		} else {
			children[t.ParentID] = append(children[t.ParentID], t)
		}
	}

	var rows []taskTreeRow
	curIndex := 0

	var walk func(t *supervisor.Task, indent int, isLast bool)
	walk = func(t *supervisor.Task, indent int, isLast bool) {
		if curIndex > selectedIndex {
			return
		}

		prefix := ""
		if indent > 0 {
			treeChar := "├─"
			if isLast {
				treeChar = "└─"
			}
			prefix = strings.Repeat("  ", indent-1) + treeChar + " "
		}

		title := t.Title
		if title == "" {
			title = t.Detail
		}
		if title == "" {
			title = "(untitled)"
		}

		kids := children[t.ID]
		isExpanded := expanded[t.ID]

		rows = append(rows, taskTreeRow{
			ID:            t.ID,
			Depth:         indent,
			Prefix:        prefix,
			State:         string(t.State),
			Title:         title,
			BlockedReason: t.BlockedReason,
			Confidence:    t.Confidence,
			Verification:  string(t.Verification),
			WorkerClass:   string(t.WorkerClass),
			HasChildren:   len(kids) > 0,
			IsExpanded:    isExpanded,
			IsSelected:    curIndex == selectedIndex,
		})
		curIndex++

		if len(kids) > 0 && isExpanded {
			for i, child := range kids {
				walk(child, indent+1, i == len(kids)-1)
			}
		}
	}

	for i, root := range roots {
		walk(root, 0, i == len(roots)-1)
	}

	return rows
}

// handleTasksPanelKey processes keyboard input when the tasks panel is visible
// on the Chat tab. Handles up/down/j/k navigation, left/right/enter expand/collapse,
// and updates scroll to keep the cursor in view.
func (m *Model) handleTasksPanelKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.eng == nil || m.eng.Tools == nil || m.eng.Tools.TaskStore() == nil {
		return m, nil
	}

	storeTasks, err := m.eng.Tools.TaskStore().ListTasks(taskstore.ListOptions{})
	if err != nil || len(storeTasks) == 0 {
		return m, nil
	}

	if m.tasksPanel.expanded == nil {
		m.tasksPanel.expanded = make(map[string]bool)
	}

	rows := buildTaskTreeRows(storeTasks, m.tasksPanel.expanded, m.tasksPanel.selectedIndex)
	totalRows := len(rows)
	if totalRows == 0 {
		return m, nil
	}

	key := msg.String()
	switch key {
	case "esc":
		m.ui.showTasksPanel = false
		m.notice = "Tasks panel closed."
	case "up", "k":
		if m.tasksPanel.selectedIndex > 0 {
			m.tasksPanel.selectedIndex--
		}
	case "down", "j":
		if m.tasksPanel.selectedIndex < totalRows-1 {
			m.tasksPanel.selectedIndex++
		}
	case "left", "right", "enter":
		// Toggle expand/collapse on the currently selected row
		// left = collapse, right/enter = expand
		if m.tasksPanel.selectedIndex >= 0 && m.tasksPanel.selectedIndex < totalRows {
			row := rows[m.tasksPanel.selectedIndex]
			if row.HasChildren {
				if key == "left" {
					// Collapse
					m.tasksPanel.expanded[row.ID] = false
				} else {
					// Expand (right or enter)
					m.tasksPanel.expanded[row.ID] = true
				}
			}
		}
	case "home":
		m.tasksPanel.selectedIndex = 0
	case "end":
		m.tasksPanel.selectedIndex = totalRows - 1
	}

	// Auto-scroll: keep selected in visible range
	scroll := m.tasksPanel.scroll
	visibleHeight := 20 // conservative visible height
	if m.tasksPanel.selectedIndex < scroll {
		m.tasksPanel.scroll = m.tasksPanel.selectedIndex
	}
	if m.tasksPanel.selectedIndex >= scroll+visibleHeight {
		m.tasksPanel.scroll = m.tasksPanel.selectedIndex - visibleHeight + 1
	}
	if m.tasksPanel.scroll < 0 {
		m.tasksPanel.scroll = 0
	}

	return m, nil
}

// renderTasksPanelOverlay composes the chat body with the tasks panel
// as a side-by-side overlay.
func (m Model) renderTasksPanelOverlay(body string, contentWidth int, innerHeight int) string {
	panelWidth := contentWidth / 3
	if panelWidth < 40 {
		panelWidth = 40
	}
	if panelWidth > 80 {
		panelWidth = 80
	}
	panelBody := m.renderTasksPanel(panelWidth, innerHeight)
	frame := lipgloss.NewStyle().
		Width(panelWidth).
		Height(innerHeight).
		Background(colorPanelBg).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorAccent).
		Padding(1, 1)
	chatWidth := contentWidth - panelWidth - 2
	chatBody := lipgloss.NewStyle().Width(chatWidth).Render(body)
	return lipgloss.JoinHorizontal(lipgloss.Top, chatBody, "  ", frame.Render(panelBody))
}
