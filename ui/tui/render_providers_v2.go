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
			lines = append(lines, subtleStyle.Render("No providers match your search."))
		} else {
			lines = append(lines,
				warnStyle.Render("No providers registered"),
				"",
				subtleStyle.Render("Engine started without providers."),
				subtleStyle.Render("Usually because another dfmc"),
				subtleStyle.Render("process holds the store lock."),
				"",
				subtleStyle.Render("→ menu has 'Add new provider'"),
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

// --- DETAIL PANE -------------------------------------------------------------

func (m Model) renderProvidersDetailPane(width, height int, _ tabPaletteEntry, rows []providerRow) string {
	header := titleStyle.Bold(true).Render("◇ DETAIL")
	lines := []string{
		header,
		renderDivider(width - 2),
		"",
	}
	if len(rows) == 0 {
		lines = append(lines, subtleStyle.Render("Select a provider to see its detail."))
		return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
	}
	scroll := clampScroll(m.providers.scroll, len(rows))
	row := rows[scroll]

	// Title — provider name + role.
	title := titleStyle.Bold(true).Render(row.Name)
	if row.IsPrimary {
		title += "  " + accentStyle.Render("PRIMARY")
	} else if pos := m.providerFallbackPosition(row.Name); pos > 0 {
		title += "  " + subtleStyle.Render(fmt.Sprintf("FALLBACK #%d", pos))
	}
	lines = append(lines, title, "")

	// Per-attribute key/value rows — readable, one fact per line.
	rowsKV := []struct{ k, v string }{
		{"Status", strings.ToUpper(row.Status)},
		{"Model", nonEmpty(row.Model, "(none)")},
		{"Models", fmt.Sprintf("%d configured", len(row.Models))},
		{"Protocol", nonEmpty(row.Protocol, "—")},
		{"Max context", fmt.Sprintf("%d tokens", row.MaxContext)},
		{"Tool support", boolWord(row.SupportsTools)},
		{"Tool style", nonEmpty(row.ToolStyle, "—")},
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

	// Status hint — what the user should do next.
	lines = append(lines, "")
	switch strings.ToLower(row.Status) {
	case "ready":
		lines = append(lines, okStyle.Render("✓ Ready to call. → menu to set primary, fallback, or cycle model."))
	case "no-key":
		lines = append(lines, warnStyle.Render("⚠ Missing API key."))
		if envVar := providerEnvVarLookup(row.Name); envVar != "" {
			lines = append(lines, subtleStyle.Render("  Set "+envVar+" or add api_key to providers.profiles."+row.Name))
		} else {
			lines = append(lines, subtleStyle.Render("  Add api_key to providers.profiles."+row.Name))
		}
	case "offline":
		lines = append(lines, subtleStyle.Render("◌ Offline provider — deterministic fallback, no network."))
	}

	body := strings.Join(lines, "\n")
	rowsOut := splitLines(body)
	if len(rowsOut) > height {
		rowsOut = rowsOut[:height]
	}
	return lipgloss.NewStyle().Width(width).Render(strings.Join(rowsOut, "\n"))
}

func boolWord(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// --- METADATA PANE -----------------------------------------------------------

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
	body := strings.Join(rendered, "\n")
	rowsOut := splitLines(body)
	if len(rowsOut) > height {
		rowsOut = rowsOut[:height]
	}
	return strings.Join(rowsOut, "\n")
}

func (m Model) providersMetaCards(rows []providerRow) []panelCard {
	if len(rows) == 0 {
		return nil
	}

	// ROUTING card — primary + fallback chain.
	primary := ""
	if m.eng != nil {
		primary = strings.TrimSpace(m.eng.Config.Providers.Primary)
	}
	primaryVal := nonEmpty(primary, "(none)")
	chainVal := "(empty)"
	if m.eng != nil {
		chain := m.eng.FallbackProviders()
		if len(chain) > 0 {
			parts := make([]string, 0, len(chain))
			for i, n := range chain {
				parts = append(parts, fmt.Sprintf("%d. %s", i+1, n))
			}
			chainVal = strings.Join(parts, " → ")
		}
	}
	routing := panelCard{
		Icon:  "⇆",
		Title: "Routing",
		Rows: []panelCardRow{
			{Key: "Primary", Value: primaryVal},
			{Key: "Fallback", Value: chainVal},
		},
		FooterHint: "→ menu: set primary · toggle fallback",
	}

	// ACTIONS card — every keyboard surface, labelled.
	actions := panelCard{
		Icon:  "⚒",
		Title: "Actions",
		Rows: []panelCardRow{
			{Key: "↑↓", Value: "select provider"},
			{Key: "→", Value: "open action menu"},
			{Key: "enter", Value: "view detail"},
			{Key: "/", Value: "search"},
			{Key: "p / f / m", Value: "primary · fallback · cycle model"},
			{Key: "s / n / r", Value: "save · new provider · refresh"},
		},
		FooterHint: "ctrl+h opens this help overlay",
	}
	return []panelCard{routing, actions}
}

func (m Model) renderProvidersMetaInline(width int, rows []providerRow) string {
	_ = width
	if len(rows) == 0 {
		return ""
	}
	primary := "(none)"
	if m.eng != nil {
		primary = nonEmpty(strings.TrimSpace(m.eng.Config.Providers.Primary), "(none)")
	}
	chainStr := "(empty)"
	if m.eng != nil {
		chain := m.eng.FallbackProviders()
		if len(chain) > 0 {
			chainStr = strings.Join(chain, " → ")
		}
	}
	parts := []string{
		accentStyle.Render("primary: ") + primary,
		subtleStyle.Render("fallback: ") + chainStr,
		subtleStyle.Render("→ action menu · enter detail"),
	}
	return strings.Join(parts, "  ·  ")
}
