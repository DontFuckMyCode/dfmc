package tui

import (
	"fmt"
	"strings"
)

func (m Model) renderProviderDetailView(width int) string {
	width = clampInt(width, 24, 1000)
	hint := "up/down move - enter actions - space actions - esc back"
	if m.providers.modelPickerActive {
		if m.providers.modelPickerManual {
			hint = "type model id - enter add - esc cancel"
		} else {
			hint = "up/down move - enter add - space custom id - esc cancel"
		}
	} else if m.providers.modelSearchActive {
		hint = "model search - type filter - enter keep - esc clear - ctrl+u clear"
	} else if m.providers.profileEditMode {
		hint = "up/down or tab select - protocol cycles with enter/left/right/space - esc cancel"
	}
	lines := []string{
		sectionHeader("P", "Provider Detail"),
		subtleStyle.Render(hint),
		renderDivider(width - 2),
	}

	if m.eng == nil || m.eng.Config == nil {
		lines = append(lines, warnStyle.Render("engine unavailable"))
		return strings.Join(lines, "\n")
	}
	name := strings.TrimSpace(m.providers.detailProvider)
	prof, ok := m.eng.Config.Providers.Profiles[name]
	if !ok {
		lines = append(lines, warnStyle.Render("provider not found: "+name))
		return strings.Join(lines, "\n")
	}

	lines = append(lines, "", accentStyle.Render(name))
	badges := []string{apiKeySourceBadge(name, prof)}
	if strings.EqualFold(m.eng.Config.Providers.Primary, name) {
		badges = append(badges, accentStyle.Render("[primary]"))
	}
	lines = append(lines, "  "+strings.Join(badges, "  "), "")

	protocol := nonEmpty(providerDisplayLine(prof.Protocol), "(auto)")
	baseURL := nonEmpty(providerDisplayLine(prof.BaseURL), "(default)")
	apiKey := "(missing)"
	if strings.TrimSpace(prof.APIKey) != "" {
		apiKey = providerDisplayLine(maskAPIKey(prof.APIKey))
	}

	lines = append(lines, sectionTitleStyle.Render("Profile"))
	if m.providers.profileEditMode {
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
	} else {
		lines = append(lines,
			"  "+subtleStyle.Render(padRight("Catalog ref:", 18))+nonEmpty(prof.CatalogID, "(custom)"),
			"  "+subtleStyle.Render(padRight("Compatible:", 18))+protocol,
			"  "+subtleStyle.Render(padRight("Endpoint:", 18))+baseURL,
			"  "+subtleStyle.Render(padRight("API key:", 18))+apiKey,
		)
		if requestURL := providerOpenAIRequestURL(prof.Protocol, prof.BaseURL); requestURL != "" {
			lines = append(lines, "  "+subtleStyle.Render("effective_post:")+" "+truncateSingleLine(requestURL, max(width-20, 16)))
		}
	}
	if m.providers.textEditActive {
		lines = append(lines, "", m.renderProviderTextEditBox(width))
	}

	allModels := m.detailProviderModels()
	models := m.detailProviderVisibleModels()
	modelTitle := fmt.Sprintf("Models (%d)", len(allModels))
	if strings.TrimSpace(m.providers.modelQuery) != "" {
		modelTitle = fmt.Sprintf("Models (%d/%d)", len(models), len(allModels))
	}
	lines = append(lines, "", sectionTitleStyle.Render(modelTitle))
	if m.providers.modelSearchActive || strings.TrimSpace(m.providers.modelQuery) != "" {
		query := nonEmpty(m.providers.modelQuery, "")
		lines = append(lines, "  "+subtleStyle.Render("search: ")+accentStyle.Render(query))
	}
	if len(models) == 0 {
		if strings.TrimSpace(m.providers.modelQuery) != "" {
			lines = append(lines, "  "+warnStyle.Render("No models match search"))
		} else {
			lines = append(lines, "  "+warnStyle.Render("No models configured"))
		}
	} else {
		selectedIdx := clampScroll(m.providers.modelEditIdx, len(models))
		start, end := scrollWindow(selectedIdx, len(models), 12)
		for i := start; i < end; i++ {
			model := models[i]
			prefix := "  "
			label := model
			if i == selectedIdx {
				prefix = accentStyle.Render("> ")
				label = accentStyle.Render(model)
			}
			if tag := m.modelTierTag(name, model); tag != "" {
				label += tag
			} else if strings.EqualFold(model, prof.Model) {
				label += subtleStyle.Render(" profile primary")
			} else if modelFallbackIndex(prof.FallbackModels, model) > 0 {
				label += subtleStyle.Render(fmt.Sprintf(" fallback:%d", modelFallbackIndex(prof.FallbackModels, model)))
			}
			if meta, ok := catalogModelForRef(prof.CatalogID, model); ok {
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
				if len(parts) > 0 {
					label += subtleStyle.Render("  " + strings.Join(parts, " "))
				}
			}
			lines = append(lines, prefix+label)
		}
	}

	if len(prof.FallbackModels) > 0 {
		lines = append(lines, "", sectionTitleStyle.Render(fmt.Sprintf("Model Fallback Chain (%d)", len(prof.FallbackModels))))
		for _, model := range prof.FallbackModels {
			lines = append(lines, "  "+model)
		}
	}

	if m.providers.modelPickerActive {
		lines = append(lines, "", sectionTitleStyle.Render("Add Model"))
		if m.providers.modelPickerManual {
			lines = append(lines, "  "+accentStyle.Render("> ")+accentStyle.Render(m.providers.modelPickerDraft))
		} else if len(m.providers.modelPickerItems) == 0 {
			lines = append(lines, "  "+subtleStyle.Render("No cached models. Press Space to type a custom model id."))
		} else {
			idx := clampScroll(m.providers.modelPickerIndex, len(m.providers.modelPickerItems))
			start, end := scrollWindow(idx, len(m.providers.modelPickerItems), 10)
			for i := start; i < end; i++ {
				prefix := "  "
				label := m.providers.modelPickerItems[i]
				if i == idx {
					prefix = accentStyle.Render("> ")
					label = accentStyle.Render(label)
				}
				lines = append(lines, prefix+label)
			}
		}
	}

	if hist := m.providerUsageStrip(name, 5); len(hist) > 0 {
		lines = append(lines, "", sectionTitleStyle.Render("Recent completions"))
		for _, line := range hist {
			lines = append(lines, "  "+subtleStyle.Render(line))
		}
	}

	return strings.Join(lines, "\n")
}

