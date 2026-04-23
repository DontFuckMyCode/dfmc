package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

func (m Model) renderActiveView(width int, height int, pal tabPaletteEntry) string {
	if height < 4 {
		height = 4
	}
	contentWidth := width - 6
	if contentWidth < 20 {
		contentWidth = 20
	}
	innerHeight := height - 4
	if innerHeight < 1 {
		innerHeight = 1
	}
	var content string
	switch m.tabs[m.activeTab] {
	case "Status":
		content = fitPanelContentHeight(m.renderStatusView(contentWidth), innerHeight)
	case "Files":
		content = fitPanelContentHeight(m.renderFilesViewSized(contentWidth, innerHeight), innerHeight)
	case "Patch":
		content = fitPanelContentHeight(m.renderPatchView(contentWidth), innerHeight)
	case "Workflow":
		content = fitPanelContentHeight(m.renderWorkflowView(contentWidth), innerHeight)
	case "Tools":
		content = fitPanelContentHeight(m.renderToolsView(contentWidth), innerHeight)
	case "Activity":
		content = m.renderActivityViewSized(contentWidth, innerHeight)
	case "Memory":
		content = fitPanelContentHeight(m.renderMemoryView(contentWidth), innerHeight)
	case "CodeMap":
		content = fitPanelContentHeight(m.renderCodemapView(contentWidth), innerHeight)
	case "Conversations":
		content = fitPanelContentHeight(m.renderConversationsView(contentWidth), innerHeight)
	case "Prompts":
		content = fitPanelContentHeight(m.renderPromptsView(contentWidth), innerHeight)
	case "Security":
		content = fitPanelContentHeight(m.renderSecurityView(contentWidth), innerHeight)
	case "Plans":
		content = fitPanelContentHeight(m.renderPlansView(contentWidth), innerHeight)
	case "Context":
		content = fitPanelContentHeight(m.renderContextView(contentWidth), innerHeight)
	case "Providers":
		content = fitPanelContentHeight(m.renderProvidersView(contentWidth), innerHeight)
	default:
		panelVisible := m.statsPanelVisible(contentWidth)
		boosted := m.statsPanelBoostActive(time.Now())
		chatWidth := contentWidth
		panelWidth := statsPanelWidth
		if panelVisible {
			panelWidth = m.statsPanelRenderWidth(contentWidth)
			chatWidth = contentWidth - panelWidth - 2
		}
		parts := m.renderChatViewParts(chatWidth, panelVisible)
		body := fitChatBody(parts.Head, parts.Tail, innerHeight, m.chat.scrollback)
		if panelVisible {
			if boosted {
				body = lipgloss.NewStyle().Faint(true).Render(body)
			}
			panel := renderStatsPanelSized(m.statsPanelInfo(), innerHeight, panelWidth)
			body = lipgloss.JoinHorizontal(lipgloss.Top, body, "  ", panel)
		}
		content = body
	}
	frame := lipgloss.NewStyle().
		Padding(1, 2).
		Background(colorPanelBg).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(pal.Border)
	return frame.Width(width).Height(height).Render(content)
}

// fitChatBody lays out the chat view so the tail (input box + pickers)
// always stays visible, and the head (header + transcript) gets clipped
// from the top to fit the remaining space.
func fitChatBody(head, tail string, maxLines, scrollbackLines int) string {
	if maxLines <= 0 {
		return head + "\n" + tail
	}
	headLines := splitLines(head)
	tailLines := splitLines(tail)
	if len(tailLines) >= maxLines {
		return strings.Join(tailLines, "\n")
	}
	available := maxLines - len(tailLines)
	if available < 3 {
		available = 3
	}
	if scrollbackLines < 0 {
		scrollbackLines = 0
	}
	end := len(headLines) - scrollbackLines
	if end > len(headLines) {
		end = len(headLines)
	}
	if end < 1 {
		end = 1
	}
	start := end - available
	if start < 0 {
		start = 0
	}
	if end-start > available {
		start = end - available
	}
	window := append([]string{}, headLines[start:end]...)
	if start > 0 {
		hint := subtleStyle.Render(fmt.Sprintf("  ↑ %d earlier lines  ·  wheel, pgup, shift+up to scroll", start))
		window[0] = hint
	}
	if end < len(headLines) {
		hint := subtleStyle.Render(fmt.Sprintf("  ↓ %d newer lines  ·  pgdown, end, shift+down to resume", len(headLines)-end))
		window[len(window)-1] = hint
	}
	return strings.Join(window, "\n") + "\n" + strings.Join(tailLines, "\n")
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
}

// chatViewParts captures the scrollable head and the always-visible tail
// of the chat view. renderActiveView composes them with fitChatBody.
type chatViewParts struct {
	Head string
	Tail string
}

func (m Model) renderChatView(width int) string {
	parts := m.renderChatViewParts(width, false)
	if parts.Tail == "" {
		return parts.Head
	}
	return parts.Head + "\n" + parts.Tail
}

