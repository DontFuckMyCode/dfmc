package theme

// stats_panel.go renders the right-hand chat stats panel. The panel is kept
// dense on purpose: it should behave like an operator console, not a second
// transcript.

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

type statsPanelBuilder struct {
	width   int
	lines   []string
	divider string
}

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

	b := statsPanelBuilder{
		width:   inner,
		divider: DividerStyle.Render(strings.Repeat("-", inner)),
	}
	b.line(RenderStatsPanelModeTabs(mode, inner))
	if info.Boosted {
		focus := "FOCUS MODE - expanded"
		if info.FocusLocked {
			focus = "FOCUS MODE - locked"
		} else if info.BoostSeconds > 0 {
			focus = fmt.Sprintf("%s - %ds", focus, info.BoostSeconds)
		}
		b.line(AccentStyle.Bold(true).Render(focus))
	}
	b.line(statsPanelStateLine(info, inner))

	switch mode {
	case StatsPanelModeTodos:
		b.section("TODO STATE", todoRows(info, inner))
		b.section("NEXT", nextRows(info, mode))
		b.section("LIVE LOOP", loopRows(info))
		b.section("CONTEXT", contextRows(info))
	case StatsPanelModeTasks:
		b.section("TASK GRAPH", taskRows(info))
		b.section("DRIVE", driveRows(info))
		b.section("NEXT", nextRows(info, mode))
		b.section("ORCHESTRATION MAP", orchestrationMapRows(info, inner))
		b.section("LIVE LOOP", loopRows(info))
	case StatsPanelModeSubagents:
		b.section("SUBAGENTS", subagentRows(info))
		b.section("NEXT", nextRows(info, mode))
		b.section("LIVE LOOP", loopRows(info))
		b.section("RECENT", recentRows(info, 4))
	case StatsPanelModeProviders:
		b.section("ACTIVE", providerActiveRows(info))
		b.section("ROUTING", providerRoutingRows(info))
		b.section("NEXT", nextRows(info, mode))
		b.section("PROVIDERS", providerListRows(info))
		b.section("CONTEXT", contextRows(info))
		b.section("SESSION", sessionRows(info))
	default:
		b.section("PROVIDER", providerRows(info))
		b.section("NEXT", nextRows(info, mode))
		b.section("CONTEXT", contextRows(info))
		b.section("TOOL LOOP", loopRows(info))
		b.section("TOOLS", toolsRows(info))
		b.section("WORKFLOW", workflowRows(info, inner))
		if rows := gitRows(info); len(rows) > 0 {
			b.section("GIT", rows)
		}
		b.section("SESSION", sessionRows(info))
	}

	footerRows := statsPanelFooterRows(mode)
	if info.FocusLocked {
		footerRows = []string{"esc unlock | retarget alt+a/s/d/f/p", statsPanelModeActionHint(mode) + " | ctrl+h"}
	} else if info.Boosted {
		footerRows = []string{"alt+a/s/d/f/p again locks", "ctrl+s hide | ctrl+h | " + statsPanelModeActionHint(mode)}
	}
	b.footer(footerRows, height)

	body := strings.Join(b.lines, "\n")
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPanelBorder).
		Padding(0, 1).
		Width(panelWidth).
		Height(height)
	return box.Render(body)
}

func (b *statsPanelBuilder) line(text string) {
	b.lines = append(b.lines, TruncateSingleLine(text, b.width))
}

func (b *statsPanelBuilder) section(title string, rows []string) {
	rows = cleanPanelRows(rows)
	if len(rows) == 0 {
		return
	}
	if len(b.lines) > 0 {
		b.lines = append(b.lines, b.divider)
	}
	b.lines = append(b.lines, panelSectionTitle(title))
	for _, row := range rows {
		b.lines = append(b.lines, "  "+TruncateSingleLine(row, max(b.width-2, 8)))
	}
}

func (b *statsPanelBuilder) footer(rows []string, height int) {
	footer := make([]string, 0, len(rows))
	for _, row := range rows {
		row = strings.TrimSpace(row)
		if row != "" {
			footer = append(footer, SubtleStyle.Render(TruncateSingleLine(row, b.width)))
		}
	}
	if len(footer) == 0 {
		footer = append(footer, "")
	}
	b.lines = append(b.lines, b.divider)
	b.lines = append(b.lines, footer...)
	if len(b.lines) > height {
		keep := height - 1 - len(footer)
		if keep < 0 {
			keep = 0
		}
		head := append([]string{}, b.lines[:keep]...)
		head = append(head, b.divider)
		b.lines = append(head, footer...)
	}
	for len(b.lines) < height {
		b.lines = append(b.lines, "")
	}
}

