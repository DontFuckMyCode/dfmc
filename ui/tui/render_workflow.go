// render_workflow.go — Workflow tab (Drive cockpit) rendering surface.
// Owns the run selector + TODO tree visuals, status glyphs, and the
// expanded TODO detail pane. Companion siblings:
//
//   - render_workflow_keys.go     — j/k/g/G/enter/o/r/esc + action menu
//                                   + cycleWorkflowTodoExpand
//   - render_workflow_routing.go  — routing-editor overlay (renderer +
//                                   key handler + draft accessor)
//
// Nothing here starts drive runs — it just displays state supplied by
// workflow events. Edits flow back through persistDriveRoutingProjectConfig
// which is invoked from the routing-editor sibling.

package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/drive"
)

// renderWorkflowView delegates to the rebuilt 3-pane Workflow panel
// in render_workflow_v2.go. The legacy 2-column shape lives in git
// history; the V2 renderer is the active F5 panel.
func (m Model) renderWorkflowView(width int) string {
	return m.renderWorkflowViewV2(width)
}

func (m Model) selectedRunForWorkflow() *drive.Run {
	if m.workflow.selectedRunID == "" {
		return nil
	}
	for _, r := range m.workflow.runs {
		if r.ID == m.workflow.selectedRunID {
			return r
		}
	}
	return nil
}

func (m Model) renderWorkflowTreeRows(run *drive.Run, width int) []string {
	if run == nil || len(run.Todos) == 0 {
		return []string{subtleStyle.Render("(no TODOs — run may still be planning)")}
	}

	kids := make(map[string][]*drive.Todo)
	var roots []*drive.Todo
	todoMap := make(map[string]*drive.Todo)
	for i := range run.Todos {
		t := &run.Todos[i]
		todoMap[t.ID] = t
	}
	for i := range run.Todos {
		t := &run.Todos[i]
		if t.ParentID == "" || todoMap[t.ParentID] == nil {
			roots = append(roots, t)
		} else {
			kids[t.ParentID] = append(kids[t.ParentID], t)
		}
	}

	var rows []string
	var walk func(t *drive.Todo, depth int)
	walk = func(t *drive.Todo, depth int) {
		prefix := strings.Repeat("  ", depth)
		icon := todoStatusIcon(t.Status)
		expanded := m.workflow.expandedTodo[t.ID]
		expandMark := " "
		if _, hasKids := kids[t.ID]; hasKids {
			if expanded {
				expandMark = "▼"
			} else {
				expandMark = "▶"
			}
		}
		title := truncateForLine(t.Title, width-depth*2-8)
		// Currently-running TODOs render with a loud LIVE chip and
		// accent-bold title so the eye locks on whichever node is
		// actually executing right now. The space-toggled live-follow
		// mode auto-snaps the cursor here, but even without follow on
		// the visual difference makes "what's spinning" obvious.
		isRunning := t.Status == drive.TodoRunning
		if isRunning {
			title = accentStyle.Bold(true).Render(title)
		}
		line := prefix + icon + expandMark + " " + title
		if isRunning {
			line += "  " + runningStyle.Render(" LIVE ")
		}
		tagStr := ""
		if t.ProviderTag != "" {
			tagStr += subtleStyle.Render("[" + t.ProviderTag + "]")
		}
		if t.WorkerClass != "" {
			tagStr += subtleStyle.Render("[" + t.WorkerClass + "]")
		}
		if tagStr != "" {
			line += "  " + tagStr
		}
		rows = append(rows, line)
		if expanded {
			for _, child := range kids[t.ID] {
				walk(child, depth+1)
			}
		}
	}
	for _, root := range roots {
		walk(root, 0)
	}
	return rows
}

func todoStatusIcon(s drive.TodoStatus) string {
	switch s {
	case drive.TodoPending:
		return "⏳"
	case drive.TodoRunning:
		return "🔄"
	case drive.TodoVerifying:
		return "🔍"
	case drive.TodoDone:
		return "✅"
	case drive.TodoBlocked:
		return "❌"
	case drive.TodoSkipped:
		return "⏭"
	default:
		return "○"
	}
}

func renderRunStatusChip(s drive.RunStatus) string {
	switch s {
	case drive.RunPlanning:
		return subtleStyle.Render("□ planning")
	case drive.RunRunning:
		return accentStyle.Render("▸ running")
	case drive.RunDone:
		return doneStyle.Render("✓ done")
	case drive.RunStopped:
		return subtleStyle.Render("■ stopped")
	case drive.RunFailed:
		return blockedStyle.Render("✗ failed")
	default:
		return subtleStyle.Render(string(s))
	}
}

