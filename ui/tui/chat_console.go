package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

func (m Model) renderChatConsoleViewParts(width int, slimHeader bool) chatViewParts {
	if width < 40 {
		width = 40
	}
	lines := []string{}
	lines = append(lines, m.renderTimelineTop(width, slimHeader)...)
	lines = append(lines, m.renderTimelineMessages(width)...)
	lines = append(lines, m.renderTimelineComposer(width)...)
	return chatViewParts{Head: strings.Join(lines, "\n"), Tail: ""}
}

func (m Model) renderTimelineTop(width int, slimHeader bool) []string {
	info := m.statsPanelInfo()
	provider := strings.TrimSpace(info.Provider)
	model := strings.TrimSpace(info.Model)
	if provider == "" {
		provider = "provider?"
	}
	if model == "" {
		model = "model?"
	}
	status := "ready"
	if !info.Configured && !strings.EqualFold(provider, "offline") {
		status = "needs provider"
	}
	if info.Streaming || info.AgentActive {
		status = "running"
	}
	parts := []string{titleStyle.Render("DFMC CHAT"), subtleStyle.Render(status), subtleStyle.Render(provider + "/" + model)}
	if info.ContextTokens > 0 || info.MaxContext > 0 {
		parts = append(parts, subtleStyle.Render(timelineContextLabel(info.ContextTokens, info.MaxContext)))
	}
	if info.MessageCount > 0 {
		parts = append(parts, subtleStyle.Render(fmt.Sprintf("%d messages", info.MessageCount)))
	}
	if info.QueuedCount > 0 {
		parts = append(parts, warnStyle.Render(fmt.Sprintf("%d queued", info.QueuedCount)))
	}
	if info.Dirty || info.Inserted > 0 || info.Deleted > 0 {
		branch := strings.TrimSpace(info.Branch)
		if branch == "" {
			branch = "worktree"
		}
		parts = append(parts, subtleStyle.Render(fmt.Sprintf("%s +%d -%d", branch, info.Inserted, info.Deleted)))
	}
	if pinned := strings.TrimSpace(m.filesView.pinned); pinned != "" {
		parts = append(parts, subtleStyle.Render("pinned: "+fileMarker(pinned)))
	}
	lines := []string{
		truncateSingleLine(strings.Join(parts, subtleStyle.Render("  |  ")), width),
		renderDivider(min(width, 140)),
	}
	lines = append(lines, renderTimelineOps(info, width, slimHeader)...)
	return lines
}

func timelineContextLabel(tokens, maxTokens int) string {
	if maxTokens <= 0 {
		return "context " + compactTokens(tokens)
	}
	pct := 0
	if tokens > 0 {
		pct = (tokens * 100) / maxTokens
	}
	return fmt.Sprintf("context %s/%s %d%%", compactTokens(tokens), compactTokens(maxTokens), pct)
}

func (m Model) renderTimelineMessages(width int) []string {
	lines := []string{m.renderTimelineHistoryHeader(width)}
	if len(m.chat.transcript) == 0 {
		lines = append(lines,
			subtleStyle.Render("  paste text, type a prompt, or use /commands"),
			subtleStyle.Render("  ctrl+j / alt+enter inserts a newline"),
		)
		return lines
	}
	assistantCounter := 0
	for i, item := range m.chat.transcript {
		eventRow := isTimelineEventMessage(item)
		prevEventRow := i > 0 && isTimelineEventMessage(m.chat.transcript[i-1])
		if i > 0 && !(eventRow && prevEventRow) {
			lines = append(lines, "")
		}
		streaming := m.chat.streamIndex == i && m.chat.sending
		durationMs := item.DurationMs
		if streaming && !m.chat.streamStartedAt.IsZero() {
			durationMs = int(time.Since(m.chat.streamStartedAt).Milliseconds())
		}
		copyIdx := 0
		if item.Role.Eq(chatRoleAssistant) {
			assistantCounter++
			copyIdx = assistantCounter
		}
		lines = append(lines, m.renderTimelineMessage(item, width, streaming, durationMs, copyIdx)...)
	}
	return lines
}

