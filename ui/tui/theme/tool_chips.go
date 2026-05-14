package theme

// tool_chips.go - per-chip rendering for the tool activity strip:
// RenderToolChip (one chip's head + meta + verb/preview/inner lines),
// FormatToolTokenCount (the "1.3k" suffix), isSubagentToolChip (the
// SUBAGENT classifier), and chipIconStyle / chipNameStyle (status -> icon +
// color and tool -> color). Sibling tool_chips_inline.go owns the multi-chip
// list views.

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func RenderToolChip(chip ToolChip, width int) string {
	icon, styleFor := ChipIconStyle(chip.Status)
	name := strings.TrimSpace(chip.Name)
	if name == "" {
		name = "tool"
	}
	status := strings.TrimSpace(chip.Status)

	displayName := name
	if isSubagentToolChip(chip) {
		displayName = "SUB " + name
	}

	headline := displayName
	reason := strings.TrimSpace(chip.Reason)
	if reason != "" {
		headline = reason
	}

	var tail []string
	if reason != "" {
		tail = append(tail, displayName)
	}
	if chip.DurationMs > 0 {
		tail = append(tail, FormatDurationShort(chip.DurationMs))
	}
	if chip.Step > 0 {
		tail = append(tail, fmt.Sprintf("step %d", chip.Step))
	}
	if chip.SavedChars > 0 && chip.CompressionPct > 0 {
		tail = append(tail, fmt.Sprintf("rtk \u2212%d (%d%%)", chip.SavedChars, chip.CompressionPct))
	}
	if chip.OutputTokens > 0 {
		tail = append(tail, fmt.Sprintf("out %s tok", FormatToolTokenCount(chip.OutputTokens)))
	}

	headLine := styleFor.Render(icon) + " " +
		SubtleStyle.Render("TOOL ") +
		ChipNameStyle(name, status).Render(toolChipStatusLabel(status)+": "+headline)
	if len(tail) > 0 {
		headLine += " " + SubtleStyle.Render(strings.Join(tail, " \u00b7 "))
	}

	preview := strings.TrimSpace(chip.Preview)
	verb := strings.TrimSpace(chip.Verb)
	innerWidth := max(width-4, 16)
	out := strings.Builder{}
	out.WriteString(TruncateSingleLine(headLine, width))

	if shouldRenderToolChipVerbInline(verb, preview, width, headLine) {
		out.WriteString(" " + SubtleStyle.Render(TruncateSingleLine(verb, remainingToolChipLineWidth(width, headLine))))
	} else if verb != "" {
		out.WriteString("\n  " + SubtleStyle.Render(TruncateSingleLine(verb, innerWidth)))
	}

	if shouldRenderToolChipPreviewInline(preview, status, width, headLine, verb) {
		out.WriteString(" " + SubtleStyle.Render(TruncateSingleLine(preview, remainingToolChipLineWidth(width, headLine))))
	} else if preview != "" && status != "running" {
		out.WriteString("\n  " + SubtleStyle.Render(TruncateSingleLine("\u2192 "+preview, innerWidth)))
	}

	if chip.Expanded {
		appendToolChipInnerLines(&out, chip.InnerLines, innerWidth)
	}

	return out.String()
}

func shouldRenderToolChipVerbInline(verb, preview string, width int, headLine string) bool {
	if verb == "" || preview != "" {
		return false
	}
	remaining := remainingToolChipLineWidth(width, headLine)
	return remaining >= 12 && lipgloss.Width(verb) <= remaining
}

func shouldRenderToolChipPreviewInline(preview, status string, width int, headLine string, verb string) bool {
	if preview == "" || status == "running" || verb != "" {
		return false
	}
	remaining := remainingToolChipLineWidth(width, headLine)
	return remaining >= 8 && lipgloss.Width(preview) <= remaining
}

func remainingToolChipLineWidth(width int, headLine string) int {
	return max(width-lipgloss.Width(headLine)-1, 1)
}

func appendToolChipInnerLines(out *strings.Builder, lines []string, innerWidth int) {
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln != "" {
			out.WriteString("\n    " + SubtleStyle.Render(TruncateSingleLine(ln, innerWidth-2)))
		}
	}
}

func toolChipStatusLabel(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "ok", "success", "done":
		return "done"
	case "failed", "error", "fail", "timeout", "denied":
		return "failed"
	case "running", "start", "pending", "subagent-running":
		return "running"
	case "subagent-ok":
		return "done"
	case "subagent-failed":
		return "failed"
	default:
		if status = strings.TrimSpace(status); status != "" {
			return status
		}
		return "tool"
	}
}

func FormatDurationShort(ms int) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return fmt.Sprintf("%.1fs", float64(ms)/1000)
}

func FormatToolTokenCount(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%.1fk", float64(n)/1000)
}

// RenderInlineToolChips + RenderInlineToolChipsSummary + plural live in
// tool_chips_inline.go.

func isSubagentToolChip(chip ToolChip) bool {
	name := strings.ToLower(strings.TrimSpace(chip.Name))
	if name == "delegate_task" || name == "orchestrate" || strings.HasPrefix(name, "subagent") {
		return true
	}
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(chip.Status)), "subagent-")
}

func ChipIconStyle(status string) (string, lipgloss.Style) {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "ok", "success", "done":
		return "\u00b7", OkStyle
	case "failed", "error", "fail":
		return "\u00d7", FailStyle
	case "running", "start", "pending":
		return "\u25cc", InfoStyle
	case "compact", "compacted":
		return "\u2195", AccentStyle
	case "budget", "budget_exhausted":
		return "!", WarnStyle
	case "handoff":
		return "\u2192", AccentStyle
	case "subagent-running":
		return "\u25c8", AccentStyle
	case "subagent-ok":
		return "\u25c8", OkStyle
	case "subagent-failed":
		return "\u25c8", FailStyle
	default:
		return "\u00b7", SubtleStyle
	}
}

func ChipNameStyle(name, status string) lipgloss.Style {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "failed", "error", "fail":
		return FailStyle
	case "running", "start", "pending":
		return InfoStyle
	}
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "write_file", "edit_file", "apply_patch":
		return WarnStyle
	case "read_file", "list_dir", "glob":
		return InfoStyle
	case "grep_codebase", "semantic_search", "ast_query", "call_graph":
		return AccentStyle
	case "run_command":
		return ToolStyle
	default:
		return SubtleStyle
	}
}
