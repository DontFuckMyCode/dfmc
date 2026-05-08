package tui

// provider_panel_render.go — F-key Providers panel render dispatcher
// + V2 banner + new-provider draft view + the action-menu and confirm
// dialog overlays. Pre-V2 inline list view (renderProviderListViewLegacy)
// and the row/detail formatters (formatProviderRowNumbered,
// formatProviderDetail) live in provider_panel_render_legacy.go.
// V2 cards (renderProviderListViewV2) and pipeline/detail subviews live
// in their own provider_panel_*.go siblings.

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func (m Model) renderProvidersView(width int) string {
	out := m.renderProvidersViewInner(width)
	if m.actionMenu.open && m.actionMenu.owner == "Providers" {
		out += "\n\n" + m.renderActionMenu(width)
	}
	return out
}

func (m Model) renderProvidersViewInner(width int) string {
	if m.providers.confirmAction != "" {
		return m.renderProvidersConfirm(width)
	}
	switch m.providers.viewMode {
	case "detail":
		return m.renderProviderDetailView(width)
	case "pipelines":
		return m.renderPipelinesView(width)
	case "new_provider":
		return m.renderNewProviderView(width)
	default:
		return m.renderProviderListView(width)
	}
}

// providersTopBanner — title + ready/no-key/offline chip summary on
// the right. Mirrors the F2-F11 banner pattern.
func (m Model) providersTopBanner(width int) string {
	title := titleStyle.Bold(true).Render("⚑ PROVIDERS")
	ready, noKey := 0, 0
	for _, r := range m.providers.rows {
		switch r.Status {
		case "ready":
			ready++
		case "no-key":
			noKey++
		}
	}
	offline := len(m.providers.rows) - ready - noKey
	chips := []string{
		okStyle.Render(fmt.Sprintf(" %d ready ", ready)),
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

func (m Model) renderProviderListView(width int) string {
	// V2 layout: clean 3-pane explorer with banner + per-provider
	// detail card + ROUTING/ACTIONS cards. Falls back to legacy when
	// the action menu's built-in confirm or pipeline editor needs the
	// older inline shape (handled in the wrapper above).
	if !m.providers.menuActive {
		out := m.renderProviderListViewV2(width)
		// Keep the legacy overlay menu rendering compatibility for
		// users mid-flow — it doesn't show unless menuActive=true.
		if extras := m.renderProvidersMenu(width - 2); len(extras) > 0 {
			out += "\n" + strings.Join(extras, "\n")
		}
		return out
	}
	return m.renderProviderListViewLegacy(width)
}

// refreshProvidersRows re-reads the router and stamps the fresh rows
// into the Model. Pure — invoked from 'r' and from the tab-switch
// first-activation path.
func (m Model) renderNewProviderView(width int) string {
	width = clampInt(width, 24, 1000)
	header := sectionHeader("⚑", "New Provider")
	hint := subtleStyle.Render("type name · enter create · esc cancel")
	lines := []string{header, hint, renderDivider(width - 2), ""}
	lines = append(lines, "  name: "+accentStyle.Render(m.providers.newProviderDraft))
	return strings.Join(lines, "\n")
}

func (m Model) renderProvidersMenu(width int) []string {
	if !m.providers.menuActive {
		return nil
	}
	labels := m.providers.menuLabels
	index := m.providers.menuIndex
	if len(labels) == 0 {
		return nil
	}

	var lines []string
	lines = append(lines, "")

	// Title with target context
	title := "Actions"
	switch m.providers.viewMode {
	case "detail":
		title = "Actions for " + m.providers.detailProvider
	case "pipelines":
		scroll := clampScroll(m.providers.pipelineScroll, len(m.providers.pipelineNames))
		if scroll >= 0 && scroll < len(m.providers.pipelineNames) {
			title = "Actions for pipeline " + m.providers.pipelineNames[scroll]
		} else {
			title = "Pipeline Actions"
		}
	default:
		scroll := clampScroll(m.providers.scroll, len(m.providers.rows))
		if scroll >= 0 && scroll < len(m.providers.rows) {
			title = "Actions for " + m.providers.rows[scroll].Name
		}
	}
	lines = append(lines, sectionTitleStyle.Render(title))

	disabled := m.providers.menuDisabled
	reasons := m.providers.menuDisabledReasons
	for i, label := range labels {
		num := fmt.Sprintf("%d. ", i+1)
		var prefix string
		l := label
		isDisabled := i < len(disabled) && disabled[i]
		isDanger := strings.Contains(strings.ToLower(label), "delete")
		if i == index {
			prefix = accentStyle.Render("▶ ") + accentStyle.Render(num)
			if isDisabled {
				l = disabledStyle.Render(l)
			} else if isDanger {
				l = failStyle.Render(l)
			} else {
				l = accentStyle.Render(l)
			}
		} else {
			prefix = "   " + subtleStyle.Render(num)
			if isDisabled {
				l = disabledStyle.Render(l)
			} else if isDanger {
				l = warnStyle.Render(l)
			} else {
				l = subtleStyle.Render(l)
			}
		}
		if isDisabled && i < len(reasons) && reasons[i] != "" {
			l += subtleStyle.Render(" (" + reasons[i] + ")")
		}
		lines = append(lines, prefix+l)
	}

	hint := "j/k scroll · 1-9 jump · enter select · esc cancel"
	lines = append(lines, subtleStyle.Render("  "+hint))
	return lines
}

func (m Model) renderProvidersConfirm(width int) string {
	width = clampInt(width, 24, 1000)
	lines := []string{""}

	var icon, question string
	switch m.providers.confirmAction {
	case "delete_provider":
		icon = warnStyle.Render("⚠")
		question = fmt.Sprintf("Delete provider %s?", accentStyle.Render(m.providers.confirmTarget))
	case "delete_model":
		icon = warnStyle.Render("⚠")
		question = fmt.Sprintf("Delete model %s from %s?",
			accentStyle.Render(m.providers.confirmTarget),
			subtleStyle.Render(m.providers.detailProvider))
	case "delete_pipeline":
		icon = warnStyle.Render("⚠")
		question = fmt.Sprintf("Delete pipeline %s?", accentStyle.Render(m.providers.confirmTarget))
	default:
		icon = subtleStyle.Render("?")
		question = "Are you sure?"
	}

	lines = append(lines, "  "+icon+"  "+question)
	if m.providers.confirmAction == "delete_provider" {
		if m.eng != nil && strings.EqualFold(m.eng.Config.Providers.Primary, m.providers.confirmTarget) {
			lines = append(lines, "     "+warnStyle.Render("→ currently set as primary"))
		}
		if m.eng != nil {
			for _, fb := range m.eng.Config.Providers.Fallback {
				if strings.EqualFold(fb, m.providers.confirmTarget) {
					lines = append(lines, "     "+warnStyle.Render("→ in fallback chain"))
					break
				}
			}
		}
	}
	lines = append(lines, "")
	lines = append(lines, "     "+okStyle.Render("y")+subtleStyle.Render(" confirm  ")+
		failStyle.Render("n")+subtleStyle.Render(" cancel"))

	content := strings.Join(lines, "\n")
	// Frame the dialog with a warning-colored border
	frameStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorWarn).
		Padding(0, 1).
		Width(width - 4)
	return frameStyle.Render(content)
}
