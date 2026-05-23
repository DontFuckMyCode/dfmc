// render_activity_v2.go — F5 Activity panel, wrapped in the same
// banner-and-divider shell as the other panel-card tabs so the layout reads visually
// consistent. The legacy renderActivityViewSized in activity_render.go
// supplies the timeline + inspector blocks; this file adds:
//
//   - Top banner: ✦ ACTIVITY title + LIVE / PAUSED chip on the right.
//   - Counts strip: total · shown · tool · agent · err · ctx — coloured.
//   - Mode + query line as a single subtle row under the strip.
//
// The timeline + inspector layout itself is unchanged so the existing
// scroll, follow, search, and inspector contracts all keep working.

package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func (m Model) renderActivityViewV2(width, height int) string {
	width = clampInt(width, 24, 1000)
	height = clampInt(height, 10, 1000)

	mode := m.activity.mode
	if mode == "" {
		mode = activityViewAll
	}
	query := strings.TrimSpace(m.activity.query)
	allCounts := activityKindCounts(m.activity.entries)
	filtered := m.filteredActivityEntries()
	scroll := clampActivityOffset(m.activity.scroll, len(filtered))
	selected := activitySelectedIndex(len(filtered), scroll)

	banner := m.activityTopBanner(width, len(m.activity.entries), len(filtered))
	statsLine := activityStatsLine(allCounts, len(m.activity.entries), len(filtered))

	hint := subtleStyle.Render("↑↓ move · pgup/pgdown page · enter open · / search")
	if m.activity.searchActive {
		hint = searchTypingHint()
	}

	queryLine := subtleStyle.Render("view ") +
		accentStyle.Render(activityModeLabel(mode)) +
		subtleStyle.Render(" ["+activityModeShortcut(mode)+"]")
	if query != "" {
		hits := countActivityQueryHits(m.activity.entries, query)
		queryLine += subtleStyle.Render(" · query ") + boldStyle.Render(query)
		queryLine += " " + activityHitsChip(hits)
	}

	lines := []string{
		banner,
		statsLine,
		queryLine,
	}
	if m.activity.searchActive {
		// Live search box: same affordance as every diagnostic panel.
		// Without the visible buffer the search felt blind — the only
		// confirmation was watching rows disappear after enter.
		lines = append(lines, renderSearchInput(query, "type to filter…"))
	}
	lines = append(lines,
		hint,
		renderDivider(width-2),
	)

	if len(m.activity.entries) == 0 {
		lines = append(lines,
			"",
			subtleStyle.Render("No events yet."),
			subtleStyle.Render("Activity is the live firehose — tool calls, subagent fan-out, drive progress, provider retries, and context lifecycle stream here as the engine runs."),
			subtleStyle.Render("Send a message in /chat or kick off /drive to make events flow. Enter opens the action menu; / searches; arrow keys move."),
		)
		return strings.Join(lines, "\n")
	}
	if len(filtered) == 0 {
		lines = append(lines,
			"",
			warnStyle.Render("No events match this filter/query."),
			subtleStyle.Render("Press c to clear, or / to search. Use the action menu (enter) to change view."),
		)
		return strings.Join(lines, "\n")
	}

	remainingHeight := max(height-len(lines), 4)

	selectedEntry := filtered[selected]
	if width >= 110 && remainingHeight >= 8 {
		leftWidth := int(float64(width-2) * 0.58)
		if leftWidth < 42 {
			leftWidth = 42
		}
		rightWidth := width - 2 - leftWidth - 2
		if rightWidth < 28 {
			rightWidth = 28
			leftWidth = width - 2 - rightWidth - 2
		}
		timeline := renderActivityTimeline(filtered, selected, leftWidth, remainingHeight)
		inspector := renderActivityInspector(selectedEntry, rightWidth, remainingHeight)
		lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Top, timeline, "  ", inspector))
	} else {
		timelineHeight := max(remainingHeight/2, 5)
		inspectorHeight := max(remainingHeight-timelineHeight-1, 4)
		lines = append(lines, renderActivityTimeline(filtered, selected, width-2, timelineHeight))
		lines = append(lines, renderDivider(width-2))
		lines = append(lines, renderActivityInspector(selectedEntry, width-2, inspectorHeight))
	}

	if !m.activity.follow {
		lines = append(lines, warnStyle.Render("paused — press G to jump to tail and resume follow"))
	}
	out := strings.Join(lines, "\n")
	if m.actionMenu.open && m.actionMenu.owner == "Activity" {
		out += "\n\n" + m.renderActionMenu(width)
	}
	return out
}

// activityTopBanner — title + LIVE/PAUSED chip right-aligned. Matches
// the chip style used by other V2 panels so the eye reads the live
// state at a glance.
func (m Model) activityTopBanner(width, total, shown int) string {
	title := titleStyle.Bold(true).Render("✦ ACTIVITY")

	followText := " LIVE "
	followStyle := okStyle
	if !m.activity.follow {
		followText = " PAUSED "
		followStyle = warnStyle
	}
	followChip := followStyle.Render(followText)

	countText := fmt.Sprintf(" %d / %d ", shown, total)
	if total == 0 {
		countText = " empty "
	}
	countChip := subtleStyle.Render(countText)

	chipStrip := countChip + " " + followChip
	gap := max(width-lipgloss.Width(title)-lipgloss.Width(chipStrip)-4, 1)
	return title + strings.Repeat(" ", gap) + chipStrip
}

// countActivityQueryHits is the live "N matches" surface for the
// query line. We deliberately count from m.activity.entries (the
// unfiltered list) rather than the mode-filtered subset so the user
// sees the total reach of their substring across every event in
// memory, then narrows further with the view filter. The filtered
// list IS what the timeline shows — those two numbers may differ
// when a mode filter is active, which is fine: shown/total in the
// banner already covers the after-filter count.
func countActivityQueryHits(entries []activityEntry, query string) int {
	query = strings.TrimSpace(query)
	if query == "" {
		return 0
	}
	n := 0
	for _, e := range entries {
		if activityMatchesQuery(e, query) {
			n++
		}
	}
	return n
}

// activityHitsChip is a thin alias over searchHitsChip; the shared
// implementation in panel_search_input.go keeps every panel's chip
// identical.
func activityHitsChip(n int) string { return searchHitsChip(n) }

// activityStatsLine — coloured count breakdown (tool/agent/err/ctx).
func activityStatsLine(counts map[activityKind]int, total, shown int) string {
	parts := []string{
		subtleStyle.Render(fmt.Sprintf("%d total", total)),
		infoStyle.Render(fmt.Sprintf("%d shown", shown)),
		accentStyle.Render(fmt.Sprintf("tool %d", counts[activityKindTool])),
		titleStyle.Render(fmt.Sprintf("agent %d", counts[activityKindAgent])),
		warnStyle.Render(fmt.Sprintf("err %d", counts[activityKindError])),
		subtleStyle.Render(fmt.Sprintf("ctx %d", counts[activityKindCtx]+counts[activityKindIndex])),
	}
	return strings.Join(parts, subtleStyle.Render(" · "))
}