func panelSectionTitle(title string) string {
	title = strings.ToUpper(strings.TrimSpace(title))
	if title == "" {
		title = "INFO"
	}
	return TitleStyle.Render(" " + title + " ")
}

func cleanPanelRows(rows []string) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		row = strings.TrimSpace(row)
		if row != "" {
			out = append(out, row)
		}
	}
	return out
}

func statsPanelStateLine(info StatsPanelInfo, width int) string {
	state := "ready"
	style := OkStyle
	switch {
	case info.Streaming:
		state = SpinnerFrame(info.SpinnerFrame) + " streaming"
		style = InfoStyle
	case info.AgentActive:
		state = SpinnerFrame(info.SpinnerFrame) + " working"
		style = AccentStyle
	case info.Parked:
		state = "parked"
		style = WarnStyle
	case !info.Configured && strings.TrimSpace(info.Provider) != "":
		state = "needs key"
		style = WarnStyle
	case strings.TrimSpace(info.Provider) == "":
		state = "needs provider"
		style = FailStyle
	}
	left := style.Bold(true).Render(state)
	right := statsPanelContextLabel(statsPanelContextUsed(info), info.MaxContext)
	if info.MessageCount > 0 {
		right += fmt.Sprintf(" | %d msgs", info.MessageCount)
	}
	return TruncateSingleLine(left+"  "+SubtleStyle.Render(right), width)
}

func statsPanelContextLabel(tokens, maxTokens int) string {
	if maxTokens <= 0 {
		return "ctx " + CompactTokens(tokens)
	}
	pct := 0
	if tokens > 0 {
		pct = int((int64(tokens) * 100) / int64(maxTokens))
	}
	return fmt.Sprintf("ctx %s/%s %d%%", CompactTokens(tokens), CompactTokens(maxTokens), pct)
}

func providerRows(info StatsPanelInfo) []string {
	provider := strings.TrimSpace(info.Provider)
	model := strings.TrimSpace(info.Model)
	switch {
	case provider == "":
		return []string{
			FailStyle.Bold(true).Render("no provider"),
			SubtleStyle.Render("f5 workflow | /provider"),
		}
	case !info.Configured:
		return []string{
			WarnStyle.Bold(true).Render(provider + " needs key"),
			BoldStyle.Render(blankFallback(model, "-")),
			SubtleStyle.Render("unconfigured - add API key"),
		}
	default:
		return []string{
			OkStyle.Bold(true).Render(provider),
			BoldStyle.Render(blankFallback(model, "-")),
		}
	}
}

func providerActiveRows(info StatsPanelInfo) []string {
	rows := providerRows(info)
	meta := []string{}
	if info.MaxContext > 0 {
		meta = append(meta, "window "+CompactTokens(info.MaxContext))
	}
	if info.CostPer1kTokens > 0 {
		meta = append(meta, FormatUSDCost(info.CostPer1kTokens)+"/1k tok")
	}
	if info.ContextWindowTokens > 0 {
		meta = append(meta, "used "+CompactTokens(info.ContextWindowTokens))
	}
	if len(meta) > 0 {
		rows = append(rows, SubtleStyle.Render(strings.Join(meta, " | ")))
	}
	return rows
}

