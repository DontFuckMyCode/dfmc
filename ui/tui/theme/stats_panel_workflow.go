package theme

// stats_panel_workflow.go — row builders for the workflow / orchestration
// half of the stats panel: orchestration map, workflow status, todos,
// tasks, subagents, drive runs, and the "recent" tail. Each renderer
// reads from StatsPanelInfo only — no engine handles, no state mutation —
// so the panel can be re-rendered on every TUI tick without lock pressure.

import (
	"fmt"
	"strings"
)

func orchestrationMapRows(info StatsPanelInfo, width int) []string {
	status := func(active bool, text string) string {
		if active {
			return AccentStyle.Bold(true).Render(text)
		}
		return SubtleStyle.Render(text)
	}
	todoState := "idle"
	if info.TodoTotal > 0 {
		todoState = fmt.Sprintf("%d total, %d doing", info.TodoTotal, info.TodoDoing)
	}
	taskState := "idle"
	switch {
	case len(info.TaskTreeLines) > 0:
		taskState = fmt.Sprintf("%d stored", len(info.TaskTreeLines))
	case info.PlanSubtasks > 0:
		mode := "serial"
		if info.PlanParallel {
			mode = "parallel"
		}
		taskState = fmt.Sprintf("%d planned, %s", info.PlanSubtasks, mode)
	}
	driveState := "idle"
	if strings.TrimSpace(info.DriveRunID) != "" || info.DriveTotal > 0 {
		driveState = fmt.Sprintf("%d/%d done", info.DriveDone, info.DriveTotal)
		if info.DriveBlocked > 0 {
			driveState += fmt.Sprintf(", %d blocked", info.DriveBlocked)
		}
	}
	agentState := "idle"
	if info.ActiveSubagents > 0 {
		agentState = fmt.Sprintf("%d active", info.ActiveSubagents)
		if info.SubagentLimit > 0 {
			agentState = fmt.Sprintf("%d/%d active", info.ActiveSubagents, info.SubagentLimit)
		}
	}
	rows := []string{
		status(info.TodoTotal > 0, "todo: "+todoState+" | shared checklist | /todos"),
		status(taskState != "idle", "task: "+taskState+" | split/graph | /tasks"),
		status(strings.TrimSpace(info.WorkflowStatus) != "" || info.TodoTotal > 0 || info.PlanSubtasks > 0 || info.ActiveSubagents > 0 || info.DriveTotal > 0,
			"workflow: live cockpit | F5 | /workflow"),
		status(driveState != "idle", "drive: "+driveState+" | persisted run | /drive"),
		status(info.ActiveSubagents > 0, "subagent: "+agentState+" | delegated worker | /subagents"),
	}
	for i, row := range rows {
		rows[i] = TruncateSingleLine(row, width)
	}
	return rows
}

func workflowRows(info StatsPanelInfo, width int) []string {
	rows := []string{}
	if status := strings.TrimSpace(info.WorkflowStatus); status != "" {
		rows = append(rows, AccentStyle.Bold(true).Render(status))
	}
	if meter := strings.TrimSpace(info.WorkflowMeter); meter != "" {
		rows = append(rows, meter)
	}
	// Phase B dedup slice 3: the runtime strip Work-row already prints
	// `todos X/Y done, Z doing - <active>` at the top of every tab, so
	// the Overview WORKFLOW section drops its redundant TODO count line
	// + active text. Users who want the full breakdown (pending count,
	// step bar) can press alt+s for the dedicated Todos mode — that's
	// where the source-of-truth deep view lives. A single subtle pointer
	// here advertises the drill-in path so signal-density doesn't suffer.
	if info.TodoTotal > 0 {
		rows = append(rows, SubtleStyle.Render("todos: alt+s for breakdown"))
	}
	if info.ActiveSubagents > 0 {
		label := fmt.Sprintf("subagents %d active", info.ActiveSubagents)
		if summary := strings.TrimSpace(info.SubagentSummary); summary != "" {
			label += " " + summary
		}
		rows = append(rows, AccentStyle.Bold(true).Render(label))
	}
	if strings.TrimSpace(info.DriveRunID) != "" && info.DriveTotal > 0 {
		drive := fmt.Sprintf("drive %d/%d", info.DriveDone, info.DriveTotal)
		if info.DriveBlocked > 0 {
			drive += fmt.Sprintf(" | %d blocked", info.DriveBlocked)
		}
		rows = append(rows, InfoStyle.Render(drive))
	}
	if info.PlanSubtasks > 0 {
		mode := "serial"
		if info.PlanParallel {
			mode = "parallel"
		}
		rows = append(rows, SubtleStyle.Render(fmt.Sprintf("plan %d tasks | %s | %.2f", info.PlanSubtasks, mode, info.PlanConfidence)))
	}
	for i, line := range info.WorkflowRecent {
		if i >= 1 {
			break
		}
		rows = append(rows, SubtleStyle.Render("recent: "+line))
	}
	if len(rows) == 0 {
		rows = append(rows,
			"No workflow state yet.",
			"Fills from todo_write, autonomy plans, drive runs, and subagents.",
			"Use alt+s todos, alt+d tasks, alt+f subagents.",
		)
	}
	return rows
}

