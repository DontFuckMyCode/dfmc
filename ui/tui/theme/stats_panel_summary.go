package theme

// stats_panel_summary.go owns the compact operator-summary rows used by
// the always-visible right panel. Verbose diagnostics still live in the
// domain row builders; this file selects only the few rows that should be
// visible during normal chat.

import (
	"fmt"
	"strings"
)

func statsPanelActiveRows(info StatsPanelInfo, mode StatsPanelMode, width int) []string {
	switch mode {
	case StatsPanelModeTodos:
		return statsPanelTodoSummaryRows(info, width)
	case StatsPanelModeTasks:
		return statsPanelTaskSummaryRows(info)
	case StatsPanelModeSubagents:
		return statsPanelSubagentSummaryRows(info)
	case StatsPanelModeProviders:
		return statsPanelProviderSummaryRows(info)
	default:
		return statsPanelOverviewSummaryRows(info)
	}
}

func statsPanelOverviewSummaryRows(info StatsPanelInfo) []string {
	rows := append([]string{}, providerActiveRows(info)...)
	if status := strings.TrimSpace(info.WorkflowStatus); status != "" {
		rows = append(rows, AccentStyle.Bold(true).Render(status))
	}
	if info.TodoTotal > 0 {
		rows = append(rows, fmt.Sprintf("todos %d | %d doing | %d pending", info.TodoTotal, info.TodoDoing, info.TodoPending))
	}
	if info.AgentActive || info.Streaming || info.Parked || strings.TrimSpace(info.LastTool) != "" {
		rows = append(rows, firstNNonEmpty(loopRows(info), 2)...)
	}
	return firstNNonEmpty(rows, 5)
}

func statsPanelTodoSummaryRows(info StatsPanelInfo, width int) []string {
	rows := []string{}
	if info.TodoTotal > 0 {
		rows = append(rows, RenderStepBar(info.TodoDone, info.TodoTotal, 12, info.SpinnerFrame))
		rows = append(rows, fmt.Sprintf("%d total | %d doing | %d pending", info.TodoTotal, info.TodoDoing, info.TodoPending))
	} else {
		rows = append(rows, "0 total | no shared checklist")
	}
	if active := strings.TrimSpace(info.TodoActive); active != "" {
		rows = append(rows, InfoStyle.Render("active: "+TruncateSingleLine(active, max(width-10, 12))))
	}
	if status := strings.TrimSpace(info.WorkflowStatus); status != "" {
		rows = append(rows, AccentStyle.Bold(true).Render(status))
	}
	return firstNNonEmpty(rows, 4)
}

func statsPanelTaskSummaryRows(info StatsPanelInfo) []string {
	rows := []string{}
	if status := strings.TrimSpace(info.WorkflowStatus); status != "" {
		rows = append(rows, AccentStyle.Bold(true).Render(status))
	}
	if execution := strings.TrimSpace(info.WorkflowExecution); execution != "" {
		rows = append(rows, InfoStyle.Render("now: "+execution))
	}
	if info.PlanSubtasks > 0 {
		mode := "serial"
		if info.PlanParallel {
			mode = "parallel"
		}
		rows = append(rows, AccentStyle.Render(fmt.Sprintf("plan %d | %s", info.PlanSubtasks, mode)))
	}
	if len(info.TaskTreeLines) > 0 {
		rows = append(rows, fmt.Sprintf("tasks stored %d", len(info.TaskTreeLines)))
	}
	if strings.TrimSpace(info.DriveRunID) != "" || info.DriveTotal > 0 {
		rows = append(rows, statsPanelDriveSummary(info))
	}
	if len(rows) == 0 {
		rows = append(rows, "no active task graph")
	}
	return firstNNonEmpty(rows, 4)
}

func statsPanelSubagentSummaryRows(info StatsPanelInfo) []string {
	rows := []string{}
	if info.SubagentLimit > 0 {
		rows = append(rows, RenderStepBar(info.ActiveSubagents, info.SubagentLimit, 12, info.SpinnerFrame))
		rows = append(rows, fmt.Sprintf("capacity %d/%d", info.ActiveSubagents, info.SubagentLimit))
	} else {
		rows = append(rows, fmt.Sprintf("active %d", info.ActiveSubagents))
	}
	if summary := strings.TrimSpace(info.SubagentSummary); summary != "" {
		rows = append(rows, AccentStyle.Bold(true).Render(summary))
	} else if info.ActiveSubagents == 0 {
		rows = append(rows, "no delegated workers active")
	}
	return firstNNonEmpty(rows, 3)
}

func statsPanelProviderSummaryRows(info StatsPanelInfo) []string {
	rows := append([]string{}, providerActiveRows(info)...)
	if fallbacks := statsPanelFallbackLabels(info, 3); len(fallbacks) > 0 {
		rows = append(rows, SubtleStyle.Render("fallbacks: "+strings.Join(fallbacks, ", ")))
	} else {
		rows = append(rows, SubtleStyle.Render("fallbacks: none usable"))
	}
	return firstNNonEmpty(rows, 4)
}

