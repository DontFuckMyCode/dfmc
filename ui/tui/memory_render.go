package tui

// memory_render.go — rendering surface for the Memory panel. Sibling
// of memory.go which keeps the load command, key dispatch, action
// menu, and tier-cycle helper. Pure render: filteredMemoryEntries,
// formatMemoryRow, oneLine, renderMemoryView, memoryTopBanner.
//
// oneLine is shared with the Conversations preview surface — both
// panels need the embedded-newline collapse to stay aligned, and
// keeping it next to formatMemoryRow's only call site is more
// findable than burying it in a generic strings_helpers.go.

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// filteredMemoryEntries applies the in-panel search query over the
// loaded entries. The filter matches Category / Key / Value / ID.
func filteredMemoryEntries(entries []types.MemoryEntry, query string) []types.MemoryEntry {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return entries
	}
	out := entries[:0:0]
	for _, e := range entries {
		if strings.Contains(strings.ToLower(e.Category), q) ||
			strings.Contains(strings.ToLower(e.Key), q) ||
			strings.Contains(strings.ToLower(e.Value), q) ||
			strings.Contains(strings.ToLower(e.ID), q) {
			out = append(out, e)
		}
	}
	return out
}

// formatMemoryRow renders one entry as a single line, clipped to width.
// Shape: `[tier] category · key — value`. When Category/Key are blank
// (bare episodic interaction) we fall back to the value on its own.
func formatMemoryRow(e types.MemoryEntry, width int, selected bool) string {
	tierLabel := strings.ToUpper(string(e.Tier))
	if tierLabel == "" {
		tierLabel = "MEM"
	}
	head := subtleStyle.Render("[" + tierLabel + "]")
	var body strings.Builder
	cat := strings.TrimSpace(e.Category)
	key := strings.TrimSpace(e.Key)
	val := strings.TrimSpace(e.Value)
	if cat != "" {
		body.WriteString(accentStyle.Render(cat))
		if key != "" {
			body.WriteString(" · ")
			body.WriteString(key)
		}
	} else if key != "" {
		body.WriteString(key)
	}
	if val != "" {
		if body.Len() > 0 {
			body.WriteString(" — ")
		}
		body.WriteString(val)
	}
	line := head + " " + oneLine(body.String())
	if width > 0 {
		return truncateSingleLine(line, width)
	}
	return line
}

