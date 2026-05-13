package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func (m Model) renderProvidersDetailPane(width, height int, _ tabPaletteEntry, rows []providerRow) string {
	lines := []string{
		titleStyle.Bold(true).Render("DETAIL"),
		renderDivider(width - 2),
		"",
	}
	if len(rows) == 0 {
		lines = append(lines, subtleStyle.Render("Select a provider to see its detail."))
		return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
	}
	row := rows[clampScroll(m.providers.scroll, len(rows))]
	title := titleStyle.Bold(true).Render(row.Name)
	if row.IsPrimary {
		title += "  " + accentStyle.Render("PRIMARY")
	}
	lines = append(lines, title, "")

	rowsKV := []struct{ k, v string }{
		{"Status", strings.ToUpper(row.Status)},
		{"Model", nonEmpty(row.Model, "(none)")},
		{"Models", fmt.Sprintf("%d configured", len(row.Models))},
		{"Protocol", nonEmpty(row.Protocol, "(auto)")},
		{"Tool support", boolWord(row.SupportsTools)},
		{"Tool style", nonEmpty(row.ToolStyle, "(auto)")},
	}
	if prof, ok := m.providerProfileForRow(row.Name); ok {
		if meta, ok := catalogModelForRef(prof.CatalogID, row.Model); ok {
			if meta.Limit.Context > 0 {
				rowsKV = append(rowsKV, struct{ k, v string }{"Model ctx", fmt.Sprintf("%d tokens", meta.Limit.Context)})
			}
			if meta.Limit.Output > 0 {
				rowsKV = append(rowsKV, struct{ k, v string }{"Model out", fmt.Sprintf("%d tokens", meta.Limit.Output)})
			}
		}
	}
	if len(row.BestFor) > 0 {
		rowsKV = append(rowsKV, struct{ k, v string }{"Best for", strings.Join(row.BestFor, ", ")})
	}
	keyWidth := 12
	for _, kv := range rowsKV {
		key := subtleStyle.Render(kv.k + ":" + strings.Repeat(" ", max(keyWidth-len(kv.k), 1)))
		val := truncateSingleLine(kv.v, max(width-keyWidth-4, 8))
		lines = append(lines, key+" "+val)
	}

	lines = append(lines, "")
	switch strings.ToLower(row.Status) {
	case "ready":
		lines = append(lines, okStyle.Render("Ready. Enter opens provider, model, fallback-model, and key actions."))
	case "no-key":
		lines = append(lines, warnStyle.Render("Missing API key."))
		if envVar := providerEnvVarLookup(row.Name); envVar != "" {
			lines = append(lines, subtleStyle.Render("Set "+envVar+" or edit api_key in details."))
		} else {
			lines = append(lines, subtleStyle.Render("Edit api_key in provider details."))
		}
	case "offline":
		lines = append(lines, subtleStyle.Render("Offline provider: deterministic fallback, no network."))
	}

	out := splitLines(strings.Join(lines, "\n"))
	if len(out) > height {
		out = out[:height]
	}
	return lipgloss.NewStyle().Width(width).Render(strings.Join(out, "\n"))
}

func boolWord(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func (m Model) renderProvidersMetaPane(width, height int, pal tabPaletteEntry, rows []providerRow) string {
	cards := m.providersMetaCards(rows)
	if len(cards) == 0 {
		return lipgloss.NewStyle().Width(width).Render(subtleStyle.Render("(no providers)"))
	}
	rendered := make([]string, 0, len(cards)*2)
	for i, c := range cards {
		if i > 0 {
			rendered = append(rendered, "")
		}
		rendered = append(rendered, renderPanelCard(c, width-2, false, pal.Accent))
	}
	out := splitLines(strings.Join(rendered, "\n"))
	if len(out) > height {
		out = out[:height]
	}
	return strings.Join(out, "\n")
}

func (m Model) providersMetaCards(rows []providerRow) []panelCard {
	if len(rows) == 0 {
		return nil
	}
	primary := ""
	if m.eng != nil && m.eng.Config != nil {
		primary = strings.TrimSpace(m.eng.Config.Providers.Primary)
	}
	selected := rows[clampScroll(m.providers.scroll, len(rows))]
	modelVal := "(none)"
	modelFallbackVal := "(empty)"
	if m.eng != nil && m.eng.Config != nil {
		if prof, ok := m.eng.Config.Providers.Profiles[selected.Name]; ok {
			modelVal = nonEmpty(strings.TrimSpace(prof.Model), "(none)")
			if len(prof.FallbackModels) > 0 {
				modelFallbackVal = strings.Join(prof.FallbackModels, " -> ")
			}
		}
	}
	routingRows := []panelCardRow{
		{Key: "Provider", Value: nonEmpty(primary, "(none)")},
		{Key: "Model", Value: modelVal},
		{Key: "Model FB", Value: modelFallbackVal},
	}
	if path, err := m.configPathForScope(m.effectivePersistScope()); err == nil {
		routingRows = append(routingRows, panelCardRow{Key: "Saves to", Value: displayConfigPath(path)})
	}
	routing := panelCard{
		Icon:       "R",
		Title:      "Runtime Routing",
		Rows:       routingRows,
		FooterHint: "Tiers map keyed models to primary + fallback slots.",
	}
	actions := panelCard{
		Icon:  "A",
		Title: "Keys",
		Rows: []panelCardRow{
			{Key: "up/down", Value: "select provider or model"},
			{Key: "enter", Value: "open actions / select"},
			{Key: "catalog", Value: "sync, add provider, then paste key"},
			{Key: "esc", Value: "back or close"},
			{Key: "ctrl+f", Value: "search providers"},
		},
	}
	return []panelCard{routing, actions}
}

func (m Model) renderProvidersMetaInline(width int, rows []providerRow) string {
	_ = width
	if len(rows) == 0 {
		return strings.Join([]string{
			accentStyle.Render("enter: ") + "actions",
			subtleStyle.Render("sync catalog"),
			subtleStyle.Render("add provider"),
			subtleStyle.Render("tiers"),
			subtleStyle.Render("skills"),
		}, "  |  ")
	}
	primary := "(none)"
	if m.eng != nil && m.eng.Config != nil {
		primary = nonEmpty(strings.TrimSpace(m.eng.Config.Providers.Primary), "(none)")
	}
	model := "(none)"
	fallbackModels := "(empty)"
	if m.eng != nil && m.eng.Config != nil && len(rows) > 0 {
		selected := rows[clampScroll(m.providers.scroll, len(rows))]
		if prof, ok := m.eng.Config.Providers.Profiles[selected.Name]; ok {
			model = nonEmpty(strings.TrimSpace(prof.Model), "(none)")
			if len(prof.FallbackModels) > 0 {
				fallbackModels = strings.Join(prof.FallbackModels, " -> ")
			}
		}
	}
	return strings.Join([]string{
		accentStyle.Render("provider: ") + primary,
		subtleStyle.Render("model: ") + model,
		subtleStyle.Render("model fb: ") + fallbackModels,
		subtleStyle.Render("enter actions"),
	}, "  |  ")
}
