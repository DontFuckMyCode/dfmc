package tui

// runtime_strip_work.go — row builders for the "what is the engine
// currently doing?" half of the runtime strip: workflow execution +
// todos + subagents + drive (work row), plan/store summary (task row),
// orchestration idle/active labels grouped under "map:" (orchestration
// row), and tool counters + RTK savings (tools row). Companion
// siblings:
//
//   - runtime_strip.go         renderRuntimeStrip dispatcher + Top
//                              and Now row builders
//   - runtime_strip_budget.go  Budget / Token / Activity / Action row
//                              builders

import (
	"fmt"
	"strings"
)

func runtimeStripUnifiedRuntimeParts(vm runtimeViewModel) []string {
	parts := []string{}
	if execution := strings.TrimSpace(vm.WorkflowExecution); execution != "" {
		parts = append(parts, humanizeWorkflowText(execution))
	} else if status := strings.TrimSpace(vm.WorkflowStatus); status != "" && status != "ready for input" {
		parts = append(parts, humanizeWorkflowText(status))
	}
	if vm.TodoTotal > 0 {
		label := fmt.Sprintf("todos %d/%d done, %d doing", vm.TodoDone, vm.TodoTotal, vm.TodoDoing)
		if active := strings.TrimSpace(vm.TodoActive); active != "" {
			label += " - " + active
		}
		parts = append(parts, label)
	}
	if vm.PlanSubtasks > 0 || len(vm.TaskTreeLines) > 0 {
		parts = append(parts, runtimeTaskStoreSummary(vm))
	}
	if strings.TrimSpace(vm.DriveRunID) != "" || vm.DriveTotal > 0 {
		parts = append(parts, runtimeDriveSummary(vm))
	}
	if vm.ActiveSubagents > 0 {
		parts = append(parts, runtimeSubagentSummary(vm))
	}
	if len(parts) == 0 && len(vm.WorkflowRecent) > 0 {
		parts = append(parts, "recent: "+humanizeWorkflowText(vm.WorkflowRecent[0]))
	}
	return parts
}

func runtimeTaskStoreSummary(vm runtimeViewModel) string {
	if len(vm.TaskTreeLines) > 0 {
		label := fmt.Sprintf("taskstore %d", len(vm.TaskTreeLines))
		if line := firstUsefulTaskLine(vm.TaskTreeLines); line != "" {
			label += " - " + line
		}
		return label
	}
	mode := "serial"
	if vm.PlanParallel {
		mode = "parallel"
	}
	return fmt.Sprintf("supervisor plan %d %s", vm.PlanSubtasks, mode)
}

func runtimeDriveSummary(vm runtimeViewModel) string {
	label := fmt.Sprintf("drive %d/%d", vm.DriveDone, vm.DriveTotal)
	if id := strings.TrimSpace(vm.DriveRunID); id != "" {
		label = fmt.Sprintf("drive %s %d/%d", id, vm.DriveDone, vm.DriveTotal)
	}
	if vm.DriveBlocked > 0 {
		label += fmt.Sprintf(", %d blocked", vm.DriveBlocked)
	}
	return label
}

func runtimeSubagentSummary(vm runtimeViewModel) string {
	label := fmt.Sprintf("agents %d", vm.ActiveSubagents)
	if vm.SubagentLimit > 0 {
		label = fmt.Sprintf("agents %d/%d", vm.ActiveSubagents, vm.SubagentLimit)
	}
	if summary := strings.TrimSpace(vm.SubagentSummary); summary != "" {
		label += " " + summary
	}
	return label
}

func runtimeStripWorkParts(vm runtimeViewModel) []string {
	parts := []string{}
	if execution := strings.TrimSpace(vm.WorkflowExecution); execution != "" {
		parts = append(parts, "work: "+humanizeWorkflowText(execution))
	}
	if vm.TodoTotal > 0 {
		label := fmt.Sprintf("todos %d/%d done, %d doing", vm.TodoDone, vm.TodoTotal, vm.TodoDoing)
		if active := strings.TrimSpace(vm.TodoActive); active != "" {
			label += " - " + active
		}
		parts = append(parts, label)
	}
	if vm.ActiveSubagents > 0 {
		label := fmt.Sprintf("agents %d", vm.ActiveSubagents)
		if vm.SubagentLimit > 0 {
			label = fmt.Sprintf("agents %d/%d", vm.ActiveSubagents, vm.SubagentLimit)
		}
		if summary := strings.TrimSpace(vm.SubagentSummary); summary != "" {
			label += " " + summary
		}
		parts = append(parts, label)
		if line := firstUsefulSubagentLine(vm.SubagentLines); line != "" {
			parts = append(parts, "agent: "+line)
		}
	}
	if strings.TrimSpace(vm.DriveRunID) != "" || vm.DriveTotal > 0 {
		label := fmt.Sprintf("drive %d/%d", vm.DriveDone, vm.DriveTotal)
		if vm.DriveRunID != "" {
			label = "drive " + vm.DriveRunID + " " + fmt.Sprintf("%d/%d", vm.DriveDone, vm.DriveTotal)
		}
		if vm.DriveBlocked > 0 {
			label += fmt.Sprintf(", %d blocked", vm.DriveBlocked)
		}
		parts = append(parts, label)
	}
	if len(parts) == 0 && len(vm.WorkflowRecent) > 0 {
		parts = append(parts, "recent: "+humanizeWorkflowText(vm.WorkflowRecent[0]))
	}
	return parts
}

