package tui

import (
	"fmt"
	"strings"
)

func (m Model) renderProviderDetailView(width int) string {
	return m.renderProviderDetailViewSized(width, 24)
}

func (m Model) renderProviderDetailViewSized(width, height int) string {
	width = clampInt(width, 24, 1000)
	height = max(height, 1)
	lines := []string{
		sectionHeader("P", "Provider Detail"),
		subtleStyle.Render(m.providerDetailHint()),
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
	lines = append(lines, m.renderProviderBadges(name, prof)...)
	lines = append(lines, "")
	lines = append(lines, m.renderProviderProfileSection(name, prof, width)...)
	if m.providers.textEditActive {
		lines = append(lines, "", m.renderProviderTextEditBox(width))
	}

	reservedRows := 6
	if m.providers.modelPickerActive {
		reservedRows += 4
	}
	modelRows := max(height-len(lines)-reservedRows, 1)
	lines = append(lines, m.renderProviderModelsSectionSized(name, prof, modelRows)...)
	lines = append(lines, m.renderProviderFallbackSection(prof)...)
	pickerRows := max(height-len(lines)-4, 1)
	lines = append(lines, m.renderProviderModelPickerSectionSized(pickerRows)...)
	lines = append(lines, m.renderProviderUsageSection(name)...)

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
