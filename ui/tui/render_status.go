// render_status.go — the F9 Status panel, rebuilt around the panel
// card primitive (panel_cards.go). Replaces the older bullet-list
// layout in render_panels.go's renderStatusView.
//
// Layout:
//   - Top banner: project root + overall health chip (engine state)
//   - Card grid (2-column on wide terminals, 1 on narrow):
//       1. PROJECT     — root path, branch, dirty/clean
//       2. PROVIDER    — provider/model + connectivity + profile chain
//       3. AST         — backend, languages, parse metrics
//       4. CODEMAP     — files, symbols, deps, hotspots count
//       5. MEMORY      — degraded indicator + reason (only when relevant)
//       6. CONTEXT IN  — last build summary (only when populated)
//   - Footer hints: arrow keys + r refresh + enter to jump
//
// Arrow-key navigation lets the user move between cards; Enter on a
// card jumps to the related detail tab.

package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

// humanizeEngineState renders the integer enum as a readable string
// for the Project card's State row.
func humanizeEngineState(s engine.EngineState) string {
	switch s {
	case engine.StateCreated:
		return "created"
	case engine.StateInitializing:
		return "initializing"
	case engine.StateReady:
		return "ready"
	case engine.StateServing:
		return "serving"
	case engine.StateShuttingDown:
		return "shutting down"
	case engine.StateStopped:
		return "stopped"
	default:
		return fmt.Sprintf("state-%d", int(s))
	}
}

// renderStatusView is the F9 Status panel entrypoint. The legacy
// implementation lived in render_panels.go and has been deleted in
// favour of this rebuilt version.
func (m Model) renderStatusViewV2(width int) string {
	return m.renderStatusViewSized(width, 24)
}

func (m Model) renderStatusViewSized(width, height int) string {
	_ = height
	cards, jumpTargets := m.statusCards()
	// Update cardCount so the key handler knows the bounds. Done
	// inside the View call (not Update) because the card list
	// depends on populated state — easier to compute here than
	// thread through a separate "available cards" hook.
	m.diagnosticPanelsState.statusPanel.cardCount = len(cards)

	pal := paletteForTab("Status", false)

	parts := []string{
		statusTopBanner(m, width),
		"",
	}

	columns := 2
	if width < 80 {
		columns = 1
	}
	grid := renderPanelCardGrid(cards, width, columns,
		m.diagnosticPanelsState.statusPanel.selectedCard, pal.Accent)
	parts = append(parts, grid)

	// Footer hints — explicit keyboard contract.
	footer := []string{
		subtleStyle.Render("hjkl/arrows move · enter jump to detail · r reload · → action menu"),
	}
	if sel := m.diagnosticPanelsState.statusPanel.selectedCard; sel >= 0 && sel < len(jumpTargets) && jumpTargets[sel] != "" {
		footer = append(footer,
			lipgloss.NewStyle().Foreground(pal.Accent).Render("→ "+jumpTargets[sel]))
	}
	parts = append(parts, "", strings.Join(footer, "    "))
	if note := strings.TrimSpace(m.notice); note != "" {
		parts = append(parts, "", subtleStyle.Render(note))
	}
	out := strings.Join(parts, "\n")
	if m.actionMenu.open && m.actionMenu.owner == "Status" {
		out += "\n\n" + m.renderActionMenu(width)
	}
	return out
}

// statusTopBanner renders a one-line "where are you / how are we"
// strip above the card grid.
func statusTopBanner(m Model, width int) string {
	root := blankFallback(m.status.ProjectRoot, "(no project root)")
	if len(root) > width-30 {
		root = "…" + root[len(root)-(width-31):]
	}
	overallStyle := okStyle
	overallText := "READY"
	switch {
	case m.status.MemoryDegraded:
		overallStyle = warnStyle
		overallText = "DEGRADED"
	case strings.TrimSpace(m.status.Provider) == "":
		overallStyle = failStyle
		overallText = "NO PROVIDER"
	case strings.EqualFold(m.status.Provider, "offline"):
		overallStyle = infoStyle
		overallText = "OFFLINE"
	}
	parts := []string{
		titleStyle.Bold(true).Render("◉ STATUS"),
		subtleStyle.Render(root),
		overallStyle.Render(" " + overallText + " "),
	}
	return strings.Join(parts, "  ·  ")
}

