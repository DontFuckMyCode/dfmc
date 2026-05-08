// tool_chips_inline.go — per-message inline list views for tool
// chips: the wrapping multi-chip list (RenderInlineToolChips) and
// the collapsed one-line summary (RenderInlineToolChipsSummary)
// shown when /tools collapses the strip. Sibling of tool_chips.go
// which keeps RenderToolChip (per-chip render) + appendInnerLines +
// FormatToolTokenCount + the chipIconStyle/chipNameStyle/
// isSubagentToolChip classifier helpers.
//
// Splitting the inline list views out keeps tool_chips.go scoped to
// "what does one chip look like and how do we colour it" while this
// file owns "how do we present a slice of chips inside an assistant
// message" — both the expanded "every chip on its own row" view and
// the collapsed "N tool calls · A ok · F fail" summary used when
// /tools collapses the strip to keep signal density tight.

package theme

import (
	"fmt"
	"strings"
)

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
	headline := SubtleStyle.Render("▸ tools · "+strings.Join(parts, " · ")) + "  " +
		AccentStyle.Render("[/tools]") + SubtleStyle.Render(" expand")

	breakdown := []string{}
	for _, name := range order {
		n := counts[name]
		if n == 1 {
			breakdown = append(breakdown, name)
		} else {
			breakdown = append(breakdown, fmt.Sprintf("%s ×%d", name, n))
		}
	}
	body := strings.Join(breakdown, ", ")
	bodyLine := SubtleStyle.Render("  ") + SubtleStyle.Render(TruncateSingleLine(body, inner-2))

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
