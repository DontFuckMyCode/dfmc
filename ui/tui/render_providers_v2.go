package tui

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

func providerEnvVarLookup(name string) string { return config.EnvVarForProvider(name) }

func (m Model) renderProviderListViewV2(width int) string {
	return m.renderProviderListViewV2Sized(width, 24)
}

func (m Model) renderProviderListViewV2Sized(width, height int) string {
	width = clampInt(width, 24, 1000)
	height = max(height, 8)

	pal := paletteForTab("Providers", false)
	threePane := width >= 120
	twoPane := !threePane && width >= 80
	listW, detailW, metaW := providersPanelWidths(width, threePane, twoPane)

	rows := m.visibleProviderRows()
	banner := m.providersTopBanner(width)
	if len(rows) == 0 && strings.TrimSpace(m.providers.query) == "" {
		return banner + "\n" + m.renderProviderSetupDashboard(width, height, pal, threePane, twoPane)
	}
	listBlock := m.renderProvidersListPane(listW, height, pal, rows)
	detailBlock := m.renderProvidersDetailPane(detailW, height, pal, rows)

	var body string
	if threePane {
		metaBlock := m.renderProvidersMetaPane(metaW, height, pal, rows)
		body = lipgloss.JoinHorizontal(lipgloss.Top, listBlock, "  ", detailBlock, "  ", metaBlock)
	} else if twoPane {
		footer := m.renderProvidersMetaInline(width, rows)
		body = lipgloss.JoinHorizontal(lipgloss.Top, listBlock, "  ", detailBlock) + "\n" + footer
	} else {
		body = listBlock + "\n" + detailBlock
	}
	return banner + "\n" + body
}

func providersPanelWidths(total int, threePane, twoPane bool) (listW, detailW, metaW int) {
	if threePane {
		listW = max(total*28/100, 26)
		metaW = max(total*28/100, 28)
		detailW = max(total-listW-metaW-4, 32)
		return
	}
	if twoPane {
		listW = max(total*35/100, 26)
		detailW = max(total-listW-2, 28)
		return
	}
	return total, total, 0
}

func (m Model) renderProviderSetupDashboard(width, height int, pal tabPaletteEntry, threePane, twoPane bool) string {
	listW, detailW, metaW := providersPanelWidths(width, threePane, twoPane)
	actions := m.renderProviderSetupActionsPane(listW, height, pal)
	cache := m.renderProviderSetupCachePane(detailW, height)
	if threePane {
		routes := m.renderProviderSetupRoutesPane(metaW, height, pal)
		return lipgloss.JoinHorizontal(lipgloss.Top, actions, "  ", cache, "  ", routes)
	}
	if twoPane {
		return lipgloss.JoinHorizontal(lipgloss.Top, actions, "  ", cache) + "\n" +
			m.renderProvidersMetaInline(width, nil)
	}
	return actions + "\n" + cache
}

func (m Model) renderProviderSetupActionsPane(width, height int, pal tabPaletteEntry) string {
	lines := []string{
		titleStyle.Bold(true).Render("SETUP"),
		renderDivider(width - 2),
		"",
		accentStyle.Render("> Enter") + subtleStyle.Render(" open actions"),
		"  " + subtleStyle.Render("1. Sync models.dev catalog"),
		"  " + subtleStyle.Render("2. Add provider from models.dev catalog"),
		"  " + subtleStyle.Render("3. Paste key in the provider form"),
		"  " + subtleStyle.Render("4. Assign models to tiers"),
		"",
		sectionTitleStyle.Render("Available Actions"),
		"  " + okStyle.Render("Sync models.dev"),
		"  " + okStyle.Render("Add provider from models.dev"),
		"  " + okStyle.Render("Tiers"),
		"  " + okStyle.Render("Skill model routes"),
		"  " + warnStyle.Render("Reset all keys"),
	}
	out := splitLines(strings.Join(lines, "\n"))
	if len(out) > height {
		out = out[:height]
	}
	_ = pal
	return lipgloss.NewStyle().Width(width).Render(strings.Join(out, "\n"))
}

func (m Model) renderProviderSetupCachePane(width, height int) string {
	path := config.ModelsDevCachePath()
	status := warnStyle.Render("missing")
	detail := "Run Sync models.dev from actions."
	if st, err := os.Stat(path); err == nil {
		status = okStyle.Render("ready")
		detail = fmt.Sprintf("%s · %s", displayConfigPath(path), formatRelativeTime(st.ModTime()))
	}
	lines := []string{
		titleStyle.Bold(true).Render("CATALOG"),
		renderDivider(width - 2),
		"",
		"  " + subtleStyle.Render("models.dev cache: ") + status,
		"  " + subtleStyle.Render(truncateSingleLine(detail, max(width-4, 8))),
		"",
		sectionTitleStyle.Render("My Providers"),
		"  " + warnStyle.Render("No providers registered."),
		"  " + subtleStyle.Render("Add a custom provider or sync models.dev, then paste a key."),
		"  " + subtleStyle.Render("Saved keyed providers appear here; env-only defaults remain usable from chat."),
		"",
		sectionTitleStyle.Render("Storage"),
		"  " + subtleStyle.Render("config: ~/.dfmc/config.yaml"),
		"  " + subtleStyle.Render("master: ~/.dfmc/secrets/master.key"),
	}
	out := splitLines(strings.Join(lines, "\n"))
	if len(out) > height {
		out = out[:height]
	}
	return lipgloss.NewStyle().Width(width).Render(strings.Join(out, "\n"))
}

