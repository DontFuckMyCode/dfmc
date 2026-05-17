package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func (m Model) renderProvidersViewSized(width, height int) string {
	out := m.renderProvidersViewInnerSized(width, height)
	if m.actionMenu.open && m.actionMenu.owner == "Providers" {
		out += "\n\n" + m.renderActionMenu(width)
	}
	return out
}

func (m Model) renderProvidersViewInnerSized(width, height int) string {
	if m.providers.confirmAction != "" {
		return m.renderProvidersConfirm(width)
	}
	switch m.providers.viewMode {
	case "detail":
		return m.renderProviderDetailViewSized(width, height)
	case providerViewCatalog:
		return m.renderProviderCatalogViewSized(width, height)
	case providerViewCatalogForm:
		return m.renderProviderCatalogFormView(width)
	case providerViewTiers:
		return m.renderProviderTiersViewSized(width, height)
	case providerViewSkills:
		return m.renderProviderSkillsViewSized(width, height)
	case "pipelines":
		return m.renderPipelinesViewSized(width, height)
	case "new_provider":
		return m.renderNewProviderView(width)
	default:
		return m.renderProviderListViewSized(width, height)
	}
}

func (m Model) providersTopBanner(width int) string {
	title := titleStyle.Bold(true).Render("MODELS & PROVIDERS")
	ready, noKey := 0, 0
	rows := m.visibleProviderRows()
	for _, r := range rows {
		switch r.Status {
		case "ready":
			ready++
		case "no-key":
			noKey++
		}
	}
	offline := len(rows) - ready - noKey
	chips := []string{okStyle.Render(fmt.Sprintf(" %d my providers ", len(rows)))}
	if ready > 0 {
		chips = append(chips, okStyle.Render(fmt.Sprintf(" %d ready ", ready)))
	}
	if noKey > 0 {
		chips = append(chips, warnStyle.Render(fmt.Sprintf(" %d no-key ", noKey)))
	}
	if offline > 0 {
		chips = append(chips, subtleStyle.Render(fmt.Sprintf(" %d offline ", offline)))
	}
	if m.providers.syncing {
		chips = append(chips, infoStyle.Render(" SYNCING "))
	}
	chipStrip := strings.Join(chips, " ")
	gap := max(width-lipgloss.Width(title)-lipgloss.Width(chipStrip)-4, 1)
	return title + strings.Repeat(" ", gap) + chipStrip
}

func (m Model) renderProviderListViewSized(width, height int) string {
	return m.renderProviderListViewV2Sized(width, height)
}

func (m Model) renderNewProviderView(width int) string {
	width = clampInt(width, 24, 1000)
	header := sectionHeader("+", "New Provider")
	hint := subtleStyle.Render("enter edit/create - esc cancel")
	lines := []string{header, hint, renderDivider(width - 2), ""}
	lines = append(lines, "  name: "+accentStyle.Render(m.providers.newProviderDraft))
	if m.providers.textEditActive {
		lines = append(lines, "", m.renderProviderTextEditBox(width))
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderProvidersConfirm(width int) string {
	width = clampInt(width, 24, 1000)
	lines := []string{""}

	var icon, question string
	switch m.providers.confirmAction {
	case "delete_provider":
		icon = warnStyle.Render("!")
		question = fmt.Sprintf("Delete provider %s?", accentStyle.Render(m.providers.confirmTarget))
	case "delete_model":
		icon = warnStyle.Render("!")
		question = fmt.Sprintf("Delete model %s from %s?",
			accentStyle.Render(m.providers.confirmTarget),
			subtleStyle.Render(m.providers.detailProvider))
	case "delete_pipeline":
		icon = warnStyle.Render("!")
		question = fmt.Sprintf("Delete pipeline %s?", accentStyle.Render(m.providers.confirmTarget))
	case "reset_all_keys":
		icon = warnStyle.Render("!")
		question = "Reset all saved provider keys?"
	default:
		icon = subtleStyle.Render("?")
		question = "Are you sure?"
	}

	lines = append(lines, "  "+icon+"  "+question)
	if m.providers.confirmAction == "delete_provider" && m.eng != nil && m.eng.Config != nil {
		if strings.EqualFold(m.eng.Config.Providers.Primary, m.providers.confirmTarget) {
			lines = append(lines, "     "+warnStyle.Render("currently set as primary"))
		}
		for _, fb := range m.eng.Config.Providers.Fallback {
			if strings.EqualFold(fb, m.providers.confirmTarget) {
				lines = append(lines, "     "+warnStyle.Render("in fallback chain"))
				break
			}
		}
	}
	if m.providers.confirmAction == "reset_all_keys" {
		lines = append(lines, "     "+warnStyle.Render("This removes api_key/api_key_enc for every provider in ~/.dfmc/config.yaml."))
	}
	lines = append(lines, "")
	lines = append(lines, "     "+okStyle.Render("enter")+subtleStyle.Render(" confirm  ")+
		failStyle.Render("esc")+subtleStyle.Render(" cancel"))

	content := strings.Join(lines, "\n")
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorWarn).
		Padding(0, 1).
		Width(width - 4).
		Render(content)
}
