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

	"github.com/charmbracelet/lipgloss"
)

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
