package tui

// Provider detail view rendering. Split from provider_panel_render.go
// because renderProviderDetailView alone is ~230 lines — the biggest
// single render block in the providers surface. Covers the per-
// provider page: header badges, profile fields (inline-editable),
// best-for tags, numbered model list with scroll window, fallback
// models, and the add-model picker (list mode + manual-entry mode).

import (
	"fmt"
	"strings"
)

func (m Model) renderProviderDetailView(width int) string {
	width = clampInt(width, 24, 1000)
	header := sectionHeader("⚑", "Provider Detail")
	hint := subtleStyle.Render("j/k scroll · g/G/home/end top/bottom · pgup/pgdown page · enter menu · esc/q back")
	if m.providers.modelPickerActive {
		if m.providers.modelPickerManual {
			hint = subtleStyle.Render("type model · enter confirm · esc cancel")
		} else {
			hint = subtleStyle.Render("j/k scroll · g/G home/end · pgup/pgdown page · enter pick · m manual · esc cancel")
		}
	} else if m.providers.profileEditMode {
		hint = subtleStyle.Render("tab field · enter commit · esc cancel")
	}
	lines := []string{header, hint, renderDivider(width - 2)}

	if m.eng == nil || m.eng.Config == nil {
		lines = append(lines, warnStyle.Render("engine unavailable"))
		return strings.Join(lines, "\n")
	}

	prof, ok := m.eng.Config.Providers.Profiles[m.providers.detailProvider]
	if !ok {
		lines = append(lines, warnStyle.Render("provider not found: "+m.providers.detailProvider))
		return strings.Join(lines, "\n")
	}

	name := m.providers.detailProvider
	status := "no-key"
	if strings.EqualFold(name, "offline") {
		status = "offline"
	} else if strings.TrimSpace(prof.APIKey) != "" || strings.TrimSpace(prof.BaseURL) != "" {
		status = "ready"
	}

	// Header badges
	badges := []string{providerStatusStyle(status)}
	badges = append(badges, apiKeySourceBadge(name, prof))
	if m.eng != nil && strings.EqualFold(m.eng.Config.Providers.Primary, name) {
		badges = append(badges, accentStyle.Render("[primary]"))
	}
	if m.eng != nil {
		fbs := m.eng.FallbackProviders()
		for fi, fb := range fbs {
			if strings.EqualFold(fb, name) {
				pos := fmt.Sprintf("%d", fi+1)
				suffix := "th"
				switch pos {
				case "1":
					suffix = "st"
				case "2":
					suffix = "nd"
				case "3":
					suffix = "rd"
				}
				badges = append(badges, subtleStyle.Render("[fallback: "+pos+suffix+"]"))
				break
			}
		}
	}
	lines = append(lines, "")
	lines = append(lines, accentStyle.Render(name))
	badgeLine := "  " + strings.Join(badges, "  ")
	if width > 0 {
		badgeLine = truncateSingleLine(badgeLine, width-2)
	}
	lines = append(lines, badgeLine)
	lines = append(lines, "")
	lines = append(lines, sectionTitleStyle.Render("Profile"))

	// Profile fields
	protocol := nonEmpty(prof.Protocol, "(auto)")
	baseURL := nonEmpty(prof.BaseURL, "(default)")
	maxContext := fmt.Sprintf("%d", prof.MaxContext)
	if prof.MaxContext == 0 {
		maxContext = "(default)"
	}
	maxTokens := fmt.Sprintf("%d", prof.MaxTokens)
	if prof.MaxTokens == 0 {
		maxTokens = "(default)"
	}
	if m.providers.profileEditMode {
		fields := []struct {
			label  string
			value  string
			active bool
		}{
			{"protocol", protocol, m.providers.profileEditField == 0},
			{"base_url", baseURL, m.providers.profileEditField == 1},
			{"max_context", maxContext, m.providers.profileEditField == 2},
			{"max_tokens", maxTokens, m.providers.profileEditField == 3},
		}
		for _, f := range fields {
			prefix := "  "
			var label string
			val := f.value
			if f.active {
				prefix = accentStyle.Render("▶ ")
				label = accentStyle.Render(f.label)
				if m.providers.profileEditDraft != "" {
					val = accentStyle.Render(m.providers.profileEditDraft)
				} else {
					val = accentStyle.Render(val)
				}
			} else {
				label = subtleStyle.Render(f.label)
				val = subtleStyle.Render(val)
			}
			lines = append(lines, prefix+label+"="+val)
		}
	} else {
		lines = append(lines, "  "+subtleStyle.Render("protocol:")+" "+protocol)
		lines = append(lines, "  "+subtleStyle.Render("base_url:")+" "+baseURL)
		lines = append(lines, "  "+subtleStyle.Render("max_context:")+" "+maxContext)
		lines = append(lines, "  "+subtleStyle.Render("max_tokens:")+" "+maxTokens)
	}

	// Best-for tags from the provider row
	for _, r := range m.providers.rows {
		if strings.EqualFold(r.Name, m.providers.detailProvider) {
			if len(r.BestFor) > 0 {
				lines = append(lines, "")
				lines = append(lines, "  "+subtleStyle.Render("best_for: ")+strings.Join(r.BestFor, ", "))
			}
			break
		}
	}

	// Models section with numbered list and scroll window
	models := prof.AllModels()
	lines = append(lines, "")
	selectedIdx := m.providers.modelEditIdx
	const modelWindow = 12
	start := 0
	if selectedIdx >= modelWindow {
		start = selectedIdx - modelWindow + 1
	}
	end := start + modelWindow
	if end > len(models) {
		end = len(models)
	}
	var modelTitle string
	if len(models) > modelWindow {
		modelTitle = fmt.Sprintf("Models (%d-%d of %d)", start+1, end, len(models))
	} else {
		modelTitle = fmt.Sprintf("Models (%d)", len(models))
	}
	lines = append(lines, sectionTitleStyle.Render(modelTitle))
	if len(models) == 0 {
		lines = append(lines, "  "+warnStyle.Render("No models configured"))
		lines = append(lines, "  "+subtleStyle.Render("Press Enter → Add Model to add one."))
	} else {
		if start > 0 {
			lines = append(lines, "  "+subtleStyle.Render(fmt.Sprintf("... %d more above", start)))
		}
		activeModel := strings.TrimSpace(prof.Model)
		for i := start; i < end; i++ {
			model := models[i]
			num := fmt.Sprintf("%2d.", i+1)
			prefix := "  " + num + " "
			label := model
			isActive := strings.EqualFold(model, activeModel)
			if i == m.providers.modelEditIdx {
				prefix = accentStyle.Render("▶ ") + num + " "
				label = accentStyle.Render(label)
				if isActive {
					label += okStyle.Render(" ← active")
				}
			} else if isActive {
				label = label + okStyle.Render(" ← active")
			}
			lines = append(lines, prefix+label)
		}
		if end < len(models) {
			lines = append(lines, "  "+subtleStyle.Render(fmt.Sprintf("... %d more below", len(models)-end)))
		}
	}

	// Fallback models
	if len(prof.FallbackModels) > 0 {
		lines = append(lines, "")
		lines = append(lines, sectionTitleStyle.Render(fmt.Sprintf("Fallback Models (%d)", len(prof.FallbackModels))))
		for i, model := range prof.FallbackModels {
			lines = append(lines, fmt.Sprintf("  %2d. %s", i+1, model))
		}
	}

	// Model picker
	if m.providers.modelPickerActive {
		lines = append(lines, "", sectionTitleStyle.Render("Add Model"))
		if m.providers.modelPickerManual {
			lines = append(lines, "  "+accentStyle.Render("▶ ")+accentStyle.Render(m.providers.modelPickerDraft))
		} else {
			items := m.providers.modelPickerItems
			if len(items) == 0 {
				lines = append(lines, "  "+subtleStyle.Render("(no models in cache — press m for manual entry)"))
			} else {
				lines = append(lines, "  "+subtleStyle.Render(fmt.Sprintf("(%d models in cache)", len(items))))
			}
			const pickerWindow = 12
			idx := m.providers.modelPickerIndex
			start := 0
			if idx >= pickerWindow {
				start = idx - pickerWindow + 1
			}
			end := start + pickerWindow
			if end > len(items) {
				end = len(items)
			}
			if start > 0 {
				lines = append(lines, "    "+subtleStyle.Render(fmt.Sprintf("... %d more above", start)))
			}
			for i := start; i < end; i++ {
				prefix := "    "
				label := items[i]
				if i == idx {
					prefix = accentStyle.Render("▶ ")
					label = accentStyle.Render(label)
				}
				lines = append(lines, prefix+label)
			}
			if end < len(items) {
				lines = append(lines, "    "+subtleStyle.Render(fmt.Sprintf("... %d more below", len(items)-end)))
			}
		}
	}

	lines = append(lines, m.renderProvidersMenu(width-2)...)
	return strings.Join(lines, "\n")
}
