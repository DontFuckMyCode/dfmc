package theme

// render.go — theme rendering helpers.
//
// Extracted from ui/tui/theme.go. All functions in this file operate
// purely on data and lipgloss styles with no engine or model dependencies.

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// --- role helpers -------------------------------------------------------

func RoleBadge(role string) string {
	role = strings.ToLower(strings.TrimSpace(role))
	switch role {
	case "user":
		return BadgeUserStyle.Render("YOU")
	case "assistant":
		return BadgeAssistantStyle.Render("DFMC")
	case "tool":
		return BadgeToolStyle.Render("TOOL")
	case "coach":
		return BadgeCoachStyle.Render("COACH")
	default:
		return BadgeSystemStyle.Render("SYS")
	}
}

func RoleLineStyle(role string) lipgloss.Style {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "user":
		return UserLineStyle
	case "assistant":
		return AssistantLineStyle
	case "tool":
		return ToolStyle
	case "coach":
		return CoachLineStyle
	default:
		return SystemLineStyle
	}
}

// --- section header -----------------------------------------------------

func SectionHeader(icon, label string) string {
	icon = strings.TrimSpace(icon)
	label = strings.TrimSpace(label)
	if icon == "" {
		return SectionTitleStyle.Render(label)
	}
	return SectionTitleStyle.Render(icon + " " + label)
}

// --- markdown-lite inline renderer ---------------------------------------

func RenderMarkdownLite(text string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	out := renderInlineTokens(text, "**", BoldStyle)
	out = renderInlineTokens(out, "`", CodeStyle)
	return out
}

func RenderMarkdownBlocks(text string) []string {
	if text == "" {
		return nil
	}
	rawLines := strings.Split(text, "\n")
	out := make([]string, 0, len(rawLines))
	inFence := false
	for i := 0; i < len(rawLines); i++ {
		line := rawLines[i]
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			marker := SubtleStyle.Render("  ╌╌╌ code ╌╌╌")
			if inFence {
				if lang := strings.TrimSpace(strings.TrimPrefix(trimmed, "```")); lang != "" {
					marker = SubtleStyle.Render("  ╌╌╌ " + lang + " ╌╌╌")
				}
			}
			out = append(out, marker)
			continue
		}
		if inFence {
			out = append(out, CodeStyle.Render("  │ "+line))
			continue
		}
		if IsTableHeader(line) && i+1 < len(rawLines) && IsTableSeparator(rawLines[i+1]) {
			consumed, rendered := renderMarkdownTable(rawLines[i:])
			out = append(out, rendered...)
			i += consumed - 1
			continue
		}
		if h := HeaderLevel(trimmed); h > 0 {
			label := strings.TrimSpace(trimmed[h:])
			out = append(out, BoldStyle.Render(AccentStyle.Render(strings.Repeat("#", h)+" "+label)))
			continue
		}
		if bullet, rest, ok := BulletLine(line); ok {
			out = append(out, AccentStyle.Render(bullet)+" "+RenderMarkdownLite(rest))
			continue
		}
		out = append(out, RenderMarkdownLite(line))
	}
	return out
}

func tableDelim(line string) (rune, bool) {
	t := strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(t, "|") && strings.Count(t, "|") >= 3:
		return '|', true
	case strings.HasPrefix(t, "│") && strings.Count(t, "│") >= 3:
		return '│', true
	}
	return 0, false
}

func IsTableHeader(line string) bool {
	_, ok := tableDelim(line)
	return ok
}

func IsTableSeparator(line string) bool {
	t := strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(t, "|"):
		return IsMarkdownSeparator(t)
	case ContainsBoxSeparator(t):
		return true
	}
	return false
}

func IsMarkdownSeparator(t string) bool {
	body := strings.Trim(t, "|")
	for _, cell := range strings.Split(body, "|") {
		c := strings.TrimSpace(cell)
		c = strings.TrimPrefix(c, ":")
		c = strings.TrimSuffix(c, ":")
		if c == "" || strings.Trim(c, "-") != "" {
			return false
		}
	}
	return true
}

func ContainsBoxSeparator(t string) bool {
	if t == "" {
		return false
	}
	hasDash := false
	for _, r := range t {
		switch r {
		case '─', '┼', '┤', '├', '┬', '┴', '│', '|', '-', ' ':
			if r == '─' || r == '-' {
				hasDash = true
			}
		default:
			return false
		}
	}
	return hasDash
}

func renderMarkdownTable(lines []string) (int, []string) {
	if len(lines) < 2 {
		return 0, nil
	}
	delim, ok := tableDelim(lines[0])
	if !ok {
		return 0, nil
	}

	rows := make([][]string, 0, 8)
	consumed := 0
	headerParsed := false
	for i, line := range lines {
		if i == 1 {
			consumed = i + 1
			continue
		}
		if !strings.HasPrefix(strings.TrimSpace(line), string(delim)) {
			break
		}
		cells := SplitTableRow(line, delim)
		if len(cells) == 0 {
			break
		}
		rows = append(rows, cells)
		consumed = i + 1
		if i == 0 {
			headerParsed = true
		}
	}
	if !headerParsed || len(rows) == 0 {
		return 0, nil
	}

	rendered := make([][]string, len(rows))
	visibleWidth := make([][]int, len(rows))
	colWidths := make([]int, 0, len(rows[0]))
	for ri, row := range rows {
		rendered[ri] = make([]string, len(row))
		visibleWidth[ri] = make([]int, len(row))
		for ci, cell := range row {
			styled := RenderMarkdownLite(cell)
			if ri == 0 {
				styled = BoldStyle.Render(AccentStyle.Render(styled))
			}
			w := lipgloss.Width(styled)
			rendered[ri][ci] = styled
			visibleWidth[ri][ci] = w
			if ci >= len(colWidths) {
				colWidths = append(colWidths, w)
				continue
			}
			if w > colWidths[ci] {
				colWidths[ci] = w
			}
		}
	}

	out := make([]string, 0, len(rows)+1)
	for ri := range rendered {
		parts := make([]string, 0, len(rendered[ri]))
		for ci, styled := range rendered[ri] {
			pad := 0
			if ci < len(colWidths) {
				pad = colWidths[ci] - visibleWidth[ri][ci]
			}
			padded := styled + strings.Repeat(" ", Max0(pad))
			parts = append(parts, padded)
		}
		joined := "  " + strings.Join(parts, SubtleStyle.Render("  │  "))
		out = append(out, joined)
		if ri == 0 {
			sepParts := make([]string, 0, len(colWidths))
			for _, w := range colWidths {
				sepParts = append(sepParts, strings.Repeat("─", w))
			}
			out = append(out, SubtleStyle.Render("  "+strings.Join(sepParts, "──┼──")))
		}
	}
	return consumed, out
}