func (m Model) renderProviderSetupRoutesPane(width, height int, pal tabPaletteEntry) string {
	lines := []string{
		titleStyle.Bold(true).Render("ROUTES"),
		renderDivider(width - 2),
		"",
		sectionTitleStyle.Render("Tiers"),
	}
	for _, tier := range providerTierNames {
		lines = append(lines, "  "+accentStyle.Render(tier)+subtleStyle.Render("  primary + 3 fallbacks"))
	}
	lines = append(lines,
		"",
		sectionTitleStyle.Render("Skills"),
		"  "+subtleStyle.Render("Each skill can use a tier or a direct keyed model."),
		"",
		sectionTitleStyle.Render("Keys"),
		"  "+subtleStyle.Render("Paste is plain text in the form; masking happens after input."),
	)
	out := splitLines(strings.Join(lines, "\n"))
	if len(out) > height {
		out = out[:height]
	}
	_ = pal
	return lipgloss.NewStyle().Width(width).Render(strings.Join(out, "\n"))
}

func (m Model) renderProvidersListPane(width, height int, pal tabPaletteEntry, rows []providerRow) string {
	header := titleStyle.Bold(true).Render("MY PROVIDERS")
	count := len(rows)
	chip := okStyle
	if count == 0 {
		chip = subtleStyle
	}
	chipRendered := chip.Render(fmt.Sprintf(" %d ", count))
	gap := max(width-lipgloss.Width(header)-lipgloss.Width(chipRendered)-2, 1)
	lines := []string{
		header + strings.Repeat(" ", gap) + chipRendered,
		renderDivider(width - 2),
		"",
	}

	if count == 0 {
		if m.providers.query != "" {
			lines = append(lines,
				subtleStyle.Render("No providers match your search."),
				subtleStyle.Render("Esc clears search; Enter opens actions."),
			)
		} else {
			lines = append(lines,
				warnStyle.Render("No providers registered"),
				"",
				subtleStyle.Render("Open actions with Enter, sync models.dev, then add a custom provider from that catalog."),
				subtleStyle.Render("Only providers with saved keys show here; keys are stored under ~/.dfmc."),
			)
		}
		return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
	}

	rowBudget := max(height-6, 1)
	scroll := clampScroll(m.providers.scroll, len(rows))
	start, end := scrollWindow(scroll, len(rows), rowBudget)
	for i := start; i < end; i++ {
		lines = append(lines, m.renderProviderListRowV2(rows[i], i, scroll, width, pal))
	}
	lines = append(lines, "", subtleStyle.Render(fmt.Sprintf("%d / %d providers", scroll+1, len(rows))))
	if m.providers.query != "" {
		lines = append(lines, subtleStyle.Render("query: "+m.providers.query))
	}
	lines = append(lines, subtleStyle.Render("up/down move - enter actions - esc back - ctrl+f search"))
	return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
}

func (m Model) renderProviderListRowV2(row providerRow, idx, cursor, width int, pal tabPaletteEntry) string {
	prefix := "  "
	if idx == cursor {
		prefix = lipgloss.NewStyle().Foreground(pal.Accent).Bold(true).Render("> ")
	}
	statusChip := providerStatusChip(row.Status)
	roleBadge := ""
	if row.IsPrimary {
		roleBadge = " " + accentStyle.Bold(true).Render("PRIMARY")
	}
	chrome := lipgloss.Width(prefix) + lipgloss.Width(statusChip) + lipgloss.Width(roleBadge) + 2
	nameWidth := max(width-chrome, 8)
	name := truncateForLine(row.Name, nameWidth)
	if idx == cursor {
		name = lipgloss.NewStyle().Foreground(pal.Accent).Bold(true).Render(name)
	}
	return prefix + name + roleBadge + "  " + statusChip
}

func providerStatusChip(status string) string {
	switch strings.ToLower(status) {
	case "ready":
		return okStyle.Render(" READY ")
	case "offline":
		return subtleStyle.Render(" OFFLINE ")
	case "no-key":
		return warnStyle.Render(" NO KEY ")
	default:
		return subtleStyle.Render(" " + strings.ToUpper(status) + " ")
	}
}