func (m Model) renderChatViewParts(width int, slimHeader bool) chatViewParts {
	suggestions := m.buildChatSuggestionState()
	headerInfo := m.chatHeaderInfo()
	headerInfo.Slim = slimHeader
	header := renderChatHeader(headerInfo, min(width, 140))
	lines := []string{
		header,
		renderDivider(min(width, 140)),
		"",
	}
	if len(m.chat.transcript) == 0 {
		lines = append(lines, renderStarterPrompts(min(width, 120), headerInfo.Configured)...)
	}
	assistantCounter := 0
	for i, item := range m.chat.transcript {
		if i > 0 {
			lines = append(lines, "")
		}
		durationMs := item.DurationMs
		if m.chat.streamIndex == i && m.chat.sending && !m.chat.streamStartedAt.IsZero() {
			durationMs = int(time.Since(m.chat.streamStartedAt).Milliseconds())
		}
		copyIdx := 0
		if item.Role.Eq(chatRoleAssistant) {
			assistantCounter++
			copyIdx = assistantCounter
		}
		hdr := renderMessageHeader(messageHeaderInfo{
			Role:         string(item.Role),
			Timestamp:    item.Timestamp,
			TokenCount:   item.TokenCount,
			DurationMs:   durationMs,
			ToolCalls:    item.ToolCalls,
			ToolFailures: item.ToolFailures,
			Streaming:    m.chat.streamIndex == i && m.chat.sending,
			SpinnerFrame: m.chat.spinnerFrame,
			CopyIndex:    copyIdx,
		})
		content := chatBubbleContent(item, m.chat.streamIndex == i && m.chat.sending)
		lines = append(lines, renderMessageBubble(string(item.Role), content, hdr, width))
		if item.Role.Eq(chatRoleAssistant) {
			if len(item.ToolChips) > 0 {
				if m.ui.toolStripExpanded {
					if strip := renderInlineToolChips(item.ToolChips, width); strip != "" {
						lines = append(lines, strip)
					}
				} else {
					if strip := renderInlineToolChipsSummary(item.ToolChips, width); strip != "" {
						lines = append(lines, strip)
					}
				}
			}
			if summary := m.chatPatchSummary(item); summary != "" {
				lines = append(lines, subtleStyle.Render("    "+summary))
			}
		}
	}
	if m.agentLoop.active {
		if !slimHeader {
			card := renderRuntimeCard(runtimeSummary{
				Active:       m.agentLoop.active,
				Phase:        m.agentLoop.phase,
				Step:         m.agentLoop.step,
				MaxSteps:     m.agentLoop.maxToolStep,
				ToolRounds:   m.agentLoop.toolRounds,
				LastTool:     m.agentLoop.lastTool,
				LastStatus:   m.agentLoop.lastStatus,
				LastDuration: m.agentLoop.lastDuration,
				Provider:     m.agentLoop.provider,
				Model:        m.agentLoop.model,
			}, min(width, 120))
			if strings.TrimSpace(card) != "" {
				lines = append(lines, "", card)
			}
		}
		if scope := strings.TrimSpace(m.agentLoop.contextScope); scope != "" {
			lines = append(lines, subtleStyle.Render(truncateSingleLine("  "+scope, width)))
		}
	}

	if m.eng != nil && m.eng.Tools != nil {
		raw := m.eng.Tools.TodoSnapshot()
		if len(raw) > 0 {
			stripItems := make([]todoStripItem, 0, len(raw))
			for _, it := range raw {
				stripItems = append(stripItems, todoStripItem{
					Content:    it.Content,
					Status:     it.Status,
					ActiveForm: it.ActiveForm,
				})
			}
			if line := renderTodoStrip(stripItems, min(width, 120)); line != "" {
				lines = append(lines, line)
			}
		}
	}

	head := strings.Join(lines, "\n")

	tailLines := []string{}
	if m.ui.showHelpOverlay {
		tailLines = append(tailLines, "", m.renderHelpOverlay(min(width, 120)))
	}
	// Suppress the in-chat Workflow Focus card when the right-hand stats
	// panel is visible — it shows the same mode (todos/tasks/subagents/
	// providers) and duplicating it in the tail steals ~20 lines from the
	// transcript window, pushing live chat off the top of the screen. The
	// card is still rendered on narrow terminals where the side panel is
	// hidden (slimHeader=false) so the info never goes fully missing.
	if !slimHeader {
		if card := renderChatWorkflowFocusCard(m.statsPanelInfo(), min(width, 120)); card != "" {
			tailLines = append(tailLines, "", card)
		}
	}
	if m.ui.resumePromptActive && !m.chat.sending {
		tailLines = append(tailLines, "", renderResumeBanner(m.agentLoop.step, m.agentLoop.maxToolStep, min(width, 100)))
	}
	var inputLine string
	if len(m.chat.pasteBlocks) > 0 && len(m.chat.input) > 0 && strings.HasPrefix(m.chat.input, "[pasted text #") {
		// User has paste blocks with placeholder text in composer.
		// Show a compact label instead of rendering placeholder gibberish.
		inputLine = renderChatInputLine(m.chat.input, m.chat.cursor, m.chat.cursorManual, m.chat.cursorInput, m.chat.sending)
	} else {
		inputLine = renderChatInputLine(m.chat.input, m.chat.cursor, m.chat.cursorManual, m.chat.cursorInput, m.chat.sending)
	}
	tailLines = append(tailLines, "", sectionHeader("›", "Input"), renderInputBox(inputLine, min(width, 100)))

	if m.pendingApproval != nil {
		tailLines = append(tailLines, "", renderApprovalModal(m.pendingApproval, min(width-2, 110)))
	}

	pickerActive := m.pendingApproval != nil || suggestions.mentionActive || suggestions.slashMenuActive || m.commandPicker.active
	if suggestions.mentionActive {
		tailLines = append(tailLines, "", renderMentionPickerModal(suggestions, m.slashMenu.mention, len(m.filesView.entries), min(width-2, 110)))
	} else if suggestions.slashMenuActive {
		tailLines = append(tailLines, "", renderSlashPickerModal(suggestions.slashCommands, m.slashMenu.command, min(width-2, 110)))
	}

	if !pickerActive {
		if strip := m.renderContextStrip(min(width, 120)); strip != "" {
			tailLines = append(tailLines, strip)
		}
	}
	lines = tailLines
	if m.commandPicker.active {
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
			mode = "persist → .dfmc/config.yaml"
		}
		lines = append(lines, sectionTitleStyle.Render(title))
		lines = append(lines, subtleStyle.Render("↑↓ move · tab cycle · enter apply · ctrl+s "+mode+" · esc close"))
		if query := strings.TrimSpace(m.commandPicker.query); query != "" {
			lines = append(lines, subtleStyle.Render("filter: "+query))
		}
		items := m.filteredCommandPickerItems()
		if len(items) == 0 {
			if strings.EqualFold(kind, "model") && strings.TrimSpace(m.commandPicker.query) != "" {
				lines = append(lines, "  "+subtleStyle.Render("No known model matched. Enter applies typed value: "+strings.TrimSpace(m.commandPicker.query)))
			} else if (strings.EqualFold(kind, "tool") || strings.EqualFold(kind, "read") || strings.EqualFold(kind, "run") || strings.EqualFold(kind, "grep")) && strings.TrimSpace(m.commandPicker.query) != "" {
				lines = append(lines, "  "+subtleStyle.Render("No exact match. Enter prepares typed value: "+strings.TrimSpace(m.commandPicker.query)))
			} else {
				lines = append(lines, "  "+subtleStyle.Render("No matching entries."))
			}
		} else {
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
				label := truncateSingleLine(formatCommandPickerItem(items[i]), width)
				if i == selected {
					prefix = "> "
					label = titleStyle.Render(label)
				}
				lines = append(lines, prefix+label)
			}
		}
	}
	if !pickerActive {
		if len(suggestions.slashArgSuggestions) > 0 {
			lines = append(lines, sectionTitleStyle.Render("Command args"))
			lines = append(lines, subtleStyle.Render("↑↓ move · tab fill"))
			selected := clampIndex(m.slashMenu.commandArg, len(suggestions.slashArgSuggestions))
			start := 0
			if selected > 4 {
				start = selected - 4
			}
			end := start + 6
			if end > len(suggestions.slashArgSuggestions) {
				end = len(suggestions.slashArgSuggestions)
			}
			for i := start; i < end; i++ {
				prefix := "  "
				label := truncateSingleLine(suggestions.slashArgSuggestions[i], width)
				if i == selected {
					prefix = "> "
					label = titleStyle.Render(label)
				}
				lines = append(lines, prefix+label)
			}
		}
		if hints := m.slashAssistHints(); len(hints) > 0 {
			lines = append(lines, sectionTitleStyle.Render("Slash Assist"))
			for _, hint := range hints {
				hint = truncateSingleLine(strings.TrimSpace(hint), width)
				if hint == "" {
					continue
				}
				lines = append(lines, "  "+subtleStyle.Render(hint))
			}
		}
		if len(suggestions.quickActions) > 0 {
			lines = append(lines, sectionTitleStyle.Render("Quick actions"))
			lines = append(lines, subtleStyle.Render("↑↓ move · tab cycle · enter run"))
			selected := clampIndex(m.slashMenu.quickAction, len(suggestions.quickActions))
			for i, action := range suggestions.quickActions {
				prefix := "  "
				label := truncateSingleLine(action.PreparedInput, width)
				if i == selected {
					prefix = "> "
					label = titleStyle.Render(label)
				}
				lines = append(lines, prefix+label)
				if reason := strings.TrimSpace(action.Reason); reason != "" {
					lines = append(lines, "  "+subtleStyle.Render(truncateSingleLine(reason, width)))
				}
			}
		}
	}
	if m.chat.sending {
		phase := "drafting reply"
		if m.agentLoop.active {
			if p := strings.TrimSpace(m.agentLoop.phase); p != "" {
				phase = p
			}
		}
		lines = append(lines, "", renderStreamingIndicator(phase, m.chat.spinnerFrame))
	}
	tail := strings.Join(lines, "\n")
	return chatViewParts{Head: head, Tail: tail}
}
