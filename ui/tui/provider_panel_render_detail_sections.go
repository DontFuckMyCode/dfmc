package tui

import (
	"fmt"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

func (m Model) providerDetailHint() string {
	switch {
	case m.providers.modelPickerActive && m.providers.modelPickerManual:
		return "type model id - enter add - esc cancel"
	case m.providers.modelPickerActive:
		return "up/down move - enter add - space custom id - esc cancel"
	case m.providers.modelSearchActive:
		return "model search - type filter - enter keep - esc clear - ctrl+u clear"
	case m.providers.profileEditMode:
		return "up/down or tab select - protocol cycles with enter/left/right/space - esc cancel"
	default:
		return "up/down move - enter actions - space actions - esc back"
	}
}

func (m Model) renderProviderBadges(name string, prof config.ModelConfig) []string {
	badges := []string{apiKeySourceBadge(name, prof)}
	if strings.EqualFold(m.eng.Config.Providers.Primary, name) {
		badges = append(badges, accentStyle.Render("[primary]"))
	}
	return []string{"  " + strings.Join(badges, "  ")}
}

func (m Model) renderProviderProfileSection(name string, prof config.ModelConfig, width int) []string {
	protocol := nonEmpty(providerDisplayLine(prof.Protocol), "(auto)")
	baseURL := nonEmpty(providerDisplayLine(prof.BaseURL), "(default)")
	apiKey := "(missing)"
	if strings.TrimSpace(prof.APIKey) != "" {
		apiKey = providerDisplayLine(maskAPIKey(prof.APIKey))
	}
	lines := []string{sectionTitleStyle.Render("Profile")}
	if m.providers.profileEditMode {
		return append(lines, m.renderProviderEditableProfileFields(name, protocol, baseURL, apiKey, width)...)
	}
	lines = append(lines,
		"  "+subtleStyle.Render(padRight("Catalog ref:", 18))+nonEmpty(prof.CatalogID, "(custom)"),
		"  "+subtleStyle.Render(padRight("Compatible:", 18))+protocol,
		"  "+subtleStyle.Render(padRight("Endpoint:", 18))+baseURL,
		"  "+subtleStyle.Render(padRight("API key:", 18))+apiKey,
	)
	if requestURL := providerOpenAIRequestURL(prof.Protocol, prof.BaseURL); requestURL != "" {
		lines = append(lines, "  "+subtleStyle.Render("effective_post:")+" "+truncateSingleLine(requestURL, max(width-20, 16)))
	}
	return lines
}

func (m Model) renderProviderEditableProfileFields(name, protocol, baseURL, apiKey string, width int) []string {
	fields := []struct {
		label  string
		value  string
		active bool
	}{
		{"Provider name", providerDisplayLine(name) + "  (fixed)", false},
		{"Compatible", protocol + "  (cycle)", m.providers.profileEditField == 0},
		{"Endpoint", baseURL, m.providers.profileEditField == 1},
		{"API key", apiKey, m.providers.profileEditField == 2},
	}
	lines := make([]string, 0, len(fields))
	for _, f := range fields {
		prefix := "  "
		labelText := padRight(f.label+":", 18)
		label := subtleStyle.Render(labelText)
		valueText := truncateSingleLine(f.value, max(width-22, 8))
		val := subtleStyle.Render(valueText)
		if f.active {
			prefix = accentStyle.Render("> ")
			label = accentStyle.Render(labelText)
			val = accentStyle.Render(valueText)
		}
		lines = append(lines, prefix+label+val)
	}
	return lines
}

func (m Model) renderProviderModelsSection(name string, prof config.ModelConfig) []string {
	return m.renderProviderModelsSectionSized(name, prof, 12)
}

func (m Model) renderProviderModelsSectionSized(name string, prof config.ModelConfig, rowBudget int) []string {
	allModels := m.detailProviderModels()
	models := m.detailProviderVisibleModels()
	modelTitle := fmt.Sprintf("Models (%d)", len(allModels))
	if strings.TrimSpace(m.providers.modelQuery) != "" {
		modelTitle = fmt.Sprintf("Models (%d/%d)", len(models), len(allModels))
	}
	lines := []string{"", sectionTitleStyle.Render(modelTitle)}
	if m.providers.modelSearchActive || strings.TrimSpace(m.providers.modelQuery) != "" {
		query := nonEmpty(m.providers.modelQuery, "")
		lines = append(lines, "  "+subtleStyle.Render("search: ")+accentStyle.Render(query))
	}
	if len(models) == 0 {
		return append(lines, providerEmptyModelsLine(m.providers.modelQuery))
	}
	selectedIdx := clampScroll(m.providers.modelEditIdx, len(models))
	start, end := scrollWindow(selectedIdx, len(models), max(rowBudget, 1))
	for i := start; i < end; i++ {
		lines = append(lines, m.providerModelRow(name, prof, models[i], i == selectedIdx))
	}
	return lines
}

func providerEmptyModelsLine(query string) string {
	if strings.TrimSpace(query) != "" {
		return "  " + warnStyle.Render("No models match search")
	}
	return "  " + warnStyle.Render("No models configured")
}

func (m Model) providerModelRow(providerName string, prof config.ModelConfig, model string, selected bool) string {
	prefix := "  "
	label := model
	if selected {
		prefix = accentStyle.Render("> ")
		label = accentStyle.Render(model)
	}
	if tag := m.modelTierTag(providerName, model); tag != "" {
		label += tag
	} else if strings.EqualFold(model, prof.Model) {
		label += subtleStyle.Render(" profile primary")
	} else if idx := modelFallbackIndex(prof.FallbackModels, model); idx > 0 {
		label += subtleStyle.Render(fmt.Sprintf(" fallback:%d", idx))
	}
	return prefix + label + providerCatalogModelMeta(prof.CatalogID, model)
}

func providerCatalogModelMeta(catalogID, model string) string {
	meta, ok := catalogModelForRef(catalogID, model)
	if !ok {
		return ""
	}
	var parts []string
	if meta.Limit.Context > 0 {
		parts = append(parts, fmt.Sprintf("ctx:%d", meta.Limit.Context))
	}
	if meta.Limit.Output > 0 {
		parts = append(parts, fmt.Sprintf("out:%d", meta.Limit.Output))
	}
	if meta.ToolCall {
		parts = append(parts, "tools")
	}
	if meta.Reasoning {
		parts = append(parts, "reasoning")
	}
	if len(parts) == 0 {
		return ""
	}
	return subtleStyle.Render("  " + strings.Join(parts, " "))
}

func (m Model) renderProviderFallbackSection(prof config.ModelConfig) []string {
	if len(prof.FallbackModels) == 0 {
		return nil
	}
	lines := []string{"", sectionTitleStyle.Render(fmt.Sprintf("Model Fallback Chain (%d)", len(prof.FallbackModels)))}
	for _, model := range prof.FallbackModels {
		lines = append(lines, "  "+model)
	}
	return lines
}

func (m Model) renderProviderModelPickerSection() []string {
	return m.renderProviderModelPickerSectionSized(10)
}

func (m Model) renderProviderModelPickerSectionSized(rowBudget int) []string {
	if !m.providers.modelPickerActive {
		return nil
	}
	lines := []string{"", sectionTitleStyle.Render("Add Model")}
	if m.providers.modelPickerManual {
		return append(lines, "  "+accentStyle.Render("> ")+accentStyle.Render(m.providers.modelPickerDraft))
	}
	if len(m.providers.modelPickerItems) == 0 {
		return append(lines, "  "+subtleStyle.Render("No cached models. Press Space to type a custom model id."))
	}
	idx := clampScroll(m.providers.modelPickerIndex, len(m.providers.modelPickerItems))
	start, end := scrollWindow(idx, len(m.providers.modelPickerItems), max(rowBudget, 1))
	for i := start; i < end; i++ {
		prefix := "  "
		label := m.providers.modelPickerItems[i]
		if i == idx {
			prefix = accentStyle.Render("> ")
			label = accentStyle.Render(label)
		}
		lines = append(lines, prefix+label)
	}
	return lines
}

func (m Model) renderProviderUsageSection(name string) []string {
	hist := m.providerUsageStrip(name, 5)
	if len(hist) == 0 {
		return nil
	}
	lines := []string{"", sectionTitleStyle.Render("Recent completions")}
	for _, line := range hist {
		lines = append(lines, "  "+subtleStyle.Render(line))
	}
	return lines
}
