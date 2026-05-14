package tui

import (
	"fmt"
	"strings"
	"time"
)

type statsWorkflowSnapshot struct {
	TaskLines      []string
	TaskTreeLines  []string
	Status         string
	Meter          string
	Execution      string
	Timeline       []string
	Recent         []string
	PlanSubtasks   int
	PlanParallel   bool
	PlanConfidence float64
}

func (m Model) statsWorkflowInfo(now time.Time, head chatHeaderInfo, todos statsTodoSummary) statsWorkflowSnapshot {
	info := statsWorkflowSnapshot{
		TaskTreeLines: m.statsTaskTreeLines(),
		Timeline:      m.recentWorkflowTimeline(6),
	}
	lastWorkflowText, lastWorkflowAge := m.latestWorkflowActivity(now)

	if m.plans.plan != nil {
		info.PlanSubtasks = len(m.plans.plan.Subtasks)
		info.PlanParallel = m.plans.plan.Parallel
		info.PlanConfidence = m.plans.plan.Confidence
		if info.PlanSubtasks > 0 {
			mode := "serial"
			if info.PlanParallel {
				mode = "parallel"
			}
			info.TaskLines = append(info.TaskLines, fmt.Sprintf("plan %d tasks%s%s%s%.2f", info.PlanSubtasks, workflowSep, mode, workflowSep, info.PlanConfidence))
			for i, sub := range m.plans.plan.Subtasks {
				if i >= 6 {
					info.TaskLines = append(info.TaskLines, fmt.Sprintf("... %d more", len(m.plans.plan.Subtasks)-i))
					break
				}
				title := strings.TrimSpace(sub.Title)
				if title == "" {
					title = strings.TrimSpace(sub.Description)
				}
				if title == "" {
					title = "(untitled)"
				}
				info.TaskLines = append(info.TaskLines, fmt.Sprintf("%d. %s", i+1, title))
			}
		}
	}

	if head.AgentActive || head.Parked || head.QueuedCount > 0 || head.PendingNotes > 0 {
		phase := strings.TrimSpace(head.AgentPhase)
		if phase == "" {
			phase = "idle"
		}
		if head.AgentMax > 0 && head.AgentStep > 0 {
			info.TaskLines = append(info.TaskLines, fmt.Sprintf("agent %s%s%d/%d", phase, workflowSep, head.AgentStep, head.AgentMax))
		} else {
			info.TaskLines = append(info.TaskLines, "agent "+phase)
		}
		if head.QueuedCount > 0 || head.PendingNotes > 0 {
			info.TaskLines = append(info.TaskLines, fmt.Sprintf("queue %d%snotes %d", head.QueuedCount, workflowSep, head.PendingNotes))
		}
		if head.Parked && !head.BannerActive {
			info.TaskLines = append(info.TaskLines, "parked"+workflowSep+"/continue")
		}
	}

	if strings.TrimSpace(head.DriveRunID) != "" {
		driveLine := fmt.Sprintf("drive %s%s%d/%d", head.DriveRunID, workflowSep, head.DriveDone, head.DriveTotal)
		if head.DriveBlocked > 0 {
			driveLine += fmt.Sprintf("%s%d blocked", workflowSep, head.DriveBlocked)
		}
		info.TaskLines = append(info.TaskLines, driveLine)
	} else if len(info.TaskLines) == 0 {
		if summary := strings.TrimSpace(m.latestWorkflowPlanSummary()); summary != "" {
			info.TaskLines = append(info.TaskLines, summary)
		}
	}

	switch {
	case head.AgentActive || head.Streaming:
		phase := strings.TrimSpace(head.AgentPhase)
		if head.Streaming && !head.AgentActive {
			waitFor := ""
			if !m.chat.streamStartedAt.IsZero() {
				waitFor = formatSessionDuration(now.Sub(m.chat.streamStartedAt))
			}
			if strings.Contains(strings.ToLower(lastWorkflowText), "throttled") {
				info.Status = fmt.Sprintf("%s waiting on provider retry", spinnerFrame(m.chat.spinnerFrame+3))
			} else {
				info.Status = fmt.Sprintf("%s waiting on model reply", spinnerFrame(m.chat.spinnerFrame+3))
			}
			if waitFor != "" {
				info.Status += workflowSep + waitFor
			}
			if lastWorkflowAge >= 3*time.Second {
				info.Status += fmt.Sprintf("%sidle %s", workflowSep, formatSessionDuration(lastWorkflowAge))
			}
			break
		}
		if phase == "" {
			phase = "working"
		}
		info.Status = fmt.Sprintf("%s live now%s%s", spinnerFrame(m.chat.spinnerFrame+3), workflowSep, phase)
		if tool := strings.TrimSpace(m.agentLoop.lastTool); tool != "" {
			info.Status += workflowSep + tool
		}
		if lastWorkflowAge >= 3*time.Second {
			info.Status += fmt.Sprintf("%sidle %s", workflowSep, formatSessionDuration(lastWorkflowAge))
		}
		if head.AgentMax > 0 {
			step := head.AgentStep
			if step <= 0 {
				step = 1
			}
			info.Meter = renderStepBar(step, head.AgentMax, 16, m.chat.spinnerFrame)
		}
	case strings.TrimSpace(head.DriveRunID) != "" && head.DriveTotal > 0:
		info.Status = fmt.Sprintf("%s drive running%s%d/%d", spinnerFrame(m.chat.spinnerFrame+3), workflowSep, head.DriveDone, head.DriveTotal)
		if head.DriveBlocked > 0 {
			info.Status += fmt.Sprintf("%s%d blocked", workflowSep, head.DriveBlocked)
		}
		info.Meter = renderStepBar(head.DriveDone, head.DriveTotal, 16, m.chat.spinnerFrame)
	case todos.Doing > 0:
		info.Status = fmt.Sprintf("%s workflow active%s%d doing", spinnerFrame(m.chat.spinnerFrame+3), workflowSep, todos.Doing)
	case head.QueuedCount > 0:
		info.Status = fmt.Sprintf("queued%s%d waiting", workflowSep, head.QueuedCount)
	case head.Parked && !head.BannerActive:
		info.Status = "parked" + workflowSep + "/continue"
	case info.PlanSubtasks > 0:
		info.Status = fmt.Sprintf("planned%s%d subtasks ready", workflowSep, info.PlanSubtasks)
	}

	lastWorkflowLower := strings.ToLower(strings.TrimSpace(lastWorkflowText))
	if !head.AgentActive && !head.Streaming && (strings.Contains(lastWorkflowLower, "agent error -") || strings.Contains(lastWorkflowLower, "tool failed -") || strings.Contains(lastWorkflowLower, "tool error -")) {
		info.Status = "stalled" + workflowSep + truncateSingleLine(lastWorkflowText, 72)
		if lastWorkflowAge > 0 {
			info.Status += fmt.Sprintf("%s%s ago", workflowSep, formatSessionDuration(lastWorkflowAge))
		}
		info.Meter = ""
	}

	info.Execution = m.statsWorkflowExecution(head, todos)
	info.Recent = m.statsWorkflowRecent()
	return info
}

