// render_providers_v2.go — F4 Providers panel rebuilt as a clean
// 3-pane explorer matching F2/F3/F4. The legacy provider list (each
// row crammed 9+ fields onto one line) is the worst-readability tab
// in the TUI; this rewrite reduces each row to "▶ name [STATUS]" and
// pushes the full metadata into a per-selection DETAIL card on the
// right.
//
// Layout strategy:
//   ≥120 cols → 3 panes (28% list · 44% detail · 28% routing/actions)
//   80-119    → 2 panes (35% list · 65% detail) + inline footer
//   <80       → 1 pane stack
//
// Right column cards (wide):
//   DETAILS    — model, protocol, max-context, tool-style, best-for
//   ROUTING    — primary chip + fallback chain (numbered)
//   ACTIONS    — every keyboard surface (set primary / fallback /
//                cycle model / save / new / refresh / search / detail)

package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

func providerEnvVarLookup(name string) string { return config.EnvVarForProvider(name) }

// renderProviderListViewV2 is the rebuilt list view. Called from
// renderProvidersViewInner when m.providers.viewMode is the default.
func (m Model) renderProviderListViewV2(width int) string {
	width = clampInt(width, 24, 1000)
	height := 24

	pal := paletteForTab("Providers", false)

	threePane := width >= 120
	twoPane := !threePane && width >= 80
	listW, detailW, metaW := providersPanelWidths(width, threePane, twoPane)

	rows := filteredProviderRows(m.providers.rows, m.providers.query)

	banner := m.providersTopBanner(width)
	listBlock := m.renderProvidersListPane(listW, height, pal, rows)
	detailBlock := m.renderProvidersDetailPane(detailW, height, pal, rows)
	var body string
	if threePane {
		metaBlock := m.renderProvidersMetaPane(metaW, height, pal, rows)
		body = lipgloss.JoinHorizontal(lipgloss.Top,
			listBlock, "  ", detailBlock, "  ", metaBlock)
	} else if twoPane {
		footer := m.renderProvidersMetaInline(width, rows)
		grid := lipgloss.JoinHorizontal(lipgloss.Top, listBlock, "  ", detailBlock)
		body = grid + "\n" + footer
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

// --- LIST PANE ---------------------------------------------------------------

func (m Model) renderProvidersListPane(width, height int, pal tabPaletteEntry, rows []providerRow) string {
	header := titleStyle.Bold(true).Render("⚑ PROVIDERS")
	count := len(rows)
	chip := okStyle
	if count == 0 {
		chip = subtleStyle
	}
	chipRendered := chip.Render(fmt.Sprintf(" %d ", count))
	gap := max(width-lipgloss.Width(header)-lipgloss.Width(chipRendered)-2, 1)
	headerLine := header + strings.Repeat(" ", gap) + chipRendered

	lines := []string{
		headerLine,
		renderDivider(width - 2),
		"",
	}

	if count == 0 {
		if m.providers.query != "" {
			lines = append(lines,
				subtleStyle.Render("No providers match your search."),
				subtleStyle.Render("Press c to clear the query, or / to edit it."),
			)
		} else {
			lines = append(lines,
				warnStyle.Render("No providers registered"),
				"",
				subtleStyle.Render("Add a new provider with → menu."),
				subtleStyle.Render("Or /provider add."),
				subtleStyle.Render("Or set ANTHROPIC_API_KEY / OPENAI_API_KEY in env."),
				"",
				subtleStyle.Render("Without one, DFMC falls back to the offline placeholder — chat works but every reply is canned."),
				subtleStyle.Render("Tip: another dfmc process holding the store lock can also block providers — close it first."),
			)
		}
		return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
	}

	rowBudget := max(height-6, 6)
	scroll := clampScroll(m.providers.scroll, len(rows))
	start, end := scrollWindow(scroll, len(rows), rowBudget)
	for i := start; i < end; i++ {
		lines = append(lines, m.renderProviderListRowV2(rows[i], i, scroll, width, pal))
	}
	lines = append(lines, "",
		subtleStyle.Render(fmt.Sprintf("%d / %d providers", scroll+1, len(rows))))
	if m.providers.query != "" {
		lines = append(lines, subtleStyle.Render("query: "+m.providers.query))
	}
	// Always-visible keyboard contract for the providers list pane.
	// Right pane (action menu / detail) repeats its own subset.
	lines = append(lines,
		subtleStyle.Render("j/k scroll · enter detail · → action menu · / search · c clear"))
	return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
}

// renderProviderListRowV2 — clean single-line row: cursor + name +
// PRIMARY/fallback marker + status chip. Everything else is in the
// detail pane.
func (m Model) renderProviderListRowV2(row providerRow, idx, cursor, width int, pal tabPaletteEntry) string {
	prefix := "  "
	if idx == cursor {
		prefix = lipgloss.NewStyle().Foreground(pal.Accent).Bold(true).Render("▶ ")
	}
	statusChip := providerStatusChip(row.Status)
	roleBadge := ""
	if row.IsPrimary {
		roleBadge = " " + accentStyle.Bold(true).Render("PRIMARY")
	} else if pos := m.providerFallbackPosition(row.Name); pos > 0 {
		roleBadge = " " + subtleStyle.Render(fmt.Sprintf("FB#%d", pos))
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

// providerFallbackPosition — 1-based position in the fallback chain,
// or 0 if the provider isn't in it.
func (m Model) providerFallbackPosition(name string) int {
	if m.eng == nil {
		return 0
	}
	want := strings.ToLower(strings.TrimSpace(name))
	for i, n := range m.eng.FallbackProviders() {
		if strings.ToLower(strings.TrimSpace(n)) == want {
			return i + 1
		}
	}
	return 0
}

// DETAIL + METADATA panes (renderProvidersDetailPane / boolWord /
// renderProvidersMetaPane / providersMetaCards / renderProvidersMetaInline)
// live in render_providers_v2_panes.go.