func todoRows(info StatsPanelInfo, width int) []string {
	rows := []string{}
	if info.TodoTotal > 0 {
		rows = append(rows, RenderStepBar(info.TodoDone, info.TodoTotal, 12, info.SpinnerFrame))
		rows = append(rows, fmt.Sprintf("%d total | %d done | %d doing | %d pending", info.TodoTotal, info.TodoDone, info.TodoDoing, info.TodoPending))
	} else {
		rows = append(rows, "0 total | no shared checklist")
	}
	if status := strings.TrimSpace(info.WorkflowStatus); status != "" {
		rows = append(rows, AccentStyle.Bold(true).Render(status))
	}
	if meter := strings.TrimSpace(info.WorkflowMeter); meter != "" {
		rows = append(rows, meter)
	}
	if active := strings.TrimSpace(info.TodoActive); active != "" {
		rows = append(rows, InfoStyle.Render("active: "+TruncateSingleLine(active, width-10)))
	}
	rows = append(rows, SubtleStyle.Render("source: todo_write | autonomy | drive plan"))
	rows = append(rows, SubtleStyle.Render("watch: /todos | F5 Workflow | Activity"))
	if len(info.TodoLines) == 0 {
		rows = append(rows,
			"No shared todo list yet.",
			"Appears after todo_write, autonomy preflight, or Drive planning.",
			"Try a multi-step ask, /split <task>, or /drive <task>.",
		)
	} else {
		rows = append(rows, info.TodoLines...)
	}
	for _, line := range info.WorkflowRecent {
		rows = append(rows, SubtleStyle.Render("recent: "+line))
	}
	return rows
}

func taskRows(info StatsPanelInfo) []string {
	rows := []string{}
	if status := strings.TrimSpace(info.WorkflowStatus); status != "" {
		rows = append(rows, AccentStyle.Bold(true).Render(status))
	}
	if meter := strings.TrimSpace(info.WorkflowMeter); meter != "" {
		rows = append(rows, meter)
	}
	if info.PlanSubtasks > 0 {
		mode := "serial"
		if info.PlanParallel {
			mode = "parallel"
		}
		rows = append(rows, AccentStyle.Render(fmt.Sprintf("plan: %d subtasks | %s | %.2f confidence", info.PlanSubtasks, mode, info.PlanConfidence)))
	}
	if execution := strings.TrimSpace(info.WorkflowExecution); execution != "" {
		rows = append(rows, InfoStyle.Render("now: "+execution))
	}
	if len(info.TaskTreeLines) > 0 {
		rows = append(rows, fmt.Sprintf("store: %d task(s)", len(info.TaskTreeLines)))
		rows = append(rows, info.TaskTreeLines...)
	} else if len(info.TaskLines) > 0 {
		rows = append(rows, info.TaskLines...)
	} else {
		rows = append(rows,
			"No active task graph yet.",
			"Fills from autonomy preflight, /split, task store, or Drive planning.",
			"Broad asks create task breakdowns; /tasks shows full graph.",
		)
	}
	for _, line := range firstNNonEmpty(info.WorkflowRecent, 3) {
		rows = append(rows, SubtleStyle.Render("recent: "+line))
	}
	return rows
}

func subagentRows(info StatsPanelInfo) []string {
	rows := []string{}
	if info.SubagentLimit > 0 {
		rows = append(rows, RenderStepBar(info.ActiveSubagents, info.SubagentLimit, 12, info.SpinnerFrame))
		rows = append(rows, fmt.Sprintf("capacity %d/%d", info.ActiveSubagents, info.SubagentLimit))
	} else {
		rows = append(rows, fmt.Sprintf("active %d", info.ActiveSubagents))
	}
	summary := strings.TrimSpace(info.SubagentSummary)
	if len(info.SubagentLines) == 0 || (len(info.SubagentLines) == 1 && strings.EqualFold(strings.TrimSpace(info.SubagentLines[0]), "idle")) {
		if summary != "" {
			rows = append(rows, AccentStyle.Bold(true).Render(summary))
		}
		return append(rows,
			"No subagent activity yet.",
			"Appears from orchestrate, delegate_task, Drive, or model fan-out.",
			"Short asks usually stay in one tool loop.",
		)
	}
	if summary != "" {
		rows = append(rows, AccentStyle.Bold(true).Render(summary))
	}
	for _, line := range info.SubagentLines {
		line = strings.TrimSpace(line)
		if strings.EqualFold(line, "recent:") {
			rows = append(rows, SubtleStyle.Render(line))
			continue
		}
		line = strings.TrimPrefix(line, "Subagent ")
		line = strings.ReplaceAll(line, "started: ", "start: ")
		rows = append(rows, line)
	}
	return rows
}

func driveRows(info StatsPanelInfo) []string {
	rows := []string{}
	if strings.TrimSpace(info.DriveRunID) != "" || info.DriveTotal > 0 {
		if info.DriveTotal > 0 {
			rows = append(rows, RenderStepBar(info.DriveDone, info.DriveTotal, 12, info.SpinnerFrame))
		}
		label := "run " + blankFallback(strings.TrimSpace(info.DriveRunID), "(active)")
		if info.DriveTotal > 0 {
			label += fmt.Sprintf(" | %d/%d done", info.DriveDone, info.DriveTotal)
		}
		if info.DriveBlocked > 0 {
			label += fmt.Sprintf(" | %d blocked", info.DriveBlocked)
		}
		rows = append(rows, AccentStyle.Render(label))
		rows = append(rows, SubtleStyle.Render("watch: F5 Workflow | /drive active | Activity"))
		return rows
	}
	rows = append(rows,
		"No drive run active.",
		"Start one with /drive <task> for persisted autonomous TODO execution.",
		"Use /drive list to inspect saved runs.",
	)
	return rows
}

func recentRows(info StatsPanelInfo, limit int) []string {
	rows := append([]string{}, firstNNonEmpty(info.WorkflowRecent, limit)...)
	if len(rows) == 0 {
		rows = append(rows, "No recent workflow/subagent event.")
	}
	return rows
}
