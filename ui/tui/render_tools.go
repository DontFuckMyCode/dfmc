// render_tools.go — F6 Tools panel, rebuilt with the same banner +
// 3-pane card shell as F2/F3/F4/F5. The keyboard surface stays in
// panel_keys.go's handleToolsKey; this file owns rendering only.
//
// Layout:
//   ≥120 cols → 3 panes (28% list · 44% spec · 28% metadata)
//   80-119    → 2 panes (35% list · 65% spec) + inline footer
//   <80       → 1 pane stack
//
// Banner reports tool count + edit-mode chip ("EDITING" when the
// param editor is active). Metadata cards expose the run-state +
// keyboard surface so the user doesn't have to read source to learn
// keys.

package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func (m Model) renderToolsViewV2(width int) string {
	width = max(width, 50)
	height := 24

	pal := paletteForTab("Tools", false)
	tools := m.availableTools()
	m.toolView.index = clampIndex(m.toolView.index, len(tools))

	threePane := width >= 120
	twoPane := !threePane && width >= 80
	listW, specW, metaW := toolsPanelWidths(width, threePane, twoPane)

	banner := m.toolsTopBanner(width, len(tools))
	listBlock := m.renderToolsListPane(listW, height, pal, tools)
	specBlock := m.renderToolsSpecPane(specW, height, pal, tools)
	var out string
	if threePane {
		metaBlock := m.renderToolsMetaPane(metaW, height, pal, tools)
		body := lipgloss.JoinHorizontal(lipgloss.Top,
			listBlock, "  ", specBlock, "  ", metaBlock)
		out = banner + "\n" + body
	} else if twoPane {
		footer := m.renderToolsMetaInline(width, tools)
		body := lipgloss.JoinHorizontal(lipgloss.Top, listBlock, "  ", specBlock)
		out = banner + "\n" + body + "\n" + footer
	} else {
		out = banner + "\n" + listBlock + "\n" + specBlock
	}
	if m.actionMenu.open && m.actionMenu.owner == "Tools" {
		out += "\n\n" + m.renderActionMenu(width)
	}
	return out
}

func toolsPanelWidths(total int, threePane, twoPane bool) (listW, specW, metaW int) {
	if threePane {
		listW = max(total*28/100, 26)
		metaW = max(total*28/100, 26)
		specW = max(total-listW-metaW-4, 32)
		return
	}
	if twoPane {
		listW = max(total*35/100, 26)
		specW = max(total-listW-2, 28)
		return
	}
	return total, total, 0
}

// --- BANNER ------------------------------------------------------------------

func (m Model) toolsTopBanner(width, count int) string {
	title := titleStyle.Bold(true).Render("⚒ TOOLS")
	chip := okStyle
	if count == 0 {
		chip = warnStyle
	}
	chipRendered := chip.Render(fmt.Sprintf(" %d ", count))
	stateChip := ""
	if m.toolView.editing {
		stateChip = "  " + warnStyle.Render(" EDITING ")
	}
	gap := max(width-lipgloss.Width(title)-lipgloss.Width(chipRendered)-lipgloss.Width(stateChip)-4, 1)
	line := title + strings.Repeat(" ", gap) + chipRendered + stateChip
	return line + "\n" + renderDivider(width-2)
}

// --- LIST PANE ---------------------------------------------------------------

func (m Model) renderToolsListPane(width, height int, pal tabPaletteEntry, tools []string) string {
	header := titleStyle.Bold(true).Render("≡ REGISTRY")
	lines := []string{
		header,
		renderDivider(width - 2),
		"",
	}
	if len(tools) == 0 {
		lines = append(lines,
			warnStyle.Render("No registered tools."),
			"",
			subtleStyle.Render("Tool engine isn't wired."),
			subtleStyle.Render("Check .dfmc/config.yaml or"),
			subtleStyle.Render("re-run dfmc init."),
		)
	} else {
		rowBudget := max(height-6, 6)
		start, end := scrollWindow(m.toolView.index, len(tools), rowBudget)
		for i := start; i < end; i++ {
			lines = append(lines, m.renderToolsListRow(i, width, pal, tools))
		}
		lines = append(lines, "",
			subtleStyle.Render(fmt.Sprintf("%d / %d tools",
				m.toolView.index+1, len(tools))))
	}
	return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
}

func (m Model) renderToolsListRow(i, width int, pal tabPaletteEntry, tools []string) string {
	name := tools[i]
	selected := i == m.toolView.index
	cursor := "  "
	if selected {
		cursor = lipgloss.NewStyle().Foreground(pal.Accent).Bold(true).Render("▶ ")
	}
	chrome := lipgloss.Width(cursor) + 1
	nameWidth := max(width-chrome, 8)
	label := truncateSingleLine(name, nameWidth)
	if selected {
		label = lipgloss.NewStyle().Foreground(pal.Accent).Bold(true).Render(label)
	}
	return cursor + label
}

// --- SPEC PANE ---------------------------------------------------------------

