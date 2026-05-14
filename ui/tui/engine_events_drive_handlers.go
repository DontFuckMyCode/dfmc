package tui

import (
	"fmt"
	"strings"
)

func (m Model) handleDriveRunStart(payload map[string]any) (Model, string) {
	task := payloadString(payload, "task", "")
	runID := payloadString(payload, "run_id", "")
	m.telemetry.driveRunID = shortID(runID)
	m.telemetry.driveTodoID = ""
	m.telemetry.driveDone = 0
	m.telemetry.driveTotal = 0
	m.telemetry.driveBlocked = 0
	m = m.refreshDriveWorkflowRuns()
	if payloadBool(payload, "resumed", false) {
		return m, fmt.Sprintf("Drive: resumed %s (task: %s)", shortID(runID), truncateForLine(task, 80))
	}
	return m, fmt.Sprintf("Drive: started %s \u2014 %s", shortID(runID), truncateForLine(task, 80))
}

func (m Model) handleDrivePlanDone(payload map[string]any) (Model, string) {
	count := payloadInt(payload, "todo_count", 0)
	m.telemetry.driveTotal = count
	return m, fmt.Sprintf("Drive: plan ready \u2014 %d TODOs queued", count)
}

func drivePlanFailedLine(payload map[string]any) string {
	errStr := payloadString(payload, "error", "")
	if warning := payloadString(payload, "warning", ""); warning != "" {
		return fmt.Sprintf("Drive: plan warning \u2014 %s", warning)
	}
	return fmt.Sprintf("Drive: plan failed \u2014 %s", truncateForLine(errStr, 200))
}

func (m Model) handleDriveTodoStart(payload map[string]any) (Model, string) {
	id := payloadString(payload, "todo_id", "")
	title := payloadString(payload, "title", "")
	attempt := payloadInt(payload, "attempt", 1)
	m.telemetry.driveTodoID = id
	if m.workflow.followLive {
		m = m.snapWorkflowToLiveTarget()
	}

	var line string
	if attempt > 1 {
		line = fmt.Sprintf("Drive: \u25b6 %s (attempt %d) \u2014 %s", id, attempt, truncateForLine(title, 80))
	} else {
		line = fmt.Sprintf("Drive: \u25b6 %s \u2014 %s", id, truncateForLine(title, 80))
	}
	line += driveWorkerSuffix(payload)
	line += driveSkillsSuffix(payloadStringSlice(payload, "skills"))
	line += driveFileScopeSuffix(payloadString(payload, "file_scope", ""))
	line += driveRouteSuffix(payload)
	return m, line
}

func (m Model) handleDriveTodoDone(payload map[string]any) (Model, string) {
	id := payloadString(payload, "todo_id", "")
	dur := payloadInt(payload, "duration_ms", 0)
	tools := payloadInt(payload, "tool_calls", 0)
	attempts := payloadInt(payload, "attempts", 0)
	fallbackUsed := payloadBool(payload, "fallback", false)
	fallbackReasons := payloadStringSlice(payload, "fallback_reasons")
	providerLabel := subagentProviderLabel(
		payloadString(payload, "provider", ""),
		payloadString(payload, "model", ""),
	)

	m.telemetry.driveDone++
	if m.telemetry.driveTodoID == id {
		m.telemetry.driveTodoID = ""
	}

	line := fmt.Sprintf("Drive: \u2713 %s done (%dms, %d tool calls)", id, dur, tools)
	line += driveWorkerSuffix(payload)
	line += driveSkillsSuffix(payloadStringSlice(payload, "skills"))
	if providerLabel != "" {
		line += " via " + providerLabel
	}
	if fallbackUsed && attempts > 1 {
		line += fmt.Sprintf(" after %d attempts", attempts)
		if len(fallbackReasons) > 0 {
			line += " - " + truncateForLine(fallbackReasons[len(fallbackReasons)-1], 120)
		}
	}
	if spawned := payloadInt(payload, "spawned", 0); spawned > 0 {
		line += fmt.Sprintf(" (+%d spawned)", spawned)
	}
	return m, line
}

