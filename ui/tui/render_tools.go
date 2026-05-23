package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func (m Model) renderToolsViewSized(width, height int) string {
	width = max(width, 50)
	height = max(height, 8)

	pal := paletteForTab("Tools", false)
	tools := m.visibleTools()
	totalTools := len(m.availableTools())
	m.toolView.index = clampIndex(m.toolView.index, len(tools))

	threePane := width >= 120
	twoPane := !threePane && width >= 80
	listW, specW, metaW := toolsPanelWidths(width, threePane, twoPane)

	banner := m.toolsTopBanner(width, len(tools), totalTools)
	listBlock := m.renderToolsListPane(listW, height, pal, tools)
	specBlock := m.renderToolsSpecPane(specW, height, pal, tools)
	var out string
	if threePane {
		metaBlock := m.renderToolsMetaPane(metaW, height, pal, tools)
		body := lipgloss.JoinHorizontal(lipgloss.Top, listBlock, "  ", specBlock, "  ", metaBlock)
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

func (m Model) toolsTopBanner(width, visibleCount, totalCount int) string {
	title := titleStyle.Bold(true).Render("TOOLS")
	chip := okStyle
	if visibleCount == 0 {
		chip = warnStyle
	}
	chipText := fmt.Sprintf(" %d ", visibleCount)
	if visibleCount != totalCount {
		chipText = fmt.Sprintf(" %d/%d ", visibleCount, totalCount)
	}
	chipRendered := chip.Render(chipText)
	stateChip := ""
	if m.toolView.editing {
		stateChip = "  " + warnStyle.Render(" EDITING ")
	}
	gap := max(width-lipgloss.Width(title)-lipgloss.Width(chipRendered)-lipgloss.Width(stateChip)-4, 1)
	line := title + strings.Repeat(" ", gap) + chipRendered + stateChip
	out := line + "\n"
	query := strings.TrimSpace(m.toolView.query)
	if m.toolView.searchActive {
		out += renderSearchInput(query, "type to filter by name / description…") + "\n"
		out += searchTypingHint() + "\n"
	} else if query != "" {
		out += subtleStyle.Render("filter ") + boldStyle.Render(query) + "  " +
			subtleStyle.Render("(press c to clear, / to edit)") + "\n"
	}
	return out + renderDivider(width-2)
}

func (m Model) renderToolsListPane(width, height int, pal tabPaletteEntry, tools []string) string {
	header := titleStyle.Bold(true).Render("REGISTRY")
	lines := []string{header, renderDivider(width - 2), ""}
	if len(tools) == 0 {
		if strings.TrimSpace(m.toolView.query) != "" {
			lines = append(lines,
				warnStyle.Render(fmt.Sprintf("No tool matches %q.", m.toolView.query)),
				"",
				subtleStyle.Render("press c to clear, / to edit"),
			)
		} else {
			lines = append(lines,
				warnStyle.Render("No registered tools."),
				"",
				subtleStyle.Render("Tool engine is not wired."),
				subtleStyle.Render("Check .dfmc/config.yaml or"),
				subtleStyle.Render("re-run dfmc init."),
			)
		}
	} else {
		rowBudget := max(height-6, 1)
		start, end := scrollWindow(m.toolView.index, len(tools), rowBudget)
		for i := start; i < end; i++ {
			lines = append(lines, m.renderToolsListRow(i, width, pal, tools))
		}
		lines = append(lines, "", subtleStyle.Render(fmt.Sprintf("%d / %d tools", m.toolView.index+1, len(tools))))
	}
	return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
}

func (m Model) renderToolsListRow(i, width int, pal tabPaletteEntry, tools []string) string {
	name := tools[i]
	selected := i == m.toolView.index
	cursor := "  "
	if selected {
		cursor = lipgloss.NewStyle().Foreground(pal.Accent).Bold(true).Render("> ")
	}
	chrome := lipgloss.Width(cursor) + 1
	nameWidth := max(width-chrome, 8)
	label := truncateSingleLine(name, nameWidth-12)
	if selected {
		label = lipgloss.NewStyle().Foreground(pal.Accent).Bold(true).Render(label)
	}
	badge := ""
	if m.eng != nil && m.eng.IsToolDisabled(name) {
		badge = " " + warnStyle.Render("[OFF]")
	} else if m.eng != nil && m.eng.ToolIsProtected(name) {
		badge = " " + subtleStyle.Render("[core]")
	}
	return cursor + label + badge
}

func (m Model) renderToolsSpecPane(width, height int, _ tabPaletteEntry, tools []string) string {
	header := titleStyle.Bold(true).Render("SPEC")
	lines := []string{header, renderDivider(width - 2), ""}
	if len(tools) == 0 {
		lines = append(lines, subtleStyle.Render("Tool engine unavailable."))
		return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
	}

	selected := tools[m.toolView.index]
	if m.eng != nil && m.eng.IsToolDisabled(selected) {
		lines = append(lines,
			warnStyle.Render(fmt.Sprintf("%s is DISABLED", selected)),
			"",
			"Press right to open actions and enable, or use:",
			subtleStyle.Render("  dfmc tool enable "+selected),
		)
	} else if m.eng != nil && m.eng.Tools != nil {
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
		resultText = subtleStyle.Render("No tool run yet.\nThis panel is the manual harness for the same tool registry the agent uses (read_file, grep_codebase, edit_file, run_command, ...). Useful for sanity-checking arguments before letting the model run them.\n↑↓ pick a tool · enter runs with current params · esc back")
	}
	rowBudget := max(height-len(lines)-2, 4)
	lines = append(lines, truncateForPanelSized(resultText, width, rowBudget))
	return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
}
