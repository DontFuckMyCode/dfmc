package theme

// tool_chips.go — rendering for the per-message tool activity chips and
// their collapsed summary. Split out of render.go for size. All symbols
// here revolve around the ToolChip view-model defined in types.go.

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
	var head string
	if isSubagentToolChip(chip) {
		head = styleFor.Render(icon+" ") + AccentStyle.Render("SUBAGENT") + " " + styleFor.Render(name)
	} else {
		head = styleFor.Render(icon + " " + name)
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
	status := strings.TrimSpace(chip.Status)
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

func RenderInlineToolChips(chips []ToolChip, width int) string {
	if len(chips) == 0 {
		return ""
	}
	if width < 20 {
		width = 20
	}
	indent := "    "
	inner := width - len(indent)
	if inner < 16 {
		inner = 16
	}
	var b strings.Builder
	for i, chip := range chips {
		if i > 0 {
			b.WriteByte('\n')
		}
		for j, line := range strings.Split(RenderToolChip(chip, inner), "\n") {
			if j > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(indent)
			b.WriteString(line)
		}
	}
	return b.String()
}

func RenderInlineToolChipsSummary(chips []ToolChip, width int) string {
	if len(chips) == 0 {
		return ""
	}
	if width < 20 {
		width = 20
	}
	indent := "    "
	inner := width - len(indent)
	if inner < 24 {
		inner = 24
	}

	var ok, fail, running, subagents int
	var totalMs int64
	var totalTok int
	counts := map[string]int{}
	order := []string{}

	for _, c := range chips {
		switch strings.ToLower(strings.TrimSpace(c.Status)) {
		case "ok", "success", "done":
			ok++
		case "failed", "error", "fail":
			fail++
		case "running", "pending":
			running++
		}
		if c.DurationMs > 0 {
			totalMs += int64(c.DurationMs)
		}
		if c.OutputTokens > 0 {
			totalTok += c.OutputTokens
		}
		name := strings.TrimSpace(c.Name)
		if name == "" {
			name = "tool"
		}
		if name == "delegate_task" || name == "orchestrate" || strings.HasPrefix(name, "subagent") {
			subagents++
		}
		if _, seen := counts[name]; !seen {
			order = append(order, name)
		}
		counts[name]++
	}

	parts := []string{fmt.Sprintf("%d tool call%s", len(chips), plural(len(chips)))}
	if ok > 0 {
		parts = append(parts, fmt.Sprintf("%s ok", OkStyle.Render(fmt.Sprintf("%d", ok))))
	}
	if fail > 0 {
		parts = append(parts, fmt.Sprintf("%s fail", FailStyle.Render(fmt.Sprintf("%d", fail))))
	}
	if running > 0 {
		parts = append(parts, fmt.Sprintf("%d running", running))
	}
	if subagents > 0 {
		parts = append(parts, fmt.Sprintf("%d sub-agent%s", subagents, plural(subagents)))
	}
	if totalTok > 0 {
		parts = append(parts, fmt.Sprintf("~%s tok", FormatToolTokenCount(totalTok)))
	}
	if totalMs > 0 {
		parts = append(parts, fmt.Sprintf("%dms", totalMs))
	}
	headline := SubtleStyle.Render("▸ tools · " + strings.Join(parts, " · "))

	breakdown := []string{}
	for _, name := range order {
		n := counts[name]
		if n == 1 {
			breakdown = append(breakdown, name)
		} else {
			breakdown = append(breakdown, fmt.Sprintf("%s ×%d", name, n))
		}
	}
	hint := SubtleStyle.Render("— /tools to expand")
	body := strings.Join(breakdown, ", ")
	bodyLine := SubtleStyle.Render("  ") + TruncateSingleLine(body+" "+hint, inner)

	var b strings.Builder
	b.WriteString(indent)
	b.WriteString(TruncateSingleLine(headline, inner))
	b.WriteByte('\n')
	b.WriteString(indent)
	b.WriteString(bodyLine)
	return b.String()
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

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