func (m Model) statsWorkflowExecution(head chatHeaderInfo, todos statsTodoSummary) string {
	if strings.TrimSpace(todos.Active) != "" && todos.ActiveIndex > 0 && todos.Total > 0 {
		out := fmt.Sprintf("task %d/%d%s%s", todos.ActiveIndex, todos.Total, workflowSep, truncateSingleLine(todos.Active, 72))
		if head.ActiveSubagents > 0 {
			out += fmt.Sprintf("%s%d subagents", workflowSep, head.ActiveSubagents)
		}
		if tool := strings.TrimSpace(m.agentLoop.lastTool); tool != "" {
			out += workflowSep + tool
		}
		return out
	}
	if m.plans.plan != nil && len(m.plans.plan.Subtasks) > 0 {
		taskIndex := head.AgentStep
		if taskIndex <= 0 {
			taskIndex = 1
		}
		if taskIndex > len(m.plans.plan.Subtasks) {
			taskIndex = len(m.plans.plan.Subtasks)
		}
		title := strings.TrimSpace(m.plans.plan.Subtasks[taskIndex-1].Title)
		if title == "" {
			title = strings.TrimSpace(m.plans.plan.Subtasks[taskIndex-1].Description)
		}
		if title == "" {
			title = "(untitled)"
		}
		out := fmt.Sprintf("task %d/%d%s%s", taskIndex, len(m.plans.plan.Subtasks), workflowSep, truncateSingleLine(title, 72))
		if head.ActiveSubagents > 0 {
			out += fmt.Sprintf("%s%d subagents", workflowSep, head.ActiveSubagents)
		}
		if tool := strings.TrimSpace(m.agentLoop.lastTool); tool != "" {
			out += workflowSep + tool
		}
		return out
	}
	if head.ActiveSubagents > 0 {
		out := fmt.Sprintf("%d subagents active", head.ActiveSubagents)
		if tool := strings.TrimSpace(m.agentLoop.lastTool); tool != "" {
			out += workflowSep + tool
		}
		return out
	}
	if strings.TrimSpace(m.agentLoop.lastTool) != "" && (head.AgentActive || head.Streaming) {
		return "active tool" + workflowSep + strings.TrimSpace(m.agentLoop.lastTool)
	}
	return ""
}

func (m Model) statsWorkflowRecent() []string {
	var recent []string
	seen := map[string]struct{}{}
	collect := func(prefix string, limit int) {
		for _, line := range m.recentWorkflowActivity(prefix, limit) {
			key := strings.ToLower(strings.TrimSpace(line))
			if key == "" {
				continue
			}
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			recent = append(recent, line)
			if len(recent) >= 4 {
				return
			}
		}
	}
	collect("tool:", 2)
	collect("drive:", 2)
	collect("agent:autonomy:", 2)
	collect("agent:loop:error", 1)
	collect("provider:throttle:retry", 1)
	return recent
}
