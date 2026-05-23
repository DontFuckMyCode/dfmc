package tui

// chat_console_event.go — tool/system "event" row rendering for the
// chat console. Companion siblings:
//
//   - chat_console.go           timeline frame: top strip, message
//                               iteration, history header, message
//                               dispatch, humanizers
//   - chat_console_composer.go  composer + per-tab widgets (paste
//                               attachments, command pickers, next
//                               actions, suggestion menus)
//
// renderTimelineEventMessage lays out a tool / system event as a
// header row + 2-char-indented body. Earlier this function rendered
// the FIRST content line on the same row as the badge+pill+header,
// then indented subsequent rows by the full prefix width (~50 chars
// on a typical tool event). The result was a deep right-aligned
// column that wasted horizontal space and made multi-line tool
// blocks read like an envelope return-address. The new layout keeps
// the prefix on its own header line and indents every content row
// by exactly 2 spaces — the standard "log entry with details below"
// shape most CLIs use.

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/dontfuckmycode/dfmc/ui/tui/theme"
)

func renderTimelineEventMessage(item chatLine, header string, width int) []string {
	content := strings.TrimSpace(chatBubbleContent(item, false))
	if content == "" {
		content = strings.TrimSpace(item.Content)
	}
	badge := timelineEventBadgeForItem(item)
	headerLine := badge

	if strings.TrimSpace(header) != "" {
		headerLine += "  " + subtleStyle.Render(header)
	}

	if strings.HasPrefix(strings.ToLower(content), "running:") {
		if elapsed := elapsedLabel(item.Timestamp); elapsed != "" {
			headerLine += "  " + ToolStyle.Render(elapsed)
		}
	}

	const bodyIndent = "  "
	limit := max(width-len(bodyIndent), 18)

	// Filter and wrap content rows
	allRows := strings.Split(content, "\n")
	filteredRows := make([]string, 0, len(allRows))
	for _, row := range allRows {
		row = strings.TrimSpace(row)
		if row == "" {
			continue
		}
		lower := strings.ToLower(row)
		// Hide boilerplate rows
		if strings.HasPrefix(lower, "state:") || strings.HasPrefix(lower, "card:") || strings.Contains(lower, "summarized") {
			continue
		}
		if strings.HasPrefix(lower, "running:") && hasTimelineDetailAfter(allRows, row) {
			continue
		}
		if item.Role.Eq(chatRoleSystem) && strings.HasPrefix(lower, "done:") {
			if detail := nextTimelineDetailAfter(allRows, row); detail != "" {
				row += " | " + detail
			}
		}
		if reason := timelineToolEventReason(item); reason != "" && (strings.HasPrefix(lower, "done:") || strings.HasPrefix(lower, "failed:")) {
			row += " | _reason: " + reason
		}

		// Clean up labels for one-line render.
		row = strings.Replace(row, "target: ", "· ", 1)
		row = strings.Replace(row, "returned: ", "» ", 1)
		row = strings.Replace(row, "result: ", "» ", 1)
		row = strings.Replace(row, "command: ", "$ ", 1)
		row = strings.Replace(row, "error: ", "× ", 1)
		row = strings.Replace(row, "_reason: ", "💭 ", 1)
		row = strings.Replace(row, "reason: ", "💭 ", 1)
		row = strings.TrimSpace(row)
		wrapped := wrapBubbleLine(row, limit)
		for _, r := range wrapped {
			filteredRows = append(filteredRows, truncateSingleLine(r, limit))
			if len(filteredRows) >= 1 { // One-line only; full details in Ctrl+Shift+T
				break
			}
		}
		if len(filteredRows) >= 1 {
			// Full detail in Ctrl+Shift+T panel
			break
		}
	}

	if len(filteredRows) == 0 {
		return []string{headerLine}
	}

	// Inline the first content row onto the header line so each event
	// occupies a single row (badge + header + body on one line). This
	// halves the vertical space: a running+done pair drops from 4 lines
	// to 2. Full detail remains in Ctrl+Shift+T panel.
	row := filteredRows[0]
	combined := headerLine + subtleStyle.Render(bodyIndent) + timelineEventRowStyle(row).Render(row)
	return []string{combined}
}

func timelineToolEventReason(item chatLine) string {
	if !item.Role.Eq(chatRoleTool) || len(item.EventLines) == 0 {
		return ""
	}
	return strings.TrimSpace(item.EventLines[0].Reason)
}

func hasTimelineDetailAfter(rows []string, current string) bool {
	return nextTimelineDetailAfter(rows, current) != ""
}

