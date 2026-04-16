package tui

// theme.go — visual primitives for the TUI workbench.
//
// Keeps colour palette, lipgloss styles, and the small rendering helpers
// (role badges, status chips, runtime card, section header, markdown-lite)
// separated from the monolithic tui.go. The goal is a consistent,
// card-oriented chat experience that mirrors modern agent CLIs without
// shouting for attention.

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// --- palette --------------------------------------------------------------

var (
	colorPanelBorder = lipgloss.Color("#2F4F6A")
	colorPanelBg     = lipgloss.Color("#0B1220")
	colorTitleBg     = lipgloss.Color("#11B981")
	colorTitleFg     = lipgloss.Color("#041014")
	colorMuted       = lipgloss.Color("#93A4BF")
	colorTabActiveBg = lipgloss.Color("#1E3A8A")
	colorTabActiveFg = lipgloss.Color("#E2EEFF")
	colorTabIdleFg   = lipgloss.Color("#7D92B2")
	colorStatusBg    = lipgloss.Color("#111A2A")
	colorStatusFg    = lipgloss.Color("#D9E6FF")

	colorRoleUser      = lipgloss.Color("#8BC7FF")
	colorRoleAssistant = lipgloss.Color("#8AF0CF")
	colorRoleSystem    = lipgloss.Color("#F6D38A")
	colorRoleTool      = lipgloss.Color("#C4A7FF")
	colorRoleCoach     = lipgloss.Color("#F4B8D6")

	colorOk     = lipgloss.Color("#6EE7A7")
	colorFail   = lipgloss.Color("#FF8A8A")
	colorWarn   = lipgloss.Color("#F6D38A")
	colorInfo   = lipgloss.Color("#67E8F9")
	colorAccent = lipgloss.Color("#BFA9FF")
	colorCode   = lipgloss.Color("#F2E5A1")
)

// --- styles ---------------------------------------------------------------

var (
	docStyle = lipgloss.NewStyle().
			Padding(1, 2).
			Background(colorPanelBg).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorPanelBorder)

	titleStyle = lipgloss.NewStyle().
			Foreground(colorTitleFg).
			Background(colorTitleBg).
			Padding(0, 1).
			Bold(true)

	subtleStyle = lipgloss.NewStyle().
			Foreground(colorMuted)

	sectionTitleStyle = lipgloss.NewStyle().
				Foreground(colorInfo).
				Bold(true)

	tabActiveStyle = lipgloss.NewStyle().
			Padding(0, 2).
			Background(colorTabActiveBg).
			Foreground(colorTabActiveFg).
			Bold(true)

	tabInactiveStyle = lipgloss.NewStyle().
				Padding(0, 2).
				Foreground(colorTabIdleFg)

	statusBarStyle = lipgloss.NewStyle().
			Padding(0, 1).
			Foreground(colorStatusFg).
			Background(colorStatusBg)

	userLineStyle      = lipgloss.NewStyle().Foreground(colorRoleUser)
	assistantLineStyle = lipgloss.NewStyle().Foreground(colorRoleAssistant)
	systemLineStyle    = lipgloss.NewStyle().Foreground(colorRoleSystem)
	coachLineStyle     = lipgloss.NewStyle().Foreground(colorRoleCoach).Italic(true)
	inputLineStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#E5F2FF"))

	boldStyle   = lipgloss.NewStyle().Bold(true)
	codeStyle   = lipgloss.NewStyle().Foreground(colorCode)
	accentStyle = lipgloss.NewStyle().Foreground(colorAccent)
	okStyle     = lipgloss.NewStyle().Foreground(colorOk)
	failStyle   = lipgloss.NewStyle().Foreground(colorFail)
	warnStyle   = lipgloss.NewStyle().Foreground(colorWarn)
	infoStyle   = lipgloss.NewStyle().Foreground(colorInfo)
	toolStyle   = lipgloss.NewStyle().Foreground(colorRoleTool)

	badgeUserStyle      = lipgloss.NewStyle().Foreground(colorTitleFg).Background(colorRoleUser).Padding(0, 1).Bold(true)
	badgeAssistantStyle = lipgloss.NewStyle().Foreground(colorTitleFg).Background(colorRoleAssistant).Padding(0, 1).Bold(true)
	badgeSystemStyle    = lipgloss.NewStyle().Foreground(colorTitleFg).Background(colorRoleSystem).Padding(0, 1).Bold(true)
	badgeToolStyle      = lipgloss.NewStyle().Foreground(colorTitleFg).Background(colorRoleTool).Padding(0, 1).Bold(true)
	badgeCoachStyle     = lipgloss.NewStyle().Foreground(colorTitleFg).Background(colorRoleCoach).Padding(0, 1).Bold(true)

	cardBorderStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorPanelBorder).
			Padding(0, 1)

	runtimeCardStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorInfo).
				Padding(0, 1)

	inputBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorRoleUser).
			Padding(0, 1)

	resumeBannerStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorWarn).
				Padding(0, 1)

	dividerStyle = lipgloss.NewStyle().Foreground(colorPanelBorder)

	bannerStyle = lipgloss.NewStyle().
			Foreground(colorTitleBg).
			Bold(true)
)