func modelFallbackIndex(models []string, model string) int {
	model = strings.TrimSpace(model)
	if model == "" {
		return 0
	}
	for i, existing := range models {
		if strings.EqualFold(strings.TrimSpace(existing), model) {
			return i + 1
		}
	}
	return 0
}

func (m Model) modelTierTag(providerName, model string) string {
	providerName = strings.TrimSpace(providerName)
	model = strings.TrimSpace(model)
	if providerName == "" || model == "" {
		return ""
	}
	ref := providerName + ":" + model
	for _, tier := range providerTierNames {
		if modelRefEqual(m.tierSlotValue(tier, 0), ref) {
			return okStyle.Render(" " + tier + " primary")
		}
		for slot := 1; slot <= 3; slot++ {
			if modelRefEqual(m.tierSlotValue(tier, slot), ref) {
				return subtleStyle.Render(fmt.Sprintf(" %s fallback:%d", tier, slot))
			}
		}
	}
	return ""
}

func modelRefEqual(left, right string) bool {
	lp, lm, lok := strings.Cut(strings.TrimSpace(left), ":")
	rp, rm, rok := strings.Cut(strings.TrimSpace(right), ":")
	if !lok || !rok {
		return strings.EqualFold(strings.TrimSpace(left), strings.TrimSpace(right))
	}
	return strings.EqualFold(strings.TrimSpace(lp), strings.TrimSpace(rp)) &&
		strings.EqualFold(strings.TrimSpace(lm), strings.TrimSpace(rm))
}
