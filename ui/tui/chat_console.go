package tui

// chat_console.go — chat-tab timeline frame: top runtime strip,
// message iteration, history header, and the message-row dispatcher
// that picks bubble vs event rendering.
//
// Companion siblings (extracted to keep this file scannable):
//
//   - chat_console_event.go     tool/system event rows (badge, pill,
//                               body wrap, header line builders)
//   - chat_console_composer.go  composer + per-tab widgets (paste
//                               attachments, command pickers, slash
//                               suggestions, next-actions strip)

import (
	"fmt"
	"strings"
	"time"
)

func (m Model) renderChatConsoleViewParts(width int, slimHeader bool) chatViewParts {
	if width < 40 {
		width = 40
	}
	headWidth := width
	if width > 42 {
		// The transcript can receive a right-edge scrollbar after it is
		// rendered. Keep a small gutter reserved up front so full-width
		// markdown tables do not lose their right border when the marker
		// column is added.
		headWidth = width - 2
	}
	head := []string{}
	head = append(head, m.renderTimelineTop(headWidth, slimHeader)...)
	head = append(head, m.renderTimelineMessages(headWidth)...)
	tail := m.renderTimelineComposer(width)
	return chatViewParts{Head: strings.Join(head, "\n"), Tail: strings.Join(tail, "\n")}
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
			subtleStyle.Render("  enter sends, alt+enter inserts a newline"),
		)
		return lines
	}

	assistantCounter := 0
	for i := 0; i < len(m.chat.transcript); i++ {
		item := m.chat.transcript[i]
		streaming := m.chat.streamIndex == i && m.chat.sending
		collapseKey, collapseLabel := timelineEventCollapseKey(item)
		collapsed := 0
		if collapseKey != "" && !streaming {
			for next := i + 1; next < len(m.chat.transcript); next++ {
				nextKey, _ := timelineEventCollapseKey(m.chat.transcript[next])
				if nextKey != collapseKey {
					break
				}
				collapsed++
			}
		}

		// Logic to prevent excessive spacing between interleaved items
		eventRow := isTimelineEventMessage(item)
		prevEventRow := i > 0 && isTimelineEventMessage(m.chat.transcript[i-1])

		// Spacing: add newline between distinct turns or blocks.
		// Keep assistant text and its immediately subsequent tools tight.
		if i > 0 {
			isAssistantToTool := m.chat.transcript[i-1].Role.Eq(chatRoleAssistant) && eventRow
			isUserToAnything := m.chat.transcript[i-1].Role.Eq(chatRoleUser)
			if (isUserToAnything || !isAssistantToTool) && !(eventRow && prevEventRow) {
				lines = append(lines, "")
			}
		}

		durationMs := item.DurationMs
		if streaming && !m.chat.streamStartedAt.IsZero() {
			durationMs = int(time.Since(m.chat.streamStartedAt).Milliseconds())
		}

		if item.Role.Eq(chatRoleAssistant) {
			assistantCounter++
		}

		lines = append(lines, m.renderTimelineMessage(item, width, streaming, durationMs, assistantCounter)...)
		if collapsed > 0 {
			summary := newChatLine(item.Role, fmt.Sprintf("collapsed %d repeated %s event(s) - full stream in Activity; details in ToolStatus", collapsed, collapseLabel))
			lines = append(lines, m.renderTimelineMessage(summary, width, false, 0, assistantCounter)...)
			i += collapsed
		}
	}
	return lines
}

func timelineEventCollapseKey(item chatLine) (string, string) {
	if !isTimelineEventMessage(item) {
		return "", ""
	}
	text := strings.ToLower(strings.TrimSpace(item.Content))
	switch {
	case strings.Contains(text, "tool round hard cap reached") || strings.Contains(text, "tool round cap still active"):
		return "tool-hard-cap", "tool hard-cap"
	case strings.Contains(text, "unverified edits") && strings.Contains(text, "stop editing"):
		return "coach-unverified-edits", "unverified-edits coach"
	default:
		return "", ""
	}
}

func (m Model) renderTimelineMessage(item chatLine, width int, streaming bool, durationMs, copyIdx int) []string {
	streamTokens := m.streamingHeaderTokenParts(item, streaming)
	if isTimelineEventMessage(item) {
		header := renderTimelineEventHeader(item, streaming, durationMs, m.chat.spinnerFrame, streamTokens)
		return renderTimelineEventMessage(item, header, width)
	}

	header := renderChatHistoryMessageHeader(item, streaming, durationMs, copyIdx, m.chat.spinnerFrame, streamTokens)

	// Interleaved logic: if it's an assistant message, we don't necessarily
	// want a full bubble if it's part of a multi-step sequence.
	// But for now, we'll keep the bubble and just ensure it's positioned correctly.
	out := []string{renderMessageBubble(string(item.Role), chatBubbleContent(item, streaming), header, width)}

	if !streaming && copyIdx > 0 && item.Role.Eq(chatRoleAssistant) && strings.TrimSpace(item.Content) != "" {
		if chip := m.renderAssistantTurnChips(copyIdx, width); chip != "" {
			out = append(out, chip)
		}
	}
	return out
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

// renderAssistantTurnChips builds the per-turn affordance line shown
// under each assistant bubble: pin/unpin · fork · save. The chip line
// is one row, indented to align with the bubble body, and uses subtle
// styling everywhere except the ★ pinned marker (which gets accentStyle
// so the eye picks it out at a glance).
func (m Model) renderAssistantTurnChips(turnNum, width int) string {
	if turnNum <= 0 {
		return ""
	}
	pinned := m.chat.pinnedAssistantTurns[turnNum]
	parts := []string{}
	if pinned {
		parts = append(parts, accentStyle.Render("★ pinned"))
		parts = append(parts, subtleStyle.Render(fmt.Sprintf("/unpin %d", turnNum)))
	} else {
		parts = append(parts, subtleStyle.Render(fmt.Sprintf("/pin %d", turnNum)))
	}
	parts = append(parts,
		subtleStyle.Render(fmt.Sprintf("/fork %d", turnNum)),
		subtleStyle.Render(fmt.Sprintf("/save %d", turnNum)),
	)
	return "  " + truncateSingleLine(strings.Join(parts, subtleStyle.Render(" · ")), max(width-4, 16))
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