func renderTimelineOps(info statsPanelInfo, width int, slimHeader bool) []string {
	rows := []string{}
	now := strings.TrimSpace(info.WorkflowStatus)
	if now == "" {
		switch {
		case info.Streaming:
			now = "streaming model reply"
		case info.AgentActive:
			phase := strings.TrimSpace(info.AgentPhase)
			if phase == "" {
				phase = "working"
			}
			now = "agent " + humanizeAgentPhase(phase)
		default:
			now = "ready for input"
		}
	}
	now = humanizeWorkflowText(now)
	loop := []string{"now: " + now}
	if meter := strings.TrimSpace(info.WorkflowMeter); meter != "" {
		loop = append(loop, meter)
	}
	if info.AgentActive && info.AgentMaxSteps > 0 {
		loop = append(loop, fmt.Sprintf("call %d/%d", max(info.AgentStep, 1), info.AgentMaxSteps))
	}
	if tool := strings.TrimSpace(info.LastTool); tool != "" {
		label := "last tool: " + tool
		if info.LastStatus != "" {
			label += " " + info.LastStatus
		}
		if info.LastDurationMs > 0 {
			label += fmt.Sprintf(" %dms", info.LastDurationMs)
		}
		loop = append(loop, label)
	}
	if info.ActiveSubagents > 0 {
		loop = append(loop, fmt.Sprintf("%d subagents", info.ActiveSubagents))
	}
	rows = append(rows, truncateSingleLine(accentStyle.Render(strings.Join(loop, subtleStyle.Render("  |  "))), width))

	if slimHeader {
		return rows
	}

	work := []string{}
	if execution := strings.TrimSpace(info.WorkflowExecution); execution != "" {
		work = append(work, "doing: "+humanizeWorkflowText(execution))
	}
	if info.TodoTotal > 0 {
		work = append(work, fmt.Sprintf("todos %d/%d done, %d doing", info.TodoDone, info.TodoTotal, info.TodoDoing))
	}
	if len(info.WorkflowRecent) > 0 {
		work = append(work, "recent: "+humanizeWorkflowText(strings.TrimSpace(info.WorkflowRecent[0])))
	}
	if len(work) > 0 {
		rows = append(rows, truncateSingleLine(strings.Join(work, subtleStyle.Render("  |  ")), width))
	}

	stats := []string{}
	if info.ContextFileCount > 0 || info.ContextBudgetTokens > 0 {
		ctxLine := "ctx"
		if info.ContextFileCount > 0 {
			ctxLine += fmt.Sprintf(" %d files", info.ContextFileCount)
		}
		if info.ContextBudgetTokens > 0 {
			ctxLine += fmt.Sprintf(" %s/%s code", compactMetric(info.ContextTokens), compactMetric(info.ContextBudgetTokens))
		}
		if info.ContextCompression != "" {
			ctxLine += " " + info.ContextCompression
		}
		stats = append(stats, ctxLine)
	}
	if info.ContextHistoryTokens > 0 || info.ContextSystemTokens > 0 {
		stats = append(stats, fmt.Sprintf("request sys %s hist %s", compactMetric(info.ContextSystemTokens), compactMetric(info.ContextHistoryTokens)))
	}
	if info.ToolCount > 0 {
		stats = append(stats, fmt.Sprintf("tools %d", info.ToolCount))
	}
	if info.CompressionSavedChars > 0 {
		pct := 0
		if info.CompressionRawChars > 0 {
			pct = int((int64(info.CompressionSavedChars) * 100) / int64(info.CompressionRawChars))
		}
		label := "rtk saved " + compactMetric(info.CompressionSavedChars)
		if pct > 0 {
			label += fmt.Sprintf(" (%d%%)", pct)
		}
		stats = append(stats, label)
	}
	if info.Dirty || info.Inserted > 0 || info.Deleted > 0 {
		branch := strings.TrimSpace(info.Branch)
		if branch == "" {
			branch = "worktree"
		}
		stats = append(stats, fmt.Sprintf("git %s +%d -%d", branch, info.Inserted, info.Deleted))
	}
	if info.MessageCount > 0 {
		stats = append(stats, fmt.Sprintf("%d msgs", info.MessageCount))
	}
	if len(stats) > 0 {
		rows = append(rows, truncateSingleLine(subtleStyle.Render("stats: ")+strings.Join(stats, subtleStyle.Render("  |  ")), width))
	}
	return rows
}

