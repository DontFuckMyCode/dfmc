package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func (m Model) renderProviderCatalogView(width int) string {
	return m.renderProviderCatalogViewSized(width, 24)
}

func (m Model) renderProviderCatalogViewSized(width, height int) string {
	width = clampInt(width, 24, 1000)
	height = max(height, 1)
	if !m.providers.catalogLoaded {
		m = m.loadProviderCatalogItems()
	}
	lines := []string{
		sectionHeader("C", "Add Provider From Catalog"),
		subtleStyle.Render("up/down move - enter select - esc back"),
		renderDivider(width - 2),
		"",
	}
	if len(m.providers.catalogItems) == 0 {
		lines = append(lines,
			warnStyle.Render("models.dev cache not available"),
			subtleStyle.Render("Back on My Providers, open actions and run Sync models.dev first."),
		)
		return strings.Join(lines, "\n")
	}
	cursor := clampScroll(m.providers.catalogScroll, len(m.providers.catalogItems))
	rowBudget := max(height-len(lines)-3, 1)
	start, end := scrollWindow(cursor, len(m.providers.catalogItems), rowBudget)
	for i := start; i < end; i++ {
		item := m.providers.catalogItems[i]
		prefix := "  "
		name := truncateSingleLine(item.Name, max(width-32, 12))
		if i == cursor {
			prefix = accentStyle.Render("> ")
			name = accentStyle.Bold(true).Render(name)
		}
		compatible := nonEmpty(item.Compatible, "compatible: not in models.dev")
		meta := subtleStyle.Render(fmt.Sprintf(" %s  %d models", compatible, item.ModelCount))
		lines = append(lines, prefix+name+meta)
		if i == cursor {
			endpoint := nonEmpty(item.Endpoint, "(endpoint not in models.dev)")
			lines = append(lines, "    "+subtleStyle.Render(item.ID+" -> "+truncateSingleLine(endpoint, max(width-12, 12))))
		}
	}
	lines = append(lines, "", subtleStyle.Render(fmt.Sprintf("%d / %d catalog providers", cursor+1, len(m.providers.catalogItems))))
	return strings.Join(lines, "\n")
}

