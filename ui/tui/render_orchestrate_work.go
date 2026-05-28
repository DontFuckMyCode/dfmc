package tui

// render_orchestrate_work.go — work-tracking sections of the
// Orchestrate tab: TODOS (the todo_write ladder shared between the
// agent and the user), TASK STORE (supervisor task tree from /split,
// orchestrate, delegate_task), and DRIVE RUN (active autonomous run
// with the routed provider tag per TODO so the user can see "which
// model is doing T3 vs T4"). Sibling files: render_orchestrate.go
// (entry point + tokens / recent activity), render_orchestrate_agents.go
// (main agent + subagents).

import (
	"fmt"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/drive"
)

// orchestrateTodosSection — the active TODO ladder with done/doing/
// pending counts and the active form of the in-flight item.
func (m Model) orchestrateTodosSection(width int, selected bool) []string {
	todos := m.workflowTodos()
	total, pending, doing, done := summarizeWorkflowTodos(todos)
	header := orchestrateSectionMarker(selected) + accentStyle.Bold(true).Render("▣") + " " + sectionTitleStyle.Render("TODOS")
	if total == 0 {
		header += "  " + subtleStyle.Render("(none)")
		return []string{
			header,
			"  " + subtleStyle.Render("no shared todo list yet · agent uses todo_write to shard work"),
		}
	}
	header += "  " + subtleStyle.Render(fmt.Sprintf("(%d total · %d done · %d doing · %d pending)",
		total, done, doing, pending))
	out := []string{header}
	for _, item := range todos {
		st := strings.ToLower(strings.TrimSpace(item.Status))
		label := strings.TrimSpace(item.Content)
		if label == "" {
			label = "(untitled)"
		}
		var glyph, body string
		switch st {
		case "completed", "done":
			glyph = doneStyle.Render("✓")
			body = subtleStyle.Render(label)
		case "in_progress", "active", "doing":
			glyph = accentStyle.Render("▶")
			active := strings.TrimSpace(item.ActiveForm)
			if active == "" {
				active = label
			}
			body = active + "  " + subtleStyle.Render("← active")
		default:
			glyph = subtleStyle.Render("⏳")
			body = label
		}
		out = append(out, "  "+glyph+" "+truncateSingleLine(body, width-6))
	}
	return out
}

// orchestrateTaskStoreSection — supervisor task tree from the task
// store. Distinct from the TODOs surface (todo_write) and the DRIVE
// surface (drive runs): tasks here come from /split, orchestrate,
// or delegate_task. Renders the hierarchical tree as already
// formatted by statsPanelInfo so root → leaf indentation matches
// the stats panel.
func (m Model) orchestrateTaskStoreSection(width int, selected bool) []string {
	header := orchestrateSectionMarker(selected) + accentStyle.Bold(true).Render("▣") + " " + sectionTitleStyle.Render("TASK STORE")
	info := m.statsPanelInfo()
	lines := info.TaskTreeLines

	// Plan-only state (no store entries yet but a /split plan exists).
	if len(lines) == 0 {
		if info.PlanSubtasks > 0 {
			mode := "serial"
			if info.PlanParallel {
				mode = "parallel"
			}
			out := []string{
				header + "  " + subtleStyle.Render(fmt.Sprintf("(plan only · %d subtasks · %s)", info.PlanSubtasks, mode)),
			}
			for _, line := range info.TaskLines {
				out = append(out, "  "+truncateSingleLine(line, width-4))
			}
			return out
		}
		return []string{
			header + "  " + subtleStyle.Render("(empty)"),
			"  " + subtleStyle.Render("populated by /split, orchestrate, delegate_task · F-keys can't reach this surface yet, but it's live"),
		}
	}

	out := []string{header + "  " + subtleStyle.Render(fmt.Sprintf("(%d entries)", len(lines)))}
	cap := 12
	if len(lines) <= cap {
		for _, line := range lines {
			out = append(out, "  "+truncateSingleLine(line, width-4))
		}
		return out
	}
	for _, line := range lines[:cap] {
		out = append(out, "  "+truncateSingleLine(line, width-4))
	}
	out = append(out, "  "+subtleStyle.Render(fmt.Sprintf("... %d more in store · open the stats panel (alt+d) for the full tree", len(lines)-cap)))
	return out
}

// orchestrateDriveSection — the active drive run + its TODO ladder
// with the routed provider tag per TODO so the user can see "which
// model is doing T3 vs T4".
func (m Model) orchestrateDriveSection(width int, selected bool) []string {
	header := orchestrateSectionMarker(selected) + accentStyle.Bold(true).Render("▣") + " " + sectionTitleStyle.Render("DRIVE RUN")
	run := m.selectedRunForWorkflow()
	if run == nil || strings.TrimSpace(string(run.Status)) == "" {
		return []string{
			header + "  " + subtleStyle.Render("(idle)"),
			"  " + subtleStyle.Render("/drive <task> in chat to start an autonomous run · F5 for cockpit"),
		}
	}
	done, blocked, skipped, pending := run.Counts()
	running := 0
	for _, t := range run.Todos {
		if t.Status == drive.TodoRunning {
			running++
		}
	}
	header += "  " + subtleStyle.Render(fmt.Sprintf("(%s · %s)", truncateForLine(run.ID, 8), strings.ToLower(string(run.Status))))
	out := []string{header}
	out = append(out, "  Task:     "+truncateSingleLine(run.Task, width-13))
	out = append(out, fmt.Sprintf("  Progress: %d done · %d running · %d pending · %d blocked · %d skipped",
		done, running, pending, blocked, skipped))
	if len(run.Todos) == 0 {
		return out
	}
	out = append(out, "")
	for _, todo := range run.Todos {
		glyph := orchestrateDriveTodoGlyph(todo.Status)
		title := strings.TrimSpace(todo.Title)
		if title == "" {
			title = strings.TrimSpace(todo.ID)
		}
		tag := strings.TrimSpace(todo.ProviderTag)
		tagHint := ""
		if tag != "" {
			tagHint = "  " + subtleStyle.Render("["+tag+"]")
		}
		idHint := ""
		if id := strings.TrimSpace(todo.ID); id != "" {
			idHint = subtleStyle.Render(id) + " "
		}
		line := fmt.Sprintf("  %s %s%s%s", glyph, idHint, truncateSingleLine(title, width-30), tagHint)
		out = append(out, line)
	}
	return out
}

func orchestrateDriveTodoGlyph(status drive.TodoStatus) string {
	switch status {
	case drive.TodoDone:
		return doneStyle.Render("✓")
	case drive.TodoRunning:
		return accentStyle.Render("▶")
	case drive.TodoBlocked:
		return failStyle.Render("✗")
	case drive.TodoSkipped:
		return subtleStyle.Render("↷")
	default:
		return subtleStyle.Render("⏳")
	}
}