func humanizeWorkflowText(text string) string {
	replacer := strings.NewReplacer(
		"tool-call", "calling tool",
		"tool-result", "reading tool result",
		"tool-error", "tool error",
		"agent:loop", "agent loop",
	)
	return replacer.Replace(text)
}

func humanizeAgentPhase(phase string) string {
	phase = strings.TrimSpace(phase)
	switch phase {
	case "tool-call":
		return "calling tool"
	case "tool-result":
		return "reading tool result"
	case "tool-error":
		return "tool error"
	case "":
		return "working"
	default:
		return phase
	}
}

func (m Model) renderTimelineHistoryHeader(width int) string {
	user, assistant, toolRows := 0, 0, 0
	for _, line := range m.chat.transcript {
		switch {
		case line.Role.Eq(chatRoleUser):
			user++
		case line.Role.Eq(chatRoleAssistant):
			assistant++
		case line.Role.Eq(chatRoleTool):
			toolRows++
		}
	}
	parts := []string{"Chat History"}
	if total := len(m.chat.transcript); total > 0 {
		parts = append(parts, fmt.Sprintf("%d rows", total))
	}
	if user > 0 || assistant > 0 {
		parts = append(parts, fmt.Sprintf("%d user / %d assistant", user, assistant))
	}
	if toolRows > 0 {
		parts = append(parts, fmt.Sprintf("%d tool events", toolRows))
	}
	if len(m.chat.transcript) > 0 {
		parts = append(parts, "model sees budgeted recent history")
	}
	if m.chat.sending {
		parts = append(parts, spinnerFrame(m.chat.spinnerFrame)+" live")
	}
	return truncateSingleLine(subtleStyle.Render(strings.Join(parts, "  |  ")), width)
}

func (m Model) renderTimelineMessage(item chatLine, width int, streaming bool, durationMs, copyIdx int) []string {
	if isTimelineEventMessage(item) {
		header := renderTimelineEventHeader(item, streaming, durationMs, m.chat.spinnerFrame)
		return renderTimelineEventMessage(item, header, width)
	}
	header := renderChatHistoryMessageHeader(item, streaming, durationMs, copyIdx, m.chat.spinnerFrame)
	return []string{renderMessageBubble(string(item.Role), chatBubbleContent(item, streaming), header, width)}
}

func isTimelineEventMessage(item chatLine) bool {
	if item.Role.Eq(chatRoleTool) {
		return true
	}
	if !item.Role.Eq(chatRoleSystem) {
		return false
	}
	content := strings.ToLower(strings.TrimSpace(item.Content))
	for _, prefix := range []string{"running:", "done:", "failed:", "warn:", "info:", "context"} {
		if strings.HasPrefix(content, prefix) {
			return true
		}
	}
	return false
}

func renderTimelineEventMessage(item chatLine, header string, width int) []string {
	content := strings.TrimSpace(chatBubbleContent(item, false))
	if content == "" {
		content = strings.TrimSpace(item.Content)
	}
	badge := timelineEventBadge(item.Role)
	prefix := badge
	if strings.TrimSpace(header) != "" {
		prefix += " " + subtleStyle.Render(header)
	}
	prefix += "  "

	// Show elapsed time for running tools (e.g. "+3s") so the user can see the tool is still alive.
	if strings.HasPrefix(strings.ToLower(content), "running:") {
		if elapsed := elapsedLabel(item.Timestamp); elapsed != "" {
			prefix += ToolStyle.Render(" "+elapsed+" ") + "  "
		}
	}

	prefixWidth := lipgloss.Width(prefix)
	limit := max(width-prefixWidth, 18)
	rows := wrapTimelineEventContent(content, limit)
	if len(rows) == 0 {
		return []string{prefix}
	}
	out := []string{prefix + timelineEventRowStyle(rows[0], content).Render(rows[0])}
	indent := strings.Repeat(" ", prefixWidth)
	for _, row := range rows[1:] {
		out = append(out, subtleStyle.Render(indent)+timelineEventRowStyle(row, content).Render(row))
	}
	return out
}