func (m Model) renderProviderCatalogFormView(width int) string {
	width = clampInt(width, 24, 1000)
	fields := []struct {
		label string
		value string
	}{
		{"Provider name", providerDisplayLine(m.providers.catalogFormName)},
		{"Endpoint", providerDisplayLine(m.providers.catalogFormURL)},
		{"Compatible", providerDisplayLine(m.providers.catalogFormCompat)},
		{"API key", providerDisplayLine(maskAPIKey(m.providers.catalogFormKey))},
	}
	lines := []string{
		sectionHeader("+", "My Provider"),
		subtleStyle.Render("up/down select - enter edit/cycle/save - left/right cycles Compatible - esc cancel"),
		renderDivider(width - 2),
		"",
		"  " + subtleStyle.Render("reference: ") + accentStyle.Render(m.providers.catalogRefID),
		"",
	}
	for i, f := range fields {
		prefix := "  "
		labelText := padRight(f.label+":", 18)
		label := subtleStyle.Render(labelText)
		valueText := truncateSingleLine(nonEmpty(f.value, "(empty)"), max(width-22, 8))
		value := valueText
		if i == m.providers.catalogFormField {
			prefix = accentStyle.Render("> ")
			label = accentStyle.Render(labelText)
			value = accentStyle.Render(valueText)
		}
		lines = append(lines, prefix+label+value)
	}
	savePrefix := "  "
	saveLabel := "Save provider"
	if m.providers.catalogFormField == 4 {
		savePrefix = accentStyle.Render("> ")
		saveLabel = accentStyle.Render(saveLabel)
	}
	lines = append(lines, savePrefix+saveLabel)
	lines = append(lines, "",
		subtleStyle.Render("Endpoint and compatible start exactly as models.dev provides them; empty means models.dev did not provide it."),
		subtleStyle.Render("Only name, endpoint, and key open input; compatible cycles with enter/left/right/space."),
		subtleStyle.Render("Key is encrypted with "+displayConfigPath(mustMasterKeyPath())+" before writing ~/.dfmc/config.yaml."),
	)
	if requestURL := providerOpenAIRequestURL(m.providers.catalogFormCompat, m.providers.catalogFormURL); requestURL != "" {
		lines = append(lines, subtleStyle.Render("Effective POST: "+truncateSingleLine(requestURL, max(width-18, 16))))
	}
	if m.providers.textEditActive {
		lines = append(lines, "", m.renderProviderTextEditBox(width))
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderProviderTextEditBox(width int) string {
	boxWidth := clampInt(width-12, 36, 88)
	title := providerDisplayLine(m.providers.textEditTitle)
	value := providerDisplayLine(m.providers.textEditDraft)
	if value == "" {
		value = "(empty)"
	}
	value = truncateSingleLine(value, max(boxWidth-6, 8))
	body := strings.Join([]string{
		accentStyle.Render(title),
		"",
		value,
		"",
		subtleStyle.Render("paste/type here - enter save - esc cancel - ctrl+u clear"),
	}, "\n")
	box := lipgloss.NewStyle().
		Width(boxWidth).
		Border(lipgloss.RoundedBorder()).
		Padding(1, 2).
		Render(body)
	return lipgloss.PlaceHorizontal(width, lipgloss.Center, box)
}

func (m Model) renderProviderTiersView(width int) string {
	return m.renderProviderTiersViewSized(width, 24)
}

func (m Model) renderProviderTiersViewSized(width, height int) string {
	width = clampInt(width, 24, 1000)
	height = max(height, 1)
	refs := m.providerModelRefs()
	leftW := max(width*45/100, 34)
	rightW := max(width-leftW-2, 28)
	left := m.renderTierMatrixPane(leftW)
	right := m.renderTierModelPickerPaneSized(rightW, refs, max(height-5, 1))
	return sectionHeader("T", "Tiers") + "\n" +
		subtleStyle.Render("up/down slot - left/right model - enter assign - esc back") + "\n" +
		renderDivider(width-2) + "\n" +
		lipgloss.JoinHorizontal(lipgloss.Top, left, "  ", right)
}

func (m Model) renderProviderSkillsView(width int) string {
	return m.renderProviderSkillsViewSized(width, 24)
}

func (m Model) renderProviderSkillsViewSized(width, height int) string {
	width = clampInt(width, 24, 1000)
	height = max(height, 1)
	skills := collectSkills(m.projectRoot())
	refs := append([]string{"frontier", "medium", "turbo", "weak"}, m.providerModelRefs()...)
	leftW := max(width*42/100, 30)
	rightW := max(width-leftW-2, 28)
	paneRows := max(height-5, 1)
	left := m.renderSkillListPaneSized(leftW, skills, paneRows)
	right := m.renderSkillRoutePickerPaneSized(rightW, refs, paneRows)
	return sectionHeader("S", "Skill Model Routes") + "\n" +
		subtleStyle.Render("up/down skill - left/right model/tier - enter assign - esc back") + "\n" +
		renderDivider(width-2) + "\n" +
		lipgloss.JoinHorizontal(lipgloss.Top, left, "  ", right)
}

func (m Model) renderSkillListPane(width int, skills []skillEntry) string {
	return m.renderSkillListPaneSized(width, skills, 20)
}

func (m Model) renderSkillListPaneSized(width int, skills []skillEntry, rowBudget int) string {
	lines := []string{sectionTitleStyle.Render("Skills")}
	if len(skills) == 0 {
		lines = append(lines, "", subtleStyle.Render("No skills discovered."))
		return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
	}
	cursor := clampScroll(m.providers.skillCursor, len(skills))
	start, end := scrollWindow(cursor, len(skills), max(rowBudget, 1))
	for i := start; i < end; i++ {
		prefix := "  "
		name := truncateSingleLine(skills[i].Name, max(width-22, 8))
		route := m.skillRouteValue(skills[i].Name)
		if i == cursor {
			prefix = accentStyle.Render("> ")
			name = accentStyle.Render(name)
		}
		lines = append(lines, prefix+padRight(name, max(width-18, 10))+subtleStyle.Render(nonEmpty(route, "(default tier)")))
	}
	return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
}

func (m Model) renderSkillRoutePickerPane(width int, refs []string) string {
	return m.renderSkillRoutePickerPaneSized(width, refs, 20)
}

func (m Model) renderSkillRoutePickerPaneSized(width int, refs []string, rowBudget int) string {
	lines := []string{sectionTitleStyle.Render("Tier Or Model")}
	if len(refs) == 0 {
		lines = append(lines, "", warnStyle.Render("No model refs available."))
		return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
	}
	cursor := clampScroll(m.providers.skillModelCursor, len(refs))
	start, end := scrollWindow(cursor, len(refs), max(rowBudget, 1))
	for i := start; i < end; i++ {
		prefix := "  "
		label := truncateSingleLine(refs[i], max(width-4, 8))
		if i == cursor {
			prefix = accentStyle.Render("> ")
			label = accentStyle.Render(label)
		}
		lines = append(lines, prefix+label)
	}
	return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
}

func (m Model) skillRouteValue(skill string) string {
	if m.eng == nil || m.eng.Config == nil || m.eng.Config.Routing.SkillModels == nil {
		return ""
	}
	return m.eng.Config.Routing.SkillModels[skill]
}

func (m Model) renderTierMatrixPane(width int) string {
	lines := []string{sectionTitleStyle.Render("Matrix")}
	cursor := m.providers.tierCursor
	for ti, tier := range providerTierNames {
		lines = append(lines, "", accentStyle.Render(strings.ToUpper(tier)))
		for si := 0; si < 4; si++ {
			idx := ti*4 + si
			slot := tierSlotName(si)
			value := m.tierSlotValue(tier, si)
			prefix := "  "
			if idx == cursor {
				prefix = accentStyle.Render("> ")
			}
			lines = append(lines, prefix+padRight(slot, 11)+subtleStyle.Render(nonEmpty(value, "(unset)")))
		}
	}
	return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
}

func (m Model) renderTierModelPickerPane(width int, refs []string) string {
	return m.renderTierModelPickerPaneSized(width, refs, 20)
}

func (m Model) renderTierModelPickerPaneSized(width int, refs []string, rowBudget int) string {
	lines := []string{sectionTitleStyle.Render("Keyed Models")}
	if len(refs) == 0 {
		lines = append(lines, "", warnStyle.Render("No keyed models yet."), subtleStyle.Render("Add a provider from the synced models.dev catalog first."))
		return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
	}
	cursor := clampScroll(m.providers.modelCursor, len(refs))
	start, end := scrollWindow(cursor, len(refs), max(rowBudget, 1))
	for i := start; i < end; i++ {
		prefix := "  "
		label := truncateSingleLine(refs[i], max(width-4, 8))
		if i == cursor {
			prefix = accentStyle.Render("> ")
			label = accentStyle.Render(label)
		}
		lines = append(lines, prefix+label)
	}
	lines = append(lines, "", subtleStyle.Render(fmt.Sprintf("%d / %d models", cursor+1, len(refs))))
	return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
}

func (m Model) tierSlotValue(tier string, slot int) string {
	if m.eng == nil || m.eng.Config == nil || m.eng.Config.Routing.Tiers == nil {
		return ""
	}
	cfg := m.eng.Config.Routing.Tiers[tier]
	if slot == 0 {
		return cfg.Primary
	}
	idx := slot - 1
	if idx >= 0 && idx < len(cfg.Fallbacks) {
		return cfg.Fallbacks[idx]
	}
	return ""
}

func mustMasterKeyPath() string {
	return "~/.dfmc/secrets/master.key"
}
