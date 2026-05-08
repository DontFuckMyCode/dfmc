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
