// render_workflow_v2.go — F5 Workflow panel, rebuilt with the same
// 3-pane card-driven shell as F2/F3/F4 (status banner + RUNS · TREE ·
// METADATA columns). The keyboard surface, routing editor, and tree
// walker stay in render_workflow.go; this file owns rendering only.
//
// Layout strategy (mirrors render_files / render_patch):
//   ≥120 cols → 3 panes (28% runs · 44% tree · 28% metadata cards)
//   80-119    → 2 panes (35% runs · 65% tree) + inline footer
//   <80       → 1 pane stack
//
// Banner: shows run counts (planning/running/done/failed/stopped) as
// coloured chips so the cockpit reads at a glance even before drilling
// into a specific run.
//
// Metadata cards (wide):
//   RUN     — status chip, task line, worker counts, age
//   TODO    — selected TODO summary (or "press enter on a row" hint)
//   ACTIONS — keyboard surface

package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/dontfuckmycode/dfmc/internal/drive"
)

// renderWorkflowViewV2 is the rebuilt F5 Workflow panel. The original
// renderWorkflowView in render_workflow.go now delegates here.
func (m Model) renderWorkflowViewV2(width int) string {
	width = max(width, 50)
	height := 24

	pal := paletteForTab("Workflow", false)

	// Routing editor overlay still wins — it's modal.
	if m.workflow.showRoutingEditor {
		return m.renderRoutingEditor(width)
	}

	threePane := width >= 120
	twoPane := !threePane && width >= 80

	listW, treeW, metaW := workflowPanelWidths(width, threePane, twoPane)

	banner := m.workflowTopBanner(width)
	listBlock := m.renderWorkflowRunsPane(listW, height, pal)
	treeBlock := m.renderWorkflowTreePane(treeW, height, pal)
	var out string
	if threePane {
		metaBlock := m.renderWorkflowMetaPane(metaW, height, pal)
		body := lipgloss.JoinHorizontal(lipgloss.Top,
			listBlock, "  ", treeBlock, "  ", metaBlock)
		out = banner + "\n" + body
	} else if twoPane {
		footer := m.renderWorkflowMetaInline(width)
		body := lipgloss.JoinHorizontal(lipgloss.Top, listBlock, "  ", treeBlock)
		out = banner + "\n" + body + "\n" + footer
	} else {
		out = banner + "\n" + listBlock + "\n" + treeBlock
	}
	if m.actionMenu.open && m.actionMenu.owner == "Workflow" {
		out += "\n\n" + m.renderActionMenu(width)
	}
	return out
}

func workflowPanelWidths(total int, threePane, twoPane bool) (listW, treeW, metaW int) {
	if threePane {
		listW = max(total*28/100, 28)
		metaW = max(total*28/100, 28)
		treeW = max(total-listW-metaW-4, 32)
		return
	}
	if twoPane {
		listW = max(total*35/100, 28)
		treeW = max(total-listW-2, 28)
		return
	}
	return total, total, 0
}

// --- BANNER ------------------------------------------------------------------

func (m Model) workflowTopBanner(width int) string {
	title := titleStyle.Bold(true).Render("⚡ WORKFLOW")
	counts := m.workflowRunCounts()

	chips := []string{
		runningStyle.Render(fmt.Sprintf(" %d running ", counts.running)),
		subtleStyle.Render(fmt.Sprintf(" %d planning ", counts.planning)),
		doneStyle.Render(fmt.Sprintf(" %d done ", counts.done)),
		blockedStyle.Render(fmt.Sprintf(" %d failed ", counts.failed)),
		subtleStyle.Render(fmt.Sprintf(" %d stopped ", counts.stopped)),
	}
	chipStrip := strings.Join(chips, " ")

	if len(m.workflow.runs) == 0 {
		hint := subtleStyle.Render("no drive runs yet — start with /drive <task>")
		gap := max(width-lipgloss.Width(title)-lipgloss.Width(hint)-4, 1)
		return title + strings.Repeat(" ", gap) + hint + "\n" + renderDivider(width-2)
	}

	gap := max(width-lipgloss.Width(title)-lipgloss.Width(chipStrip)-4, 1)
	return title + strings.Repeat(" ", gap) + chipStrip + "\n" + renderDivider(width-2)
}

type workflowCounts struct {
	planning, running, done, failed, stopped int
}

func (m Model) workflowRunCounts() workflowCounts {
	var c workflowCounts
	for _, r := range m.workflow.runs {
		switch r.Status {
		case drive.RunPlanning:
			c.planning++
		case drive.RunRunning:
			c.running++
		case drive.RunDone:
			c.done++
		case drive.RunFailed:
			c.failed++
		case drive.RunStopped:
			c.stopped++
		}
	}
	return c
}

// --- RUNS LIST PANE ----------------------------------------------------------

