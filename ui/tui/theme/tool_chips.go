package theme

// tool_chips.go — per-chip rendering for the tool activity strip:
// RenderToolChip (one chip's head + meta + verb/preview/inner lines),
// appendInnerLines (the indented sub-lines), FormatToolTokenCount
// (the "1.3k" suffix), isSubagentToolChip (the SUBAGENT classifier),
// and chipIconStyle / chipNameStyle (status → icon + colour and tool
// → colour). Sibling tool_chips_inline.go owns the multi-chip list
// views (RenderInlineToolChips and the collapsed
// RenderInlineToolChipsSummary used when /tools collapses the strip).

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
	
	// Compact Name: Use SUB prefix for subagents
	displayName := name
	if isSubagentToolChip(chip) {
		displayName = "SUB " + name
	}
	
	// Narrative headline: Use Reason if available, else Tool Name
	headline := displayName
	reason := strings.TrimSpace(chip.Reason)
	if reason != "" {
		headline = reason
	}
	
	// Technical Tail: [Icon] [Name if reason used] [Duration] [Tokens]
	var tail []string
	if reason != "" {
		tail = append(tail, displayName)
	}
	if chip.DurationMs > 0 {
		tail = append(tail, FormatDurationShort(chip.DurationMs))
	}
	if chip.OutputTokens > 0 {
		tail = append(tail, FormatToolTokenCount(chip.OutputTokens)+"t")
	}

	headLine := styleFor.Render(icon) + " " + ChipNameStyle(name, status).Render(headline)
	if len(tail) > 0 {
		headLine += " " + SubtleStyle.Render(strings.Join(tail, " · "))
	}

	preview := strings.TrimSpace(chip.Preview)
	innerWidth := max(width-4, 16)
	out := strings.Builder{}
	out.WriteString(TruncateSingleLine(headLine, width))

	// Collapsible logic: only show details if expanded
	if chip.Expanded {
		// Only show preview if it's helpful and not in running state
		if preview != "" && status != "running" {
			out.WriteString("\n  " + SubtleStyle.Render(TruncateSingleLine("→ "+preview, innerWidth)))
		}
		
		// Keep inner lines (e.g. command output) but keep them very subtle
		for _, ln := range chip.InnerLines {
			ln = strings.TrimSpace(ln)
			if ln != "" {
				out.WriteString("\n    " + SubtleStyle.Render(TruncateSingleLine(ln, innerWidth-2)))
			}
		}
	}

	return out.String()
}

func FormatDurationShort(ms int) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return fmt.Sprintf("%.1fs", float64(ms)/1000)
}

func appendInnerLines(out *strings.Builder, lines []string, innerWidth int) {
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		out.WriteString("\n  ")
		out.WriteString(SubtleStyle.Render(TruncateSingleLine(ln, innerWidth)))
	}
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
		return "·", OkStyle
	case "failed", "error", "fail":
		return "×", FailStyle
	case "running", "start", "pending":
		return "◌", InfoStyle
	case "compact", "compacted":
		return "↕", AccentStyle
	case "budget", "budget_exhausted":
		return "!", WarnStyle
	case "handoff":
		return "→", AccentStyle
	case "subagent-running":
		return "◈", AccentStyle
	case "subagent-ok":
		return "◈", OkStyle
	case "subagent-failed":
		return "◈", FailStyle
	default:
		return "·", SubtleStyle
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
