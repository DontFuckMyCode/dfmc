package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/dontfuckmycode/dfmc/internal/supervisor"
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
			_ = isLast
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
	headerLine := fmt.Sprintf("%s  /tasks to close", header)

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

// tasksSlash is the /tasks slash command handler.
func (m Model) tasksSlash(args []string) string {
	sub := ""
	if len(args) > 0 {
		sub = strings.ToLower(strings.TrimSpace(args[0]))
	}
	switch sub {
	case "", "list":
		m.ui.showTasksPanel = !m.ui.showTasksPanel
		if m.ui.showTasksPanel {
			m.notice = "Tasks panel open — j/k navigate, enter/right expand, left collapse, esc closes."
			if m.tasksPanel.expanded == nil {
				m.tasksPanel.expanded = make(map[string]bool)
			}
		}
		return ""
	case "tree":
		if m.eng == nil {
			return "Engine unavailable."
		}
		store := m.eng.Tools.TaskStore()
		if store == nil {
			return "Task store not initialized."
		}
		return renderTasksInlineTree(store, "")
	case "show":
		if len(args) < 2 {
			return "Usage: /tasks show <id>"
		}
		id := strings.TrimSpace(args[1])
		if m.eng == nil {
			return "Engine unavailable."
		}
		store := m.eng.Tools.TaskStore()
		if store == nil {
			return "Task store not initialized."
		}
		t, err := store.LoadTask(id)
		if err != nil {
			return "load error: " + err.Error()
		}
		if t == nil {
			return "task not found: " + id
		}
		return formatTaskDetailInline(t)
	case "roots":
		if m.eng == nil {
			return "Engine unavailable."
		}
		store := m.eng.Tools.TaskStore()
		if store == nil {
			return "Task store not initialized."
		}
		all, err := store.ListTasks(taskstore.ListOptions{})
		if err != nil {
			return "error: " + err.Error()
		}
		var roots []*supervisor.Task
		for _, t := range all {
			if t.ParentID == "" {
				roots = append(roots, t)
			}
		}
		return renderTasksInlineList(roots)
	default:
		return "tasks: unknown subcommand. Try: /tasks [list|tree|show <id>|roots]"
	}
}

func renderTasksInlineTree(store *taskstore.Store, rootID string) string {
	var trees [][]*supervisor.Task
	if rootID != "" {
		tree, err := store.GetTree(rootID)
		if err != nil {
			return "error: " + err.Error()
		}
		trees = append(trees, tree)
	} else {
		all, err := store.ListTasks(taskstore.ListOptions{})
		if err != nil {
			return "error: " + err.Error()
		}
		seen := make(map[string]bool)
		for _, t := range all {
			if t.ParentID == "" && !seen[t.ID] {
				tree, err := store.GetTree(t.ID)
				if err != nil {
					return "error: " + err.Error()
				}
				for _, node := range tree {
					seen[node.ID] = true
				}
				trees = append(trees, tree)
			}
		}
	}
	if len(trees) == 0 {
		return "(no tasks)"
	}
	var b strings.Builder
	for i, tree := range trees {
		if i > 0 {
			b.WriteString("\n")
		}
		for _, t := range tree {
			b.WriteString(renderTaskTreeNode(t))
			b.WriteString("\n")
		}
	}
	return b.String()
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
	indent := strings.Repeat("  ", t.Depth)
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
	if len(t.BlockedBy) > 0 {
		fmt.Fprintf(&b, "  blocked:  %s\n", strings.Join(t.BlockedBy, ", "))
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
	if t.Depth > 0 {
		fmt.Fprintf(&b, "  depth:    %d\n", t.Depth)
	}
	if t.Order > 0 {
		fmt.Fprintf(&b, "  order:    %d\n", t.Order)
	}
	return strings.TrimRight(b.String(), "\n")
}