// --- role helpers ---------------------------------------------------------

func roleBadge(role string) string {
	role = strings.ToLower(strings.TrimSpace(role))
	switch role {
	case "user":
		return badgeUserStyle.Render("YOU")
	case "assistant":
		return badgeAssistantStyle.Render("DFMC")
	case "tool":
		return badgeToolStyle.Render("TOOL")
	case "coach":
		return badgeCoachStyle.Render("COACH")
	default:
		return badgeSystemStyle.Render("SYS")
	}
}

func roleLineStyle(role string) lipgloss.Style {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "user":
		return userLineStyle
	case "assistant":
		return assistantLineStyle
	case "tool":
		return toolStyle
	case "coach":
		return coachLineStyle
	default:
		return systemLineStyle
	}
}

// --- section header ------------------------------------------------------

func sectionHeader(icon, label string) string {
	icon = strings.TrimSpace(icon)
	label = strings.TrimSpace(label)
	if icon == "" {
		return sectionTitleStyle.Render(label)
	}
	return sectionTitleStyle.Render(icon + " " + label)
}

// --- markdown-lite inline renderer ---------------------------------------
//
// Inline: **bold**, `inline code`.
// Block: # / ## / ### headers, - / * bullets, 1. numbered lists, ``` fences.
// Everything else passes through unchanged. Kept deliberately small so
// rendering stays allocation-light and predictable.

func renderMarkdownLite(text string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	out := renderInlineTokens(text, "**", boldStyle)
	out = renderInlineTokens(out, "`", codeStyle)
	return out
}

// renderMarkdownBlocks turns a multi-line assistant response into a slice of
// pre-styled lines, honoring block-level markdown. Callers (currently only
// renderMessageBubble) are expected to prepend their bubble bar — this
// function owns all content styling so code blocks aren't re-tinted with the
// role color.
func renderMarkdownBlocks(text string) []string {
	if text == "" {
		return nil
	}
	rawLines := strings.Split(text, "\n")
	out := make([]string, 0, len(rawLines))
	inFence := false
	for _, line := range rawLines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			// Render a subtle fence marker so users see the boundary.
			marker := subtleStyle.Render("  ╌╌╌ code ╌╌╌")
			if inFence {
				if lang := strings.TrimSpace(strings.TrimPrefix(trimmed, "```")); lang != "" {
					marker = subtleStyle.Render("  ╌╌╌ " + lang + " ╌╌╌")
				}
			}
			out = append(out, marker)
			continue
		}
		if inFence {
			out = append(out, codeStyle.Render("  │ "+line))
			continue
		}
		if h := headerLevel(trimmed); h > 0 {
			label := strings.TrimSpace(trimmed[h:])
			out = append(out, boldStyle.Render(accentStyle.Render(strings.Repeat("#", h)+" "+label)))
			continue
		}
		if bullet, rest, ok := bulletLine(line); ok {
			out = append(out, accentStyle.Render(bullet)+" "+renderMarkdownLite(rest))
			continue
		}
		out = append(out, renderMarkdownLite(line))
	}
	return out
}

