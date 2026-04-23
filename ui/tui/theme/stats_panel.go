package theme

// stats_panel.go — Stats Panel rendering. Split out of render.go for size.
// Everything here revolves around the StatsPanelInfo view-model defined in
// types.go and the StatsPanelMode tabs at the top of the panel.

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

const (
	StatsPanelWidth                = 38
	StatsPanelBoostWidthMin        = 48
	StatsPanelBoostMinContentWidth = 96
	StatsPanelMinContentWidth      = 120
)

func RenderStatsPanel(info StatsPanelInfo, height int) string {
	return RenderStatsPanelSized(info, height, StatsPanelWidth)
}

func RenderStatsPanelSized(info StatsPanelInfo, height int, panelWidth int) string {
	if height < 6 {
		height = 6
	}
	if panelWidth < StatsPanelWidth {
		panelWidth = StatsPanelWidth
	}
	inner := panelWidth - 4
	if inner < 16 {
		inner = 16
	}
	mode := info.Mode
	if mode == "" {
		mode = StatsPanelModeOverview
	}

	lines := []string{RenderStatsPanelModeTabs(mode, inner)}
	if info.Boosted {
		label := "FOCUS MODE · expanded"
		if info.FocusLocked {
			label = "FOCUS MODE · locked"
		} else if info.BoostSeconds > 0 {
			label = fmt.Sprintf("%s · %ds", label, info.BoostSeconds)
		}
		lines = append(lines, AccentStyle.Bold(true).Render(label))
	}
	divider := DividerStyle.Render(strings.Repeat("─", inner))
	addSection := func(icon, title string, body []string) {
		if len(body) == 0 {
			return
		}
		if len(lines) > 0 {
			lines = append(lines, divider)
		}
		header := AccentStyle.Bold(true).Render(icon) + " " + SectionTitleStyle.Render(title)
		lines = append(lines, header)
		for _, b := range body {
			if b == "" {
				lines = append(lines, "")
				continue
			}
			lines = append(lines, "  "+TruncateSingleLine(b, inner))
		}
	}

	providerIcon := "◉"
	if info.Streaming {
		providerIcon = SpinnerFrame(info.SpinnerFrame)
	}
	agentIcon := "⚙"
	if info.AgentActive {
		agentIcon = SpinnerFrame(info.SpinnerFrame + 3)
	}

	providerTrim := strings.TrimSpace(info.Provider)
	modelTrim := strings.TrimSpace(info.Model)
	var providerBody []string
	switch {
	case providerTrim == "":
		providerBody = []string{
			FailStyle.Bold(true).Render("⚠ no provider"),
			SubtleStyle.Render("f5 workflow · /provider"),
		}
	case !info.Configured:
		providerBody = []string{
			WarnStyle.Bold(true).Render(providerTrim + " ⚠"),
			BoldStyle.Render(blankFallback(modelTrim, "-")),
			SubtleStyle.Render("unconfigured — add API key"),
		}
	default:
		providerBody = []string{
			AccentStyle.Bold(true).Render(providerTrim),
			BoldStyle.Render(blankFallback(modelTrim, "-")),
		}
	}

	contextBody := []string{RenderContextBarFrame(info.ContextTokens, info.MaxContext, 10, info.SpinnerFrame)}
	if info.MaxContext > 0 {
		remaining := max(info.MaxContext-info.ContextTokens, 0)
		contextBody = append(contextBody, SubtleStyle.Render(fmt.Sprintf("%s free · %s used", CompactTokens(remaining), CompactTokens(info.ContextTokens))))
	}

	agentBody := []string{RenderChatModeSegment(ChatHeaderInfo{
		Streaming:    info.Streaming,
		AgentActive:  info.AgentActive,
		AgentPhase:   info.AgentPhase,
		AgentStep:    info.AgentStep,
		AgentMax:     info.AgentMaxSteps,
		SpinnerFrame: info.SpinnerFrame,
	})}
	if info.AgentActive && info.AgentMaxSteps > 0 {
		agentBody = append(agentBody, SubtleStyle.Render(fmt.Sprintf("call budget %d/%d", info.AgentStep, info.AgentMaxSteps)))
		agentBody = append(agentBody, RenderStepBar(info.AgentStep, info.AgentMaxSteps, 14, info.SpinnerFrame))
	}
	if info.ToolRounds > 0 {
		agentBody = append(agentBody, SubtleStyle.Render(fmt.Sprintf("tool rounds: %d", info.ToolRounds)))
	}
	if tool := strings.TrimSpace(info.LastTool); tool != "" {
		icon, style := chipIconStyle(info.LastStatus)
		tail := icon + " " + tool
		if info.LastDurationMs > 0 {
			tail += fmt.Sprintf(" · %dms", info.LastDurationMs)
		}
		agentBody = append(agentBody, style.Render(tail))
	}
	if info.Parked {
		agentBody = append(agentBody,
			WarnStyle.Bold(true).Render("⏸ parked"),
			SubtleStyle.Render("/continue to resume"),
		)
	}
	if info.QueuedCount > 0 {
		agentBody = append(agentBody, AccentStyle.Bold(true).Render(fmt.Sprintf("▸ queued %d", info.QueuedCount)))
	}
	if info.PendingNotes > 0 {
		agentBody = append(agentBody, InfoStyle.Render(fmt.Sprintf("✎ btw %d", info.PendingNotes)))
	}

	toolsBody := []string{}
	if info.ToolsEnabled {
		line := OkStyle.Render("enabled")
		if info.ToolCount > 0 {
			line += SubtleStyle.Render(fmt.Sprintf("  %d registered", info.ToolCount))
		}
		toolsBody = append(toolsBody, line)
	} else {
		toolsBody = append(toolsBody, SubtleStyle.Render("off"))
	}
	if info.CompressionSavedChars > 0 {
		pct := 0
		if info.CompressionRawChars > 0 {
			pct = int((int64(info.CompressionSavedChars) * 100) / int64(info.CompressionRawChars))
		}
		label := fmt.Sprintf("rtk saved %s chars", CompactTokens(info.CompressionSavedChars))
		if pct > 0 {
			label += fmt.Sprintf(" (%d%%)", pct)
		}
		toolsBody = append(toolsBody, OkStyle.Render(label))
	}

	workflowBody := []string{}
	if status := strings.TrimSpace(info.WorkflowStatus); status != "" {
		workflowBody = append(workflowBody, AccentStyle.Bold(true).Render(status))
	}
	if meter := strings.TrimSpace(info.WorkflowMeter); meter != "" {
		workflowBody = append(workflowBody, meter)
	}
	if info.TodoTotal > 0 {
		todoLine := fmt.Sprintf("todos %d · %d done · %d doing · %d pending", info.TodoTotal, info.TodoDone, info.TodoDoing, info.TodoPending)
		workflowBody = append(workflowBody, AccentStyle.Render(todoLine))
		if active := strings.TrimSpace(info.TodoActive); active != "" {
			workflowBody = append(workflowBody, InfoStyle.Render("active: "+TruncateSingleLine(active, inner-10)))
		}
	}
	if info.ActiveSubagents > 0 {
		workflowBody = append(workflowBody, AccentStyle.Bold(true).Render(fmt.Sprintf("subagents %d active", info.ActiveSubagents)))
	}
	if strings.TrimSpace(info.DriveRunID) != "" && info.DriveTotal > 0 {
		driveLine := fmt.Sprintf("drive %d/%d", info.DriveDone, info.DriveTotal)
		if info.DriveBlocked > 0 {
			driveLine += fmt.Sprintf(" · %d blocked", info.DriveBlocked)
		}
		workflowBody = append(workflowBody, InfoStyle.Render(driveLine))
	}
	if info.PlanSubtasks > 0 {
		planMode := "serial"
		if info.PlanParallel {
			planMode = "parallel"
		}
		workflowBody = append(workflowBody, SubtleStyle.Render(fmt.Sprintf("plan %d tasks · %s · %.2f", info.PlanSubtasks, planMode, info.PlanConfidence)))
	}

	for _, line := range info.WorkflowRecent {
		workflowBody = append(workflowBody, SubtleStyle.Render("recent: "+TruncateSingleLine(line, inner-10)))
	}

	branch := strings.TrimSpace(info.Branch)
	gitBody := []string{}
	if branch != "" {
		chip := BoldStyle.Render(branch)
		if info.Dirty {
			chip += WarnStyle.Render("*")
		}
		if info.Detached {
			chip += SubtleStyle.Render(" (detached)")
		}
		gitBody = append(gitBody, chip)
		if info.Inserted > 0 || info.Deleted > 0 {
			churn := OkStyle.Render(fmt.Sprintf("+%d", info.Inserted)) +
				SubtleStyle.Render(" / ") +
				FailStyle.Render(fmt.Sprintf("-%d", info.Deleted))
			gitBody = append(gitBody, churn)
		}
	}

	sessionHead := BoldStyle.Render(formatSessionDuration(info.SessionElapsed))
	if info.MessageCount > 0 {
		sessionHead += SubtleStyle.Render(fmt.Sprintf(" · %d msgs", info.MessageCount))
	}
	sessionBody := []string{sessionHead}
	if pinned := strings.TrimSpace(info.Pinned); pinned != "" {
		sessionBody = append(sessionBody, AccentStyle.Render("◆ ")+BoldStyle.Render(FileMarker(pinned)))
	}

	switch mode {
	case StatsPanelModeTodos:
		addSection(providerIcon, "PROVIDER", providerBody)
		addSection("▦", "CONTEXT", contextBody)
		addSection(agentIcon, "TOOL LOOP", agentBody)
		body := []string{fmt.Sprintf("%d total · %d done · %d doing · %d pending", info.TodoTotal, info.TodoDone, info.TodoDoing, info.TodoPending)}
		if status := strings.TrimSpace(info.WorkflowStatus); status != "" {
			body = append(body, status)
		}
		if meter := strings.TrimSpace(info.WorkflowMeter); meter != "" {
			body = append(body, meter)
		}
		if active := strings.TrimSpace(info.TodoActive); active != "" {
			body = append(body, "active: "+active)
		}
		if len(info.TodoLines) == 0 {
			body = append(body, "No shared todo list yet.")
			body = append(body, "It appears after todo_write or when autonomy preflight seeds one for a broad task.")
			body = append(body, "Try a multi-step ask, /split, or /todos after the tool loop gets going.")
		} else {
			body = append(body, info.TodoLines...)
		}
		for _, line := range info.WorkflowRecent {
			body = append(body, "recent: "+line)
		}
		addSection("☑", "TODOS", body)
	case StatsPanelModeTasks:
		addSection(providerIcon, "PROVIDER", providerBody)
		addSection("▦", "CONTEXT", contextBody)
		addSection(agentIcon, "TOOL LOOP", agentBody)
		body := []string{}
		if status := strings.TrimSpace(info.WorkflowStatus); status != "" {
			body = append(body, status)
		}
		if meter := strings.TrimSpace(info.WorkflowMeter); meter != "" {
			body = append(body, meter)
		}
		if len(info.TaskTreeLines) > 0 {
			body = append(body, fmt.Sprintf("%d task(s) in store", len(info.TaskTreeLines)))
			body = append(body, info.TaskTreeLines...)
		} else if len(info.TaskLines) == 0 {
			body = append(body, "No active task graph yet.")
			body = append(body, "This fills from autonomy preflight, /split, or drive planning.")
			body = append(body, "Broad asks are more likely to create task breakdowns than tiny one-shot prompts.")
		} else {
			body = append(body, info.TaskLines...)
		}
		for _, line := range info.WorkflowRecent {
			body = append(body, "recent: "+line)
		}
		addSection("◈", "TASKS", body)
	case StatsPanelModeSubagents:
		addSection(providerIcon, "PROVIDER", providerBody)
		addSection("?", "CONTEXT", contextBody)
		addSection(agentIcon, "TOOL LOOP", agentBody)
		body := []string{}
		if len(info.SubagentLines) == 0 || (len(info.SubagentLines) == 1 && strings.EqualFold(strings.TrimSpace(info.SubagentLines[0]), "idle")) {
			body = append(body, "No subagent activity yet.")
			body = append(body, "Subagents appear only when the model uses delegate_task or orchestrate fan-out.")
			body = append(body, "Most short asks stay inside one tool loop and never spawn workers.")
		} else {
			body = append(body, info.SubagentLines...)
		}
		addSection("?", "SUBAGENTS", body)
	case StatsPanelModeProviders:
		addSection(providerIcon, "PROVIDER", providerBody)
		addSection("▦", "CONTEXT", contextBody)
		addSection(agentIcon, "TOOL LOOP", agentBody)
		body := []string{}
		if len(info.Providers) == 0 {
			body = append(body, "No providers registered.")
			body = append(body, "Configure providers in .dfmc/config.yaml or via dfmc providers setup.")
		} else {
			for _, row := range info.Providers {
				var prefix string
				if row.Active {
					prefix = OkStyle.Render("◉ ")
				} else if row.Primary {
					prefix = AccentStyle.Render("◎ ")
				} else {
					prefix = SubtleStyle.Render("○ ")
				}
				name := BoldStyle.Render(row.Name)
				if row.Active {
					name = OkStyle.Bold(true).Render(row.Name)
				}
				line := prefix + name
				if len(row.Models) > 0 {
					line += SubtleStyle.Render(" · " + strings.Join(row.Models, " › "))
				}
				body = append(body, line)
				if row.Status == "no-key" {
					body = append(body, SubtleStyle.Render("  ⚠ no API key — set providers.profiles."+row.Name+".api_key"))
				}
				if len(row.FallbackModels) > 0 {
					body = append(body, SubtleStyle.Render("  fallback: "+strings.Join(row.FallbackModels, " › ")))
				}
			}
		}
		addSection("◉", "PROVIDERS", body)
		addSection("?", "SESSION", sessionBody)
	default:
		addSection(providerIcon, "PROVIDER", providerBody)
		addSection("?", "CONTEXT", contextBody)
		addSection(agentIcon, "TOOL LOOP", agentBody)
		addSection("?", "TOOLS", toolsBody)
		if len(workflowBody) == 0 {
			workflowBody = append(workflowBody, "No workflow state yet.")
			workflowBody = append(workflowBody, "This fills from todo_write, autonomy plans, drive runs, and subagent fan-out.")
			workflowBody = append(workflowBody, "Use alt+s for todos, alt+d for tasks, alt+f for subagents.")
		}
		addSection("?", "WORKFLOW", workflowBody)
		if len(gitBody) > 0 {
			addSection("?", "GIT", gitBody)
		}
		addSection("?", "SESSION", sessionBody)
	}
	footerText := "  ctrl+s hide ? alt+a/s/d/f/p switch ? ctrl+h keys"
	if info.FocusLocked {
		footerText = "  esc unlock ? ctrl+s hide ? alt+a/s/d/f/p retarget ? ctrl+h keys"
	} else if info.Boosted {
		footerText = "  alt+a/s/d/f again locks ? ctrl+s hide ? ctrl+h keys"
	}
	footer := SubtleStyle.Render(footerText)
	lines = append(lines, divider, footer)
	if len(lines) > height {
		reserve := 2
		if height < 2 {
			reserve = 0
		}
		if reserve > 0 {
			keep := height - reserve
			if keep < 0 {
				keep = 0
			}
			head := append([]string{}, lines[:keep]...)
			lines = append(head, divider, footer)
		} else {
			lines = lines[:height]
		}
	}
	for len(lines) < height {
		lines = append(lines, "")
	}

	body := strings.Join(lines, "\n")
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPanelBorder).
		Padding(0, 1).
		Width(panelWidth).
		Height(height)
	return box.Render(body)
}

func RenderStatsPanelModeTabs(mode StatsPanelMode, width int) string {
	items := []struct {
		key   string
		label string
		mode  StatsPanelMode
	}{
		{key: "A", label: "overview", mode: StatsPanelModeOverview},
		{key: "S", label: "todos", mode: StatsPanelModeTodos},
		{key: "D", label: "tasks", mode: StatsPanelModeTasks},
		{key: "F", label: "subagents", mode: StatsPanelModeSubagents},
		{key: "P", label: "providers", mode: StatsPanelModeProviders},
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		label := item.key + " " + item.label
		if mode == item.mode {
			parts = append(parts, TitleStyle.Render(" "+strings.ToUpper(label)+" "))
			continue
		}
		parts = append(parts, SubtleStyle.Render(label))
	}
	return TruncateSingleLine(strings.Join(parts, "  "), width)
}

func formatSessionDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	s := int(d.Seconds())
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	m := s / 60
	s = s % 60
	if m < 60 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	h := m / 60
	m = m % 60
	return fmt.Sprintf("%dh %dm", h, m)
}
