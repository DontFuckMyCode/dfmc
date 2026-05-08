// render_providers_v2_panes.go — DETAIL + METADATA panes for the
// rebuilt F4 Providers explorer. Sibling of render_providers_v2.go
// which keeps the layout dispatcher (renderProviderListViewV2 + the
// 3-pane / 2-pane / single-pane width split), the LIST pane (header
// chip + per-row cursor/role/status formatter), the providerStatusChip
// helper, and the providerFallbackPosition lookup.
//
// Splitting the panes into a sibling keeps the layout code small and
// makes each pane's contract (what it renders, what it depends on)
// easier to find when iterating on copy.

package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

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
	routingRows := []panelCardRow{
		{Key: "Primary", Value: primaryVal},
		{Key: "Fallback", Value: chainVal},
	}
	// Show the EFFECTIVE save target — i.e. the file the next click
	// will actually write to, which is the same file that wins on
	// reload. Without this the user can't tell why "I saved minimax"
	// flips back to whatever the project file says next entry.
	if path, err := m.configPathForScope(m.effectivePersistScope()); err == nil {
		routingRows = append(routingRows, panelCardRow{Key: "Saves to", Value: displayConfigPath(path)})
	}
	// When project AND user both have providers blocks, the user can
	// be confused which one is winning ("amk neden minimax değil").
	// Spell out the shadowed file so the conflict is visible.
	if m.projectConfigHasProvidersOverride() {
		if userPath, err := m.userConfigPath(); err == nil {
			routingRows = append(routingRows, panelCardRow{Key: "Shadowed", Value: displayConfigPath(userPath)})
		}
	}
	routing := panelCard{
		Icon:       "⇆",
		Title:      "Routing",
		Rows:       routingRows,
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
