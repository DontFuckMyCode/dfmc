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

	"github.com/dontfuckmycode/dfmc/ui/tui/theme"
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
		lines = append(lines, m.renderEmptyTranscriptHint(width)...)
		return lines
	}

	assistantCounter := 0
	turnCounter := 0
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

		// Turn separator: each user message starts a new logical turn.
		// Render a subtle `─── turn N · 2m ago ───` rule above it so
		// long scrollback gains visual landmarks. Skipped for the very
		// first turn (the history header already serves as a top edge).
		if item.Role.Eq(chatRoleUser) {
			turnCounter++
			if i > 0 {
				lines = append(lines, "", m.renderTurnSeparator(turnCounter, item.Timestamp, width))
			}
		}

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

	// Build the bubble body. Long, non-streaming assistant turns get
	// collapsed to a head preview when the user hasn't opted them open
	// via /expand. The footer chip teaches the recovery action so the
	// affordance is self-explaining when scrollback first hits the cap.
	body := chatBubbleContent(item, streaming)
	if collapsed := m.maybeCollapseAssistantBody(item, body, streaming, copyIdx); collapsed != "" {
		body = collapsed
	}
	if q := m.chat.lastSearchQuery; q != "" {
		body = highlightSearchHits(body, q)
	}
	out := []string{renderMessageBubble(string(item.Role), body, header, width)}
	if item.Role.Eq(chatRoleAssistant) {
		if chips := m.renderMessageToolChips(item.ToolChips, width, streaming); chips != "" {
			out = append(out, chips)
		}
	}

	if !streaming && copyIdx > 0 && item.Role.Eq(chatRoleAssistant) && strings.TrimSpace(item.Content) != "" {
		if chip := m.renderAssistantTurnChips(copyIdx, width); chip != "" {
			out = append(out, chip)
		}
	}
	return out
}

// maybeCollapseAssistantBody returns the truncated body when the
// assistant turn exceeds chatCollapseThreshold lines and has not been
// opted-expanded. Returns "" when the body should render in full
// (covers user/system rows, the active streaming row, and any turn the
// user already opened via /expand).
func (m Model) maybeCollapseAssistantBody(item chatLine, body string, streaming bool, copyIdx int) string {
	if streaming || copyIdx <= 0 || !item.Role.Eq(chatRoleAssistant) {
		return ""
	}
	if m.chat.expandedAssistantTurns[copyIdx] {
		return ""
	}
	lines := strings.Split(body, "\n")
	if len(lines) <= chatCollapseThreshold {
		return ""
	}
	hidden := len(lines) - chatCollapseHeadLines
	head := lines[:chatCollapseHeadLines]
	footer := fmt.Sprintf("… +%d hidden line(s) · /expand %d to show all", hidden, copyIdx)
	return strings.Join(head, "\n") + "\n\n" + subtleStyle.Render(footer)
}

func (m Model) renderMessageToolChips(chips []toolChip, width int, streaming bool) string {
	if len(chips) == 0 {
		return ""
	}
	if streaming || m.ui.toolStripExpanded {
		return renderInlineToolChips(chips, width)
	}
	return renderInlineToolChipsSummary(chips, width)
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

// renderEmptyTranscriptHint paints the empty chat panel. Kept
// deliberately compact — the existing TUI design treats the empty
// state as a "ready" signal, not a tutorial. The starter catalog
// (1-6 digit hotkeys; DefaultStarterPrompts returns 6 entries
// today, StarterTemplateForDigit accepts 1-9 for future growth)
// is exposed via a one-liner pointing users at the affordance
// without flooding the panel.
func (m Model) renderEmptyTranscriptHint(_ int) []string {
	// Keyboard affordances (enter / alt+enter / @ / /) live on the
	// always-visible composer hint a few lines below — duplicating them
	// here was clutter on the empty-state. Keep one tutorial line that
	// names the things the composer hint cannot: starters + paste flow.
	return []string{
		subtleStyle.Render("  paste text, type a prompt, or press 1-6 for a starter"),
	}
}

// renderTurnSeparator draws a subtle horizontal rule that marks the
// start of a new user turn: `─── turn 3 · 2m ago ────────…`. The label
// sits left-anchored so PageUp/PageDown landings line up at predictable
// row counts; the trailing fill takes whatever width is left. Returns
// "" when width is too narrow to render anything useful (the caller
// already skipped the very first turn separately).
func (m Model) renderTurnSeparator(turn int, ts time.Time, width int) string {
	if width < 12 {
		return ""
	}
	label := fmt.Sprintf("─── turn %d", turn)
	if !ts.IsZero() {
		stamp := ts.Format("15:04:05")
		if rel := theme.FormatRelativeTime(ts, time.Now()); rel != "" {
			stamp += " " + rel
		}
		label += " · " + stamp
	}
	label += " "
	target := width
	if target > 140 {
		target = 140
	}
	if pad := target - len([]rune(label)); pad > 0 {
		label += strings.Repeat("─", pad)
	}
	return subtleStyle.Render(label)
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