func (m Model) renderToolsSpecPane(width, height int, _ tabPaletteEntry, tools []string) string {
	header := titleStyle.Bold(true).Render("▸ SPEC")
	lines := []string{
		header,
		renderDivider(width - 2),
		"",
	}
	if len(tools) == 0 {
		lines = append(lines, subtleStyle.Render("Tool engine unavailable."))
		return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
	}
	selected := tools[m.toolView.index]
	if m.eng != nil && m.eng.Tools != nil {
		if spec, ok := m.eng.Tools.Spec(selected); ok {
			lines = append(lines, highlightToolSpecLines(formatToolSpec(spec), width)...)
		} else {
			lines = append(lines,
				fmt.Sprintf("Name:        %s", selected),
				subtleStyle.Render("(no spec registered)"),
			)
		}
	} else {
		lines = append(lines,
			fmt.Sprintf("Name:        %s", selected),
			fmt.Sprintf("Description: %s", truncateForPanel(m.toolDescription(selected), width)),
		)
	}
	lines = append(lines,
		"",
		subtleStyle.Render("Effective params"),
		truncateForPanelSized(m.toolPresetSummary(selected), width, 6),
		"",
	)
	if selected == "run_command" {
		if suggestions := m.runCommandSuggestions(); len(suggestions) > 0 {
			lines = append(lines, subtleStyle.Render("Suggested presets"))
			for _, s := range suggestions {
				lines = append(lines, truncateForPanel("- "+s, width))
			}
			lines = append(lines, "")
		}
	}
	if m.toolView.editing {
		lines = append(lines,
			warnStyle.Render("Param Editor (enter to apply, esc to cancel)"),
			truncateForPanel(m.toolView.draft, width),
			"",
		)
	}
	lines = append(lines, subtleStyle.Render("Last result"))
	resultText := strings.TrimSpace(m.toolView.output)
	if resultText == "" {
		resultText = subtleStyle.Render("No tool run yet.\nThis panel is the manual harness for the same tool registry the agent uses (read_file, grep_codebase, edit_file, run_command, ...). Useful for sanity-checking arguments before letting the model loose.\nj/k pick a tool · e edits params · enter runs with current params · x resets to defaults.")
	}
	rowBudget := max(height-len(lines)-2, 4)
	lines = append(lines, truncateForPanelSized(resultText, width, rowBudget))
	return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
}

// --- METADATA PANE -----------------------------------------------------------

func (m Model) renderToolsMetaPane(width, height int, pal tabPaletteEntry, tools []string) string {
	cards := m.toolsMetaCards(tools)
	if len(cards) == 0 {
		return lipgloss.NewStyle().Width(width).Render(
			subtleStyle.Render("No tool selected."))
	}
	rendered := make([]string, 0, len(cards)*2)
	for i, c := range cards {
		if i > 0 {
			rendered = append(rendered, "")
		}
		rendered = append(rendered, renderPanelCard(c, width-2, false, pal.Accent))
	}
	body := strings.Join(rendered, "\n")
	rows := splitLines(body)
	if len(rows) > height {
		rows = rows[:height]
	}
	return strings.Join(rows, "\n")
}

func (m Model) toolsMetaCards(tools []string) []panelCard {
	if len(tools) == 0 {
		return nil
	}
	selected := tools[m.toolView.index]
	var cards []panelCard

	// CURRENT card.
	chip := "READY"
	chipStyle := okStyle
	if m.toolView.editing {
		chip = "EDITING"
		chipStyle = warnStyle
	}
	rows := []panelCardRow{
		{Key: "Name", Value: selected},
		{Key: "Position", Value: fmt.Sprintf("%d / %d", m.toolView.index+1, len(tools))},
	}
	if m.eng != nil && m.eng.Tools != nil {
		if spec, ok := m.eng.Tools.Spec(selected); ok {
			rows = append(rows,
				panelCardRow{Key: "Args", Value: fmt.Sprintf("%d", len(spec.Args))})
		}
	}
	cards = append(cards, panelCard{
		Icon:            "◉",
		Title:           "Current",
		StatusChip:      chip,
		StatusChipStyle: &chipStyle,
		Rows:            rows,
	})

	// ACTIONS card.
	cards = append(cards, panelCard{
		Icon:  "⚒",
		Title: "Actions",
		Rows: []panelCardRow{
			{Key: "j / k", Value: "next / prev tool"},
			{Key: "enter", Value: "run with current params"},
			{Key: "e", Value: "edit params"},
			{Key: "x", Value: "reset params to default"},
			{Key: "r", Value: "rerun last invocation"},
		},
		FooterHint: "ctrl+h keys",
	})
	return cards
}

func (m Model) renderToolsMetaInline(width int, tools []string) string {
	_ = width
	if len(tools) == 0 {
		return subtleStyle.Render("No tools registered.")
	}
	selected := tools[m.toolView.index]
	chip := "READY"
	chipStyle := okStyle
	if m.toolView.editing {
		chip = "EDITING"
		chipStyle = warnStyle
	}
	parts := []string{
		chipStyle.Render(" " + chip + " "),
		titleStyle.Render(selected),
		subtleStyle.Render(fmt.Sprintf("%d / %d", m.toolView.index+1, len(tools))),
		subtleStyle.Render("enter run · e edit · x reset · r rerun"),
	}
	return strings.Join(parts, "  ·  ")
}