// headerLevel returns 1, 2, or 3 for `# `, `## `, `### ` prefixes and 0
// otherwise. Anything above level 3 is treated as body text to avoid
// overrendering very heavy hashes.
func headerLevel(trimmed string) int {
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

// bulletLine detects `- foo`, `* foo`, `+ foo`, or `N. foo` and returns a
// pretty bullet glyph and the remaining text.
func bulletLine(line string) (bullet string, rest string, ok bool) {
	// Preserve indent — nested bullets indent by 2+ spaces.
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
	// Numbered list: digits + ". "
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

// --- status chips & runtime card -----------------------------------------

type toolChip struct {
	Name         string
	Status       string // "ok", "failed", "running"
	DurationMs   int
	Preview      string
	Step         int
	OutputTokens int // estimated tokens returned by the tool (0 when unknown)
	Truncated    bool
	// RTK-style output compression stats (0 when unknown). CompressedChars
	// is the model-bound payload size after compression; SavedChars is the
	// number of characters dropped from the raw tool output.
	CompressedChars int
	SavedChars      int
	CompressionPct  int // 0–99, how much of the raw output was dropped
}

func renderToolChip(chip toolChip, width int) string {
	icon, styleFor := chipIconStyle(chip.Status)
	name := strings.TrimSpace(chip.Name)
	if name == "" {
		name = "tool"
	}
	head := styleFor.Render(icon + " " + name)
	meta := []string{}
	if chip.Step > 0 {
		meta = append(meta, fmt.Sprintf("step %d", chip.Step))
	}
	if chip.DurationMs > 0 {
		meta = append(meta, fmt.Sprintf("%dms", chip.DurationMs))
	}
	if chip.OutputTokens > 0 {
		if chip.Truncated {
			meta = append(meta, fmt.Sprintf("+%s tok⚠", formatToolTokenCount(chip.OutputTokens)))
		} else {
			meta = append(meta, fmt.Sprintf("+%s tok", formatToolTokenCount(chip.OutputTokens)))
		}
	}
	if chip.SavedChars > 0 {
		if chip.CompressionPct > 0 {
			meta = append(meta, fmt.Sprintf("rtk −%s (%d%%)", formatToolTokenCount(chip.SavedChars), chip.CompressionPct))
		} else {
			meta = append(meta, fmt.Sprintf("rtk −%s", formatToolTokenCount(chip.SavedChars)))
		}
	}
	status := strings.TrimSpace(chip.Status)
	if status != "" && status != "ok" && status != "running" {
		meta = append(meta, status)
	}
	head1 := head
	if len(meta) > 0 {
		head1 += " " + subtleStyle.Render("· "+strings.Join(meta, " · "))
	}
	preview := strings.TrimSpace(chip.Preview)
	if preview == "" {
		return truncateSingleLine(head1, width)
	}
	single := head1 + " " + subtleStyle.Render("· "+preview)
	if ansi.StringWidth(single) <= width {
		return single
	}
	// Preview won't fit — render head on one line, indented preview below so
	// nothing important gets silently clipped.
	second := max(width-2, 16)
	return truncateSingleLine(head1, width) + "\n  " + subtleStyle.Render(truncateSingleLine(preview, second))
}

// formatToolTokenCount renders a tool's output token estimate in the chip
// — compact for small counts, "1.2k" style once four digits are needed.
func formatToolTokenCount(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%.1fk", float64(n)/1000)
}

// renderInlineToolChips paints a compact multi-row tool strip below an
// assistant bubble — one line per chip, indented so it visually hangs
// under the message. Each chip shows icon + name + (step) + (duration) +
// short preview, colour-coded by status. Wraps at `width` columns.
func renderInlineToolChips(chips []toolChip, width int) string {
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
		// renderToolChip may return a two-line block when the preview
		// can't fit alongside the head — indent each line.
		for j, line := range strings.Split(renderToolChip(chip, inner), "\n") {
			if j > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(indent)
			b.WriteString(line)
		}
	}
	return b.String()
}

func chipIconStyle(status string) (string, lipgloss.Style) {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "ok", "success", "done":
		return "✓", okStyle
	case "failed", "error", "fail":
		return "✗", failStyle
	case "running", "start", "pending":
		return "◌", infoStyle
	case "compact", "compacted":
		return "⇵", accentStyle
	case "budget", "budget_exhausted":
		return "✦", warnStyle
	case "handoff":
		return "⇨", accentStyle
	case "subagent-running":
		return "◈", accentStyle
	case "subagent-ok":
		return "◈", okStyle
	case "subagent-failed":
		return "◈", failStyle
	default:
		return "•", subtleStyle
	}
}

// runtimeSummary is the compact one-line summary of the agent loop state.
// Replaces the old 9-line key=value dump.
type runtimeSummary struct {
	Active       bool
	Phase        string
	Step         int
	MaxSteps     int
	ToolRounds   int
	LastTool     string
	LastStatus   string
	LastDuration int
	Provider     string
	Model        string
}

