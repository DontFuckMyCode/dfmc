package tui

// Drive-event branch of the engine-event router. Extracted from
// engine_events.go so the giant switch there stays readable; every
// drive:* case funnels through handleDriveEvent which returns the
// updated Model plus the activity/notice line the parent appends.

func (m Model) handleDriveEvent(eventType string, payload map[string]any) (Model, string) {
	switch eventType {
	case "drive:run:start":
		return m.handleDriveRunStart(payload)
	case "drive:plan:done":
		return m.handleDrivePlanDone(payload)
	case "drive:plan:failed":
		return m, drivePlanFailedLine(payload)
	case "drive:todo:start":
		return m.handleDriveTodoStart(payload)
	case "drive:todo:done":
		return m.handleDriveTodoDone(payload)
	case "drive:todo:blocked":
		return m.handleDriveTodoBlocked(payload)
	case "drive:todo:skipped":
		return m, driveTodoSkippedLine(payload)
	case "drive:todo:retry":
		return m, driveTodoRetryLine(payload)
	case "drive:run:warning":
		return m, driveRunWarningLine(payload)
	case "drive:run:done", "drive:run:stopped", "drive:run:failed":
		return m.handleDriveRunTerminal(payload)
	default:
		return m, ""
	}
}