func (m Model) renderWorkflowRunsPane(width, height int, pal tabPaletteEntry) string {
	header := m.workflowRunsHeader(width)
	lines := []string{
		header,
		renderDivider(width - 2),
		"",
	}
	if len(m.workflow.runs) == 0 {
		lines = append(lines,
			subtleStyle.Render("No drive runs yet."),
			"",
			subtleStyle.Render("Start one from Chat:"),
			subtleStyle.Render("  /drive <task description>"),
			"",
			subtleStyle.Render("Or via CLI:"),
			subtleStyle.Render("  dfmc drive \"<task>\""),
		)
	} else {
		rowBudget := max(height-6, 6)
		cursor := m.workflow.selectedIndex
		if m.workflow.selectedRunID != "" {
			for i, r := range m.workflow.runs {
				if r.ID == m.workflow.selectedRunID {
					cursor = i
					break
				}
			}
		}
		start, end := scrollWindow(cursor, len(m.workflow.runs), rowBudget)
		for i := start; i < end; i++ {
			lines = append(lines, m.renderWorkflowRunRow(i, width, pal, cursor))
		}
		lines = append(lines, "",
			subtleStyle.Render(fmt.Sprintf("%d / %d runs",
				cursor+1, len(m.workflow.runs))))
	}
	return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
}

func (m Model) workflowRunsHeader(width int) string {
	title := titleStyle.Bold(true).Render("◎ RUNS")
	count := len(m.workflow.runs)
	chip := okStyle
	if count == 0 {
		chip = subtleStyle
	}
	chipRendered := chip.Render(fmt.Sprintf(" %d ", count))
	gap := max(width-lipgloss.Width(title)-lipgloss.Width(chipRendered)-2, 1)
	return title + strings.Repeat(" ", gap) + chipRendered
}

func (m Model) renderWorkflowRunRow(i, width int, pal tabPaletteEntry, cursorIdx int) string {
	r := m.workflow.runs[i]
	selected := i == cursorIdx
	cursor := "  "
	if selected {
		cursor = lipgloss.NewStyle().Foreground(pal.Accent).Bold(true).Render("▶ ")
	}
	statusChip := renderRunStatusChip(r.Status)
	// Show the FULL run ID — users need it for /drive stop and
	// /drive resume. The 8-char truncation made cancel/remove
	// impossible because there was no way to copy the full one. The
	// row gets two lines on narrow widths instead.
	idLabel := r.ID

	chrome := lipgloss.Width(cursor) + lipgloss.Width(statusChip) + 4
	idWidth := lipgloss.Width(idLabel)
	taskWidth := width - chrome - idWidth - 2
	if taskWidth < 12 {
		// Two-line layout: ID on its own line above, task + status on
		// the next so the ID stays whole on narrow terminals.
		taskWidth = max(width-chrome, 12)
		task := truncateForLine(r.Task, taskWidth)
		if selected {
			task = lipgloss.NewStyle().Foreground(pal.Accent).Bold(true).Render(task)
		}
		return cursor + subtleStyle.Render(idLabel) + "\n  " +
			"  " + task + "  " + statusChip
	}
	task := truncateForLine(r.Task, taskWidth)
	if selected {
		task = lipgloss.NewStyle().Foreground(pal.Accent).Bold(true).Render(task)
	}
	return cursor + subtleStyle.Render(idLabel) + "  " + task + "  " + statusChip
}

// --- TREE PANE ---------------------------------------------------------------

func (m Model) renderWorkflowTreePane(width, height int, pal tabPaletteEntry) string {
	_ = pal
	header := m.workflowTreeHeader(width)
	lines := []string{
		header,
		renderDivider(width - 2),
		"",
	}
	run := m.selectedRunForWorkflow()
	if run == nil {
		lines = append(lines,
			subtleStyle.Render("Select a run on the left (j/k + enter) to inspect its TODO tree."),
			"",
			subtleStyle.Render("Press r on the runs list to open the routing editor."))
		return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
	}

	// Counts strip under the tree header.
	done, blocked, skipped, pending := run.Counts()
	running := 0
	for _, t := range run.Todos {
		if t.Status == drive.TodoRunning {
			running++
		}
	}
	stripParts := []string{
		doneStyle.Render(fmt.Sprintf("%d done", done)),
		runningStyle.Render(fmt.Sprintf("%d running", running)),
		pendingStyle.Render(fmt.Sprintf("%d pending", pending)),
		blockedStyle.Render(fmt.Sprintf("%d blocked", blocked)),
		skippedStyle.Render(fmt.Sprintf("%d skipped", skipped)),
	}
	lines = append(lines, strings.Join(stripParts, "  ·  "), "")

	rowBudget := max(height-10, 8)
	rows := m.renderWorkflowTreeRows(run, width)
	if len(rows) > rowBudget {
		rows = rows[:rowBudget]
	}
	lines = append(lines, rows...)

	if m.workflow.selectedTodoID != "" {
		lines = append(lines, "", renderDivider(width-2), "")
		lines = append(lines, m.renderWorkflowTodoDetail(run, width)...)
	}
	return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
}

func (m Model) workflowTreeHeader(width int) string {
	title := titleStyle.Bold(true).Render("⛓ TREE")
	run := m.selectedRunForWorkflow()
	if run == nil {
		return title + "  " + subtleStyle.Render("(no run selected)")
	}
	task := truncateForLine(run.Task, width-lipgloss.Width(title)-6)
	return title + "  " + subtleStyle.Render(task)
}

