package theme

// stats_panel.go — right-hand chat stats panel scaffolding. Renders the
// outer box, mode tabs, state line, and footer; row content for every
// section is delegated to a sibling so each domain can grow without this
// file ballooning:
//
//   stats_panel_runtime.go   provider/context/tokens/loop/tools/git/session
//                            + provider list + provider routing
//   stats_panel_workflow.go  orchestration/workflow/todos/tasks/subagents/
//                            drive/recent
//   stats_panel_next.go      mode-aware "what's next" and critical hints
//
// This file owns the StatsPanelMode → section ordering switch, the
// section/footer/line primitives on statsPanelBuilder, and the small
// usage helpers (window/used/pct) that runtime and workflow rows both
// read. The panel is kept dense on purpose: it should behave like an
// operator console, not a second transcript.

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const (
	StatsPanelWidth = 38
	StatsPanelBoostWidthMin = 48
	// StatsPanelBoostMinContentWidth is the threshold above which the
	// panel is allowed to grow into "boost mode" (≥48 cols); below this
	// the panel stays at the compact StatsPanelWidth.
	StatsPanelBoostMinContentWidth = 110
	// StatsPanelMinContentWidth is the threshold below which the chat
	// body is too narrow to share with the panel and the panel hides.
	// The previous 120-col threshold meant a stock 100×30 terminal
	// never saw the panel — users opened DFMC and asked "where did the
	// stats panel go?" The new 88-col threshold fits a ~50-col chat
	// body next to the 38-col panel (plus the 2-col separator) so the
	// panel appears on any terminal ≥ ~94 cols, which is the floor for
	// usable chat anyway.
	StatsPanelMinContentWidth = 88
)

type statsPanelBuilder struct {
	width   int
	lines   []string
	divider string
}

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

	b := statsPanelBuilder{
		width:   inner,
		divider: DividerStyle.Render(strings.Repeat("-", inner)),
	}
	b.line(RenderStatsPanelModeTabs(mode, inner))
	if info.Boosted {
		focus := "FOCUS MODE - expanded"
		if info.FocusLocked {
			focus = "FOCUS MODE - locked"
		} else if info.BoostSeconds > 0 {
			focus = fmt.Sprintf("%s - %ds", focus, info.BoostSeconds)
		}
		b.line(AccentStyle.Bold(true).Render(focus))
	}
	b.line(statsPanelStateLine(info, inner))

	switch mode {
	case StatsPanelModeTodos:
		b.section("TODO STATE", todoRows(info, inner))
		b.section("NEXT", nextRows(info, mode))
		b.section("LIVE LOOP", loopRows(info))
		b.section("CONTEXT", contextRows(info))
	case StatsPanelModeTasks:
		b.section("TASK GRAPH", taskRows(info))
		b.section("DRIVE", driveRows(info))
		b.section("NEXT", nextRows(info, mode))
		b.section("ORCHESTRATION MAP", orchestrationMapRows(info, inner))
		b.section("LIVE LOOP", loopRows(info))
	case StatsPanelModeSubagents:
		b.section("SUBAGENTS", subagentRows(info))
		b.section("NEXT", nextRows(info, mode))
		b.section("LIVE LOOP", loopRows(info))
		b.section("RECENT", recentRows(info, 4))
	case StatsPanelModeProviders:
		b.section("ACTIVE", providerActiveRows(info))
		b.section("ROUTING", providerRoutingRows(info))
		b.section("NEXT", nextRows(info, mode))
		b.section("PROVIDERS", providerListRows(info))
		b.section("CONTEXT", contextRows(info))
		b.section("TOKENS", tokenRows(info))
		b.section("SESSION", sessionRows(info))
	default:
		b.section("PROVIDER", providerRows(info))
		b.section("NEXT", nextRows(info, mode))
		b.section("CONTEXT", contextRows(info))
		b.section("TOKENS", tokenRows(info))
		b.section("TOOL LOOP", loopRows(info))
		b.section("TOOLS", toolsRows(info))
		b.section("WORKFLOW", workflowRows(info, inner))
		if rows := gitRows(info); len(rows) > 0 {
			b.section("GIT", rows)
		}
		b.section("SESSION", sessionRows(info))
	}

	footerRows := statsPanelFooterRows(mode)
	if info.FocusLocked {
		footerRows = []string{"esc unlock | retarget alt+a/s/d/f/p", statsPanelModeActionHint(mode) + " | ctrl+h"}
	} else if info.Boosted {
		footerRows = []string{"alt+a/s/d/f/p again locks", "ctrl+s hide | ctrl+h | " + statsPanelModeActionHint(mode)}
	}
	b.footer(footerRows, height, info.StatsPanelScroll)

	body := strings.Join(b.lines, "\n")

	// Show scroll indicator when content overflows the panel height.
	if info.StatsPanelScroll > 0 || len(b.lines) > height {
		totalContent := len(b.lines)
		visible := height - len(footerRows) - 1 // exclude divider + footer rows
		if visible < 1 {
			visible = 1
		}
		scrollMax := totalContent - visible
		if scrollMax < 0 {
			scrollMax = 0
		}
		if info.StatsPanelScroll > 0 || scrollMax > 0 {
			pct := 0
			if scrollMax > 0 {
				pct = int(float64(info.StatsPanelScroll) / float64(scrollMax) * 100)
				if pct > 100 {
					pct = 100
				}
			}
			body += "\n" + AccentStyle.Faint(true).Render(fmt.Sprintf(" ▲ %d%% ", pct))
		}
	}
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPanelBorder).
		Padding(0, 1).
		Width(panelWidth).
		Height(height)
	return box.Render(body)
}

