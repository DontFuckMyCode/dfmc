package tui

// describe_workflow.go — shared helpers used by every describe_workflow_*
// sibling. Read-only accessors over the engine's tools registry and the
// activity feed plus a small render helper for todo rows.
//
// Companion siblings (the user-facing surface):
//
//   - describe_workflow_stats.go   /stats card + autonomy health rows
//   - describe_workflow_summary.go per-turn buildTurnSummary
//   - describe_workflow_panels.go  /workflow, /todos, /subagents,
//                                  /queue describe panels
//
// Health/hooks/approval describe helpers live in describe_health.go;
// transcript export + compaction stay in describe.go.

import (
	"fmt"
	"strings"
	"time"

	toolruntime "github.com/dontfuckmycode/dfmc/internal/tools"
)

func (m Model) workflowTodos() []toolruntime.TodoItem {
	if m.eng == nil || m.eng.Tools == nil {
		return nil
	}
	return m.eng.Tools.TodoSnapshot()
}

func summarizeWorkflowTodos(todos []toolruntime.TodoItem) (total, pending, doing, done int) {
	total = len(todos)
	for _, it := range todos {
		switch strings.ToLower(strings.TrimSpace(it.Status)) {
		case "completed", "done":
			done++
		case "in_progress", "active", "doing":
			doing++
		default:
			pending++
		}
	}
	return total, pending, doing, done
}

func formatWorkflowTodoLines(todos []toolruntime.TodoItem, limit int) []string {
	if len(todos) == 0 || limit <= 0 {
		return nil
	}
	if limit > len(todos) {
		limit = len(todos)
	}
	out := make([]string, 0, limit)
	for _, it := range todos[:limit] {
		label := strings.TrimSpace(it.Content)
		if label == "" {
			label = "(untitled)"
		}
		switch strings.ToLower(strings.TrimSpace(it.Status)) {
		case "completed", "done":
			label = "[done] " + label
		case "in_progress", "active", "doing":
			active := strings.TrimSpace(it.ActiveForm)
			if active == "" {
				active = label
			}
			label = "[doing] " + active
		default:
			label = "[todo] " + label
		}
		out = append(out, truncateSingleLine(label, 100))
	}
	return out
}

func (m Model) recentWorkflowActivity(prefix string, limit int) []string {
	if limit <= 0 || len(m.activity.entries) == 0 {
		return nil
	}
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	out := make([]string, 0, limit)
	for i := len(m.activity.entries) - 1; i >= 0 && len(out) < limit; i-- {
		entry := m.activity.entries[i]
		eventID := strings.ToLower(strings.TrimSpace(entry.EventID))
		if prefix != "" && !strings.HasPrefix(eventID, prefix) {
			continue
		}
		text := strings.TrimSpace(entry.Text)
		if text == "" {
			continue
		}
		out = append(out, truncateSingleLine(text, 100))
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func (m Model) recentWorkflowTimeline(limit int) []string {
	if limit <= 0 || len(m.activity.entries) == 0 {
		return nil
	}
	out := make([]string, 0, limit)
	now := time.Now()
	for i := len(m.activity.entries) - 1; i >= 0 && len(out) < limit; i-- {
		entry := m.activity.entries[i]
		eventID := strings.ToLower(strings.TrimSpace(entry.EventID))
		switch {
		case strings.HasPrefix(eventID, "tool:"),
			strings.HasPrefix(eventID, "drive:"),
			strings.HasPrefix(eventID, "agent:subagent:"),
			strings.HasPrefix(eventID, "agent:autonomy:"),
			strings.HasPrefix(eventID, "agent:loop:"),
			strings.HasPrefix(eventID, "provider:throttle:retry"):
		default:
			continue
		}
		text := strings.TrimSpace(entry.Text)
		if text == "" {
			continue
		}
		age := ""
		if !entry.At.IsZero() {
			age = formatSessionDuration(now.Sub(entry.At))
		}
		if age != "" {
			text = age + " ago · " + text
		}
		out = append(out, truncateSingleLine(text, 120))
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func (m Model) latestWorkflowPlanSummary() string {
	if m.plans.plan != nil && len(m.plans.plan.Subtasks) > 0 {
		mode := "sequential"
		if m.plans.plan.Parallel {
			mode = "parallel"
		}
		return fmt.Sprintf("%d subtasks · %s · confidence %.2f", len(m.plans.plan.Subtasks), mode, m.plans.plan.Confidence)
	}
	for i := len(m.activity.entries) - 1; i >= 0; i-- {
		entry := m.activity.entries[i]
		if strings.EqualFold(strings.TrimSpace(entry.EventID), "agent:autonomy:plan") {
			return strings.TrimSpace(entry.Text)
		}
	}
	return ""
}