func nextTimelineDetailAfter(rows []string, current string) string {
	seenCurrent := false
	for _, row := range rows {
		row = strings.TrimSpace(row)
		if !seenCurrent {
			seenCurrent = row == current
			continue
		}
		if row == "" {
			continue
		}
		lower := strings.ToLower(row)
		if strings.HasPrefix(lower, "state:") || strings.HasPrefix(lower, "card:") || strings.Contains(lower, "summarized") {
			continue
		}
		return row
	}
	return ""
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

	icon, style := theme.ChipIconStyle(ev.Status)
	label := strings.ToUpper(name)

	switch name {
	case "read_file", "list_dir", "glob":
		label = "READ"
		if strings.EqualFold(strings.TrimSpace(ev.Status), "running") && name == "read_file" {
			label = "CALL"
		}
	case "grep_codebase", "semantic_search", "ast_query", "call_graph":
		label = "SEARCH"
	case "run_command":
		label = "RUN"
	case "write_file":
		label = "WRITE"
	case "edit_file":
		label = "EDIT"
	case "apply_patch":
		label = "PATCH"
	case "tool_batch_call":
		label = "BATCH"
	}

	switch strings.ToLower(strings.TrimSpace(ev.Status)) {
	case "ok", "done":
		label = "DONE"
	case "failed", "error", "denied", "timeout":
		label = "FAIL"
	}

	if ev.Step > 0 {
		// Step is step-in-round; Round (when present) is the agent loop
		// iteration. Showing them as `#step·round` keeps the chip width
		// bounded but lets users spot a tool that fired twice in the
		// same round (signal of stuck-on-rerun) vs. once per round.
		if ev.Round > 0 && ev.Round != ev.Step {
			label += fmt.Sprintf(" #%d·r%d", ev.Step, ev.Round)
		} else {
			label += fmt.Sprintf(" #%d", ev.Step)
		}
	}

	return style.Render(icon + " " + label)
}

func timelineEventRowStyle(row string) lipgloss.Style {
	trimmed := strings.ToLower(strings.TrimSpace(row))
	switch {
	case strings.HasPrefix(trimmed, "state:"), strings.HasPrefix(trimmed, "card:"):
		return subtleStyle
	case strings.HasPrefix(trimmed, "💭"):
		return accentStyle.Italic(true)
	case strings.HasPrefix(trimmed, "· "), strings.HasPrefix(trimmed, "$ "):
		return infoStyle
	case strings.HasPrefix(trimmed, "diff:"), strings.HasPrefix(trimmed, "impact:"), strings.HasPrefix(trimmed, "outcome:"), strings.HasPrefix(trimmed, "» "):
		return okStyle
	case strings.HasPrefix(trimmed, "× "):
		return failStyle
	case strings.HasPrefix(trimmed, "input:"), strings.HasPrefix(trimmed, "params:"), strings.HasPrefix(trimmed, "payload:"):
		return subtleStyle
	default:
		return subtleStyle
	}
}

func renderTimelineEventHeader(item chatLine, streaming bool, durationMs, spinner int, streamTokens []string) string {
	parts := []string{}
	if !item.Timestamp.IsZero() {
		stamp := item.Timestamp.Format("15:04:05")
		if rel := theme.FormatRelativeTime(item.Timestamp, time.Now()); rel != "" {
			stamp += " " + rel
		}
		parts = append(parts, stamp)
	}
	if streaming && len(streamTokens) > 0 {
		parts = append(parts, streamTokens...)
	} else if item.TokenCount > 0 {
		parts = append(parts, theme.CompactTokens(item.TokenCount)+" tok")
	}
	if !streaming && durationMs > 0 {
		parts = append(parts, theme.FormatDurationChip(durationMs))
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
	// Use the shared RenderMessageHeader path so the badge styling and
	// relative-time / model-badge / cancelled affordances stay in one
	// place. Token count comes from streamTokens when streaming (in/out
	// split) so we feed those in as a suffix instead of via TokenCount.
	info := messageHeaderInfo{
		Role:         role,
		Timestamp:    item.Timestamp,
		Now:          time.Now(),
		DurationMs:   durationMs,
		ToolCalls:    item.ToolCalls,
		ToolFailures: item.ToolFailures,
		Streaming:    streaming,
		SpinnerFrame: spinner,
		CopyIndex:    copyIdx,
		Provider:     item.Provider,
		Model:        item.Model,
		Cancelled:    item.Cancelled,
		// Done is "finalized assistant turn": not streaming, not
		// cancelled, has body. Other roles never get the chip — a
		// user prompt is sealed by definition; tool/system events
		// already carry their own status badge.
		Done: !streaming && !item.Cancelled && item.Role.Eq(chatRoleAssistant) && strings.TrimSpace(item.Content) != "",
	}
	if streaming {
		// During streaming we surface the live in/out token split via
		// streamTokens, so don't let the static TokenCount duplicate it.
		info.TokenCount = 0
	} else {
		info.TokenCount = item.TokenCount
	}
	out := renderMessageHeader(info)
	if streaming {
		extra := append([]string{}, streamTokens...)
		extra = append(extra, "streaming")
		out += "  " + subtleStyle.Render(strings.Join(extra, " · "))
	}
	return out
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