func contextRows(info StatsPanelInfo) []string {
	rows := []string{RenderContextBarFrame(statsPanelContextUsed(info), info.MaxContext, 12, info.SpinnerFrame)}
	workspaceEvidenceOff := contextReasonContains(info.ContextReasons, "conversation history only")
	if workspaceEvidenceOff {
		rows = append(rows, InfoStyle.Render("conversation history only"))
		rows = append(rows, SubtleStyle.Render("workspace evidence off"))
	} else if info.ContextFileCount > 0 || info.ContextBudgetTokens > 0 {
		files := fmt.Sprintf("files %d", info.ContextFileCount)
		if info.ContextMaxFiles > 0 {
			files += fmt.Sprintf("/%d", info.ContextMaxFiles)
		}
		rows = append(rows, InfoStyle.Render(files))
		if info.ContextBudgetTokens > 0 {
			rows = append(rows, InfoStyle.Render(fmt.Sprintf(
				"evidence %s/%s tok",
				CompactTokens(info.ContextTokens),
				CompactTokens(info.ContextBudgetTokens),
			)))
		}
	} else {
		rows = append(rows, SubtleStyle.Render("no context build reported yet"))
	}
	dials := []string{}
	if task := strings.TrimSpace(info.ContextTask); task != "" {
		dials = append(dials, "task "+task)
	}
	if compression := strings.TrimSpace(info.ContextCompression); compression != "" {
		dials = append(dials, "zip "+compression)
	}
	if info.ContextMaxTokensPerFile > 0 {
		dials = append(dials, fmt.Sprintf("slice %s", CompactTokens(info.ContextMaxTokensPerFile)))
	}
	if len(dials) > 0 {
		rows = append(rows, SubtleStyle.Render(strings.Join(dials, " | ")))
	}
	if info.ContextAvailableTokens > 0 {
		rows = append(rows, SubtleStyle.Render(fmt.Sprintf("available %s tok", CompactTokens(info.ContextAvailableTokens))))
	}
	if used, remaining := statsPanelWindowUsage(info); used > 0 {
		if info.MaxContext > 0 {
			line := fmt.Sprintf("window %s/%s tok", CompactTokens(used), CompactTokens(info.MaxContext))
			if remaining >= 0 {
				line += " | left " + CompactTokens(remaining)
			} else {
				line += " | over " + CompactTokens(-remaining)
			}
			rows = append(rows, SubtleStyle.Render(line))
		} else {
			rows = append(rows, SubtleStyle.Render(fmt.Sprintf("window %s tok", CompactTokens(used))))
		}
	}
	if info.ContextSystemTokens > 0 || info.ContextHistoryTokens > 0 || info.ContextResponseTokens > 0 || info.ContextToolTokens > 0 {
		rows = append(rows, SubtleStyle.Render(fmt.Sprintf(
			"budget sys %s | hist %s | code %s",
			CompactTokens(info.ContextSystemTokens),
			CompactTokens(info.ContextHistoryTokens),
			CompactTokens(info.ContextTokens),
		)))
		rows = append(rows, SubtleStyle.Render(fmt.Sprintf(
			"reserve resp %s | tools %s",
			CompactTokens(info.ContextResponseTokens),
			CompactTokens(info.ContextToolTokens),
		)))
	}
	if info.Streaming && (info.LiveInputTokens > 0 || info.LiveOutputTokens > 0 || info.LiveTotalTokens > 0) {
		total := info.LiveTotalTokens
		if total <= 0 {
			total = info.LiveInputTokens + info.LiveOutputTokens
		}
		rows = append(rows, InfoStyle.Bold(true).Render(fmt.Sprintf(
			"live in ~%s | out ~%s",
			CompactTokens(info.LiveInputTokens),
			CompactTokens(info.LiveOutputTokens),
		)))
		rows = append(rows, InfoStyle.Render(fmt.Sprintf(
			"live total ~%s | estimating",
			CompactTokens(total),
		)))
		rows = append(rows, SubtleStyle.Render("estimate until provider done"))
	}
	if info.LastInputTokens > 0 || info.LastOutputTokens > 0 || info.LastTotalTokens > 0 {
		rows = append(rows, InfoStyle.Render(fmt.Sprintf(
			"last in %s | out %s | total %s",
			CompactTokens(info.LastInputTokens),
			CompactTokens(info.LastOutputTokens),
			CompactTokens(info.LastTotalTokens),
		)))
	}
	if info.SessionInputTokens > 0 || info.SessionOutputTokens > 0 || info.SessionTotalTokens > 0 {
		rows = append(rows, SubtleStyle.Render(fmt.Sprintf(
			"session in %s | out %s | total %s",
			CompactTokens(info.SessionInputTokens),
			CompactTokens(info.SessionOutputTokens),
			CompactTokens(info.SessionTotalTokens),
		)))
		if info.CostPer1kTokens > 0 && info.SessionTotalTokens > 0 {
			cost := (float64(info.SessionTotalTokens) / 1000) * info.CostPer1kTokens
			rows = append(rows, SubtleStyle.Render(fmt.Sprintf(
				"cost %s @ %s/1k",
				FormatUSDCost(cost),
				FormatUSDCost(info.CostPer1kTokens),
			)))
		}
	}
	if len(info.ContextTopFiles) > 0 {
		files := make([]string, 0, len(info.ContextTopFiles))
		for _, path := range info.ContextTopFiles {
			if path = strings.TrimSpace(path); path != "" {
				files = append(files, TruncateSingleLine(path, 28))
			}
		}
		if len(files) > 0 {
			rows = append(rows, AccentStyle.Render("top: "+strings.Join(files, ", ")))
		}
	}
	if len(info.ContextReasons) > 0 {
		rows = append(rows, SubtleStyle.Render("why: "+TruncateSingleLine(info.ContextReasons[0], 42)))
	}
	return rows
}

