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
	"github.com/charmbracelet/x/ansi"
)

func RenderToolChip(chip ToolChip, width int) string {
	icon, styleFor := chipIconStyle(chip.Status)
	name := strings.TrimSpace(chip.Name)
	if name == "" {
		name = "tool"
	}
	status := strings.TrimSpace(chip.Status)
	var head string
	if isSubagentToolChip(chip) {
		head = styleFor.Render(icon+" ") + AccentStyle.Render("SUBAGENT") + " " + styleFor.Render(name)
	} else {
		head = styleFor.Render(icon+" ") + chipNameStyle(name, status).Render(name)
	}
	meta := []string{}
	if chip.Step > 0 {
		meta = append(meta, fmt.Sprintf("step %d", chip.Step))
	}
	if chip.DurationMs > 0 {
		meta = append(meta, fmt.Sprintf("%dms", chip.DurationMs))
	}
	if chip.OutputTokens > 0 {
		if chip.Truncated {
			meta = append(meta, fmt.Sprintf("+%s tok⚠", FormatToolTokenCount(chip.OutputTokens)))
		} else {
			meta = append(meta, fmt.Sprintf("+%s tok", FormatToolTokenCount(chip.OutputTokens)))
		}
	}
	if chip.SavedChars > 0 {
		if chip.CompressionPct > 0 {
			meta = append(meta, fmt.Sprintf("rtk −%s (%d%%)", FormatToolTokenCount(chip.SavedChars), chip.CompressionPct))
		} else {
			meta = append(meta, fmt.Sprintf("rtk −%s", FormatToolTokenCount(chip.SavedChars)))
		}
	}
	// Hard-truncation badge — distinct from `rtk` (compression of
	// noise) and from `Truncated` (the sandbox flag). This fires when
	// the per-call char cap forced bytes out of the model-bound
	// payload — meaning the model is missing real content. Prominent
	// warn style so the user knows to widen the window or split the
	// call. The rune count tells them HOW MUCH was lost so they can
	// judge severity at a glance.
	if chip.HardTruncated {
		label := "✂ truncated"
		if chip.HardTruncatedRunes > 0 {
			label = fmt.Sprintf("✂ −%s chars cut", FormatToolTokenCount(chip.HardTruncatedRunes))
		}
		meta = append(meta, WarnStyle.Bold(true).Render(label))
	}
	if status != "" && status != "ok" && status != "running" {
		meta = append(meta, status)
	}
	head1 := head
	if len(meta) > 0 {
		head1 += " " + SubtleStyle.Render("· "+strings.Join(meta, " · "))
	}
	verb := strings.TrimSpace(chip.Verb)
	preview := strings.TrimSpace(chip.Preview)
	reason := strings.TrimSpace(chip.Reason)
	if len(reason) > 140 {
		reason = reason[:137] + "..."
	}
	innerWidth := max(width-2, 16)
	if verb != "" && preview != "" {
		out := strings.Builder{}
		out.WriteString(TruncateSingleLine(head1, width))
		if reason != "" {
			out.WriteString("\n  ")
			out.WriteString(SubtleStyle.Render(TruncateSingleLine("↳ "+reason, innerWidth)))
		}
		out.WriteString("\n  ")
		out.WriteString(SubtleStyle.Render(TruncateSingleLine(verb, innerWidth)))
		out.WriteString("\n  ")
		out.WriteString(SubtleStyle.Render(TruncateSingleLine("→ "+preview, innerWidth)))
		appendInnerLines(&out, chip.InnerLines, innerWidth)
		return out.String()
	}
	if verb != "" {
		single := head1 + " " + SubtleStyle.Render("· "+verb)
		if reason == "" && ansi.StringWidth(single) <= width && len(chip.InnerLines) == 0 {
			return single
		}
		out := strings.Builder{}
		out.WriteString(TruncateSingleLine(head1, width))
		if reason != "" {
			out.WriteString("\n  ")
			out.WriteString(SubtleStyle.Render(TruncateSingleLine("↳ "+reason, innerWidth)))
		}
		out.WriteString("\n  ")
		out.WriteString(SubtleStyle.Render(TruncateSingleLine(verb, innerWidth)))
		appendInnerLines(&out, chip.InnerLines, innerWidth)
		return out.String()
	}
	var headRendered string
	if preview != "" {
		single := head1 + " " + SubtleStyle.Render("· "+preview)
		if reason == "" && ansi.StringWidth(single) <= width && len(chip.InnerLines) == 0 {
			return single
		}
		headRendered = TruncateSingleLine(head1, width)
		if reason != "" {
			headRendered += "\n  " + SubtleStyle.Render(TruncateSingleLine("↳ "+reason, innerWidth))
		}
		headRendered += "\n  " + SubtleStyle.Render(TruncateSingleLine(preview, innerWidth))
	} else {
		headRendered = TruncateSingleLine(head1, width)
		if reason != "" {
			headRendered += "\n  " + SubtleStyle.Render(TruncateSingleLine("↳ "+reason, innerWidth))
		}
	}
	if len(chip.InnerLines) == 0 {
		return headRendered
	}
	out := strings.Builder{}
	out.WriteString(headRendered)
	appendInnerLines(&out, chip.InnerLines, innerWidth)
	return out.String()
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

func chipIconStyle(status string) (string, lipgloss.Style) {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "ok", "success", "done":
		return "✓", OkStyle
	case "failed", "error", "fail":
		return "✗", FailStyle
	case "running", "start", "pending":
		return "◌", InfoStyle
	case "compact", "compacted":
		return "⇵", AccentStyle
	case "budget", "budget_exhausted":
		return "✦", WarnStyle
	case "handoff":
		return "⇨", AccentStyle
	case "subagent-running":
		return "◈", AccentStyle
	case "subagent-ok":
		return "◈", OkStyle
	case "subagent-failed":
		return "◈", FailStyle
	default:
		return "•", SubtleStyle
	}
}

func chipNameStyle(name, status string) lipgloss.Style {
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
	case "grep_codebase", "semantic_search", "ast_query":
		return AccentStyle
	case "run_command":
		return ToolStyle
	default:
		return OkStyle
	}
}