func statsPanelFallbackLabels(info StatsPanelInfo, limit int) []string {
	labels := []string{}
	for _, row := range info.Providers {
		if !row.Fallback {
			continue
		}
		label := strings.TrimSpace(row.Name)
		if label == "" {
			continue
		}
		if status := strings.TrimSpace(row.Status); status != "" && status != "ready" {
			label += " (" + status + ")"
		}
		labels = append(labels, label)
		if len(labels) == limit {
			break
		}
	}
	return labels
}

func statsPanelDriveSummary(info StatsPanelInfo) string {
	label := "drive"
	if id := strings.TrimSpace(info.DriveRunID); id != "" {
		label += " " + id
	}
	if info.DriveTotal > 0 {
		label += fmt.Sprintf(" | %d/%d done", info.DriveDone, info.DriveTotal)
	}
	if info.DriveBlocked > 0 {
		label += fmt.Sprintf(" | %d blocked", info.DriveBlocked)
	}
	return InfoStyle.Render(label)
}

func statsPanelBudgetRows(info StatsPanelInfo) []string {
	payload := ContextPayloadFromStats(info)
	rows := []string{RenderContextBarFrame(payload.WindowTokens, payload.MaxContext, 12, info.SpinnerFrame)}
	// Provider/model identity already lives in the ACTIVE section above.
	// BUDGET only carries the limit source kind + window, so the model name
	// isn't echoed three times within one panel.
	// The MaxContext value is already shown by the bar (above) and the
	// `input n/max | free` row (below); the limit row carries only the
	// source attribution so the user can see where the cap came from.
	if src := trimLimitSourceIdentity(payload.LimitSource, payload.Model); src != "" {
		rows = append(rows, SubtleStyle.Render("limit "+src))
	}
	if payload.WindowTokens > 0 {
		line := "input " + CompactTokens(payload.WindowTokens)
		if payload.MaxContext > 0 {
			line += "/" + CompactTokens(payload.MaxContext)
			if payload.FreeTokens >= 0 {
				line += " | free " + CompactTokens(payload.FreeTokens)
			} else {
				line += " | over " + CompactTokens(-payload.FreeTokens)
			}
		}
		rows = append(rows, SubtleStyle.Render(line))
	}
	if payload.EvidenceBudgetTokens > 0 {
		rows = append(rows, SubtleStyle.Render(fmt.Sprintf(
			"evidence %s/%s tok",
			CompactTokens(payload.EvidenceTokens),
			CompactTokens(payload.EvidenceBudgetTokens),
		)))
	}
	if info.Streaming && (info.LiveInputTokens > 0 || info.LiveOutputTokens > 0 || info.LiveTotalTokens > 0) {
		total := info.LiveTotalTokens
		if total <= 0 {
			total = info.LiveInputTokens + info.LiveOutputTokens
		}
		rows = append(rows, InfoStyle.Render(fmt.Sprintf(
			"live input ~%s | output ~%s | total ~%s",
			CompactTokens(info.LiveInputTokens),
			CompactTokens(info.LiveOutputTokens),
			CompactTokens(total),
		)))
	}
	if reserve := statsPanelReserveSummary(payload); reserve != "" {
		rows = append(rows, SubtleStyle.Render("reserve "+reserve))
	}
	if savings := statsPanelSavingsSummary(info); savings != "" {
		rows = append(rows, OkStyle.Render(savings))
	}
	if payload.WorkspaceEvidenceOff {
		rows = append(rows, InfoStyle.Render("workspace evidence off"))
	}
	rows = append(rows, SubtleStyle.Render("/context for full budget"))
	return firstNNonEmpty(rows, 8)
}

// trimLimitSourceIdentity strips a trailing `<catalog>/<model>` (or bare
// `<model>`) token from a LimitSource string when it merely echoes the
// model already named by the ACTIVE section. Multi-segment drift forms
// like "runtime 200k; models.dev 128k" are kept verbatim — those carry
// information the identity row cannot.
func trimLimitSourceIdentity(src, model string) string {
	src = strings.TrimSpace(src)
	model = strings.TrimSpace(model)
	if src == "" || model == "" {
		return src
	}
	idx := strings.LastIndex(src, " ")
	if idx < 0 {
		return src
	}
	tail := src[idx+1:]
	if tail == model || strings.HasSuffix(tail, "/"+model) {
		return strings.TrimSpace(src[:idx])
	}
	return src
}

func statsPanelReserveSummary(payload ContextPayloadSnapshot) string {
	parts := []string{}
	if payload.ResponseReserve > 0 {
		parts = append(parts, "out "+CompactTokens(payload.ResponseReserve))
	}
	if payload.ToolReserve > 0 {
		parts = append(parts, "tools "+CompactTokens(payload.ToolReserve))
	}
	if payload.HistoryReserve > 0 {
		parts = append(parts, "hist "+CompactTokens(payload.HistoryReserve))
	}
	return strings.Join(parts, " | ")
}

func statsPanelSavingsSummary(info StatsPanelInfo) string {
	if info.CompressionSavedChars <= 0 {
		return ""
	}
	label := "rtk saved " + CompactTokens(info.CompressionSavedChars) + " chars"
	if info.CompressionRawChars > 0 {
		pct := int((int64(info.CompressionSavedChars) * 100) / int64(info.CompressionRawChars))
		if pct > 0 {
			label += fmt.Sprintf(" (%d%%)", pct)
		}
	}
	return label
}