func contextReasonContains(reasons []string, needle string) bool {
	needle = strings.ToLower(strings.TrimSpace(needle))
	if needle == "" {
		return false
	}
	for _, reason := range reasons {
		if strings.Contains(strings.ToLower(reason), needle) {
			return true
		}
	}
	return false
}

func statsPanelWindowUsage(info StatsPanelInfo) (int, int) {
	used := info.ContextWindowTokens
	if used <= 0 {
		used = info.ContextSystemTokens + info.ContextHistoryTokens + info.ContextTokens + info.ContextResponseTokens + info.ContextToolTokens
	}
	if used <= 0 {
		used = info.ContextTokens
	}
	if used <= 0 {
		return 0, 0
	}
	if info.MaxContext <= 0 {
		return used, -1
	}
	return used, info.MaxContext - used
}

func statsPanelContextUsed(info StatsPanelInfo) int {
	if used, _ := statsPanelWindowUsage(info); used > 0 {
		return used
	}
	return info.ContextTokens
}

func loopRows(info StatsPanelInfo) []string {
	rows := []string{RenderChatModeSegment(ChatHeaderInfo{
		Streaming:    info.Streaming,
		AgentActive:  info.AgentActive,
		AgentPhase:   info.AgentPhase,
		AgentStep:    info.AgentStep,
		AgentMax:     info.AgentMaxSteps,
		SpinnerFrame: info.SpinnerFrame,
	})}
	if info.AgentActive && info.AgentMaxSteps > 0 {
		rows = append(rows, fmt.Sprintf("call budget %d/%d", info.AgentStep, info.AgentMaxSteps))
	}
	if info.ToolRounds > 0 {
		rows = append(rows, fmt.Sprintf("tool rounds %d", info.ToolRounds))
	}
	if tool := strings.TrimSpace(info.LastTool); tool != "" {
		icon, style := chipIconStyle(info.LastStatus)
		line := icon + " " + tool
		if info.LastDurationMs > 0 {
			line += fmt.Sprintf(" | %dms", info.LastDurationMs)
		}
		rows = append(rows, style.Render(line))
	}
	if info.Parked {
		rows = append(rows, WarnStyle.Bold(true).Render("parked"), SubtleStyle.Render("/continue to resume"))
	}
	if info.QueuedCount > 0 {
		rows = append(rows, AccentStyle.Bold(true).Render(fmt.Sprintf("queued %d", info.QueuedCount)))
	}
	if info.PendingNotes > 0 {
		rows = append(rows, InfoStyle.Render(fmt.Sprintf("btw notes %d", info.PendingNotes)))
	}
	return rows
}