// renderRuntimeCard paints the live agent activity chip shown above the input
// box. The chat header already shows provider/model, the agent phase, and
// step X/Y — so this card only surfaces what the header can't: the last tool
// that ran and a rolling tool count. Returns empty when nothing useful is
// available, which drops the decorative blank line above it too.
func renderRuntimeCard(rs runtimeSummary, width int) string {
	if !rs.Active {
		return ""
	}
	parts := []string{}
	if rs.ToolRounds > 0 {
		parts = append(parts, subtleStyle.Render(fmt.Sprintf("tools %d", rs.ToolRounds)))
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
	return truncateSingleLine(strings.Join(parts, "  ·  "), width)
}

// --- message card --------------------------------------------------------

// messageHeaderInfo is the per-message metadata rendered above each bubble.
// The renderer wraps role + timestamp + tokens + duration + tool usage into a
// single scannable header line so the reader can see at a glance how expensive
// a turn was and whether tools fired.
type messageHeaderInfo struct {
	Role         string
	Timestamp    time.Time
	TokenCount   int
	DurationMs   int
	ToolCalls    int
	ToolFailures int
	Streaming    bool
	SpinnerFrame int
}

// spinnerFrames is the braille dot cycle used for the live streaming glyph.
// Ten frames at ~125ms interval = one revolution per ~1.25s — calm enough to
// read, alive enough to reassure.
var spinnerFrames = [...]string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// spinnerFrame returns the frame glyph for the given counter. Safe for any int.
func spinnerFrame(frame int) string {
	if frame < 0 {
		frame = -frame
	}
	return spinnerFrames[frame%len(spinnerFrames)]
}

func renderMessageHeader(info messageHeaderInfo) string {
	parts := []string{roleBadge(info.Role)}
	if info.Streaming {
		parts = append(parts, infoStyle.Bold(true).Render(spinnerFrame(info.SpinnerFrame)))
	}
	if !info.Timestamp.IsZero() {
		parts = append(parts, subtleStyle.Render(info.Timestamp.Format("15:04:05")))
	}
	if info.DurationMs > 0 {
		parts = append(parts, subtleStyle.Render(formatDurationChip(info.DurationMs)))
	}
	if info.TokenCount > 0 {
		parts = append(parts, subtleStyle.Render(fmt.Sprintf("%s tok", formatThousands(info.TokenCount))))
	}
	if info.ToolCalls > 0 {
		chip := fmt.Sprintf("⚒ %d", info.ToolCalls)
		if info.ToolFailures > 0 {
			parts = append(parts, accentStyle.Render(chip)+" "+failStyle.Bold(true).Render(fmt.Sprintf("✗ %d", info.ToolFailures)))
		} else {
			parts = append(parts, accentStyle.Render(chip))
		}
	}
	return strings.Join(parts, " ")
}

// formatDurationChip returns a compact human-readable duration: 850ms, 2.3s,
// 1m04s. Kept tight so the message header stays on one line.
func formatDurationChip(ms int) string {
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

// renderMessageBubble renders a chat message as a left-bar "bubble" with the
// role-coloured accent stripe. Content is markdown-lite rendered. Multi-line
// content keeps the stripe on every line. Width is the total line width.
func renderMessageBubble(role, content, header string, width int) string {
	style := roleLineStyle(role)
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
	for _, line := range renderMarkdownBlocks(content) {
		out = append(out, bar+" "+truncateSingleLine(line, width-2))
	}
	return strings.Join(out, "\n")
}

// renderDivider returns a subtle horizontal rule.
func renderDivider(width int) string {
	if width <= 0 {
		return ""
	}
	if width > 200 {
		width = 200
	}
	return dividerStyle.Render(strings.Repeat("─", width))
}

// renderRuntimeCardFramed wraps renderRuntimeCard in a coloured rounded box.
func renderRuntimeCardFramed(rs runtimeSummary, width int) string {
	inner := renderRuntimeCard(rs, width-4)
	if strings.TrimSpace(inner) == "" {
		return ""
	}
	return runtimeCardStyle.Width(width).Render(inner)
}

// renderInputBox wraps a prompt line in a coloured rounded frame.
func renderInputBox(line string, width int) string {
	if width < 10 {
		return inputLineStyle.Render(line)
	}
	inner := truncateSingleLine(line, width-4)
	return inputBoxStyle.Width(width).Render(inputLineStyle.Render(inner))
}

// renderBanner renders the top-of-app banner with a chunky ▌▐ accent.
func renderBanner(title, subtitle string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "DFMC"
	}
	accent := bannerStyle.Render("▌▌")
	main := titleStyle.Render(" " + title + " ")
	sub := ""
	if s := strings.TrimSpace(subtitle); s != "" {
		sub = "  " + subtleStyle.Render(s)
	}
	return accent + " " + main + sub
}

// --- chat header / empty-state / streaming -------------------------------

// chatHeaderInfo is the data shown in the compact chat header — who the
// user is talking to, how much context is available, and whether the agent
// is currently working. When Slim is true the stats panel is visible on the
// right; the header drops the static fields (provider/model/ctx/tools) that
// the panel owns and keeps only transient alerts (streaming/parked/queued).
type chatHeaderInfo struct {
	Provider      string
	Model         string
	Configured    bool
	MaxContext    int
	ContextTokens int
	Pinned        string
	ToolsEnabled  bool
	Streaming     bool
	AgentActive   bool
	AgentPhase    string
	AgentStep     int
	AgentMax      int
	QueuedCount   int
	Parked        bool
	PendingNotes  int
	Slim          bool
	// ActiveTools / ActiveSubagents are live counts of in-flight tool calls
	// and delegated sub-agents. They are shown as compact header badges when
	// > 0 so the user can see fan-out (batch / delegate_task) in real time.
	ActiveTools     int
	ActiveSubagents int
}

// renderChatHeader returns 1 pre-styled line summarising chat state.
// Order of segments: CHAT icon · provider/model · token meter · mode · agent · pinned.
func renderChatHeader(info chatHeaderInfo, width int) string {
	brand := titleStyle.Render(" CHAT ")
	segments := []string{brand}

	if !info.Slim {
		providerTrim := strings.TrimSpace(info.Provider)
		modelTrim := strings.TrimSpace(info.Model)
		provider := blankFallback(providerTrim, "no-provider")
		model := blankFallback(modelTrim, "no-model")

		providerPill := accentStyle.Bold(true).Render(provider)
		modelPill := boldStyle.Render(model)
		switch {
		case providerTrim == "":
			providerPill = failStyle.Bold(true).Render("⚠ no provider")
			modelPill = subtleStyle.Render(model)
		case !info.Configured:
			providerPill = warnStyle.Bold(true).Render(provider + "⚠")
		}
		who := providerPill + subtleStyle.Render(" / ") + modelPill
		meter := renderTokenMeter(info.ContextTokens, info.MaxContext)

		tools := subtleStyle.Render("tools off")
		if info.ToolsEnabled {
			tools = okStyle.Render("tools on")
		}
		segments = append(segments, who, meter)
		segments = append(segments, renderChatModeSegment(info))
		segments = append(segments, tools)
	} else {
		// Slim header: only show the mode chip when something is actively
		// happening. A resting chat gets just the brand + alerts, letting the
		// panel carry every stable fact.
		if info.Streaming || info.AgentActive {
			segments = append(segments, renderChatModeSegment(info))
		}
	}

	if info.Parked {
		segments = append(segments, warnStyle.Bold(true).Render("⏸ parked — /continue"))
	}
	if info.ActiveTools > 0 {
		segments = append(segments, infoStyle.Bold(true).Render(fmt.Sprintf("◌ tools %d", info.ActiveTools)))
	}
	if info.ActiveSubagents > 0 {
		segments = append(segments, accentStyle.Bold(true).Render(fmt.Sprintf("◈ subagents %d", info.ActiveSubagents)))
	}
	if info.QueuedCount > 0 {
		segments = append(segments, accentStyle.Bold(true).Render(fmt.Sprintf("▸ queued %d", info.QueuedCount)))
	}
	if info.PendingNotes > 0 {
		segments = append(segments, infoStyle.Render(fmt.Sprintf("✎ btw %d", info.PendingNotes)))
	}
	sep := subtleStyle.Render("  ·  ")
	head := truncateSingleLine(strings.Join(segments, sep), width)
	if pinned := strings.TrimSpace(info.Pinned); pinned != "" {
		pinLine := accentStyle.Render("  ◆ pinned: ") + boldStyle.Render(fileMarker(pinned))
		return head + "\n" + pinLine
	}
	return head
}

// renderChatModeSegment returns the mode chip (ready/streaming/agent phase+step)
// as a single lipgloss-styled string. Shared between the full and slim header
// variants and the stats panel so the wording never drifts.
func renderChatModeSegment(info chatHeaderInfo) string {
	switch {
	case info.Streaming:
		return infoStyle.Bold(true).Render("◉ streaming")
	case info.AgentActive:
		phase := blankFallback(strings.TrimSpace(info.AgentPhase), "working")
		if info.AgentStep > 0 && info.AgentMax > 0 {
			return accentStyle.Bold(true).Render(fmt.Sprintf("◉ agent %s · %d/%d", phase, info.AgentStep, info.AgentMax))
		}
		if info.AgentStep > 0 {
			return accentStyle.Bold(true).Render(fmt.Sprintf("◉ agent %s · step %d", phase, info.AgentStep))
		}
		return accentStyle.Bold(true).Render("◉ agent " + phase)
	default:
		return okStyle.Render("● ready")
	}
}

// renderTokenMeter returns "used / max (pct%)" with colour thresholds:
// <60% ok, 60-85% warn, >85% fail. Unknown max falls back to plain count.
func renderTokenMeter(used, max int) string {
	if max <= 0 {
		if used <= 0 {
			return subtleStyle.Render("ctx —")
		}
		return subtleStyle.Render("ctx ") + boldStyle.Render(formatThousands(used)+" tok")
	}
	pct := 0
	if used > 0 {
		pct = int((int64(used) * 100) / int64(max))
	}
	style := okStyle
	switch {
	case pct >= 85:
		style = failStyle
	case pct >= 60:
		style = warnStyle
	}
	label := fmt.Sprintf("%s / %s (%d%%)", formatThousands(used), formatThousands(max), pct)
	return subtleStyle.Render("ctx ") + style.Bold(true).Render(label)
}

// renderStepBar draws a compact [████░░░░░░] step/max chip for the agent-loop
// step budget. Green when there's room, yellow when nearing the cap, red when
// within one step of parking. `cells` is the bar width in rune-cells.
func renderStepBar(step, maxSteps, cells int) string {
	if cells < 4 {
		cells = 4
	}
	if maxSteps <= 0 {
		return subtleStyle.Render(fmt.Sprintf("step %d", step))
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
	style := okStyle
	remaining := maxSteps - step
	switch {
	case remaining <= 1:
		style = failStyle
	case remaining <= 3:
		style = warnStyle
	}
	bar := style.Render(strings.Repeat("█", filled)) + subtleStyle.Render(strings.Repeat("░", cells-filled))
	label := fmt.Sprintf(" %d/%d", step, maxSteps)
	return "[" + bar + "]" + style.Bold(true).Render(label)
}

// renderContextBar draws a compact progress bar [OOOO-----] followed by
// used/max (pct%), coloured by the same ok/warn/fail thresholds as
// renderTokenMeter. `cells` controls the bar width in rune-cells; 10 is a
// sensible default for the footer. When max is unknown it falls back to the
// plain meter.
func renderContextBar(used, max, cells int) string {
	if cells < 4 {
		cells = 4
	}
	if max <= 0 {
		return renderTokenMeter(used, max)
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
	style := okStyle
	switch {
	case pct >= 85:
		style = failStyle
	case pct >= 60:
		style = warnStyle
	}
	bar := style.Render(strings.Repeat("█", filled)) + subtleStyle.Render(strings.Repeat("░", cells-filled))
	label := fmt.Sprintf("%s/%s (%d%%)", compactTokens(used), compactTokens(max), pct)
	return "[" + bar + "] " + style.Bold(true).Render(label)
}

// compactTokens returns 120000 as "120k" and 1_500_000 as "1.5M" — tighter
// than formatThousands for status-line real estate.
func compactTokens(n int) string {
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

// formatThousands returns n with comma thousands separators (e.g. 12,450).
func formatThousands(n int) string {
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

// starterPrompt is a single actionable suggestion shown when the chat
// transcript is empty. Keys 1-9 insert the prepared input directly.
type starterPrompt struct {
	Key   string
	Title string
	Cmd   string
	Hint  string
}

func defaultStarterPrompts() []starterPrompt {
	return []starterPrompt{
		{Key: "1", Title: "Review this project", Cmd: "/review", Hint: "quality, risks, suggestions"},
		{Key: "2", Title: "Explain a file", Cmd: "/explain @", Hint: "press @ to pick a file"},
		{Key: "3", Title: "Analyze architecture", Cmd: "/analyze", Hint: "symbols, hotspots, deps"},
		{Key: "4", Title: "Map the codebase", Cmd: "/codemap", Hint: "dependency graph, cycles"},
		{Key: "5", Title: "Find bugs & smells", Cmd: "/security", Hint: "security + correctness scan"},
		{Key: "6", Title: "Draft a refactor plan", Cmd: "/refactor", Hint: "stepwise, low-risk"},
	}
}

// starterTemplateForDigit returns the composer text to load when the user
// presses a digit hotkey on the empty-welcome screen. Returns ok=false for
// any digit that isn't wired to a starter.
func starterTemplateForDigit(r rune) (string, bool) {
	if r < '1' || r > '9' {
		return "", false
	}
	idx := int(r - '1')
	prompts := defaultStarterPrompts()
	if idx >= len(prompts) {
		return "", false
	}
	return prompts[idx].Cmd, true
}

// renderStarterPrompts returns the empty-state block — a friendly welcome +
// numbered actionable suggestions. Callers append these to the line buffer.
// The width argument is advisory — each line is truncated to that width so
// pillars align inside narrow terminals. When configured is false the block
// is prefaced with a setup banner so a fresh user isn't left guessing.
func renderStarterPrompts(width int, configured bool) []string {
	prompts := defaultStarterPrompts()
	if width <= 0 {
		width = 100
	}
	lines := []string{""}
	if !configured {
		lines = append(lines,
			failStyle.Bold(true).Render("⚠ No provider configured"),
			subtleStyle.Render("  Press ")+accentStyle.Bold(true).Render("f5")+subtleStyle.Render(" for the Setup tab, or type ")+codeStyle.Render("/provider")+subtleStyle.Render(" to pick one — starters need a model to run."),
			"",
		)
	}
	lines = append(lines,
		boldStyle.Render(accentStyle.Render("Welcome — what would you like DFMC to do?")),
		subtleStyle.Render("  Pick a starter, type a question, or use "+codeStyle.Render("@file")+" / "+codeStyle.Render("/command")+"."),
		"",
	)
	for _, p := range prompts {
		key := titleStyle.Render(" " + p.Key + " ")
		title := boldStyle.Render(p.Title)
		cmd := codeStyle.Render(p.Cmd)
		hint := subtleStyle.Render("— " + p.Hint)
		// Keep the visible portion ≤ width so ANSI codes don't push layout.
		raw := fmt.Sprintf("   %-2s  %-26s  %-18s  %s", p.Key, p.Title, p.Cmd, "— "+p.Hint)
		if len([]rune(raw)) > width {
			lines = append(lines, truncateSingleLine(raw, width))
			continue
		}
		lines = append(lines, "  "+key+"  "+title+"  "+cmd+"  "+hint)
	}
	lines = append(lines,
		"",
		subtleStyle.Render("  Tips: "+accentStyle.Render("enter")+" send · "+accentStyle.Render("@")+" file mention · "+accentStyle.Render("/")+" commands · "+accentStyle.Render("ctrl+p")+" palette · "+accentStyle.Render("alt+1..6")+" tabs"),
	)
	return lines
}

// renderStreamingIndicator returns a live spinner line for active turns.
// Shown below the input box while a response is being generated. The frame
// argument advances on tea.Tick so the glyph animates; when the caller has no
// frame counter (tests, stills), passing 0 still reads fine.
func renderStreamingIndicator(phase string, frame int) string {
	phase = strings.TrimSpace(phase)
	if phase == "" {
		phase = "drafting reply"
	}
	glyph := spinnerFrame(frame)
	return infoStyle.Bold(true).Render(glyph+" "+phase) + " " + subtleStyle.Render("· esc cancels · tokens stream live")
}

// renderResumeBanner paints the yellow-accented "agent parked" prompt shown
// above the composer when the tool loop has hit its step cap. The user can
// Enter to resume, Esc to dismiss, or type a note first to steer the
// continuation.
func renderResumeBanner(step, maxSteps, width int) string {
	if width < 20 {
		width = 20
	}
	title := warnStyle.Bold(true).Render("⏸ Agent loop parked")
	progress := ""
	if maxSteps > 0 {
		progress = subtleStyle.Render(fmt.Sprintf(" at step %d/%d", step, maxSteps))
	} else if step > 0 {
		progress = subtleStyle.Render(fmt.Sprintf(" at step %d", step))
	}
	hint := subtleStyle.Render("  ↵ enter resumes") + subtleStyle.Render(" · ") +
		subtleStyle.Render("esc dismisses") + subtleStyle.Render(" · ") +
		subtleStyle.Render("type a note first to steer /continue")
	head := truncateSingleLine(title+progress, width)
	body := truncateSingleLine(hint, width)
	return resumeBannerStyle.Width(width).Render(head + "\n" + body)
}

// --- right-side stats panel ----------------------------------------------

// statsPanelWidth is the fixed column count the stats panel reserves. Tuned
// so common model names (claude-opus-4-6, gpt-5.4-turbo, glm-5.1) + short
// labels fit on a line without clipping.
const statsPanelWidth = 38

// statsPanelMinContentWidth is the threshold below which the stats panel is
// suppressed entirely — a chat viewport narrower than ~80 columns would be
// unreadable if the panel stole another 38. The caller (renderActiveView)
// checks this before deciding to compose the panel.
const statsPanelMinContentWidth = 120

// statsPanelInfo is the full snapshot the panel needs each frame. The model
// assembles it from status / git / agent loop / session state.
type statsPanelInfo struct {
	Provider       string
	Model          string
	Configured     bool
	ContextTokens  int
	MaxContext     int
	Streaming      bool
	AgentActive    bool
	AgentPhase     string
	AgentStep      int
	AgentMaxSteps  int
	ToolRounds     int
	LastTool       string
	LastStatus     string
	LastDurationMs int
	Parked         bool
	QueuedCount    int
	PendingNotes   int
	ToolsEnabled   bool
	ToolCount      int
	Branch         string
	Dirty          bool
	Detached       bool
	Inserted       int
	Deleted        int
	SessionElapsed time.Duration
	MessageCount   int
	Pinned         string
	// Cumulative RTK-style tool-output compression stats for the session,
	// aggregated across all tool:result events.
	CompressionSavedChars int
	CompressionRawChars   int
}

// renderStatsPanel paints the right-hand "mission control" column for the
// chat tab. Fixed width, height set by the caller so it tiles nicely next to
// the chat body. Sections: PROVIDER · CONTEXT · AGENT · TOOLS · GIT · SESSION.
// Each section prints a bold title followed by 1–3 indented value lines.
func renderStatsPanel(info statsPanelInfo, height int) string {
	if height < 6 {
		height = 6
	}
	inner := statsPanelWidth - 4
	if inner < 16 {
		inner = 16
	}

	lines := []string{}
	divider := dividerStyle.Render(strings.Repeat("─", inner))
	addSection := func(icon, title string, body []string) {
		if len(body) == 0 {
			return
		}
		if len(lines) > 0 {
			lines = append(lines, divider)
		}
		header := accentStyle.Bold(true).Render(icon) + " " + sectionTitleStyle.Render(title)
		lines = append(lines, header)
		for _, b := range body {
			if b == "" {
				lines = append(lines, "")
				continue
			}
			lines = append(lines, "  "+truncateSingleLine(b, inner))
		}
	}

	// PROVIDER -------------------------------------------------------------
	providerTrim := strings.TrimSpace(info.Provider)
	modelTrim := strings.TrimSpace(info.Model)
	var providerBody []string
	switch {
	case providerTrim == "":
		providerBody = []string{
			failStyle.Bold(true).Render("⚠ no provider"),
			subtleStyle.Render("f5 setup · /provider"),
		}
	case !info.Configured:
		providerBody = []string{
			warnStyle.Bold(true).Render(providerTrim + " ⚠"),
			boldStyle.Render(blankFallback(modelTrim, "-")),
			subtleStyle.Render("unconfigured — add API key"),
		}
	default:
		providerBody = []string{
			accentStyle.Bold(true).Render(providerTrim),
			boldStyle.Render(blankFallback(modelTrim, "-")),
		}
	}
	addSection("◉", "PROVIDER", providerBody)

	// CONTEXT --------------------------------------------------------------
	contextBody := []string{renderContextBar(info.ContextTokens, info.MaxContext, 10)}
	if info.MaxContext > 0 {
		remaining := max(info.MaxContext-info.ContextTokens, 0)
		contextBody = append(contextBody, subtleStyle.Render(fmt.Sprintf("%s free · %s used", compactTokens(remaining), compactTokens(info.ContextTokens))))
	}
	addSection("▦", "CONTEXT", contextBody)

	// AGENT ----------------------------------------------------------------
	agentBody := []string{renderChatModeSegment(chatHeaderInfo{
		Streaming:   info.Streaming,
		AgentActive: info.AgentActive,
		AgentPhase:  info.AgentPhase,
		AgentStep:   info.AgentStep,
		AgentMax:    info.AgentMaxSteps,
	})}
	if info.AgentActive && info.AgentMaxSteps > 0 {
		agentBody = append(agentBody, renderStepBar(info.AgentStep, info.AgentMaxSteps, 14))
	}
	if info.ToolRounds > 0 {
		agentBody = append(agentBody, subtleStyle.Render(fmt.Sprintf("tool rounds: %d", info.ToolRounds)))
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
			warnStyle.Bold(true).Render("⏸ parked"),
			subtleStyle.Render("/continue to resume"),
		)
	}
	if info.QueuedCount > 0 {
		agentBody = append(agentBody, accentStyle.Bold(true).Render(fmt.Sprintf("▸ queued %d", info.QueuedCount)))
	}
	if info.PendingNotes > 0 {
		agentBody = append(agentBody, infoStyle.Render(fmt.Sprintf("✎ btw %d", info.PendingNotes)))
	}
	addSection("⚙", "AGENT", agentBody)

	// TOOLS ----------------------------------------------------------------
	toolsBody := []string{}
	if info.ToolsEnabled {
		line := okStyle.Render("enabled")
		if info.ToolCount > 0 {
			line += subtleStyle.Render(fmt.Sprintf("  %d registered", info.ToolCount))
		}
		toolsBody = append(toolsBody, line)
	} else {
		toolsBody = append(toolsBody, subtleStyle.Render("off"))
	}
	if info.CompressionSavedChars > 0 {
		pct := 0
		if info.CompressionRawChars > 0 {
			pct = int((int64(info.CompressionSavedChars) * 100) / int64(info.CompressionRawChars))
		}
		label := fmt.Sprintf("rtk saved %s chars", compactTokens(info.CompressionSavedChars))
		if pct > 0 {
			label += fmt.Sprintf(" (%d%%)", pct)
		}
		toolsBody = append(toolsBody, okStyle.Render(label))
	}
	addSection("⚒", "TOOLS", toolsBody)

	// GIT ------------------------------------------------------------------
	branch := strings.TrimSpace(info.Branch)
	if branch != "" {
		chip := boldStyle.Render(branch)
		if info.Dirty {
			chip += warnStyle.Render("*")
		}
		if info.Detached {
			chip += subtleStyle.Render(" (detached)")
		}
		gitBody := []string{chip}
		if info.Inserted > 0 || info.Deleted > 0 {
			churn := okStyle.Render(fmt.Sprintf("+%d", info.Inserted)) +
				subtleStyle.Render(" / ") +
				failStyle.Render(fmt.Sprintf("-%d", info.Deleted))
			gitBody = append(gitBody, churn)
		}
		addSection("⎇", "GIT", gitBody)
	}

	// SESSION --------------------------------------------------------------
	sessionHead := boldStyle.Render(formatSessionDuration(info.SessionElapsed))
	if info.MessageCount > 0 {
		sessionHead += subtleStyle.Render(fmt.Sprintf(" · %d msgs", info.MessageCount))
	}
	sessionBody := []string{sessionHead}
	if pinned := strings.TrimSpace(info.Pinned); pinned != "" {
		sessionBody = append(sessionBody, accentStyle.Render("◆ ")+boldStyle.Render(fileMarker(pinned)))
	}
	addSection("⏱", "SESSION", sessionBody)

	// Footer hint so users discover the toggle without grepping for ctrl+s.
	lines = append(lines, divider, subtleStyle.Render("  ctrl+s hide · ctrl+h keys"))

	// Pad to requested height so vertical join aligns cleanly. If content is
	// taller than available height we truncate from the bottom (hint line
	// goes last so it's what gets cut first — preferable to hiding live state).
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}

	body := strings.Join(lines, "\n")
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorPanelBorder).
		Padding(0, 1).
		Width(statsPanelWidth).
		Height(height)
	return box.Render(body)
}