func SplitTableRow(line string, delim rune) []string {
	t := strings.TrimSpace(line)
	ds := string(delim)
	if !strings.HasPrefix(t, ds) {
		return nil
	}
	t = strings.Trim(t, ds)
	cells := strings.Split(t, ds)
	out := make([]string, 0, len(cells))
	for _, c := range cells {
		out = append(out, strings.TrimSpace(c))
	}
	return out
}

func Max0(v int) int {
	if v < 0 {
		return 0
	}
	return v
}

func HeaderLevel(trimmed string) int {
	switch {
	case strings.HasPrefix(trimmed, "### "):
		return 3
	case strings.HasPrefix(trimmed, "## "):
		return 2
	case strings.HasPrefix(trimmed, "# "):
		return 1
	}
	return 0
}

func BulletLine(line string) (bullet string, rest string, ok bool) {
	indent := 0
	for indent < len(line) && line[indent] == ' ' {
		indent++
	}
	body := line[indent:]
	if len(body) < 2 {
		return "", "", false
	}
	marker := body[0]
	switch marker {
	case '-', '*', '+':
		if body[1] != ' ' {
			return "", "", false
		}
		return strings.Repeat(" ", indent) + "•", strings.TrimSpace(body[2:]), true
	}
	digits := 0
	for digits < len(body) && body[digits] >= '0' && body[digits] <= '9' {
		digits++
	}
	if digits > 0 && digits < len(body)-1 && body[digits] == '.' && body[digits+1] == ' ' {
		return strings.Repeat(" ", indent) + body[:digits+1], strings.TrimSpace(body[digits+2:]), true
	}
	return "", "", false
}

func renderInlineTokens(text, delim string, style lipgloss.Style) string {
	if !strings.Contains(text, delim) {
		return text
	}
	var b strings.Builder
	i := 0
	for i < len(text) {
		idx := strings.Index(text[i:], delim)
		if idx < 0 {
			b.WriteString(text[i:])
			break
		}
		b.WriteString(text[i : i+idx])
		start := i + idx + len(delim)
		end := strings.Index(text[start:], delim)
		if end < 0 {
			b.WriteString(text[i+idx:])
			break
		}
		token := text[start : start+end]
		b.WriteString(style.Render(token))
		i = start + end + len(delim)
	}
	return b.String()
}

// --- tool chips ---------------------------------------------------------

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

// --- todo strip ---------------------------------------------------------

func RenderTodoStrip(items []TodoStripItem, width int) string {
	if len(items) == 0 {
		return ""
	}
	if width < 24 {
		width = 24
	}

	var done, doing, pending int
	var activeText string
	for _, it := range items {
		switch strings.ToLower(strings.TrimSpace(it.Status)) {
		case "completed", "done":
			done++
		case "in_progress", "active", "doing":
			doing++
			if activeText == "" {
				activeText = strings.TrimSpace(it.ActiveForm)
				if activeText == "" {
					activeText = strings.TrimSpace(it.Content)
				}
			}
		default:
			pending++
		}
	}
	if done == 0 && doing == 0 && pending == 0 {
		return ""
	}

	parts := []string{}
	if done > 0 {
		parts = append(parts, OkStyle.Render(fmt.Sprintf("%d done", done)))
	}
	if doing > 0 {
		parts = append(parts, AccentStyle.Render(fmt.Sprintf("%d doing", doing)))
	}
	if pending > 0 {
		parts = append(parts, fmt.Sprintf("%d pending", pending))
	}
	headline := SubtleStyle.Render("▸ TODOs · " + strings.Join(parts, " · "))
	if activeText != "" {
		headline += " " + SubtleStyle.Render("→ "+TruncateSingleLine(activeText, width-30))
	}
	return "    " + TruncateSingleLine(headline, width-4)
}

// --- runtime card -------------------------------------------------------

func RenderRuntimeCard(rs RuntimeSummary, width int) string {
	if !rs.Active {
		return ""
	}
	parts := []string{}
	if rs.ToolRounds > 0 {
		parts = append(parts, SubtleStyle.Render(fmt.Sprintf("tools %d", rs.ToolRounds)))
	}
	if tool := strings.TrimSpace(rs.LastTool); tool != "" {
		icon, style := chipIconStyle(rs.LastStatus)
		tail := icon + " " + tool
		if rs.LastDuration > 0 {
			tail += fmt.Sprintf(" %dms", rs.LastDuration)
		}
		parts = append(parts, style.Render(tail))
	}
	if len(parts) == 0 {
		return ""
	}
	return TruncateSingleLine(strings.Join(parts, "  ·  "), width)
}

// --- workflow focus card ------------------------------------------------