func wrapTimelineEventContent(content string, limit int) []string {
	rows := []string{}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimRight(line, " \t\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		rows = append(rows, wrapBubbleLine(line, limit)...)
	}
	return rows
}

func timelineEventBadge(role chatRole) string {
	label := "SYS"
	style := titleStyle
	if role.Eq(chatRoleTool) {
		label = "TOOL"
		style = ToolLineStyle
	}
	return style.Render(" " + label + " ")
}

func timelineEventStyle(content string) lipgloss.Style {
	lower := strings.ToLower(strings.TrimSpace(content))
	switch {
	case strings.HasPrefix(lower, "failed:"), strings.Contains(lower, "error"), strings.Contains(lower, "conflict"):
		return warnStyle
	case strings.HasPrefix(lower, "done:"):
		return okStyle
	case strings.HasPrefix(lower, "running:"):
		return infoStyle
	default:
		return subtleStyle
	}
}

func timelineEventRowStyle(row, content string) lipgloss.Style {
	trimmed := strings.ToLower(strings.TrimSpace(row))
	switch {
	case strings.HasPrefix(trimmed, "_reason:"):
		return subtleStyle
	case strings.HasPrefix(trimmed, "params:"):
		return ToolStyle
	case strings.HasPrefix(trimmed, "summary:"), strings.HasPrefix(trimmed, "calls:"):
		return subtleStyle
	case strings.HasPrefix(row, "  "):
		return subtleStyle
	default:
		return timelineEventStyle(content)
	}
}

func renderTimelineEventHeader(item chatLine, streaming bool, durationMs, spinner int) string {
	parts := []string{}
	if !item.Timestamp.IsZero() {
		parts = append(parts, item.Timestamp.Format("15:04:05"))
	}
	if item.TokenCount > 0 {
		parts = append(parts, fmt.Sprintf("%d tok", item.TokenCount))
	}
	if durationMs > 0 {
		parts = append(parts, fmt.Sprintf("%dms", durationMs))
	}
	if item.ToolCalls > 0 || item.ToolFailures > 0 {
		parts = append(parts, fmt.Sprintf("tools %d fail %d", item.ToolCalls, item.ToolFailures))
	}
	if streaming {
		parts = append(parts, spinnerFrame(spinner)+" streaming")
	}
	return strings.Join(parts, "  |  ")
}

func renderChatHistoryMessageHeader(item chatLine, streaming bool, durationMs, copyIdx, spinner int) string {
	role := strings.ToUpper(strings.TrimSpace(string(item.Role)))
	if role == "" {
		role = "MESSAGE"
	}
	parts := []string{role}
	if !item.Timestamp.IsZero() {
		parts = append(parts, item.Timestamp.Format("15:04:05"))
	}
	if item.TokenCount > 0 {
		parts = append(parts, fmt.Sprintf("%d tok", item.TokenCount))
	}
	if durationMs > 0 {
		parts = append(parts, fmt.Sprintf("%dms", durationMs))
	}
	if item.ToolCalls > 0 || item.ToolFailures > 0 {
		parts = append(parts, fmt.Sprintf("tools %d fail %d", item.ToolCalls, item.ToolFailures))
	}
	if copyIdx > 0 {
		parts = append(parts, fmt.Sprintf("copy #%d", copyIdx))
	}
	if streaming {
		parts = append(parts, spinnerFrame(spinner)+" streaming")
	}
	return strings.Join(parts, "  |  ")
}