// statusCards builds the live card list + parallel jumpTargets list
// (one entry per card; empty string means "no jump target"). Cards
// only appear when their underlying state is populated, so the grid
// adapts to what's actually relevant in this session.
func (m Model) statusCards() ([]panelCard, []string) {
	cards := []panelCard{}
	jumps := []string{}

	// --- PROJECT -----------------------------------------------------
	chip := okStyle
	cards = append(cards, panelCard{
		Icon:            "◉",
		Title:           "Project",
		StatusChip:      "OPEN",
		StatusChipStyle: &chip,
		Rows: []panelCardRow{
			{Key: "Root", Value: blankFallback(m.status.ProjectRoot, "(none)")},
			{Key: "State", Value: humanizeEngineState(m.status.State)},
		},
		FooterHint: "F2 Files · F3 Patch",
	})
	jumps = append(jumps, "F2 Files for project tree")

	// --- PROVIDER ----------------------------------------------------
	provider := blankFallback(m.status.Provider, "-")
	model := blankFallback(m.status.Model, "-")
	connHint := providerConnectivityHintTUI(m.status)
	provChip := okStyle
	provChipText := "ONLINE"
	connLower := strings.ToLower(connHint)
	switch {
	case strings.Contains(connLower, "offline"):
		provChip = infoStyle
		provChipText = "OFFLINE"
	case strings.Contains(connLower, "missing") || strings.Contains(connLower, "no key"):
		provChip = failStyle
		provChipText = "NO KEY"
	case strings.Contains(connLower, "error"):
		provChip = warnStyle
		provChipText = "ERR"
	}
	cards = append(cards, panelCard{
		Icon:            "⎈",
		Title:           "Provider",
		StatusChip:      provChipText,
		StatusChipStyle: &provChip,
		Rows: []panelCardRow{
			{Key: "Provider", Value: provider},
			{Key: "Model", Value: model},
			{Key: "Profile", Value: formatProviderProfileSummaryTUI(m.status.ProviderProfile)},
			{Key: "Runtime", Value: connHint},
			{Key: "Catalog", Value: formatModelsDevCacheSummaryTUI(m.status.ModelsDevCache)},
		},
		FooterHint: "Ctrl+O Providers panel",
	})
	jumps = append(jumps, "Ctrl+O Providers")

	// --- AST ---------------------------------------------------------
	astBackend := blankFallback(m.status.ASTBackend, "-")
	astChip := okStyle
	astChipText := "TS"
	if strings.EqualFold(astBackend, "regex") {
		astChip = warnStyle
		astChipText = "REGEX"
	}
	cards = append(cards, panelCard{
		Icon:            "≡",
		Title:           "AST",
		StatusChip:      astChipText,
		StatusChipStyle: &astChip,
		Rows: []panelCardRow{
			{Key: "Backend", Value: astBackend},
			{Key: "Langs", Value: formatASTLanguageSummaryTUI(m.status.ASTLanguages)},
			{Key: "Metrics", Value: formatASTMetricsSummaryTUI(m.status.ASTMetrics)},
		},
		FooterHint: "F9 CodeMap for symbol graph",
	})
	jumps = append(jumps, "F9 CodeMap")

	// --- CODEMAP -----------------------------------------------------
	cards = append(cards, panelCard{
		Icon:  "◇",
		Title: "CodeMap",
		Rows: []panelCardRow{
			{Key: "Summary", Value: formatCodeMapMetricsSummaryTUI(m.status.CodeMap)},
		},
		FooterHint: "F9 to explore the graph",
	})
	jumps = append(jumps, "F9 CodeMap")

	// --- MEMORY (only when degraded — surfaces a real problem) ------
	if m.status.MemoryDegraded {
		reason := strings.TrimSpace(m.status.MemoryLoadErr)
		if reason == "" {
			reason = "load failed"
		}
		memChip := warnStyle
		cards = append(cards, panelCard{
			Icon:            "❖",
			Title:           "Memory",
			StatusChip:      "DEGRADED",
			StatusChipStyle: &memChip,
			Rows: []panelCardRow{
				{Key: "Reason", Value: truncateSingleLine(reason, 80)},
				{Key: "Tier", Value: "in-memory only · episodic + semantic disabled"},
			},
			FooterHint: "F6 Memory · check .dfmc/ ownership",
		})
		jumps = append(jumps, "F6 Memory")
	}

	// --- CONTEXT IN (only when populated) ---------------------------
	if summary := formatContextInSummaryTUI(m.status.ContextIn); summary != "" {
		rows := []panelCardRow{{Key: "Last", Value: summary}}
		if why := formatContextInReasonSummaryTUI(m.status.ContextIn); why != "" {
			rows = append(rows, panelCardRow{Key: "Why", Value: why})
		}
		if files := formatContextInTopFilesTUI(m.status.ContextIn, 3); files != "" {
			rows = append(rows, panelCardRow{Key: "Top", Value: files})
		}
		cards = append(cards, panelCard{
			Icon:       "▦",
			Title:      "Context In",
			Rows:       rows,
			FooterHint: "Ctrl+W Context preview",
		})
		jumps = append(jumps, "Ctrl+W Context")
	}

	// --- SUBAGENTS LIMIT (engine concurrency snapshot) --------------
	if m.status.SubagentsLimit > 0 || m.status.SubagentsActive > 0 {
		subChip := okStyle
		txt := fmt.Sprintf("%d / %d", m.status.SubagentsActive, m.status.SubagentsLimit)
		if m.status.SubagentsActive >= m.status.SubagentsLimit && m.status.SubagentsLimit > 0 {
			subChip = warnStyle
		}
		cards = append(cards, panelCard{
			Icon:  "⌬",
			Title: "Subagents",
			Rows: []panelCardRow{
				{Key: "Active", Value: txt, Style: &subChip},
			},
			FooterHint: "Alt+R Orchestrate · alt+f stats panel",
		})
		jumps = append(jumps, "Alt+R Orchestrate")
	}

	return cards, jumps
}
