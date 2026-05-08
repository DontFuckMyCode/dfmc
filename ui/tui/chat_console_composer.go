package tui

// chat_console_composer.go — composer + per-tab widget rendering for
// the chat console: paste-attachment list, command picker, slash-arg
// suggestions, quick actions, next-action strip, plus the small
// title/line/widget primitives all of them share. Companion siblings:
//
//   - chat_console.go        timeline frame + message dispatch
//   - chat_console_event.go  tool/system event rows + badges + pills

import (
	"fmt"
	"strings"
)

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
	// Persistent composer hint: send / newline first (the load-bearing
	// pair people forget), then the discovery row — slash, mention,
	// and the new alt+m / alt+P model/provider quick switchers so the
	// user doesn't have to leave chat to reconfigure.
	hintRows := []string{
		"  ctrl+x send · enter newline · / commands · @ mention",
		"  alt+m model · alt+P provider · alt+p providers · ctrl+p palette · f1-f12 tabs",
	}
	// Phase E item 2 — live budget meter pinned at the composer footer
	// so the user can read context pressure while typing without
	// glancing at the global footer or stats panel. The meter only
	// renders when we know the max-context (otherwise the bar is
	// meaningless), and visually escalates above 70%.
	if budget := composerBudgetSegment(m); budget != "" {
		hintRows = append(hintRows, "  "+budget)
	}
	for _, row := range hintRows {
		lines = append(lines, subtleStyle.Render(row))
	}

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
	// Next-actions strip — only when the LLM emitted a `[next: ...]`
	// tail on the most recent answer AND the user hasn't started
	// typing yet (don't shadow the composer with old suggestions).
	if !m.chat.sending && len(m.assistantNextActions.actions) > 0 && strings.TrimSpace(m.chat.input) == "" {
		lines = append(lines, m.renderNextActionsStrip(width)...)
	}
	return lines
}

// renderNextActionsStrip lays out the LLM's [next: ...] suggestions
// as a numbered list under the composer. Pressing the digit key
// drops the matching suggestion into the input field — turn this on
// only when there are 1-9 actions; we cap at 9 anyway.
func (m Model) renderNextActionsStrip(width int) []string {
	actions := m.assistantNextActions.actions
	if len(actions) == 0 {
		return nil
	}
	if len(actions) > 9 {
		actions = actions[:9]
	}
	out := []string{
		"",
		accentStyle.Render("  ↪ next actions") + subtleStyle.Render("  (press 1-"+fmt.Sprint(len(actions))+" to use, esc to dismiss)"),
	}
	for i, a := range actions {
		key := titleStyle.Render(fmt.Sprintf(" %d ", i+1))
		body := truncateSingleLine(a, max(width-12, 20))
		out = append(out, "  "+key+"  "+body)
	}
	return out
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

// composerBudgetSegment renders the live context-budget meter that
// pins to the composer footer (Phase E item 2). Returns "" when the
// max-context isn't known so the bar isn't meaningless. Visually
// escalates above 70% via the warnStyle wrapper inside RenderContextBar.
//
// Source-of-truth note (Phase B): the meter here is the SAME one the
// global footer renders. We reuse `liveContextSnapshot` so both
// surfaces stay in sync; the only added value is proximity — the user
// reads it without taking their eyes off the input box.
func composerBudgetSegment(m Model) string {
	tokens, maxCtx := 0, 0
	if m.status.ContextIn != nil {
		tokens = m.status.ContextIn.TokenCount
		maxCtx = m.status.ContextIn.ProviderMaxContext
	}
	if maxCtx == 0 {
		maxCtx = m.status.ProviderProfile.MaxContext
	}
	if live := m.liveContextSnapshot(); live.ok {
		if live.windowTokens > 0 {
			tokens = live.windowTokens
		}
		if live.maxContext > 0 {
			maxCtx = live.maxContext
		}
	}
	if maxCtx <= 0 {
		return ""
	}
	pct := 0
	if tokens > 0 {
		pct = (tokens * 100) / maxCtx
	}
	bar := renderContextBar(tokens, maxCtx, 12)
	chip := fmt.Sprintf("%s %s/%s · %d%%",
		bar,
		compactMetric(tokens), compactMetric(maxCtx), pct)
	if pct >= 90 {
		return failStyle.Render("ctx ") + warnStyle.Render(chip) + failStyle.Render(" · pressure HIGH — /compact / /context drop")
	}
	if pct >= 70 {
		return warnStyle.Render("ctx ") + chip + warnStyle.Render(" · /compact when ready")
	}
	return subtleStyle.Render("ctx ") + chip
}
