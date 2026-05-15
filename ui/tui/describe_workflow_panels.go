package tui

// describe_workflow_panels.go — read-only describe panels for the
// /workflow, /todos, /subagents, /queue slash commands. Each
// function builds a multi-line transcript card from Model state.
// Companion siblings:
//
//   - describe_workflow.go         shared helpers (workflowTodos,
//                                  summarizeWorkflowTodos,
//                                  formatWorkflowTodoLines,
//                                  recentWorkflowActivity / Timeline,
//                                  latestWorkflowPlanSummary)
//   - describe_workflow_stats.go   /stats card + autonomy health rows
//   - describe_workflow_summary.go per-turn buildTurnSummary

import (
	"fmt"
	"strings"
)

// describeWorkflow renders the high-level autonomous-workflow snapshot:
// todo list counts, active subagent fan-out, drive progress, and the
// latest available plan summary.
func (m Model) describeWorkflow() string {
	lines := []string{"▸ Workflow snapshot"}

	lines = append(lines, "", "What is what:")
	for _, line := range m.workflowConceptRows() {
		lines = append(lines, "  "+line)
	}

	todos := m.workflowTodos()
	total, pending, doing, done := summarizeWorkflowTodos(todos)
	switch {
	case total == 0:
		lines = append(lines, "  todos:      no shared todo list yet (this session may still be on a single-step ask)")
	default:
		lines = append(lines, fmt.Sprintf("  todos:      %d total · %d pending · %d doing · %d done", total, pending, doing, done))
		for i, line := range formatWorkflowTodoLines(todos, 5) {
			prefix := "             "
			if i == 0 {
				prefix = "  now:        "
			}
			lines = append(lines, prefix+line)
		}
	}

	if m.telemetry.activeSubagentCount > 0 {
		lines = append(lines, fmt.Sprintf("  subagents:  %d active", m.telemetry.activeSubagentCount))
	} else {
		lines = append(lines, "  subagents:  idle")
	}
	for i, line := range m.recentWorkflowActivity("agent:subagent:", 3) {
		prefix := "             "
		if i == 0 {
			prefix = "  recent:     "
		}
		lines = append(lines, prefix+line)
	}

	if runID := strings.TrimSpace(m.telemetry.driveRunID); runID != "" {
		lines = append(lines, fmt.Sprintf("  drive:      %s · %d/%d done · %d blocked", runID, m.telemetry.driveDone, m.telemetry.driveTotal, m.telemetry.driveBlocked))
	} else {
		lines = append(lines, "  drive:      idle")
	}

	if summary := strings.TrimSpace(m.latestWorkflowPlanSummary()); summary != "" {
		lines = append(lines, "  plan:       "+summary)
	} else {
		lines = append(lines, "  plan:       no recent split/autonomy plan recorded")
	}

	lines = append(lines,
		"",
		"Shortcuts:",
		"  /todos shows the shared todo list",
		"  /subagents shows recent subagent fan-out",
		"  ctrl+y jumps to Plans · ctrl+g jumps to Activity",
	)
	return strings.Join(lines, "\n")
}

func (m Model) workflowConceptRows() []string {
	return []string{
		"todo: shared checklist the agent updates while working; visible in /todos and stats alt+s.",
		"task: planned split or stored task graph; visible in /tasks, Plans, and stats alt+d.",
		"workflow: live cockpit joining todos, tasks, drive, tools, and subagents; visible in F5 and /workflow.",
		"drive: long-running autonomous driver started with /drive <task>; persists runs and TODO progress; visible in F5, /drive active, Activity.",
		"subagent: delegated worker/fan-out job from orchestrate, delegate_task, or drive; visible in /subagents, stats alt+f, Activity.",
	}
}

// describeTodos prints the current shared todo_write state directly into the
// chat transcript so the user can inspect the agent's checklist in-place.
func (m Model) describeTodos() string {
	lines := []string{"▸ Shared todo list"}
	todos := m.workflowTodos()
	total, pending, doing, done := summarizeWorkflowTodos(todos)
	if total == 0 {
		lines = append(lines,
			"  no todo list is active right now.",
			"  A todo appears when the model uses todo_write, autonomy seeds a multi-step ask, or Drive plans TODOs.",
			"  Watch it in the right stats panel (alt+s), /todos, and Activity.",
		)
		return strings.Join(lines, "\n")
	}
	lines = append(lines, fmt.Sprintf("  total:      %d · %d pending · %d doing · %d done", total, pending, doing, done))
	for i, line := range formatWorkflowTodoLines(todos, 12) {
		lines = append(lines, fmt.Sprintf("  %2d. %s", i+1, line))
	}
	if len(todos) > 12 {
		lines = append(lines, fmt.Sprintf("  … %d more item(s) not shown here", len(todos)-12))
	}
	return strings.Join(lines, "\n")
}

// describeSubagents shows current fan-out plus the most recent subagent
// events mirrored into the Activity feed.
func (m Model) describeSubagents() string {
	lines := []string{"▸ Subagent activity"}
	active := m.telemetry.activeSubagentCount
	if m.status.SubagentsActive > active {
		active = m.status.SubagentsActive
	}
	if active > 0 {
		lines = append(lines, fmt.Sprintf("  active:     %d subagent(s) currently running", active))
	} else {
		lines = append(lines, "  active:     no subagents currently running")
	}
	if m.status.SubagentsLimit > 0 {
		lines = append(lines, fmt.Sprintf("  capacity:   %d/%d", active, m.status.SubagentsLimit))
	}

	recent := m.recentWorkflowActivity("agent:subagent:", 6)
	if len(recent) == 0 {
		lines = append(lines,
			"  recent:     no subagent events recorded this session",
			"  A subagent appears when work is delegated through orchestrate, delegate_task, or a Drive run.",
			"  Watch it in stats alt+f, /subagents, and Activity.",
		)
		return strings.Join(lines, "\n")
	}
	for i, line := range recent {
		prefix := "             "
		if i == 0 {
			prefix = "  recent:     "
		}
		lines = append(lines, prefix+line)
	}
	lines = append(lines, "  jump:       ctrl+g opens Activity for the full event stream")
	return strings.Join(lines, "\n")
}

func (m Model) describePendingQueue() string {
	lines := []string{"▸ Pending chat queue"}
	if len(m.chat.pendingQueue) == 0 {
		lines = append(lines,
			"  state:      empty",
			"  note:       while a turn is streaming, normal follow-up prompts queue here",
			"  commands:   /queue clear · /queue drop N",
		)
		return strings.Join(lines, "\n")
	}
	lines = append(lines,
		fmt.Sprintf("  count:      %d queued message(s)", len(m.chat.pendingQueue)),
		"  commands:   /queue clear · /queue drop N",
	)
	for i, item := range m.chat.pendingQueue {
		lines = append(lines, fmt.Sprintf("  %2d. %s", i+1, truncateSingleLine(item, 120)))
	}
	return strings.Join(lines, "\n")
}
