package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

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
			rows = append(rows, panelCardRow{Key: "Args", Value: fmt.Sprintf("%d", len(spec.Args))})
		}
	}
	cards = append(cards, panelCard{
		Icon:            "*",
		Title:           "Current",
		StatusChip:      chip,
		StatusChipStyle: &chipStyle,
		Rows:            rows,
	})

	actionRows := []panelCardRow{
		{Key: "up/down", Value: "navigate tools"},
		{Key: "enter", Value: "run selected tool"},
		{Key: "right", Value: "action menu (run/edit/enable/disable)"},
	}
	if m.eng != nil {
		selected := tools[m.toolView.index]
		if m.eng.IsToolDisabled(selected) {
			actionRows = append(actionRows, panelCardRow{Key: "", Value: okStyle.Render("DISABLED - open action menu to enable")})
		} else if m.eng.ToolIsProtected(selected) {
			actionRows = append(actionRows, panelCardRow{Key: "", Value: subtleStyle.Render("core tool - cannot be disabled")})
		}
	}
	cards = append(cards, panelCard{
		Icon:       "#",
		Title:      "Actions",
		Rows:       actionRows,
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
		subtleStyle.Render("right actions | enter run"),
	}
	return strings.Join(parts, "  |  ")
}