// memoryDetailWrap is a simple word-wrap for the per-entry detail
// expand (Phase H item 3). Splits a line on word boundaries; any
// single token longer than the width falls through as a hard chunk
// rather than breaking mid-word badly. Returns at least one line so
// callers can blindly iterate without a length check.
func memoryDetailWrap(s string, width int) []string {
	if width <= 0 {
		return []string{s}
	}
	if len(s) <= width {
		return []string{s}
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return []string{s}
	}
	var out []string
	cur := ""
	for _, w := range words {
		switch {
		case cur == "":
			cur = w
		case len(cur)+1+len(w) <= width:
			cur += " " + w
		default:
			out = append(out, cur)
			cur = w
		}
		// Emergency split: a single token longer than width falls
		// through as a hard chunk so the user still sees it rather
		// than producing a truncated line.
		if len(cur) > width {
			out = append(out, cur[:width])
			cur = cur[width:]
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

func (m Model) renderMemoryView(width int) string {
	return m.renderMemoryViewSized(width, 24)
}

func (m Model) renderMemoryViewSized(width, height int) string {
	width = clampInt(width, 24, 1000)
	height = max(height, 8)
	tier := m.memory.tier
	if tier == "" {
		tier = memoryTierAll
	}
	banner := m.memoryTopBanner(width, tier)
	tierLine := subtleStyle.Render("tier ") + accentStyle.Render(tier)
	// Per-tier counts surface "how much of each kind do we hold"
	// at a glance so the user judges whether to cycle the filter
	// without scrolling. Counts walk the unfiltered list so the
	// numbers stay stable as queries narrow the visible rows.
	ep, sem := countMemoryEntriesByTier(m.memory.entries)
	tierLine += subtleStyle.Render(fmt.Sprintf("  ·  episodic %d · semantic %d", ep, sem))
	query := strings.TrimSpace(m.memory.query)
	if query != "" {
		tierLine += subtleStyle.Render(" · query ") + boldStyle.Render(query)
		hits := len(filteredMemoryEntries(m.memory.entries, query))
		tierLine += " " + memoryHitsChip(hits)
	}
	hint := subtleStyle.Render("↑↓ scroll · enter expand · / search · t tier · r reload · → action menu")
	if m.memory.searchActive {
		hint = searchTypingHint()
	}
	lines := []string{banner, tierLine}
	if m.memory.searchActive {
		// Live search input — shared with every other diagnostic panel
		// via renderSearchInput so the affordance feels identical
		// regardless of which panel the user is on.
		lines = append(lines, renderSearchInput(query, "type to filter…"))
	}
	lines = append(lines, hint, subtleStyle.Render(strings.Repeat("─", width-2)))

	if m.memory.err != "" {
		lines = append(lines, "", "  "+warnStyle.Render("error · "+m.memory.err))
		return strings.Join(lines, "\n")
	}
	if m.memory.loading {
		lines = append(lines, "", "  "+subtleStyle.Render("loading..."))
		return strings.Join(lines, "\n")
	}

	filtered := filteredMemoryEntries(m.memory.entries, m.memory.query)
	if len(filtered) == 0 {
		lines = append(lines, "")
		if len(m.memory.entries) == 0 {
			lines = append(lines,
				"  "+subtleStyle.Render("No memory entries."),
				"  "+subtleStyle.Render("Memory is the engine's working / episodic / semantic store — facts and decisions the assistant should carry across turns and sessions (sqlite-backed, project-local)."),
				"  "+subtleStyle.Render("Memory fills as the agent runs and on /remember; press t to cycle tiers, r to reload, or /remember <text> to write an entry yourself."),
			)
		} else {
			lines = append(lines,
				"  "+warnStyle.Render(fmt.Sprintf("No matches for %q", m.memory.query)),
				"  "+subtleStyle.Render("Press c to clear the query."),
			)
		}
		return strings.Join(lines, "\n")
	}

	cursor := clampScroll(m.memory.scroll, len(filtered))
	rowBudget := max(height-len(lines)-3, 1)
	start, end := scrollWindow(cursor, len(filtered), rowBudget)
	for i := start; i < end; i++ {
		e := filtered[i]
		expanded := e.ID != "" && e.ID == m.memory.expandedID
		highlighted := i == cursor
		row := formatMemoryRow(e, width-4, highlighted || expanded)
		lines = append(lines, row)

		if expanded {
			body := strings.TrimSpace(e.Value)
			if body != "" {
				for _, raw := range strings.Split(body, "\n") {
					raw = strings.ReplaceAll(raw, "\t", "    ")
					for _, chunk := range memoryDetailWrap(raw, width-8) {
						lines = append(lines, "    "+subtleStyle.Render(chunk))
					}
				}
			}
			meta := []string{}
			if e.Category != "" {
				meta = append(meta, "category="+e.Category)
			}
			if !e.UpdatedAt.IsZero() {
				meta = append(meta, "updated="+e.UpdatedAt.Local().Format("2006-01-02 15:04"))
			}
			if e.Confidence > 0 {
				meta = append(meta, fmt.Sprintf("confidence=%.2f", e.Confidence))
			}
			if e.ID != "" {
				meta = append(meta, "id="+e.ID)
			}
			if len(meta) > 0 {
				lines = append(lines, "    "+subtleStyle.Render("· "+strings.Join(meta, " · ")))
			}
		}
	}

	lines = append(lines, "", "  "+subtleStyle.Render(fmt.Sprintf(
		"%d / %d shown · %d loaded · tier=%s",
		cursor+1, len(filtered), len(m.memory.entries), tier,
	)))
	body := strings.Join(lines, "\n")
	if m.actionMenu.open && m.actionMenu.owner == "Memory" {
		body += "\n\n" + m.renderActionMenu(width)
	}
	return body
}

// countMemoryEntriesByTier walks the unfiltered entries and returns
// (episodic, semantic) counts. Other tiers (working / future kinds)
// are silently bucketed under episodic — the panel only renders two
// chips, and a more granular surface is out of scope for the polish
// pass. The order matches the tier-cycle action menu so the number
// next to "episodic" always lines up with the next tier filter
// you'd reach by pressing `t`.
func countMemoryEntriesByTier(entries []types.MemoryEntry) (episodic, semantic int) {
	for _, e := range entries {
		switch e.Tier {
		case types.MemorySemantic:
			semantic++
		default:
			episodic++
		}
	}
	return
}

// memoryHitsChip is a thin alias over searchHitsChip; the shared
// implementation in panel_search_input.go keeps every panel's chip
// identical.
func memoryHitsChip(n int) string { return searchHitsChip(n) }

// memoryTopBanner draws the title + a status chip on the right.
// Chip: HEALTHY (entries loaded), EMPTY, ERROR, LOADING.
func (m Model) memoryTopBanner(width int, tier string) string {
	title := titleStyle.Bold(true).Render("◈ MEMORY")
	chipText, chipStyle := " HEALTHY ", okStyle
	switch {
	case m.memory.err != "":
		chipText, chipStyle = " ERROR ", warnStyle
	case m.memory.loading:
		chipText, chipStyle = " LOADING ", infoStyle
	case len(m.memory.entries) == 0:
		chipText, chipStyle = " EMPTY ", subtleStyle
	}
	chip := chipStyle.Render(chipText)
	tierChip := subtleStyle.Render(" tier=" + tier + " ")
	chipStrip := tierChip + " " + chip
	gap := max(width-lipgloss.Width(title)-lipgloss.Width(chipStrip)-4, 1)
	return title + strings.Repeat(" ", gap) + chipStrip
}
