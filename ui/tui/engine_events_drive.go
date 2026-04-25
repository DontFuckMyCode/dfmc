package tui

// Drive-event branch of the engine-event router. Extracted from
// engine_events.go so the giant switch there stays readable; every
// drive:* case funnels through handleDriveEvent which returns the
// updated Model plus the activity/notice line the parent appends.

import (
	"fmt"
	"strings"
)

func (m Model) handleDriveEvent(eventType string, payload map[string]any) (Model, string) {
	line := ""
	switch eventType {
	case "drive:run:start":
		task := payloadString(payload, "task", "")
		runID := payloadString(payload, "run_id", "")
		m.telemetry.driveRunID = shortID(runID)
		m.telemetry.driveTodoID = ""
		m.telemetry.driveDone = 0
		m.telemetry.driveTotal = 0
		m.telemetry.driveBlocked = 0
		if res, err := buildTUIDriver(m.eng, nil); err == nil {
			if runs, err := res.listRuns(); err == nil {
				m.workflow.runs = runs
			}
		}
		if resumed := payloadBool(payload, "resumed", false); resumed {
			line = fmt.Sprintf("Drive: resumed %s (task: %s)", shortID(runID), truncateForLine(task, 80))
		} else {
			line = fmt.Sprintf("Drive: started %s — %s", shortID(runID), truncateForLine(task, 80))
		}
	case "drive:plan:done":
		count := payloadInt(payload, "todo_count", 0)
		m.telemetry.driveTotal = count
		line = fmt.Sprintf("Drive: plan ready — %d TODOs queued", count)
	case "drive:plan:failed":
		errStr := payloadString(payload, "error", "")
		warning := payloadString(payload, "warning", "")
		if warning != "" {
			line = fmt.Sprintf("Drive: plan warning — %s", warning)
		} else {
			line = fmt.Sprintf("Drive: plan failed — %s", truncateForLine(errStr, 200))
		}
	case "drive:todo:start":
		id := payloadString(payload, "todo_id", "")
		title := payloadString(payload, "title", "")
		attempt := payloadInt(payload, "attempt", 1)
		workerClass := payloadString(payload, "worker_class", "")
		providerTag := payloadString(payload, "provider_tag", "")
		profileSelected := payloadString(payload, "profile_selected", "")
		skills := payloadStringSlice(payload, "skills")
		m.telemetry.driveTodoID = id
		if attempt > 1 {
			line = fmt.Sprintf("Drive: ▶ %s (attempt %d) — %s", id, attempt, truncateForLine(title, 80))
		} else {
			line = fmt.Sprintf("Drive: ▶ %s — %s", id, truncateForLine(title, 80))
		}
		if workerClass != "" {
			line += " [" + workerClass + "]"
		}
		if len(skills) > 0 {
			line += " ⚙" + strings.Join(skills, "·")
		}
		if fileScope := payloadString(payload, "file_scope", ""); fileScope != "" {
			parts := strings.Split(fileScope, ",")
			if len(parts) <= 3 {
				line += " ⊃" + strings.ReplaceAll(fileScope, ",", " ")
			} else {
				line += fmt.Sprintf(" ⊃%s +%d more", parts[0], len(parts)-1)
			}
		}
		if providerTag != "" {
			if profileSelected != "" {
				line += fmt.Sprintf(" [route:%s→%s]", providerTag, profileSelected)
			} else {
				line += " [route:" + providerTag + "]"
			}
		}
	case "drive:todo:done":
		id := payloadString(payload, "todo_id", "")
		dur := payloadInt(payload, "duration_ms", 0)
		tools := payloadInt(payload, "tool_calls", 0)
		providerLabel := subagentProviderLabel(
			payloadString(payload, "provider", ""),
			payloadString(payload, "model", ""),
		)
		attempts := payloadInt(payload, "attempts", 0)
		fallbackUsed := payloadBool(payload, "fallback", false)
		fallbackReasons := payloadStringSlice(payload, "fallback_reasons")
		skills := payloadStringSlice(payload, "skills")
		m.telemetry.driveDone++
		if m.telemetry.driveTodoID == id {
			m.telemetry.driveTodoID = ""
		}
		line = fmt.Sprintf("Drive: ✓ %s done (%dms, %d tool calls)", id, dur, tools)
		if workerClass := payloadString(payload, "worker_class", ""); workerClass != "" {
			line += " [" + workerClass + "]"
		}
		if len(skills) > 0 {
			line += " ⚙" + strings.Join(skills, "·")
		}
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
	case "drive:todo:blocked":
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
		line = fmt.Sprintf("Drive: ✗ %s blocked — %s%s", id, truncateForLine(errStr, 160), extra)
	case "drive:todo:skipped":
		id := payloadString(payload, "todo_id", "")
		reason := payloadString(payload, "reason", "")
		line = fmt.Sprintf("Drive: ↷ %s skipped — %s", id, reason)
	case "drive:todo:retry":
		id := payloadString(payload, "todo_id", "")
		attempt := payloadInt(payload, "attempt", 0)
		class := payloadString(payload, "class", "")
		var extra string
		if class != "" && class != "unknown" {
			extra = " [" + class + "]"
		}
		line = fmt.Sprintf("Drive: ↻ %s retry (attempt %d)%s", id, attempt, extra)
	case "drive:run:warning":
		errStr := payloadString(payload, "error", "")
		line = fmt.Sprintf("Drive: warning — %s", truncateForLine(errStr, 200))
	case "drive:run:done", "drive:run:stopped", "drive:run:failed":
		status := payloadString(payload, "status", "")
		done := payloadInt(payload, "done", 0)
		blocked := payloadInt(payload, "blocked", 0)
		skipped := payloadInt(payload, "skipped", 0)
		dur := payloadInt(payload, "duration_ms", 0)
		reason := payloadString(payload, "reason", "")
		m.telemetry.driveRunID = ""
		m.telemetry.driveTodoID = ""
		base := fmt.Sprintf("Drive: %s — %d done, %d blocked, %d skipped (%dms)", status, done, blocked, skipped, dur)
		if reason != "" {
			line = base + " · " + reason
		} else {
			line = base
		}
		if res, err := buildTUIDriver(m.eng, nil); err == nil {
			if runs, err := res.listRuns(); err == nil {
				m.workflow.runs = runs
			}
		}
	}
	return m, line
}
