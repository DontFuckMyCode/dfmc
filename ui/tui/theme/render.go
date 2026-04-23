package theme

// render.go — theme rendering helpers.
//
// Extracted from ui/tui/theme.go. All functions in this file operate
// purely on data and lipgloss styles with no engine or model dependencies.

import (
	"fmt"
	"strings"

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
