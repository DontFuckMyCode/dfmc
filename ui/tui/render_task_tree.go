package tui

// render_task_tree.go — floating Tasks panel: builds the flat tree
// rows from the task store, paints them with state chips and an
// expand/collapse cursor, and hosts the keyboard router that drives
// up/down/left/right/home/end navigation. The /tasks slash command lives
// in render_task_tree_inline.go; shared inline task formatting lives in
// internal/taskview so CLI and TUI stay byte-for-byte aligned. Companion
// sibling:
//
//   - render_task_tree_inline.go  tasksSlash dispatcher +
//                                 renderTasksInlineTree compatibility shim

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/dontfuckmycode/dfmc/internal/taskstore"
	"github.com/dontfuckmycode/dfmc/ui/tui/theme"
)

// taskTreeRow represents a single rendered row in the tasks panel tree.
type taskTreeRow struct {
	ID            string
	Depth         int
	Prefix        string
	State         string
	Title         string
	BlockedReason string
	Confidence    float64
	Verification  string
	WorkerClass   string
	HasChildren   bool
	IsExpanded    bool
	IsSelected    bool
}

// renderTasksPanel renders the floating tasks dashboard panel.
func (m Model) renderTasksPanel(width, height int) string {
	if width < 40 {
		width = 40
	}
	if height < 10 {
		height = 10
	}

	innerWidth := width - 4
	if innerWidth < 20 {
		innerWidth = 20
	}

	header := theme.SectionTitleStyle.Render("Tasks")
	headerLine := fmt.Sprintf("%s  esc/q or /tasks close", header)

	lines := []string{headerLine, theme.DividerStyle.Render(strings.Repeat("─", innerWidth))}

	if m.eng == nil || m.eng.Tools == nil || m.eng.Tools.TaskStore() == nil {
		lines = append(lines, theme.SubtleStyle.Render("  task store not available"))
		return strings.Join(lines, "\n")
	}

	storeTasks, err := m.eng.Tools.TaskStore().ListTasks(taskstore.ListOptions{})
	if err != nil || len(storeTasks) == 0 {
		lines = append(lines, theme.SubtleStyle.Render("  no tasks in store"))
		if err != nil {
			lines = append(lines, fmt.Sprintf("  error: %v", err))
		}
		return strings.Join(lines, "\n")
	}

	if m.tasksPanel.expanded == nil {
		m.tasksPanel.expanded = make(map[string]bool)
	}

	rows := buildTaskTreeRows(storeTasks, m.tasksPanel.expanded, m.tasksPanel.selectedIndex)

	if len(rows) == 0 {
		lines = append(lines, theme.SubtleStyle.Render("  (no tasks visible)"))
		return strings.Join(lines, "\n")
	}

	// Filter to visible range based on scroll
	scroll := m.tasksPanel.scroll
	if scroll < 0 {
		scroll = 0
	}
	visibleLines := height - len(lines) - 2
	if visibleLines < 3 {
		visibleLines = 3
	}

	end := scroll + visibleLines
	if end > len(rows) {
		end = len(rows)
	}
	if scroll >= len(rows) {
		scroll = len(rows) - 1
		end = len(rows)
	}

	for i := scroll; i < end; i++ {
		row := rows[i]

		stateStyle := theme.SubtleStyle
		switch row.State {
		case "done", "completed":
			stateStyle = theme.OkStyle
		case "running", "active", "doing":
			stateStyle = theme.AccentStyle
		case "blocked":
			stateStyle = theme.WarnStyle
		case "failed", "error":
			stateStyle = theme.FailStyle
		}

		stateChip := stateStyle.Render(fmt.Sprintf("[%s]", row.State))

		title := row.Title
		if len(title) > innerWidth-20 {
			title = title[:innerWidth-23] + "..."
		}

		// Build extra info suffix
		extras := []string{}
		if row.BlockedReason != "" {
			extras = append(extras, fmt.Sprintf("blocked: %s", row.BlockedReason))
		}
		if row.Confidence > 0 {
			extras = append(extras, fmt.Sprintf("conf: %.0f%%", row.Confidence*100))
		}
		if row.Verification != "" && row.Verification != "none" {
			extras = append(extras, fmt.Sprintf("verif: %s", row.Verification))
		}
		if row.WorkerClass != "" {
			extras = append(extras, fmt.Sprintf("worker: %s", row.WorkerClass))
		}

		extra := ""
		if len(extras) > 0 {
			extra = "  " + strings.Join(extras, " · ")
		}

		lineText := fmt.Sprintf("%s %s %s%s",
			row.Prefix,
			stateChip,
			title,
			extra,
		)

		if row.IsSelected {
			lineText = lipgloss.NewStyle().
				Foreground(theme.ColorAccent).
				Reverse(true).
				Render(lineText)
		}

		lines = append(lines, lineText)
	}

	// Footer hint
	if len(rows) > visibleLines {
		lines = append(lines, theme.SubtleStyle.Render(fmt.Sprintf("  ↑↓ navigate · → expand · ← collapse · scroll: %d/%d", scroll, len(rows))))
	} else {
		lines = append(lines, theme.SubtleStyle.Render("  ↑↓ navigate · → expand · ← collapse"))
	}

	return strings.Join(lines, "\n")
}

// buildTaskTreeRows + handleTasksPanelKey + renderTasksPanelOverlay
// live in render_task_tree_keyboard.go.
