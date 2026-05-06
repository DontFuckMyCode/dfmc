// render_activity_v2.go — F7 Activity panel, wrapped in the same
// banner-and-divider shell as F2-F6 so the tab reads visually
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

	hint := "j/k older-newer · pgup/pgdn page · enter/o open · r refresh · f file · y copy · 1-6 filter · / search"
	if m.activity.searchActive {
		hint = "typing search · enter commit · esc stop · backspace delete"
	}

	queryLine := subtleStyle.Render("view ") +
		accentStyle.Render(activityModeLabel(mode)) +
		subtleStyle.Render(" ["+activityModeShortcut(mode)+"]")
	if query != "" {
		queryLine += subtleStyle.Render(" · query ") + boldStyle.Render(query)
	}

	lines := []string{
		banner,
		statsLine,
		queryLine,
		subtleStyle.Render(hint),
		renderDivider(width - 2),
	}

	if len(m.activity.entries) == 0 {
		lines = append(lines,
			"",
			subtleStyle.Render("No events yet."),
			subtleStyle.Render("Tool calls, subagent fan-out, drive progress, provider retries,"),
			subtleStyle.Render("and context lifecycle stream here live."),
		)
		return strings.Join(lines, "\n")
	}
	if len(filtered) == 0 {
		lines = append(lines,
			"",
			warnStyle.Render("No events match this filter/query."),
			subtleStyle.Render("Press c to clear · v / 1-6 to change the view."),
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
