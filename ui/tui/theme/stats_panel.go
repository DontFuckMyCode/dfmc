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
		b.section("PROVIDER", providerRows(info))
		b.section("CONTEXT", contextRows(info))
		b.section("TODO FLOW", todoRows(info, inner))
	case StatsPanelModeTasks:
		b.section("PROVIDER", providerRows(info))
		b.section("TOOL LOOP", loopRows(info))
		b.section("TASKS", taskRows(info))
	case StatsPanelModeSubagents:
		b.section("PROVIDER", providerRows(info))
		b.section("TOOL LOOP", loopRows(info))
		b.section("SUBAGENTS", subagentRows(info))
	case StatsPanelModeProviders:
		b.section("ACTIVE", providerRows(info))
		b.section("PROVIDERS", providerListRows(info))
		b.section("CONTEXT", contextRows(info))
		b.section("SESSION", sessionRows(info))
	default:
		b.section("PROVIDER", providerRows(info))
		b.section("CONTEXT", contextRows(info))
		b.section("TOOL LOOP", loopRows(info))
		b.section("TOOLS", toolsRows(info))
		b.section("WORKFLOW", workflowRows(info, inner))
		if rows := gitRows(info); len(rows) > 0 {
			b.section("GIT", rows)
		}
		b.section("SESSION", sessionRows(info))
	}

	footerRows := []string{"ctrl+s hide | ctrl+h keys", "alt+a/s/d/f/p switch"}
	if info.FocusLocked {
		footerRows = []string{"esc unlock | retarget", "ctrl+s hide | alt+a/s/d/f/p"}
	} else if info.Boosted {
		footerRows = []string{"alt+a/s/d/f/p again locks", "ctrl+s hide | ctrl+h keys"}
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
	right := statsPanelContextLabel(info.ContextTokens, info.MaxContext)
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

func contextRows(info StatsPanelInfo) []string {
	rows := []string{RenderContextBarFrame(info.ContextTokens, info.MaxContext, 12, info.SpinnerFrame)}
	if info.ContextFileCount > 0 || info.ContextBudgetTokens > 0 {
		files := fmt.Sprintf("files %d", info.ContextFileCount)
		if info.ContextMaxFiles > 0 {
			files += fmt.Sprintf("/%d", info.ContextMaxFiles)
		}
		if info.ContextBudgetTokens > 0 {
			files += fmt.Sprintf(" | code %s/%s tok", CompactTokens(info.ContextTokens), CompactTokens(info.ContextBudgetTokens))
		}
		rows = append(rows, InfoStyle.Render(files))
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
	rows := []string{
		fmt.Sprintf("%d total | %d done | %d doing | %d pending", info.TodoTotal, info.TodoDone, info.TodoDoing, info.TodoPending),
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
	if len(info.TodoLines) == 0 {
		rows = append(rows,
			"No shared todo list yet.",
			"Appears after todo_write or autonomy preflight.",
			"Try a multi-step ask, /split, or /todos.",
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
	if len(info.TaskTreeLines) > 0 {
		rows = append(rows, fmt.Sprintf("%d task(s) in store", len(info.TaskTreeLines)))
		rows = append(rows, info.TaskTreeLines...)
	} else if len(info.TaskLines) > 0 {
		rows = append(rows, info.TaskLines...)
	} else {
		rows = append(rows,
			"No active task graph yet.",
			"Fills from autonomy preflight, /split, or drive planning.",
			"Broad asks create task breakdowns.",
		)
	}
	for _, line := range info.WorkflowRecent {
		rows = append(rows, SubtleStyle.Render("recent: "+line))
	}
	return rows
}

func subagentRows(info StatsPanelInfo) []string {
	capacity := ""
	if info.SubagentLimit > 0 {
		capacity = fmt.Sprintf("capacity %d/%d", info.ActiveSubagents, info.SubagentLimit)
	}
	summary := strings.TrimSpace(info.SubagentSummary)
	if len(info.SubagentLines) == 0 || (len(info.SubagentLines) == 1 && strings.EqualFold(strings.TrimSpace(info.SubagentLines[0]), "idle")) {
		rows := []string{}
		if capacity != "" {
			rows = append(rows, capacity)
		}
		if summary != "" {
			rows = append(rows, AccentStyle.Bold(true).Render(summary))
		}
		return append(rows,
			"No subagent activity yet.",
			"Appears when the model delegates or fans out work.",
			"Short asks usually stay in one tool loop.",
		)
	}
	rows := make([]string, 0, len(info.SubagentLines))
	if capacity != "" {
		rows = append(rows, capacity)
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

func providerListRows(info StatsPanelInfo) []string {
	if len(info.Providers) == 0 {
		return []string{
			"No providers registered.",
			"Configure providers in .dfmc/config.yaml or dfmc providers setup.",
		}
	}
	rows := make([]string, 0, len(info.Providers)*2)
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
		if row.Status == "no-key" {
			rows = append(rows, SubtleStyle.Render("    no API key: providers.profiles."+row.Name+".api_key"))
		}
		if len(row.FallbackModels) > 0 {
			rows = append(rows, SubtleStyle.Render("    fallback: "+strings.Join(row.FallbackModels, " > ")))
		}
	}
	return rows
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