func (m Model) renderTimelineComposer(width int) []string {
	suggestions := m.buildChatSuggestionState()
	lines := []string{""}
	if m.ui.showHelpOverlay {
		helpLines := []string{}
		for _, row := range splitLines(m.renderHelpOverlay(min(width-4, 120))) {
			if strings.TrimSpace(row) != "" {
				helpLines = append(helpLines, row)
			}
		}
		lines = append(lines, renderConsoleWidget("HELP", helpLines, width)...)
	}
	if m.ui.resumePromptActive && !m.chat.sending {
		resumeLines := []string{}
		for _, row := range splitLines(renderResumeBanner(m.agentLoop.step, m.agentLoop.maxToolStep, min(width-4, 100))) {
			if strings.TrimSpace(row) != "" {
				resumeLines = append(resumeLines, row)
			}
		}
		lines = append(lines, renderConsoleWidget("RESUME", resumeLines, width)...)
	}
	attachmentLines := []string{}
	for _, block := range m.chat.pasteBlocks {
		text := fmt.Sprintf("%s stored  %d bytes", block.placeholder(), len([]byte(block.content)))
		attachmentLines = append(attachmentLines, text)
	}
	if len(m.chat.pasteBlocks) > 0 {
		attachmentLines = append(attachmentLines, "delete any placeholder character to remove its stored content")
		lines = append(lines, consoleWidgetTitle("PASTE", "Attachments", width))
		for _, row := range attachmentLines {
			lines = append(lines, renderConsoleWidgetLine(row, width, subtleStyle.Render))
		}
	}
	inputLine := renderChatInputLine(m.chat.input, m.chat.cursor, m.chat.cursorManual, m.chat.cursorInput, m.chat.sending)
	lines = append(lines, sectionHeader("›", "Input"))
	lines = append(lines, renderInputBox(inputLine, max(width-2, 20)))

	if m.pendingApproval != nil {
		lines = append(lines, splitLines(renderApprovalModal(m.pendingApproval, min(width-2, 110)))...)
	}

	pickerActive := m.pendingApproval != nil || suggestions.mentionActive || suggestions.slashMenuActive || m.commandPicker.active
	if suggestions.mentionActive {
		lines = append(lines, splitLines(renderMentionPickerModal(suggestions, m.slashMenu.mention, len(m.filesView.entries), min(width-2, 110)))...)
	} else if suggestions.slashMenuActive {
		lines = append(lines, splitLines(renderSlashPickerModal(suggestions.slashCommands, m.slashMenu.command, min(width-2, 110)))...)
	}
	trimmedInput := strings.TrimSpace(m.chat.input)
	showContextStrip := (trimmedInput != "" && !strings.HasPrefix(trimmedInput, "/")) || len(m.chat.pasteBlocks) > 0 || strings.TrimSpace(m.filesView.pinned) != ""
	if !pickerActive && showContextStrip {
		if strip := m.renderContextStrip(min(width, 120)); strip != "" {
			lines = append(lines, splitLines(strip)...)
		}
	}
	if m.commandPicker.active {
		lines = append(lines, m.renderTimelineCommandPicker(width)...)
	}
	if !pickerActive && strings.HasPrefix(trimmedInput, "/") {
		lines = append(lines, m.renderTimelineSuggestions(width, suggestions)...)
	}
	if m.chat.sending {
		phase := "drafting reply"
		if m.agentLoop.active {
			if p := strings.TrimSpace(m.agentLoop.phase); p != "" {
				phase = p
			}
		}
		lines = append(lines, splitLines(renderStreamingIndicator(phase, m.chat.spinnerFrame))...)
	}
	return lines
}

