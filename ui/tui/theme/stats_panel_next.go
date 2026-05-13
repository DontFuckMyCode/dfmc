package theme

// stats_panel_next.go — "what should I do next" hint rows. nextRows is
// mode-aware (one set of suggestions per stats-panel tab); criticalNextRows
// is shared above every mode and surfaces global blockers (no provider, no
// API key, parked agent, queued prompts, hot context).

import (
	"fmt"
	"strings"
)

func nextRows(info StatsPanelInfo, mode StatsPanelMode) []string {
	rows := criticalNextRows(info)
	switch mode {
	case StatsPanelModeTodos:
		switch {
		case info.TodoDoing > 0 && strings.TrimSpace(info.TodoActive) != "":
			rows = append(rows, AccentStyle.Render("finish active todo: "+info.TodoActive))
		case info.TodoPending > 0:
			rows = append(rows, AccentStyle.Render("pick next pending todo | /todos"))
		case info.TodoTotal > 0:
			rows = append(rows, OkStyle.Render("todo list is clear enough to continue"))
		default:
			rows = append(rows, "seed work with /split <task> or /drive <task>")
		}
		rows = append(rows, SubtleStyle.Render("todo_write is the shared checklist source"))
	case StatsPanelModeTasks:
		switch {
		case len(info.TaskTreeLines) > 0:
			rows = append(rows, AccentStyle.Render("inspect graph: /tasks tree"))
		case info.PlanSubtasks > 0:
			rows = append(rows, AccentStyle.Render("run planned subtasks | ctrl+y Plans"))
		default:
			rows = append(rows, "create graph with /split <task>")
		}
		if strings.TrimSpace(info.DriveRunID) != "" || info.DriveTotal > 0 {
			rows = append(rows, SubtleStyle.Render("drive is executing this graph | /drive active"))
		} else {
			rows = append(rows, SubtleStyle.Render("drive persists multi-step execution"))
		}
	case StatsPanelModeSubagents:
		if info.ActiveSubagents > 0 {
			rows = append(rows, AccentStyle.Render("watch live agents in F7 Activity"))
		} else {
			rows = append(rows, "subagents appear when work fans out")
		}
		rows = append(rows, SubtleStyle.Render("broad tasks can delegate via Drive/orchestrate"))
	case StatsPanelModeProviders:
		if len(info.Providers) > 0 {
			rows = append(rows, AccentStyle.Render("enter switches selected provider"))
			rows = append(rows, SubtleStyle.Render("/model changes model | /reload refreshes config"))
		} else {
			rows = append(rows, "configure .dfmc/config.yaml providers")
		}
	default:
		switch {
		case info.TodoDoing > 0 && strings.TrimSpace(info.TodoActive) != "":
			rows = append(rows, AccentStyle.Render("finish active todo: "+info.TodoActive))
		case len(info.TaskTreeLines) > 0 || info.PlanSubtasks > 0:
			rows = append(rows, AccentStyle.Render("inspect graph: /tasks tree"))
		case strings.TrimSpace(info.DriveRunID) != "" || info.DriveTotal > 0:
			rows = append(rows, AccentStyle.Render("inspect drive: /drive active"))
		case info.ActiveSubagents > 0:
			rows = append(rows, AccentStyle.Render("watch live agents in F7 Activity"))
		case strings.TrimSpace(info.Provider) != "" && info.Configured:
			rows = append(rows, SubtleStyle.Render("ready for input"))
		}
	}
	return firstNNonEmpty(rows, 5)
}

func criticalNextRows(info StatsPanelInfo) []string {
	rows := []string{}
	provider := strings.TrimSpace(info.Provider)
	switch {
	case provider == "":
		rows = append(rows, FailStyle.Render("select a provider with /provider"))
	case !info.Configured:
		rows = append(rows, WarnStyle.Render("add API key for "+provider+" then /reload"))
	}
	// Same dedup as the runtime row + chat-header chip: skip the
	// "/continue resumes parked agent" critical hint while the resume
	// banner is up. Banner already advertises ctrl+x / esc with full
	// step counters, so this would be a noisy echo.
	if info.Parked && !info.BannerActive {
		rows = append(rows, WarnStyle.Render("/continue resumes parked agent"))
	}
	if info.QueuedCount > 0 {
		rows = append(rows, AccentStyle.Render(fmt.Sprintf("%d queued prompt(s) after current turn", info.QueuedCount)))
	}
	if info.Streaming {
		rows = append(rows, InfoStyle.Render("streaming now | ctrl+c cancels | tokens live"))
	}
	if pct := contextUsagePct(statsPanelContextUsed(info), info.MaxContext); pct >= 85 {
		rows = append(rows, WarnStyle.Render(fmt.Sprintf("context hot %d%% | /compact or Ctrl+I", pct)))
	}
	return rows
}
