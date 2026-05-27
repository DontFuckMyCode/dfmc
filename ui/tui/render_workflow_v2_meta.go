package tui

// render_workflow_v2_meta.go — metadata pane (RUN / TODO / ACTIONS
// cards) plus the inline medium-mode metadata strip and the
// chipStyleForRun + findTodoByID helpers. Sibling to render_workflow_v2.go
// which keeps the F5 layout dispatcher, banner + run counts, runs pane,
// tree pane, and the routing editor overlay hand-off.

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/dontfuckmycode/dfmc/internal/drive"
)

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
		total := len(run.Todos)
		progressRow := panelCardRow{
			Key:   "Progress",
			Value: renderRunProgressChip(done, total),
		}
		cards = append(cards, panelCard{
			Icon:            "⚡",
			Title:           "Run",
			StatusChip:      strings.ToUpper(string(run.Status)),
			StatusChipStyle: &chipStyle,
			Rows: []panelCardRow{
				{Key: "ID", Value: truncateForLine(run.ID, 24)},
				progressRow,
				{Key: "TODOs", Value: fmt.Sprintf("%d total", total)},
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
	followLabel := "follow OFF"
	if m.workflow.followLive {
		followLabel = "follow ON"
	}
	cards = append(cards, panelCard{
		Icon:  "⚒",
		Title: "Actions",
		Rows: []panelCardRow{
			{Key: "j / k", Value: "move · enter select"},
			{Key: "enter", Value: "expand TODO / select run"},
			{Key: "space", Value: "toggle live-follow · " + followLabel},
			{Key: "esc", Value: "back · deselect · release follow"},
			{Key: "r", Value: "routing editor"},
			{Key: "→", Value: "action menu (stop · resume · copy id)"},
			{Key: "g / G", Value: "top / bottom"},
		},
		FooterHint: "ctrl+h keys",
	})
	return cards
}

// renderRunProgressChip is the glanceable "X/Y · Z% [bar]" surface
// rendered in the RUN card's Progress row and inlined into the
// narrow-mode footer strip. The bar is 10 cells wide so even at 80
// columns it fits without truncation; we deliberately don't scale
// with available width because a wider gauge would crowd the chip
// with sibling card rows.
//
// Edge cases:
//
//	total == 0           → "no todos" subtle (avoids div-by-zero AND
//	                       the meaningless "0/0 · 0%" surface)
//	done == total > 0    → "X/X · 100% ✓" with a fully-filled bar so
//	                       completed runs read as Done at a glance
func renderRunProgressChip(done, total int) string {
	if total <= 0 {
		return subtleStyle.Render("no todos")
	}
	pct := done * 100 / total
	bar := renderProgressBar(done, total, 10)
	suffix := ""
	if done == total {
		suffix = " " + okStyle.Render("✓")
	}
	return fmt.Sprintf("%d/%d · %s · %s%s", done, total, bar, accentStyle.Render(fmt.Sprintf("%d%%", pct)), suffix)
}

// renderProgressBar prints a `[████░░░░░░]` style horizontal gauge
// where `cells` is the total slot count. Filled cells use okStyle
// foreground; empty cells use subtle. Cap the fill at `cells` so a
// runaway counter doesn't render past the bar's edge.
func renderProgressBar(done, total, cells int) string {
	if total <= 0 || cells <= 0 {
		return ""
	}
	filled := done * cells / total
	if filled > cells {
		filled = cells
	}
	if filled < 0 {
		filled = 0
	}
	return okStyle.Render(strings.Repeat("█", filled)) +
		subtleStyle.Render(strings.Repeat("░", cells-filled))
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
			renderRunProgressChip(done, len(run.Todos)),
			runningStyle.Render(fmt.Sprintf("%d running", running)),
			pendingStyle.Render(fmt.Sprintf("%d pending", pending)),
			blockedStyle.Render(fmt.Sprintf("%d blocked", blocked)),
		)
	}
	hint := "↑↓ move · enter select · space follow · esc back"
	if m.workflow.followLive {
		hint = "● LIVE · " + hint
	}
	parts = append(parts, subtleStyle.Render(hint))
	return strings.Join(parts, "  ·  ")
}

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
