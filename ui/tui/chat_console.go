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
	lines := renderRuntimeStrip(m.runtimeViewModel(), width, slimHeader)
	lines = append(lines, renderDivider(min(width, 140)))
	return lines
}

func (m Model) renderTimelineMessages(width int) []string {
	lines := []string{m.renderTimelineHistoryHeader(width)}
	if len(m.chat.transcript) == 0 {
		lines = append(lines,
			subtleStyle.Render("  paste text, type a prompt, or use /commands"),
			subtleStyle.Render("  ctrl+x sends, enter inserts a newline"),
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
	case "thinking":
		return "thinking"
	case "complete":
		return "complete"
	case "finalizing":
		return "finalizing answer"
	case "auto-resuming":
		return "compacting + resuming"
	case "parked":
		return "parked"
	case "budget-exhausted":
		return "budget exhausted"
	case "max-steps":
		return "max steps reached"
	case "error":
		return "error"
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
	streamTokens := m.streamingHeaderTokenParts(item, streaming)
	if isTimelineEventMessage(item) {
		header := renderTimelineEventHeader(item, streaming, durationMs, m.chat.spinnerFrame, streamTokens)
		return renderTimelineEventMessage(item, header, width)
	}
	header := renderChatHistoryMessageHeader(item, streaming, durationMs, copyIdx, m.chat.spinnerFrame, streamTokens)
	return []string{renderMessageBubble(string(item.Role), chatBubbleContent(item, streaming), header, width)}
}

func (m Model) streamingHeaderTokenParts(item chatLine, streaming bool) []string {
	if !streaming {
		return nil
	}
	inputTokens := m.chat.streamInputTokens
	if inputTokens <= 0 && m.telemetry.lastInputTokens > 0 && m.chat.sending {
		inputTokens = m.telemetry.lastInputTokens
	}
	outputTokens := item.TokenCount
	if outputTokens <= 0 && strings.TrimSpace(item.Content) != "" {
		outputTokens = estimatedChatTokens(item.Content)
	}
	parts := []string{}
	if inputTokens > 0 {
		parts = append(parts, "in ~"+compactMetric(inputTokens)+" tok")
	}
	if outputTokens > 0 {
		parts = append(parts, "out ~"+compactMetric(outputTokens)+" tok")
	}
	return parts
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

// renderTimelineEventMessage lays out a tool / system event as a header
// row + 2-char-indented body. Earlier this function rendered the FIRST
// content line on the same row as the badge+pill+header, then indented
// subsequent rows by the full prefix width (~50 chars on a typical tool
// event). The result was a deep right-aligned column that wasted
// horizontal space and made multi-line tool blocks read like an
// envelope return-address. The new layout keeps the prefix on its own
// header line and indents every content row by exactly 2 spaces — the
// user's request and the standard "log entry with details below" shape
// most CLIs use.
func renderTimelineEventMessage(item chatLine, header string, width int) []string {
	content := strings.TrimSpace(chatBubbleContent(item, false))
	if content == "" {
		content = strings.TrimSpace(item.Content)
	}
	badge := timelineEventBadgeForItem(item)
	headerLine := badge
	if item.Role.Eq(chatRoleTool) && len(item.EventLines) > 0 {
		if pill := timelineToolStatusPill(item.EventLines[0]); pill != "" {
			headerLine += " " + pill
		}
	}
	if strings.TrimSpace(header) != "" {
		headerLine += "  " + subtleStyle.Render(header)
	}

	// "+Ns" elapsed marker for running tools — kept on the header line
	// because it's part of the event identity, not body content.
	if strings.HasPrefix(strings.ToLower(content), "running:") {
		if elapsed := elapsedLabel(item.Timestamp); elapsed != "" {
			headerLine += "  " + ToolStyle.Render(" "+elapsed+" ")
		}
	}

	const bodyIndent = "  "
	limit := max(width-len(bodyIndent), 18)
	rows := wrapTimelineEventContent(content, limit)
	if len(rows) == 0 {
		return []string{headerLine}
	}
	out := make([]string, 0, len(rows)+1)
	out = append(out, headerLine)
	for _, row := range rows {
		out = append(out, subtleStyle.Render(bodyIndent)+timelineEventRowStyle(row, content).Render(row))
	}
	return out
}

func wrapTimelineEventContent(content string, limit int) []string {
	const maxRows = 8
	rows := []string{}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimRight(line, " \t\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		wrapped := wrapBubbleLine(line, limit)
		for _, row := range wrapped {
			rows = append(rows, truncateSingleLine(row, limit))
			if len(rows) == maxRows {
				return appendTimelineOverflowMarker(rows, limit)
			}
		}
	}
	return rows
}

func appendTimelineOverflowMarker(rows []string, limit int) []string {
	marker := truncateSingleLine("... more tool detail hidden", limit)
	if len(rows) == 0 || strings.TrimSpace(rows[len(rows)-1]) != marker {
		rows = append(rows, marker)
	}
	return rows
}

func timelineEventBadgeForItem(item chatLine) string {
	if item.Role.Eq(chatRoleTool) && len(item.EventLines) > 0 {
		return timelineToolEventBadge(item.EventLines[0])
	}
	return timelineEventBadge(item.Role)
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

func timelineToolEventBadge(ev chatEventLine) string {
	name := strings.ToLower(strings.TrimSpace(ev.ToolName))
	if name == "" {
		name = strings.ToLower(strings.TrimSpace(ev.Title))
	}
	label := "TOOL"
	style := ToolLineStyle
	switch name {
	case "read_file", "list_dir", "glob":
		label = "TOOL READ"
		style = infoStyle.Background(colorPanelBg).Bold(true)
	case "grep_codebase", "semantic_search", "ast_query":
		label = "TOOL SEARCH"
		style = accentStyle.Background(colorPanelBg).Bold(true)
	case "run_command":
		label = "TOOL RUN"
		style = ToolStyle.Background(colorPanelBg).Bold(true)
	case "write_file":
		label = "TOOL WRITE"
		style = warnStyle.Background(colorPanelBg).Bold(true)
	case "edit_file":
		label = "TOOL EDIT"
		style = warnStyle.Background(colorPanelBg).Bold(true)
	case "apply_patch":
		label = "TOOL PATCH"
		style = okStyle.Background(colorPanelBg).Bold(true)
	case "tool_batch_call":
		label = "TOOL BATCH"
		style = accentStyle.Background(colorPanelBg).Bold(true)
	}
	if strings.EqualFold(ev.Status, "failed") || strings.EqualFold(ev.Status, "error") {
		style = failStyle.Background(colorPanelBg).Bold(true)
	}
	return style.Render(" " + label + " ")
}

func timelineToolStatusPill(ev chatEventLine) string {
	status := strings.ToLower(strings.TrimSpace(ev.Status))
	label := "INFO"
	style := subtleStyle.Background(colorPanelBg).Bold(true)
	switch status {
	case "running":
		label = "CALL"
		style = infoStyle.Background(colorPanelBg).Bold(true)
	case "ok", "done":
		label = "DONE"
		style = okStyle.Background(colorPanelBg).Bold(true)
	case "failed", "error":
		label = "FAIL"
		style = failStyle.Background(colorPanelBg).Bold(true)
	case "warn", "throttle":
		label = "WARN"
		style = warnStyle.Background(colorPanelBg).Bold(true)
	}
	if ev.Step > 0 {
		label += fmt.Sprintf(" #%d", ev.Step)
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
	case strings.HasPrefix(trimmed, "state:"):
		return infoStyle
	case strings.HasPrefix(trimmed, "_reason:"):
		return subtleStyle
	case strings.HasPrefix(trimmed, "target:"), strings.HasPrefix(trimmed, "range:"), strings.HasPrefix(trimmed, "command:"), strings.HasPrefix(trimmed, "cwd:"), strings.HasPrefix(trimmed, "files:"):
		return infoStyle
	case strings.HasPrefix(trimmed, "diff:"), strings.HasPrefix(trimmed, "impact:"), strings.HasPrefix(trimmed, "review:"), strings.HasPrefix(trimmed, "next:"), strings.HasPrefix(trimmed, "verify:"):
		return accentStyle
	case strings.HasPrefix(trimmed, "card:"):
		return ToolStyle
	case strings.HasPrefix(trimmed, "output:"), strings.HasPrefix(trimmed, "returned:"), strings.HasPrefix(trimmed, "summary:"), strings.HasPrefix(trimmed, "outcome:"):
		return okStyle
	case strings.HasPrefix(trimmed, "error:"):
		return failStyle
	case strings.HasPrefix(trimmed, "mode:"), strings.HasPrefix(trimmed, "payload:"):
		return ToolStyle
	case strings.HasPrefix(trimmed, "input:"), strings.HasPrefix(trimmed, "params:"):
		return ToolStyle
	case strings.HasPrefix(trimmed, "calls:"):
		return subtleStyle
	case strings.HasPrefix(row, "  "):
		return subtleStyle
	default:
		return timelineEventStyle(content)
	}
}

func renderTimelineEventHeader(item chatLine, streaming bool, durationMs, spinner int, streamTokens []string) string {
	parts := []string{}
	if !item.Timestamp.IsZero() {
		parts = append(parts, item.Timestamp.Format("15:04:05"))
	}
	if streaming && len(streamTokens) > 0 {
		parts = append(parts, streamTokens...)
	} else if item.TokenCount > 0 {
		parts = append(parts, fmt.Sprintf("%d tok", item.TokenCount))
	}
	if !streaming && durationMs > 0 {
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

func renderChatHistoryMessageHeader(item chatLine, streaming bool, durationMs, copyIdx, spinner int, streamTokens []string) string {
	role := strings.ToUpper(strings.TrimSpace(string(item.Role)))
	if role == "" {
		role = "MESSAGE"
	}
	parts := []string{roleBadge(role)}
	if !item.Timestamp.IsZero() {
		parts = append(parts, item.Timestamp.Format("15:04:05"))
	}
	if streaming && len(streamTokens) > 0 {
		parts = append(parts, streamTokens...)
	} else if item.TokenCount > 0 {
		parts = append(parts, fmt.Sprintf("%d tok", item.TokenCount))
	}
	if !streaming && durationMs > 0 {
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
	lines = append(lines, subtleStyle.Render("  ctrl+x send · enter newline · / commands · @ mention"))

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