func RenderChatWorkflowFocusCard(info StatsPanelInfo, width int) string {
	if width < 36 {
		width = 36
	}
	mode := info.Mode
	if string(mode) == "" || mode == StatsPanelModeOverview {
		return ""
	}
	title := "Workflow Focus"
	switch mode {
	case StatsPanelModeTodos:
		title += " · TODOS"
	case StatsPanelModeTasks:
		title += " · TASKS"
	case StatsPanelModeSubagents:
		title += " · SUBAGENTS"
	case StatsPanelModeProviders:
		title += " · PROVIDERS"
	}
	lines := []string{SectionHeader("»", title)}
	if status := info.WorkflowStatus; status != "" {
		lines = append(lines, "  "+TruncateSingleLine(status, width))
	}
	if meter := info.WorkflowMeter; meter != "" {
		lines = append(lines, "  "+TruncateSingleLine(meter, width))
	}
	if execution := info.WorkflowExecution; execution != "" {
		lines = append(lines, "  "+AccentStyle.Render(TruncateSingleLine(execution, width)))
	}
	appendBlock := func(items []string, fallback string) {
		if len(items) == 0 {
			if fallback != "" {
				lines = append(lines, "  "+TruncateSingleLine(fallback, width))
			}
			return
		}
		for i, line := range items {
			if i >= 4 {
				lines = append(lines, "  ...")
				break
			}
			lines = append(lines, "  "+TruncateSingleLine(line, width))
		}
	}
	switch mode {
	case StatsPanelModeTodos:
		appendBlock(info.TodoLines, "No shared todo list yet.")
	case StatsPanelModeTasks:
		appendBlock(info.TaskLines, "No active task graph yet.")
	case StatsPanelModeSubagents:
		appendBlock(info.SubagentLines, "No subagent activity yet.")
	case StatsPanelModeProviders:
		if len(info.Providers) == 0 {
			appendBlock(nil, "No providers registered.")
		} else {
			var providerLines []string
			for i, row := range info.Providers {
				var prefix string
				if i == info.ProvidersSelectedIndex {
					prefix = "» "
				}
				line := prefix + row.Name
				if len(row.Models) > 0 {
					line += " · " + strings.Join(row.Models, " › ")
				}
				if row.Status == "no-key" {
					line += " ⚠ no-key"
				} else if row.Status == "offline" {
					line += " ○ offline"
				} else {
					line += " ● ready"
				}
				providerLines = append(providerLines, line)
			}
			appendBlock(providerLines, "")

			// Detail pane for the selected provider
			if info.ProvidersSelectedIndex >= 0 && info.ProvidersSelectedIndex < len(info.Providers) {
				sel := info.Providers[info.ProvidersSelectedIndex]
				detail := []string{
					AccentStyle.Bold(true).Render("▸ " + sel.Name),
				}
				if sel.Primary {
					detail = append(detail, SubtleStyle.Render("  primary"))
				}
				if sel.Active {
					detail = append(detail, AccentStyle.Render("  ◉ active"))
				}
				if len(sel.Models) > 0 {
					detail = append(detail, SubtleStyle.Render("  models:    ")+strings.Join(sel.Models, " › "))
				}
				if len(sel.FallbackModels) > 0 {
					detail = append(detail, SubtleStyle.Render("  fallback:  ")+strings.Join(sel.FallbackModels, " › "))
				}
				detail = append(detail, SubtleStyle.Render("  protocol:  "+sel.Protocol))
				detail = append(detail, SubtleStyle.Render(fmt.Sprintf("  max_ctx:   %d", sel.MaxContext)))
				if sel.HasAPIKey {
					detail = append(detail, OkStyle.Render("  api_key:   ● set"))
				} else {
					detail = append(detail, FailStyle.Render("  api_key:   ⚠ missing"))
				}
				lines = append(lines, strings.Join(detail, "\n"))
				lines = append(lines, "")
				lines = append(lines, SubtleStyle.Render("  enter:switch · m:model · f:fallback · s:save"))
			}
		}
	}
	if len(info.WorkflowTimeline) > 0 {
		lines = append(lines, "  live log:")
		for i, line := range info.WorkflowTimeline {
			if i >= 4 {
				lines = append(lines, "    ...")
				break
			}
			lines = append(lines, "    "+TruncateSingleLine(line, width-2))
		}
	}
	if len(info.WorkflowRecent) > 0 {
		lines = append(lines, "  recent:")
		for i, line := range info.WorkflowRecent {
			if i >= 2 {
				break
			}
			lines = append(lines, "    "+TruncateSingleLine(line, width-2))
		}
	}
	return strings.Join(lines, "\n")
}

// --- message card -------------------------------------------------------

func RenderMessageHeader(info MessageHeaderInfo) string {
	parts := []string{RoleBadge(info.Role)}
	if info.CopyIndex > 0 {
		parts = append(parts, SubtleStyle.Render(fmt.Sprintf("#%d", info.CopyIndex)))
	}
	if info.Streaming {
		parts = append(parts, InfoStyle.Bold(true).Render(SpinnerFrame(info.SpinnerFrame)))
	}
	if !info.Timestamp.IsZero() {
		parts = append(parts, SubtleStyle.Render(info.Timestamp.Format("15:04:05")))
	}
	if info.DurationMs > 0 {
		parts = append(parts, SubtleStyle.Render(FormatDurationChip(info.DurationMs)))
	}
	if info.TokenCount > 0 {
		parts = append(parts, SubtleStyle.Render(fmt.Sprintf("%s tok", FormatThousands(info.TokenCount))))
	}
	if info.ToolCalls > 0 {
		chip := fmt.Sprintf("⚒ %d", info.ToolCalls)
		if info.ToolFailures > 0 {
			parts = append(parts, AccentStyle.Render(chip)+" "+FailStyle.Bold(true).Render(fmt.Sprintf("✗ %d", info.ToolFailures)))
		} else {
			parts = append(parts, AccentStyle.Render(chip))
		}
	}
	return strings.Join(parts, " ")
}

func FormatDurationChip(ms int) string {
	if ms <= 0 {
		return ""
	}
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	if ms < 60_000 {
		return fmt.Sprintf("%.1fs", float64(ms)/1000)
	}
	mins := ms / 60_000
	secs := (ms % 60_000) / 1000
	return fmt.Sprintf("%dm%02ds", mins, secs)
}

var spinnerFrames = [...]string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func SpinnerFrame(frame int) string {
	if frame < 0 {
		frame = -frame
	}
	return spinnerFrames[frame%len(spinnerFrames)]
}

func RenderMessageBubble(role, content, header string, width int) string {
	style := RoleLineStyle(role)
	bar := style.Render("▎")
	out := []string{bar + " " + header}
	content = strings.TrimSpace(content)
	if content == "" {
		return strings.Join(out, "\n")
	}
	if width <= 4 {
		out = append(out, bar+" "+style.Render(content))
		return strings.Join(out, "\n")
	}
	for _, line := range RenderMarkdownBlocks(content) {
		for _, wrapped := range WrapBubbleLine(line, width-2) {
			out = append(out, bar+" "+wrapped)
		}
	}
	return strings.Join(out, "\n")
}

func WrapBubbleLine(line string, limit int) []string {
	if limit <= 0 {
		return []string{line}
	}
	if ansi.StringWidth(line) <= limit {
		return []string{line}
	}
	wrapped := ansi.Wrap(line, limit, " 	,;:.!?/\\_-")
	if wrapped == "" {
		return []string{line}
	}
	parts := strings.Split(wrapped, "\n")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if ansi.StringWidth(p) <= limit {
			out = append(out, p)
			continue
		}
		out = append(out, HardWrapByCells(p, limit)...)
	}
	return out
}

