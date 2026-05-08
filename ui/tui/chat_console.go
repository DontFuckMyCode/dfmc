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
	out := []string{renderMessageBubble(string(item.Role), chatBubbleContent(item, streaming), header, width)}
	// Phase E item 1 — pin/fork/save chips: under finished assistant
	// turns (not while streaming so the chip line doesn't flash) render
	// a one-line affordance strip with the slash commands the user can
	// run against this turn. When pinned, the chip flips to ★ and
	// advertises /unpin instead of /pin. The chip line is intentionally
	// subtle styling so it doesn't compete with the answer body.
	if !streaming && copyIdx > 0 && item.Role.Eq(chatRoleAssistant) && strings.TrimSpace(item.Content) != "" {
		if chip := m.renderAssistantTurnChips(copyIdx, width); chip != "" {
			out = append(out, chip)
		}
	}
	return out
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