func (b *statsPanelBuilder) line(text string) {
	b.lines = append(b.lines, TruncateSingleLine(text, b.width))
}

func (b *statsPanelBuilder) section(title string, rows []string) {
	rows = cleanPanelRows(rows)
	if len(rows) == 0 {
		return
	}
	if len(b.lines) > 0 {
		b.lines = append(b.lines, b.divider)
	}
	b.lines = append(b.lines, panelSectionTitle(title))
	for _, row := range rows {
		b.lines = append(b.lines, "  "+TruncateSingleLine(row, max(b.width-2, 8)))
	}
}

func (b *statsPanelBuilder) footer(rows []string, height int, scroll int) {
	footer := make([]string, 0, len(rows))
	for _, row := range rows {
		row = strings.TrimSpace(row)
		if row != "" {
			footer = append(footer, SubtleStyle.Render(TruncateSingleLine(row, b.width)))
		}
	}
	if len(footer) == 0 {
		footer = append(footer, "")
	}
	b.lines = append(b.lines, b.divider)
	b.lines = append(b.lines, footer...)
	if len(b.lines) > height {
		keep := height - 1 - len(footer)
		if keep < 0 {
			keep = 0
		}
		head := append([]string{}, b.lines[:keep]...)
		head = append(head, b.divider)
		b.lines = append(head, footer...)
	}
	for len(b.lines) < height {
		b.lines = append(b.lines, "")
	}
	// Apply scroll offset: drop lines from the top of body content
	// (above the footer). The visible window excludes the footer rows.
	if scroll > 0 && len(b.lines) > height {
		// body lines = total - footer rows - divider
		footerRowCount := len(footer) + 1 // footer lines + their divider
		bodyEnd := len(b.lines) - footerRowCount
		if bodyEnd < 0 {
			bodyEnd = 0
		}
		start := scroll
		if start > bodyEnd {
			start = bodyEnd
		}
		// Reconstruct: header portion + scrolled body portion + footer
		headerEnd := 2 // mode-tabs line + optional focus line
		if start < headerEnd {
			start = headerEnd
		}
		visibleBody := b.lines[start:bodyEnd]
		remainder := b.lines[bodyEnd:]
		b.lines = append(b.lines[:0], b.lines[:headerEnd]...)
		b.lines = append(b.lines, visibleBody...)
		b.lines = append(b.lines, remainder...)
	}
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
	parts := make([]string, 0, len(items)+1)
	parts = append(parts, AccentStyle.Bold(true).Render("STATS"))
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

