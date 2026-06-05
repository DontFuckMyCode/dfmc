// render_workflow_v2.go — F4 Workflow panel, rebuilt with the same
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

// renderWorkflowViewV2 is the rebuilt F4 Workflow panel. The original
// renderWorkflowView in render_workflow.go now delegates here.
func (m Model) renderWorkflowViewV2(width int) string {
	return m.renderWorkflowViewV2Sized(width, 24)
}

func (m Model) renderWorkflowViewV2Sized(width, height int) string {
	width = max(width, 50)
	height = max(height, 8)

	pal := paletteForTab("Workflow", false)

	// Routing editor overlay still wins — it's modal.
	if m.workflow.showRoutingEditor {
		return m.renderRoutingEditor(width)
	}

	threePane := width >= 120
	twoPane := !threePane && width >= 80

	listW, treeW, metaW := workflowPanelWidths(width, threePane, twoPane)

	banner := m.workflowTopBanner(width)
	// Hold each pane to its column width so the horizontal split stays exact
	// — a run row or header running wider than its budget would shove the
	// neighbouring pane right and clip it at the outer frame.
	listBlock := clipBlock(m.renderWorkflowRunsPane(listW, height, pal), listW, 0)
	treeBlock := clipBlock(m.renderWorkflowTreePane(treeW, height, pal), treeW, 0)
	var out string
	if threePane {
		metaBlock := clipBlock(m.renderWorkflowMetaPane(metaW, height, pal), metaW, 0)
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
	title := titleStyle.Bold(true).Render(" ◎ WORKFLOW")
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
		hint := subtleStyle.Render("no drive runs yet")
		gap := max(width-lipgloss.Width(title)-lipgloss.Width(hint)-4, 1)
		return title + strings.Repeat(" ", gap) + hint + "\n" + subtleStyle.Render(strings.Repeat("─", width-2))
	}

	gap := max(width-lipgloss.Width(title)-lipgloss.Width(chipStrip)-4, 1)
	return title + strings.Repeat(" ", gap) + chipStrip + "\n" + subtleStyle.Render(strings.Repeat("─", width-2))
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
		subtleStyle.Render(strings.Repeat("─", width-2)),
		"",
	}
	if len(m.workflow.runs) == 0 {
		lines = append(lines,
			"  "+subtleStyle.Render("No drive runs yet."),
			"  "+subtleStyle.Render("Start one from chat with /drive <task>."),
			"  "+subtleStyle.Render("CLI: dfmc drive <task>."))
	} else {
		rowBudget := max(height-6, 1)
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
		cursor = accentStyle.Bold(true).Render("· ")
	}
	statusChip := renderRunStatusChip(r.Status)
	idLabel := r.ID

	chrome := lipgloss.Width(cursor) + lipgloss.Width(statusChip) + 3
	idWidth := lipgloss.Width(idLabel)
	taskWidth := width - chrome - idWidth - 2

	var row string
	if taskWidth < 12 {
		taskWidth = max(width-chrome, 12)
		task := truncateForLine(r.Task, taskWidth)
		row = cursor + subtleStyle.Render(idLabel) + "\n  " + "  " + task + " " + statusChip
	} else {
		task := truncateForLine(r.Task, taskWidth)
		row = cursor + subtleStyle.Render(idLabel) + "  " + task + " " + statusChip
	}

	if selected {
		row = lipgloss.NewStyle().
			Background(colorTabActiveBg).
			Foreground(colorTitleFg).
			Bold(true).
			Width(width).
			Render(row)
	}
	return row
}

func (m Model) renderWorkflowTreePane(width, height int, pal tabPaletteEntry) string {
	_ = pal
	header := m.workflowTreeHeader(width)
	lines := []string{
		header,
		subtleStyle.Render(strings.Repeat("─", width-2)),
		"",
	}
	run := m.selectedRunForWorkflow()
	if run == nil {
		lines = append(lines,
			"  "+subtleStyle.Render("Select a run to inspect its tasks"))
		return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
	}

	// Counts strip under the tree header.
	done, blocked, skipped, pending := run.Counts()
	running := 0
	verifying := 0
	for _, t := range run.Todos {
		if t.Status == drive.TodoRunning {
			running++
		}
		if t.Status == drive.TodoVerifying {
			verifying++
		}
	}
	stripParts := []string{
		doneStyle.Render(fmt.Sprintf("%d done", done)),
		runningStyle.Render(fmt.Sprintf("%d running", running)),
		infoStyle.Render(fmt.Sprintf("%d verifying", verifying)),
		pendingStyle.Render(fmt.Sprintf("%d pending", pending-running-verifying)),
		blockedStyle.Render(fmt.Sprintf("%d blocked", blocked)),
		skippedStyle.Render(fmt.Sprintf("%d skipped", skipped)),
	}
	lines = append(lines, "  "+strings.Join(stripParts, "  ·  "), "")

	rowBudget := max(height-10, 1)
	rows := m.renderWorkflowTreeRows(run, width)
	if len(rows) > 0 {
		cursor := clampScroll(m.workflow.scrollY, len(rows))
		renderModel := m
		renderModel.workflow.scrollY = cursor
		rows = renderModel.renderWorkflowTreeRows(run, width)
		start, end := scrollWindow(cursor, len(rows), rowBudget)
		rows = rows[start:end]
	}
	lines = append(lines, rows...)

	if m.workflow.selectedTodoID != "" {
		lines = append(lines, "", subtleStyle.Render(strings.Repeat("─", width-2)), "")
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