// renderWorkflowTodoDetail shows the expanded detail of a selected TODO:
// ID, status, ProviderTag, WorkerClass, Brief, Detail, and the routed
// profile name from the drive.Config.Routing map.
func (m Model) renderWorkflowTodoDetail(run *drive.Run, width int) []string {
	if run == nil || m.workflow.selectedTodoID == "" {
		return nil
	}
	var todo *drive.Todo
	for i := range run.Todos {
		if run.Todos[i].ID == m.workflow.selectedTodoID {
			todo = &run.Todos[i]
			break
		}
	}
	if todo == nil {
		return nil
	}

	lines := []string{
		titleStyle.Render("TODO Detail"),
		"",
		fmt.Sprintf("  ID:       %s", subtleStyle.Render(todo.ID)),
		fmt.Sprintf("  Status:   %s", todoStatusIcon(todo.Status)+" "+subtleStyle.Render(string(todo.Status))),
	}
	if todo.ProviderTag != "" {
		lines = append(lines, fmt.Sprintf("  Tag:      %s", accentStyle.Render(todo.ProviderTag)))
		// Show which profile this tag routes to
		if m.eng != nil && m.eng.Config != nil {
			routing := m.workflow.routingDraft
			if profile, ok := routing[todo.ProviderTag]; ok {
				lines = append(lines, fmt.Sprintf("  Routed:   %s → %s", subtleStyle.Render(todo.ProviderTag), accentStyle.Render(profile)))
			} else {
				lines = append(lines, fmt.Sprintf("  Routed:   %s → %s", subtleStyle.Render(todo.ProviderTag), subtleStyle.Render("(default)")))
			}
		}
	}
	if todo.WorkerClass != "" {
		lines = append(lines, fmt.Sprintf("  Worker:   %s", subtleStyle.Render(todo.WorkerClass)))
	}
	if todo.Brief != "" {
		lines = append(lines, fmt.Sprintf("  Brief:    %s", truncateForPanel(todo.Brief, width)))
	}
	if todo.Detail != "" {
		lines = append(lines, "")
		lines = append(lines, subtleStyle.Render("  Detail:"))
		for _, detailLine := range strings.Split(todo.Detail, "\n") {
			lines = append(lines, "  "+subtleStyle.Render(truncateForPanel(detailLine, width-2)))
		}
	}
	if len(todo.FileScope) > 0 {
		lines = append(lines, fmt.Sprintf("  Scope:    %s", subtleStyle.Render(strings.Join(todo.FileScope, ", "))))
	}
	if len(todo.DependsOn) > 0 {
		lines = append(lines, fmt.Sprintf("  Depends:  %s", subtleStyle.Render(strings.Join(todo.DependsOn, ", "))))
	}
	if todo.Error != "" {
		lines = append(lines, "")
		lines = append(lines, failStyle.Render("  Error:   ")+failStyle.Render(truncateForPanel(todo.Error, width-8)))
	}

	// Lifecycle timing — answers "how long has this been running" for
	// active TODOs and "how long did it take" for finished ones.
	if !todo.StartedAt.IsZero() {
		switch todo.Status {
		case drive.TodoRunning:
			elapsed := time.Since(todo.StartedAt).Round(time.Second)
			lines = append(lines, "", fmt.Sprintf("  Elapsed:  %s %s",
				runningStyle.Render("◌"),
				accentStyle.Render(elapsed.String())))
		case drive.TodoDone, drive.TodoBlocked, drive.TodoSkipped:
			if !todo.EndedAt.IsZero() {
				dur := todo.EndedAt.Sub(todo.StartedAt).Round(time.Second)
				lines = append(lines, "", fmt.Sprintf("  Took:     %s", subtleStyle.Render(dur.String())))
			}
		}
	}

	// Live tool activity feed — when the TODO is running, show the
	// last few tool events so the user can see exactly what the
	// drive agent is doing without leaving the workflow panel. The
	// user explicitly asked for this: "drive agentların o an ne bok
	// yediğini her detayı ile bilmeliyim". Activity entries are
	// global, but drive sub-agents run mostly-sequentially so the
	// most recent N tool events almost always belong to the active
	// TODO.
	if todo.Status == drive.TodoRunning {
		recent := m.recentToolActivityForTodo(todo, 6)
		if len(recent) > 0 {
			lines = append(lines, "", subtleStyle.Render("  Live activity:"))
			for _, entry := range recent {
				ago := time.Since(entry.At).Round(time.Second)
				prefix := fmt.Sprintf("  · %s ago  ", ago)
				body := truncateForPanel(entry.Text, width-len(prefix)-2)
				if entry.Kind == activityKindError {
					lines = append(lines, prefix+failStyle.Render(body))
				} else {
					lines = append(lines, prefix+body)
				}
			}
		} else {
			lines = append(lines, "", subtleStyle.Render("  Live activity: (waiting for first tool call…)"))
		}
	}

	lines = append(lines, "", subtleStyle.Render("esc deselect · ctrl+g full activity feed"))
	return lines
}

// recentToolActivityForTodo returns up to `max` recent activity
// entries that look like tool work (call/result/error). When a
// TODO is Running this is the closest the workflow panel can get to
// "what is this agent doing right now" without dedicated subagent→
// TODO correlation in the engine — drive workers are mostly serial
// so the freshest tool events almost always belong to the live TODO.
func (m Model) recentToolActivityForTodo(todo *drive.Todo, max int) []activityEntry {
	if todo == nil || todo.StartedAt.IsZero() {
		return nil
	}
	out := make([]activityEntry, 0, max)
	for i := len(m.activity.entries) - 1; i >= 0 && len(out) < max; i-- {
		entry := m.activity.entries[i]
		if entry.At.Before(todo.StartedAt) {
			break
		}
		if entry.Kind == activityKindTool || entry.Kind == activityKindError {
			out = append(out, entry)
		}
	}
	// Reverse so the oldest-of-the-recent shows on top, freshest at
	// the bottom — matches the natural reading order users expect
	// from a live tail.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}