func (m Model) renderTimelineCommandPicker(width int) []string {
	kind := strings.TrimSpace(strings.ToLower(m.commandPicker.kind))
	title := "Command Picker"
	switch kind {
	case "provider":
		title = "Provider Picker"
	case "model":
		title = "Model Picker"
	case "tool":
		title = "Tool Picker"
	case "read":
		title = "Read Picker"
	case "run":
		title = "Run Picker"
	case "grep":
		title = "Grep Picker"
	}
	mode := "session"
	if m.commandPicker.persist {
		mode = "persist -> .dfmc/config.yaml"
	}
	lines := []string{
		consoleWidgetTitle("PICK", title, width),
		subtleStyle.Render("    up/down move | tab cycle | enter apply | ctrl+s " + mode + " | esc close"),
	}
	if query := strings.TrimSpace(m.commandPicker.query); query != "" {
		lines = append(lines, subtleStyle.Render("    filter: "+truncateSingleLine(query, width-12)))
	}
	items := m.filteredCommandPickerItems()
	if len(items) == 0 {
		lines = append(lines, subtleStyle.Render("    No matching entries."))
		return lines
	}
	selected := clampIndex(m.commandPicker.index, len(items))
	start := 0
	if selected > 4 {
		start = selected - 4
	}
	end := start + 8
	if end > len(items) {
		end = len(items)
	}
	for i := start; i < end; i++ {
		prefix := "  "
		render := subtleStyle.Render
		if i == selected {
			prefix = "> "
			render = titleStyle.Render
		}
		lines = append(lines, renderConsoleWidgetLine(prefix+formatCommandPickerItem(items[i]), width, render))
	}
	return lines
}

func (m Model) renderTimelineSuggestions(width int, suggestions chatSuggestionState) []string {
	lines := []string{}
	if len(suggestions.slashArgSuggestions) > 0 {
		selected := clampIndex(m.slashMenu.commandArg, len(suggestions.slashArgSuggestions))
		parts := []string{}
		for i, suggestion := range suggestions.slashArgSuggestions {
			if i >= 6 {
				break
			}
			suggestion = strings.TrimSpace(suggestion)
			if suggestion == "" {
				continue
			}
			if i == selected {
				parts = append(parts, titleStyle.Render("> "+suggestion))
			} else {
				parts = append(parts, subtleStyle.Render(suggestion))
			}
		}
		if len(parts) > 0 {
			line := "tab " + strings.Join(parts, subtleStyle.Render("  "))
			lines = append(lines, subtleStyle.Render("  "+truncateSingleLine(line, width-2)))
		}
	}
	if len(suggestions.quickActions) > 0 {
		lines = append(lines, consoleWidgetTitle("QUICK", "Quick actions", width))
		lines = append(lines, subtleStyle.Render("    up/down move | tab cycle | enter run"))
		selected := clampIndex(m.slashMenu.quickAction, len(suggestions.quickActions))
		for i, action := range suggestions.quickActions {
			render := subtleStyle.Render
			prefix := "  "
			if i == selected {
				render = titleStyle.Render
				prefix = "> "
			}
			lines = append(lines, renderConsoleWidgetLine(prefix+action.PreparedInput, width, render))
			if reason := strings.TrimSpace(action.Reason); reason != "" {
				lines = append(lines, renderConsoleWidgetLine(reason, width, subtleStyle.Render))
			}
		}
	}
	return lines
}

func consoleWidgetTitle(label, text string, width int) string {
	label = strings.ToUpper(strings.TrimSpace(label))
	if label == "" {
		label = "INFO"
	}
	prefix := "  " + label + "  "
	return titleStyle.Render(prefix) + subtleStyle.Render(truncateSingleLine(strings.TrimSpace(text), width-len([]rune(prefix))-2))
}

func renderConsoleWidgetLine(text string, width int, render func(...string) string) string {
	return render("    " + truncateSingleLine(strings.TrimSpace(text), max(width-4, 12)))
}

func renderConsoleWidget(label string, rows []string, width int) []string {
	cleaned := []string{}
	for _, row := range rows {
		row = strings.TrimSpace(row)
		if row != "" {
			cleaned = append(cleaned, row)
		}
	}
	if len(cleaned) == 0 {
		return nil
	}
	out := []string{consoleWidgetTitle(label, cleaned[0], width)}
	for _, row := range cleaned[1:] {
		out = append(out, renderConsoleWidgetLine(row, width, subtleStyle.Render))
	}
	return out
}