func runtimeStripTaskParts(vm runtimeViewModel) []string {
	parts := []string{}
	if vm.PlanSubtasks > 0 {
		mode := "serial"
		if vm.PlanParallel {
			mode = "parallel"
		}
		label := fmt.Sprintf("plan %d %s", vm.PlanSubtasks, mode)
		if vm.PlanConfidence > 0 {
			label += fmt.Sprintf(" %.2f", vm.PlanConfidence)
		}
		parts = append(parts, label)
	}
	if len(vm.TaskTreeLines) > 0 {
		parts = append(parts, fmt.Sprintf("store %d", len(vm.TaskTreeLines)))
		if line := firstUsefulTaskLine(vm.TaskTreeLines); line != "" {
			parts = append(parts, "task: "+line)
		}
	} else if vm.PlanSubtasks > 0 {
		if line := firstUsefulTaskLine(vm.TaskLines); line != "" {
			parts = append(parts, line)
		}
	}
	return parts
}

func runtimeStripOrchestrationParts(vm runtimeViewModel) []string {
	parts := []string{}
	if vm.TodoTotal > 0 {
		parts = append(parts, fmt.Sprintf("todo %d/%d done", vm.TodoDone, vm.TodoTotal))
	} else {
		parts = append(parts, "todo idle (/todos)")
	}
	if vm.PlanSubtasks > 0 || len(vm.TaskTreeLines) > 0 {
		if vm.PlanSubtasks > 0 {
			mode := "serial"
			if vm.PlanParallel {
				mode = "parallel"
			}
			parts = append(parts, fmt.Sprintf("task plan %d %s", vm.PlanSubtasks, mode))
		} else {
			parts = append(parts, fmt.Sprintf("task store %d", len(vm.TaskTreeLines)))
		}
	} else {
		parts = append(parts, "task idle (/tasks)")
	}
	if strings.TrimSpace(vm.DriveRunID) != "" || vm.DriveTotal > 0 {
		parts = append(parts, fmt.Sprintf("drive %d/%d (F5)", vm.DriveDone, vm.DriveTotal))
	} else {
		parts = append(parts, "drive idle (F5)")
	}
	if vm.ActiveSubagents > 0 {
		label := fmt.Sprintf("subagents %d", vm.ActiveSubagents)
		if vm.SubagentLimit > 0 {
			label = fmt.Sprintf("subagents %d/%d", vm.ActiveSubagents, vm.SubagentLimit)
		}
		parts = append(parts, label)
	} else {
		parts = append(parts, "subagents idle (/subagents)")
	}
	return parts
}

func runtimeStripToolParts(vm runtimeViewModel) []string {
	parts := []string{}
	if vm.ToolsEnabled {
		label := "enabled"
		if vm.ToolCount > 0 {
			label += fmt.Sprintf(", %d registered", vm.ToolCount)
		}
		parts = append(parts, label)
	} else if vm.ToolCount > 0 {
		parts = append(parts, fmt.Sprintf("%d registered", vm.ToolCount))
	}
	if vm.ActiveTools > 0 {
		parts = append(parts, fmt.Sprintf("%d active", vm.ActiveTools))
	}
	if vm.CompressionSavedChars > 0 {
		label := "rtk saved " + compactMetric(vm.CompressionSavedChars)
		if vm.CompressionRawChars > 0 {
			pct := int((int64(vm.CompressionSavedChars) * 100) / int64(vm.CompressionRawChars))
			if pct > 0 {
				label += fmt.Sprintf(" (%d%%)", pct)
			}
		}
		parts = append(parts, label)
	}
	return parts
}