// --- METADATA PANE -----------------------------------------------------------

func (m Model) renderWorkflowMetaPane(width, height int, pal tabPaletteEntry) string {
	cards := m.workflowMetaCards()
	if len(cards) == 0 {
		return lipgloss.NewStyle().Width(width).Render(
			subtleStyle.Render("Select a run to see metadata."))
	}
	rendered := make([]string, 0, len(cards)*2)
	for i, c := range cards {
		if i > 0 {
			rendered = append(rendered, "")
		}
		rendered = append(rendered, renderPanelCard(c, width-2, false, pal.Accent))
	}
	body := strings.Join(rendered, "\n")
	rows := splitLines(body)
	if len(rows) > height {
		rows = rows[:height]
	}
	return strings.Join(rows, "\n")
}

func (m Model) workflowMetaCards() []panelCard {
	var cards []panelCard

	run := m.selectedRunForWorkflow()
	if run != nil {
		// RUN card.
		done, blocked, skipped, pending := run.Counts()
		running := 0
		for _, t := range run.Todos {
			if t.Status == drive.TodoRunning {
				running++
			}
		}
		chipStyle := chipStyleForRun(run.Status)
		cards = append(cards, panelCard{
			Icon:            "⚡",
			Title:           "Run",
			StatusChip:      strings.ToUpper(string(run.Status)),
			StatusChipStyle: &chipStyle,
			Rows: []panelCardRow{
				{Key: "ID", Value: truncateForLine(run.ID, 24)},
				{Key: "TODOs", Value: fmt.Sprintf("%d total", len(run.Todos))},
				{Key: "Done", Value: fmt.Sprintf("%d", done)},
				{Key: "Running", Value: fmt.Sprintf("%d", running)},
				{Key: "Pending", Value: fmt.Sprintf("%d", pending)},
				{Key: "Blocked", Value: fmt.Sprintf("%d", blocked)},
				{Key: "Skipped", Value: fmt.Sprintf("%d", skipped)},
			},
		})

		// TODO card (when a TODO is selected).
		if m.workflow.selectedTodoID != "" {
			if todo := findTodoByID(run, m.workflow.selectedTodoID); todo != nil {
				rows := []panelCardRow{
					{Key: "Status", Value: todoStatusIcon(todo.Status) + " " + string(todo.Status)},
				}
				if todo.ProviderTag != "" {
					rows = append(rows, panelCardRow{Key: "Tag", Value: todo.ProviderTag})
				}
				if todo.WorkerClass != "" {
					rows = append(rows, panelCardRow{Key: "Worker", Value: todo.WorkerClass})
				}
				if len(todo.DependsOn) > 0 {
					rows = append(rows, panelCardRow{Key: "Depends", Value: strings.Join(todo.DependsOn, ", ")})
				}
				cards = append(cards, panelCard{
					Icon:       "◇",
					Title:      "TODO",
					Rows:       rows,
					FooterHint: "esc deselect",
				})
			}
		}
	}

	// ACTIONS card always.
	cards = append(cards, panelCard{
		Icon:  "⚒",
		Title: "Actions",
		Rows: []panelCardRow{
			{Key: "j / k", Value: "move · enter select"},
			{Key: "enter", Value: "expand TODO / select run"},
			{Key: "esc", Value: "back · deselect"},
			{Key: "r", Value: "routing editor"},
			{Key: "g / G", Value: "top / bottom"},
		},
		FooterHint: "ctrl+h keys",
	})
	return cards
}

func (m Model) renderWorkflowMetaInline(width int) string {
	_ = width
	run := m.selectedRunForWorkflow()
	parts := []string{}
	if run != nil {
		done, blocked, _, pending := run.Counts()
		running := 0
		for _, t := range run.Todos {
			if t.Status == drive.TodoRunning {
				running++
			}
		}
		parts = append(parts,
			renderRunStatusChip(run.Status),
			doneStyle.Render(fmt.Sprintf("%d done", done)),
			runningStyle.Render(fmt.Sprintf("%d running", running)),
			pendingStyle.Render(fmt.Sprintf("%d pending", pending)),
			blockedStyle.Render(fmt.Sprintf("%d blocked", blocked)),
		)
	}
	parts = append(parts, subtleStyle.Render("j/k move · enter select · r routing · esc back"))
	return strings.Join(parts, "  ·  ")
}

// --- helpers -----------------------------------------------------------------

func chipStyleForRun(s drive.RunStatus) lipgloss.Style {
	switch s {
	case drive.RunRunning:
		return runningStyle
	case drive.RunDone:
		return doneStyle
	case drive.RunFailed:
		return blockedStyle
	case drive.RunStopped:
		return subtleStyle
	default:
		return subtleStyle
	}
}

func findTodoByID(run *drive.Run, id string) *drive.Todo {
	if run == nil {
		return nil
	}
	for i := range run.Todos {
		if run.Todos[i].ID == id {
			return &run.Todos[i]
		}
	}
	return nil
}