func toolsRows(info StatsPanelInfo) []string {
	rows := []string{}
	if info.ToolsEnabled {
		line := OkStyle.Render("enabled")
		if info.ToolCount > 0 {
			line += SubtleStyle.Render(fmt.Sprintf(" | %d registered", info.ToolCount))
		}
		rows = append(rows, line)
	} else {
		rows = append(rows, SubtleStyle.Render("off"))
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
		rows = append(rows, OkStyle.Render(label))
	}
	return rows
}

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
	if info.TodoTotal > 0 {
		rows = append(rows, AccentStyle.Render(fmt.Sprintf("todos %d | %d done | %d doing | %d pending", info.TodoTotal, info.TodoDone, info.TodoDoing, info.TodoPending)))
		if active := strings.TrimSpace(info.TodoActive); active != "" {
			rows = append(rows, InfoStyle.Render("active: "+TruncateSingleLine(active, width-10)))
		}
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

func providerListRows(info StatsPanelInfo) []string {
	if len(info.Providers) == 0 {
		return []string{
			"No providers registered.",
			"Configure providers in .dfmc/config.yaml or dfmc providers setup.",
		}
	}
	rows := []string{SubtleStyle.Render("* active | + primary | - available")}
	for i, row := range info.Providers {
		cursor := "  "
		if i == info.ProvidersSelectedIndex {
			cursor = "> "
		}
		marker := "-"
		style := SubtleStyle
		switch {
		case row.Active:
			marker = "*"
			style = OkStyle
		case row.Primary:
			marker = "+"
			style = AccentStyle
		}
		line := cursor + marker + " " + style.Bold(row.Active).Render(row.Name)
		if len(row.Models) > 0 {
			line += SubtleStyle.Render(" | " + strings.Join(row.Models, " > "))
		}
		rows = append(rows, line)
		meta := []string{}
		if row.Protocol != "" {
			meta = append(meta, row.Protocol)
		}
		if row.MaxContext > 0 {
			meta = append(meta, "ctx "+CompactTokens(row.MaxContext))
		}
		if row.Status != "" {
			meta = append(meta, row.Status)
		}
		if len(meta) > 0 {
			rows = append(rows, SubtleStyle.Render("    "+strings.Join(meta, " | ")))
		}
		if row.Status == "no-key" {
			rows = append(rows, SubtleStyle.Render("    no API key: providers.profiles."+row.Name+".api_key"))
		}
		if len(row.FallbackModels) > 0 {
			rows = append(rows, SubtleStyle.Render("    fallback: "+strings.Join(row.FallbackModels, " > ")))
		}
	}
	return rows
}

func providerRoutingRows(info StatsPanelInfo) []string {
	rows := []string{}
	if provider := strings.TrimSpace(info.Provider); provider != "" {
		rows = append(rows, "active: "+provider+" / "+blankFallback(strings.TrimSpace(info.Model), "-"))
	} else {
		rows = append(rows, FailStyle.Render("active: none"))
	}
	primary := ""
	fallbacks := []string{}
	for _, row := range info.Providers {
		if row.Primary {
			primary = row.Name
		}
		if !row.Active && !row.Primary && row.Status != "no-key" {
			fallbacks = append(fallbacks, row.Name)
		}
	}
	if primary != "" {
		rows = append(rows, "primary: "+primary)
	}
	if len(fallbacks) > 0 {
		rows = append(rows, "ready fallback: "+strings.Join(firstNNonEmpty(fallbacks, 3), ", "))
	}
	rows = append(rows, SubtleStyle.Render("change: /provider or alt+p then enter"))
	return rows
}

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
		if len(rows) == 0 {
			return nil
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
	if info.Parked {
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

func contextUsagePct(tokens, maxTokens int) int {
	if tokens <= 0 || maxTokens <= 0 {
		return 0
	}
	return int((int64(tokens) * 100) / int64(maxTokens))
}

func statsPanelFooterRows(mode StatsPanelMode) []string {
	return []string{statsPanelModeActionHint(mode), "ctrl+s hide | ctrl+h keys"}
}

func statsPanelModeActionHint(mode StatsPanelMode) string {
	switch mode {
	case StatsPanelModeTodos:
		return "/todos | /split task | /drive task"
	case StatsPanelModeTasks:
		return "/tasks tree | ctrl+y Plans | F5 Workflow"
	case StatsPanelModeSubagents:
		return "/subagents | F7 Activity | /drive active"
	case StatsPanelModeProviders:
		return "/provider | /model | /reload"
	default:
		return "alt+a/s/d/f/p switch | F7 Activity"
	}
}

func recentRows(info StatsPanelInfo, limit int) []string {
	rows := []string{}
	for _, line := range firstNNonEmpty(info.WorkflowRecent, limit) {
		rows = append(rows, line)
	}
	if len(rows) == 0 {
		rows = append(rows, "No recent workflow/subagent event.")
	}
	return rows
}

func firstNNonEmpty(items []string, limit int) []string {
	if limit <= 0 {
		return nil
	}
	out := make([]string, 0, min(len(items), limit))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		out = append(out, item)
		if len(out) == limit {
			break
		}
	}
	return out
}

func gitRows(info StatsPanelInfo) []string {
	branch := strings.TrimSpace(info.Branch)
	if branch == "" {
		return nil
	}
	chip := BoldStyle.Render(branch)
	if info.Dirty {
		chip += WarnStyle.Render("*")
	}
	if info.Detached {
		chip += SubtleStyle.Render(" (detached)")
	}
	rows := []string{chip}
	if info.Inserted > 0 || info.Deleted > 0 {
		rows = append(rows, OkStyle.Render(fmt.Sprintf("+%d", info.Inserted))+
			SubtleStyle.Render(" / ")+
			FailStyle.Render(fmt.Sprintf("-%d", info.Deleted)))
	}
	return rows
}

func sessionRows(info StatsPanelInfo) []string {
	head := BoldStyle.Render(formatSessionDuration(info.SessionElapsed))
	if info.MessageCount > 0 {
		head += SubtleStyle.Render(fmt.Sprintf(" | %d msgs", info.MessageCount))
	}
	rows := []string{head}
	if pinned := strings.TrimSpace(info.Pinned); pinned != "" {
		rows = append(rows, AccentStyle.Render("pinned ")+BoldStyle.Render(FileMarker(pinned)))
	}
	return rows
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
	parts := make([]string, 0, len(items)+1)
	parts = append(parts, AccentStyle.Bold(true).Render("STATS"))
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