func HardWrapByCells(s string, limit int) []string {
	if limit <= 0 || ansi.StringWidth(s) <= limit {
		return []string{s}
	}
	out := []string{}
	cur := strings.Builder{}
	width := 0
	for _, r := range s {
		w := ansi.StringWidth(string(r))
		if width+w > limit {
			out = append(out, cur.String())
			cur.Reset()
			width = 0
		}
		cur.WriteRune(r)
		width += w
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func RenderDivider(width int) string {
	if width <= 0 {
		return ""
	}
	if width > 200 {
		width = 200
	}
	return DividerStyle.Render(strings.Repeat("─", width))
}

func RenderInputBox(line string, width int) string {
	if width < 10 {
		return InputLineStyle.Render(line)
	}
	inner := FormatInputBoxContent(line, width-4)
	return InputBoxStyle.Width(width).Render(InputLineStyle.Render(inner))
}

func FormatInputBoxContent(content string, limit int) string {
	if content == "" || limit <= 0 {
		return content
	}
	content = strings.ReplaceAll(content, "\r\n", "\n")
	logical := strings.Split(content, "\n")
	out := make([]string, 0, len(logical))
	for _, line := range logical {
		if ansi.StringWidth(line) <= limit {
			out = append(out, line)
			continue
		}
		wrapped := ansi.Wrap(line, limit, " 	,;:.!?/\\_-")
		if wrapped == "" {
			out = append(out, line)
			continue
		}
		for _, p := range strings.Split(wrapped, "\n") {
			if ansi.StringWidth(p) <= limit {
				out = append(out, p)
				continue
			}
			out = append(out, HardWrapByCells(p, limit)...)
		}
	}
	return strings.Join(out, "\n")
}

// --- chat header --------------------------------------------------------

func RenderChatHeader(info ChatHeaderInfo, width int) string {
	brand := TitleStyle.Render(" CHAT ")
	segments := []string{brand}

	if !info.Slim {
		providerTrim := strings.TrimSpace(info.Provider)
		modelTrim := strings.TrimSpace(info.Model)
		provider := blankFallback(providerTrim, "no-provider")
		model := blankFallback(modelTrim, "no-model")

		providerPill := AccentStyle.Bold(true).Render(provider)
		modelPill := BoldStyle.Render(model)
		switch {
		case providerTrim == "":
			providerPill = FailStyle.Bold(true).Render("⚠ no provider")
			modelPill = SubtleStyle.Render(model)
		case !info.Configured:
			providerPill = WarnStyle.Bold(true).Render(provider + "⚠")
		}
		who := providerPill + SubtleStyle.Render(" / ") + modelPill
		meter := RenderTokenMeter(info.ContextTokens, info.MaxContext)

		tools := SubtleStyle.Render("tools off")
		if info.ToolsEnabled {
			tools = OkStyle.Render("tools on")
		}
		segments = append(segments, who, meter)
		segments = append(segments, RenderChatModeSegment(info))
		segments = append(segments, tools)
	} else {
		if info.Streaming || info.AgentActive {
			segments = append(segments, RenderChatModeSegment(info))
		}
	}

	if info.PlanMode {
		segments = append(segments, WarnStyle.Bold(true).Render("◈ PLAN — /code exits"))
	}
	if info.ApprovalPending {
		segments = append(segments, FailStyle.Bold(true).Render("⚠ APPROVAL — y/n"))
	} else if info.ApprovalGated {
		segments = append(segments, WarnStyle.Render("⚠ gate on"))
	}
	if info.Parked {
		segments = append(segments, WarnStyle.Bold(true).Render("⏸ parked — /continue"))
	}
	if info.ActiveTools > 0 {
		segments = append(segments, InfoStyle.Bold(true).Render(fmt.Sprintf("◌ tools %d", info.ActiveTools)))
	}
	if info.ActiveSubagents > 0 {
		segments = append(segments, AccentStyle.Bold(true).Render(fmt.Sprintf("◈ subagents %d", info.ActiveSubagents)))
	}
	if info.QueuedCount > 0 {
		segments = append(segments, AccentStyle.Bold(true).Render(fmt.Sprintf("▸ queued %d", info.QueuedCount)))
	}
	if info.PendingNotes > 0 {
		segments = append(segments, InfoStyle.Render(fmt.Sprintf("✎ btw %d", info.PendingNotes)))
	}
	if last := strings.TrimSpace(info.IntentLast); last != "" {
		segments = append(segments, SubtleStyle.Render("⚙ intent "+last))
	}
	if strings.TrimSpace(info.DriveRunID) != "" {
		label := fmt.Sprintf("▸ drive %d/%d", info.DriveDone, info.DriveTotal)
		if id := strings.TrimSpace(info.DriveTodoID); id != "" {
			label += " · " + id
		}
		if info.DriveBlocked > 0 {
			label += fmt.Sprintf(" (blocked %d)", info.DriveBlocked)
			segments = append(segments, WarnStyle.Bold(true).Render(label))
		} else {
			segments = append(segments, AccentStyle.Bold(true).Render(label))
		}
	}
	sep := SubtleStyle.Render("  ·  ")
	head := TruncateSingleLine(strings.Join(segments, sep), width)
	if pinned := strings.TrimSpace(info.Pinned); pinned != "" {
		pinLine := AccentStyle.Render("  ◆ pinned: ") + BoldStyle.Render(FileMarker(pinned))
		return head + "\n" + pinLine
	}
	return head
}

// FileMarker returns a rel path with a file:// prefix for display.
// Defined here to avoid a import cycle with chat_helpers. callers in
// this package should use chat_helpers.FileMarker from ui/tui instead.
var FileMarker func(string) string = func(rel string) string { return rel }

func blankFallback(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

func RenderChatModeSegment(info ChatHeaderInfo) string {
	glyph := SpinnerFrame(info.SpinnerFrame)
	switch {
	case info.Streaming:
		return InfoStyle.Bold(true).Render(glyph + " streaming")
	case info.AgentActive:
		phase := blankFallback(strings.TrimSpace(info.AgentPhase), "working")
		if info.AgentStep > 0 && info.AgentMax > 0 {
			return AccentStyle.Bold(true).Render(fmt.Sprintf("%s tool loop %s - %d/%d", glyph, phase, info.AgentStep, info.AgentMax))
		}
		if info.AgentStep > 0 {
			return AccentStyle.Bold(true).Render(fmt.Sprintf("%s tool loop %s - step %d", glyph, phase, info.AgentStep))
		}
		return AccentStyle.Bold(true).Render(glyph + " tool loop " + phase)
	default:
		return OkStyle.Render("● ready")
	}
}

func RenderTokenMeter(used, max int) string {
	if max <= 0 {
		if used <= 0 {
			return SubtleStyle.Render("ctx —")
		}
		return SubtleStyle.Render("ctx ") + BoldStyle.Render(FormatThousands(used)+" tok")
	}
	pct := 0
	if used > 0 {
		pct = int((int64(used) * 100) / int64(max))
	}
	style := OkStyle
	switch {
	case pct >= 85:
		style = FailStyle
	case pct >= 60:
		style = WarnStyle
	}
	label := fmt.Sprintf("%s / %s (%d%%)", FormatThousands(used), FormatThousands(max), pct)
	return SubtleStyle.Render("ctx ") + style.Bold(true).Render(label)
}

func RenderStepBar(step, maxSteps, cells, frame int) string {
	if cells < 4 {
		cells = 4
	}
	if maxSteps <= 0 {
		return SubtleStyle.Render(fmt.Sprintf("step %d", step))
	}
	if step < 0 {
		step = 0
	}
	if step > maxSteps {
		step = maxSteps
	}
	filled := (step * cells) / maxSteps
	if step > 0 && filled == 0 {
		filled = 1
	}
	style := OkStyle
	remaining := maxSteps - step
	switch {
	case remaining <= 1:
		style = FailStyle
	case remaining <= 3:
		style = WarnStyle
	}
	filledStr := strings.Repeat("█", filled)
	if filled > 0 && step < maxSteps && frame%2 == 1 {
		filledStr = strings.Repeat("█", filled-1) + "▓"
	}
	bar := style.Render(filledStr) + SubtleStyle.Render(strings.Repeat("░", cells-filled))
	label := fmt.Sprintf(" %d/%d", step, maxSteps)
	return "[" + bar + "]" + style.Bold(true).Render(label)
}

func RenderContextBar(used, max, cells int) string {
	return RenderContextBarFrame(used, max, cells, 0)
}

func RenderContextBarFrame(used, max, cells, frame int) string {
	if cells < 4 {
		cells = 4
	}
	if max <= 0 {
		return RenderTokenMeter(used, max)
	}
	pct := 0
	if used > 0 {
		pct = int((int64(used) * 100) / int64(max))
		if pct > 100 {
			pct = 100
		}
	}
	filled := (pct * cells) / 100
	if used > 0 && filled == 0 {
		filled = 1
	}
	style := OkStyle
	switch {
	case pct >= 85:
		style = FailStyle
	case pct >= 60:
		style = WarnStyle
	}
	filledStr := strings.Repeat("█", filled)
	if filled > 0 && filled < cells && pct >= 60 && frame%2 == 1 {
		filledStr = strings.Repeat("█", filled-1) + "▓"
	}
	bar := style.Render(filledStr) + SubtleStyle.Render(strings.Repeat("░", cells-filled))
	label := fmt.Sprintf("%s/%s (%d%%)", CompactTokens(used), CompactTokens(max), pct)
	return "[" + bar + "] " + style.Bold(true).Render(label)
}

func CompactTokens(n int) string {
	if n < 1_000 {
		return fmt.Sprintf("%d", n)
	}
	if n < 1_000_000 {
		if n%1_000 == 0 {
			return fmt.Sprintf("%dk", n/1_000)
		}
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	}
	if n%1_000_000 == 0 {
		return fmt.Sprintf("%dM", n/1_000_000)
	}
	return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
}

func FormatThousands(n int) string {
	neg := n < 0
	if neg {
		n = -n
	}
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		if neg {
			return "-" + s
		}
		return s
	}
	var b strings.Builder
	rem := len(s) % 3
	if rem > 0 {
		b.WriteString(s[:rem])
		if len(s) > rem {
			b.WriteString(",")
		}
	}
	for i := rem; i < len(s); i += 3 {
		b.WriteString(s[i : i+3])
		if i+3 < len(s) {
			b.WriteString(",")
		}
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// --- starter prompts ---------------------------------------------------

type StarterPrompt struct {
	Key   string
	Title string
	Cmd   string
	Hint  string
}

func DefaultStarterPrompts() []StarterPrompt {
	return []StarterPrompt{
		{Key: "1", Title: "Review this project", Cmd: "/review", Hint: "quality, risks, suggestions"},
		{Key: "2", Title: "Explain a file", Cmd: "/explain @", Hint: "press @ to pick a file"},
		{Key: "3", Title: "Analyze architecture", Cmd: "/analyze", Hint: "symbols, hotspots, deps"},
		{Key: "4", Title: "Map the codebase", Cmd: "/map", Hint: "dependency graph, cycles"},
		{Key: "5", Title: "Find bugs & smells", Cmd: "/scan", Hint: "security + correctness scan"},
		{Key: "6", Title: "Draft a refactor plan", Cmd: "/refactor", Hint: "stepwise, low-risk"},
	}
}

func StarterTemplateForDigit(r rune) (string, bool) {
	if r < '1' || r > '9' {
		return "", false
	}
	idx := int(r - '1')
	prompts := DefaultStarterPrompts()
	if idx >= len(prompts) {
		return "", false
	}
	return prompts[idx].Cmd, true
}

func RenderStarterPrompts(width int, configured bool) []string {
	prompts := DefaultStarterPrompts()
	if width <= 0 {
		width = 100
	}
	lines := []string{""}
	if !configured {
		lines = append(lines,
			FailStyle.Bold(true).Render("⚠ No provider configured"),
			SubtleStyle.Render("  Press ")+AccentStyle.Bold(true).Render("f5")+SubtleStyle.Render(" for the Workflow tab, or type ")+CodeStyle.Render("/provider")+SubtleStyle.Render(" to pick one — starters need a model to run."),
			"",
		)
	}
	lines = append(lines,
		BoldStyle.Render(AccentStyle.Render("Welcome — what would you like DFMC to do?")),
		SubtleStyle.Render("  Pick a starter, type a question, or use "+CodeStyle.Render("@file")+" / "+CodeStyle.Render("/command")+"."),
		"",
	)
	for _, p := range prompts {
		key := TitleStyle.Render(" " + p.Key + " ")
		title := BoldStyle.Render(p.Title)
		cmd := CodeStyle.Render(p.Cmd)
		hint := SubtleStyle.Render("— " + p.Hint)
		raw := fmt.Sprintf("   %-2s  %-26s  %-18s  %s", p.Key, p.Title, p.Cmd, "— "+p.Hint)
		if len([]rune(raw)) > width {
			lines = append(lines, TruncateSingleLine(raw, width))
			continue
		}
		lines = append(lines, "  "+key+"  "+title+"  "+cmd+"  "+hint)
	}
	lines = append(lines,
		"",
		SubtleStyle.Render("  Tips: "+AccentStyle.Render("enter")+" send · "+AccentStyle.Render("@")+" file mention · "+AccentStyle.Render("/")+" commands · "+AccentStyle.Render("ctrl+p")+" palette · "+AccentStyle.Render("f1-f12 / alt+i/y/w/t/o")+" tabs"),
	)
	return lines
}

// --- streaming / resume ------------------------------------------------

func RenderStreamingIndicator(phase string, frame int) string {
	phase = strings.TrimSpace(phase)
	if phase == "" {
		phase = "drafting reply"
	}
	glyph := SpinnerFrame(frame)
	return InfoStyle.Bold(true).Render(glyph+" "+phase) + " " + SubtleStyle.Render("· esc cancels · tokens stream live")
}

func RenderResumeBanner(step, maxSteps, width int) string {
	if width < 20 {
		width = 20
	}
	title := WarnStyle.Bold(true).Render("⏸ Tool loop parked")
	progress := ""
	if maxSteps > 0 {
		progress = SubtleStyle.Render(fmt.Sprintf(" at step %d/%d", step, maxSteps))
	} else if step > 0 {
		progress = SubtleStyle.Render(fmt.Sprintf(" at step %d", step))
	}
	hint := SubtleStyle.Render("  ↵ enter resumes") + SubtleStyle.Render(" · ") +
		SubtleStyle.Render("esc dismisses") + SubtleStyle.Render(" · ") +
		SubtleStyle.Render("type a note first to steer /continue")
	head := TruncateSingleLine(title+progress, width)
	body := TruncateSingleLine(hint, width)
	return ResumeBannerStyle.Width(width).Render(head + "\n" + body)
}

// --- stats panel --------------------------------------------------------

const (
	StatsPanelWidth                = 38
	StatsPanelBoostWidthMin        = 48
	StatsPanelBoostMinContentWidth = 96
	StatsPanelMinContentWidth      = 120
)

func RenderStatsPanel(info StatsPanelInfo, height int) string {
	return RenderStatsPanelSized(info, height, StatsPanelWidth)
}

func RenderStatsPanelSized(info StatsPanelInfo, height int, panelWidth int) string {
	if height < 6 {
		height = 6
	}
	if panelWidth < StatsPanelWidth {
		panelWidth = StatsPanelWidth
	}
	inner := panelWidth - 4
	if inner < 16 {
		inner = 16
	}
	mode := info.Mode
	if mode == "" {
		mode = StatsPanelModeOverview
	}

	lines := []string{RenderStatsPanelModeTabs(mode, inner)}
	if info.Boosted {
		label := "FOCUS MODE · expanded"
		if info.FocusLocked {
			label = "FOCUS MODE · locked"
		} else if info.BoostSeconds > 0 {
			label = fmt.Sprintf("%s · %ds", label, info.BoostSeconds)
		}
		lines = append(lines, AccentStyle.Bold(true).Render(label))
	}
	divider := DividerStyle.Render(strings.Repeat("─", inner))
	addSection := func(icon, title string, body []string) {
		if len(body) == 0 {
			return
		}
		if len(lines) > 0 {
			lines = append(lines, divider)
		}
		header := AccentStyle.Bold(true).Render(icon) + " " + SectionTitleStyle.Render(title)
		lines = append(lines, header)
		for _, b := range body {
			if b == "" {
				lines = append(lines, "")
				continue
			}
			lines = append(lines, "  "+TruncateSingleLine(b, inner))
		}
	}

	providerIcon := "◉"
	if info.Streaming {
		providerIcon = SpinnerFrame(info.SpinnerFrame)
	}
	agentIcon := "⚙"
	if info.AgentActive {
		agentIcon = SpinnerFrame(info.SpinnerFrame + 3)
	}

	providerTrim := strings.TrimSpace(info.Provider)
	modelTrim := strings.TrimSpace(info.Model)
	var providerBody []string
	switch {
	case providerTrim == "":
		providerBody = []string{
			FailStyle.Bold(true).Render("⚠ no provider"),
			SubtleStyle.Render("f5 workflow · /provider"),
		}
	case !info.Configured:
		providerBody = []string{
			WarnStyle.Bold(true).Render(providerTrim + " ⚠"),
			BoldStyle.Render(blankFallback(modelTrim, "-")),
			SubtleStyle.Render("unconfigured — add API key"),
		}
	default:
		providerBody = []string{
			AccentStyle.Bold(true).Render(providerTrim),
			BoldStyle.Render(blankFallback(modelTrim, "-")),
		}
	}

	contextBody := []string{RenderContextBarFrame(info.ContextTokens, info.MaxContext, 10, info.SpinnerFrame)}
	if info.MaxContext > 0 {
		remaining := max(info.MaxContext-info.ContextTokens, 0)
		contextBody = append(contextBody, SubtleStyle.Render(fmt.Sprintf("%s free · %s used", CompactTokens(remaining), CompactTokens(info.ContextTokens))))
	}

	agentBody := []string{RenderChatModeSegment(ChatHeaderInfo{
		Streaming:    info.Streaming,
		AgentActive:  info.AgentActive,
		AgentPhase:   info.AgentPhase,
		AgentStep:    info.AgentStep,
		AgentMax:     info.AgentMaxSteps,
		SpinnerFrame: info.SpinnerFrame,
	})}
	if info.AgentActive && info.AgentMaxSteps > 0 {
		agentBody = append(agentBody, SubtleStyle.Render(fmt.Sprintf("call budget %d/%d", info.AgentStep, info.AgentMaxSteps)))
		agentBody = append(agentBody, RenderStepBar(info.AgentStep, info.AgentMaxSteps, 14, info.SpinnerFrame))
	}
	if info.ToolRounds > 0 {
		agentBody = append(agentBody, SubtleStyle.Render(fmt.Sprintf("tool rounds: %d", info.ToolRounds)))
	}
	if tool := strings.TrimSpace(info.LastTool); tool != "" {
		icon, style := chipIconStyle(info.LastStatus)
		tail := icon + " " + tool
		if info.LastDurationMs > 0 {
			tail += fmt.Sprintf(" · %dms", info.LastDurationMs)
		}
		agentBody = append(agentBody, style.Render(tail))
	}
	if info.Parked {
		agentBody = append(agentBody,
			WarnStyle.Bold(true).Render("⏸ parked"),
			SubtleStyle.Render("/continue to resume"),
		)
	}
	if info.QueuedCount > 0 {
		agentBody = append(agentBody, AccentStyle.Bold(true).Render(fmt.Sprintf("▸ queued %d", info.QueuedCount)))
	}
	if info.PendingNotes > 0 {
		agentBody = append(agentBody, InfoStyle.Render(fmt.Sprintf("✎ btw %d", info.PendingNotes)))
	}

	toolsBody := []string{}
	if info.ToolsEnabled {
		line := OkStyle.Render("enabled")
		if info.ToolCount > 0 {
			line += SubtleStyle.Render(fmt.Sprintf("  %d registered", info.ToolCount))
		}
		toolsBody = append(toolsBody, line)
	} else {
		toolsBody = append(toolsBody, SubtleStyle.Render("off"))
	}
	if info.CompressionSavedChars > 0 {
		pct := 0
		if info.CompressionRawChars > 0 {
			pct = int((int64(info.CompressionSavedChars) * 100) / int64(info.CompressionRawChars))
		}
		label := fmt.Sprintf("rtk saved %s chars", CompactTokens(info.CompressionSavedChars))
		if pct > 0 {
			label += fmt.Sprintf(" (%d%%)", pct)
		}
		toolsBody = append(toolsBody, OkStyle.Render(label))
	}

	workflowBody := []string{}
	if status := strings.TrimSpace(info.WorkflowStatus); status != "" {
		workflowBody = append(workflowBody, AccentStyle.Bold(true).Render(status))
	}
	if meter := strings.TrimSpace(info.WorkflowMeter); meter != "" {
		workflowBody = append(workflowBody, meter)
	}
	if info.TodoTotal > 0 {
		todoLine := fmt.Sprintf("todos %d · %d done · %d doing · %d pending", info.TodoTotal, info.TodoDone, info.TodoDoing, info.TodoPending)
		workflowBody = append(workflowBody, AccentStyle.Render(todoLine))
		if active := strings.TrimSpace(info.TodoActive); active != "" {
			workflowBody = append(workflowBody, InfoStyle.Render("active: "+TruncateSingleLine(active, inner-10)))
		}
	}
	if info.ActiveSubagents > 0 {
		workflowBody = append(workflowBody, AccentStyle.Bold(true).Render(fmt.Sprintf("subagents %d active", info.ActiveSubagents)))
	}
	if strings.TrimSpace(info.DriveRunID) != "" && info.DriveTotal > 0 {
		driveLine := fmt.Sprintf("drive %d/%d", info.DriveDone, info.DriveTotal)
		if info.DriveBlocked > 0 {
			driveLine += fmt.Sprintf(" · %d blocked", info.DriveBlocked)
		}
		workflowBody = append(workflowBody, InfoStyle.Render(driveLine))
	}
	if info.PlanSubtasks > 0 {
		planMode := "serial"
		if info.PlanParallel {
			planMode = "parallel"
		}
		workflowBody = append(workflowBody, SubtleStyle.Render(fmt.Sprintf("plan %d tasks · %s · %.2f", info.PlanSubtasks, planMode, info.PlanConfidence)))
	}

	for _, line := range info.WorkflowRecent {
		workflowBody = append(workflowBody, SubtleStyle.Render("recent: "+TruncateSingleLine(line, inner-10)))
	}

	branch := strings.TrimSpace(info.Branch)
	gitBody := []string{}
	if branch != "" {
		chip := BoldStyle.Render(branch)
		if info.Dirty {
			chip += WarnStyle.Render("*")
		}
		if info.Detached {
			chip += SubtleStyle.Render(" (detached)")
		}
		gitBody = append(gitBody, chip)
		if info.Inserted > 0 || info.Deleted > 0 {
			churn := OkStyle.Render(fmt.Sprintf("+%d", info.Inserted)) +
				SubtleStyle.Render(" / ") +
				FailStyle.Render(fmt.Sprintf("-%d", info.Deleted))
			gitBody = append(gitBody, churn)
		}
	}

	sessionHead := BoldStyle.Render(formatSessionDuration(info.SessionElapsed))
	if info.MessageCount > 0 {
		sessionHead += SubtleStyle.Render(fmt.Sprintf(" · %d msgs", info.MessageCount))
	}
	sessionBody := []string{sessionHead}
	if pinned := strings.TrimSpace(info.Pinned); pinned != "" {
		sessionBody = append(sessionBody, AccentStyle.Render("◆ ")+BoldStyle.Render(FileMarker(pinned)))
	}

	switch mode {
	case StatsPanelModeTodos:
		addSection(providerIcon, "PROVIDER", providerBody)
		addSection("▦", "CONTEXT", contextBody)
		addSection(agentIcon, "TOOL LOOP", agentBody)
		body := []string{fmt.Sprintf("%d total · %d done · %d doing · %d pending", info.TodoTotal, info.TodoDone, info.TodoDoing, info.TodoPending)}
		if status := strings.TrimSpace(info.WorkflowStatus); status != "" {
			body = append(body, status)
		}
		if meter := strings.TrimSpace(info.WorkflowMeter); meter != "" {
			body = append(body, meter)
		}
		if active := strings.TrimSpace(info.TodoActive); active != "" {
			body = append(body, "active: "+active)
		}
		if len(info.TodoLines) == 0 {
			body = append(body, "No shared todo list yet.")
			body = append(body, "It appears after todo_write or when autonomy preflight seeds one for a broad task.")
			body = append(body, "Try a multi-step ask, /split, or /todos after the tool loop gets going.")
		} else {
			body = append(body, info.TodoLines...)
		}
		for _, line := range info.WorkflowRecent {
			body = append(body, "recent: "+line)
		}
		addSection("☑", "TODOS", body)
	case StatsPanelModeTasks:
		addSection(providerIcon, "PROVIDER", providerBody)
		addSection("▦", "CONTEXT", contextBody)
		addSection(agentIcon, "TOOL LOOP", agentBody)
		body := []string{}
		if status := strings.TrimSpace(info.WorkflowStatus); status != "" {
			body = append(body, status)
		}
		if meter := strings.TrimSpace(info.WorkflowMeter); meter != "" {
			body = append(body, meter)
		}
		if len(info.TaskTreeLines) > 0 {
			body = append(body, fmt.Sprintf("%d task(s) in store", len(info.TaskTreeLines)))
			body = append(body, info.TaskTreeLines...)
		} else if len(info.TaskLines) == 0 {
			body = append(body, "No active task graph yet.")
			body = append(body, "This fills from autonomy preflight, /split, or drive planning.")
			body = append(body, "Broad asks are more likely to create task breakdowns than tiny one-shot prompts.")
		} else {
			body = append(body, info.TaskLines...)
		}
		for _, line := range info.WorkflowRecent {
			body = append(body, "recent: "+line)
		}
		addSection("◈", "TASKS", body)
	case StatsPanelModeSubagents:
		addSection(providerIcon, "PROVIDER", providerBody)
		addSection("?", "CONTEXT", contextBody)
		addSection(agentIcon, "TOOL LOOP", agentBody)
		body := []string{}
		if len(info.SubagentLines) == 0 || (len(info.SubagentLines) == 1 && strings.EqualFold(strings.TrimSpace(info.SubagentLines[0]), "idle")) {
			body = append(body, "No subagent activity yet.")
			body = append(body, "Subagents appear only when the model uses delegate_task or orchestrate fan-out.")
			body = append(body, "Most short asks stay inside one tool loop and never spawn workers.")
		} else {
			body = append(body, info.SubagentLines...)
		}
		addSection("?", "SUBAGENTS", body)
	case StatsPanelModeProviders:
		addSection(providerIcon, "PROVIDER", providerBody)
		addSection("▦", "CONTEXT", contextBody)
		addSection(agentIcon, "TOOL LOOP", agentBody)
		body := []string{}
		if len(info.Providers) == 0 {
			body = append(body, "No providers registered.")
			body = append(body, "Configure providers in .dfmc/config.yaml or via dfmc providers setup.")
		} else {
			for _, row := range info.Providers {
				var prefix string
				if row.Active {
					prefix = OkStyle.Render("◉ ")
				} else if row.Primary {
					prefix = AccentStyle.Render("◎ ")
				} else {
					prefix = SubtleStyle.Render("○ ")
				}
				name := BoldStyle.Render(row.Name)
				if row.Active {
					name = OkStyle.Bold(true).Render(row.Name)
				}
				line := prefix + name
				if len(row.Models) > 0 {
					line += SubtleStyle.Render(" · " + strings.Join(row.Models, " › "))
				}
				body = append(body, line)
				if row.Status == "no-key" {
					body = append(body, SubtleStyle.Render("  ⚠ no API key — set providers.profiles."+row.Name+".api_key"))
				}
				if len(row.FallbackModels) > 0 {
					body = append(body, SubtleStyle.Render("  fallback: "+strings.Join(row.FallbackModels, " › ")))
				}
			}
		}
		addSection("◉", "PROVIDERS", body)
		addSection("?", "SESSION", sessionBody)
	default:
		addSection(providerIcon, "PROVIDER", providerBody)
		addSection("?", "CONTEXT", contextBody)
		addSection(agentIcon, "TOOL LOOP", agentBody)
		addSection("?", "TOOLS", toolsBody)
		if len(workflowBody) == 0 {
			workflowBody = append(workflowBody, "No workflow state yet.")
			workflowBody = append(workflowBody, "This fills from todo_write, autonomy plans, drive runs, and subagent fan-out.")
			workflowBody = append(workflowBody, "Use alt+s for todos, alt+d for tasks, alt+f for subagents.")
		}
		addSection("?", "WORKFLOW", workflowBody)
		if len(gitBody) > 0 {
			addSection("?", "GIT", gitBody)
		}
		addSection("?", "SESSION", sessionBody)
	}
	footerText := "  ctrl+s hide ? alt+a/s/d/f/p switch ? ctrl+h keys"
	if info.FocusLocked {
		footerText = "  esc unlock ? ctrl+s hide ? alt+a/s/d/f/p retarget ? ctrl+h keys"
	} else if info.Boosted {
		footerText = "  alt+a/s/d/f again locks ? ctrl+s hide ? ctrl+h keys"
	}
	footer := SubtleStyle.Render(footerText)
	lines = append(lines, divider, footer)
	if len(lines) > height {
		reserve := 2
		if height < 2 {
			reserve = 0
		}
		if reserve > 0 {
			keep := height - reserve
			if keep < 0 {
				keep = 0
			}
			head := append([]string{}, lines[:keep]...)
			lines = append(head, divider, footer)
		} else {
			lines = lines[:height]
		}
	}
	for len(lines) < height {
		lines = append(lines, "")
	}

	body := strings.Join(lines, "\n")
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPanelBorder).
		Padding(0, 1).
		Width(panelWidth).
		Height(height)
	return box.Render(body)
}

func RenderStatsPanelModeTabs(mode StatsPanelMode, width int) string {
	items := []struct {
		key   string
		label string
		mode  StatsPanelMode
	}{
		{key: "A", label: "overview", mode: StatsPanelModeOverview},
		{key: "S", label: "todos", mode: StatsPanelModeTodos},
		{key: "D", label: "tasks", mode: StatsPanelModeTasks},
		{key: "F", label: "subagents", mode: StatsPanelModeSubagents},
		{key: "P", label: "providers", mode: StatsPanelModeProviders},
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		label := item.key + " " + item.label
		if mode == item.mode {
			parts = append(parts, TitleStyle.Render(" "+strings.ToUpper(label)+" "))
			continue
		}
		parts = append(parts, SubtleStyle.Render(label))
	}
	return TruncateSingleLine(strings.Join(parts, "  "), width)
}

// --- session duration ----------------------------------------------------

func formatSessionDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	// Round to nearest second for display
	s := int(d.Seconds())
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	m := s / 60
	s = s % 60
	if m < 60 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	h := m / 60
	m = m % 60
	return fmt.Sprintf("%dh %dm", h, m)
}

// TruncateSingleLine truncates text to fit within width cells, adding "…"
// if truncation occurred. Exported so callers in ui/tui can use it directly.
func TruncateSingleLine(text string, width int) string {
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(text) <= width {
		return text
	}
	// Count usable width excluding the "…"
	ellipsis := "…"
	ellipsisWidth := ansi.StringWidth(ellipsis)
	usable := width - ellipsisWidth
	if usable <= 0 {
		return ellipsis
	}
	var b strings.Builder
	widthSoFar := 0
	for _, r := range text {
		w := ansi.StringWidth(string(r))
		if widthSoFar+w > usable {
			b.WriteString(ellipsis)
			break
		}
		b.WriteRune(r)
		widthSoFar += w
	}
	return b.String()
}