func (m Model) handleDriveTodoBlocked(payload map[string]any) (Model, string) {
	id := payloadString(payload, "todo_id", "")
	errStr := payloadString(payload, "error", "")
	class := payloadString(payload, "class", "")
	blockedReason := payloadString(payload, "blocked_reason", "")
	m.telemetry.driveBlocked++
	if m.telemetry.driveTodoID == id {
		m.telemetry.driveTodoID = ""
	}
	extra := ""
	if blockedReason != "" && blockedReason != "none" {
		extra = " [" + blockedReason + "]"
	} else if class != "" && class != "unknown" {
		extra = " [class:" + class + "]"
	}
	return m, fmt.Sprintf("Drive: \u2717 %s blocked \u2014 %s%s", id, truncateForLine(errStr, 160), extra)
}

func driveTodoSkippedLine(payload map[string]any) string {
	id := payloadString(payload, "todo_id", "")
	reason := payloadString(payload, "reason", "")
	return fmt.Sprintf("Drive: \u21b7 %s skipped \u2014 %s", id, reason)
}

func driveTodoRetryLine(payload map[string]any) string {
	id := payloadString(payload, "todo_id", "")
	attempt := payloadInt(payload, "attempt", 0)
	class := payloadString(payload, "class", "")
	extra := ""
	if class != "" && class != "unknown" {
		extra = " [" + class + "]"
	}
	return fmt.Sprintf("Drive: \u21bb %s retry (attempt %d)%s", id, attempt, extra)
}

func driveRunWarningLine(payload map[string]any) string {
	errStr := payloadString(payload, "error", "")
	return fmt.Sprintf("Drive: warning \u2014 %s", truncateForLine(errStr, 200))
}

func (m Model) handleDriveRunTerminal(payload map[string]any) (Model, string) {
	status := payloadString(payload, "status", "")
	done := payloadInt(payload, "done", 0)
	blocked := payloadInt(payload, "blocked", 0)
	skipped := payloadInt(payload, "skipped", 0)
	dur := payloadInt(payload, "duration_ms", 0)
	reason := payloadString(payload, "reason", "")
	m.telemetry.driveRunID = ""
	m.telemetry.driveTodoID = ""
	m.telemetry.driveTotal = 0
	m.telemetry.driveDone = 0
	m.telemetry.driveBlocked = 0
	if m.eng != nil {
		m.eng.ClearContextSnapshot()
	}
	base := fmt.Sprintf("Drive: %s \u2014 %d done, %d blocked, %d skipped (%dms)", status, done, blocked, skipped, dur)
	if reason != "" {
		base += " \u00b7 " + reason
	}
	m = m.refreshDriveWorkflowRuns()
	return m, base
}

func (m Model) refreshDriveWorkflowRuns() Model {
	if res, err := buildTUIDriver(m.eng, nil); err == nil {
		if runs, err := res.listRuns(); err == nil {
			m.workflow.runs = runs
		}
	}
	return m
}

func driveWorkerSuffix(payload map[string]any) string {
	if workerClass := payloadString(payload, "worker_class", ""); workerClass != "" {
		return " [" + workerClass + "]"
	}
	return ""
}

func driveSkillsSuffix(skills []string) string {
	if len(skills) == 0 {
		return ""
	}
	return " \u2699" + strings.Join(skills, "\u00b7")
}

func driveFileScopeSuffix(fileScope string) string {
	if fileScope == "" {
		return ""
	}
	parts := strings.Split(fileScope, ",")
	if len(parts) <= 3 {
		return " \u2283" + strings.ReplaceAll(fileScope, ",", " ")
	}
	return fmt.Sprintf(" \u2283%s +%d more", parts[0], len(parts)-1)
}

func driveRouteSuffix(payload map[string]any) string {
	providerTag := payloadString(payload, "provider_tag", "")
	if providerTag == "" {
		return ""
	}
	if profileSelected := payloadString(payload, "profile_selected", ""); profileSelected != "" {
		return fmt.Sprintf(" [route:%s\u2192%s]", providerTag, profileSelected)
	}
	return " [route:" + providerTag + "]"
}
