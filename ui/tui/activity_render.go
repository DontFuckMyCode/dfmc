package tui

// Activity panel rendering: filter/select helpers, timeline + inspector
// panes, and the top-level renderActivityView entry point. Split from
// activity.go so the event-ingestion core (recordActivityEvent,
// classifyActivity) stays separate from the presentation layer — the
// two churn on different cadences.

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// clampInt is a package-wide int clamp used across panel renderers.
// Lives here because this file is the busiest clamp caller; nothing
// Activity-specific about the helper itself.
func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func (m Model) filteredActivityEntries() []activityEntry {
	mode := m.activity.mode
	if mode == "" {
		mode = activityViewAll
	}
	query := strings.TrimSpace(m.activity.query)
	filtered := make([]activityEntry, 0, len(m.activity.entries))
	for _, entry := range m.activity.entries {
		if !activityMatchesMode(entry, mode) || !activityMatchesQuery(entry, query) {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

func clampActivityOffset(scroll, total int) int {
	if total <= 0 {
		return 0
	}
	if scroll < 0 {
		return 0
	}
	if scroll >= total {
		return total - 1
	}
	return scroll
}

func activitySelectedIndex(total, scroll int) int {
	if total <= 0 {
		return -1
	}
	scroll = clampActivityOffset(scroll, total)
	return total - 1 - scroll
}

func formatActivityLine(entry activityEntry, width int, selected bool) string {
	ts := entry.At.Format("15:04:05")
	icon := kindIcon(entry.Kind)
	count := ""
	if entry.Count > 1 {
		count = subtleStyle.Render(fmt.Sprintf(" x%d", entry.Count))
	}
	prefix := "  "
	if selected {
		prefix = accentStyle.Render("› ")
	}
	line := prefix + subtleStyle.Render(ts) + " " + icon + " " + entry.Text + count
	line = truncateSingleLine(line, width)
	if selected {
		line = lipgloss.NewStyle().
			Foreground(colorTitleFg).
			Background(colorAccent).
			Bold(true).
			Render(line)
	}
	return line
}

func renderActivityPane(title string, body []string, width, height int) string {
	if height < 3 {
		height = 3
	}
	lines := []string{
		accentStyle.Bold(true).Render(title),
		renderDivider(max(width-1, 1)),
	}
	lines = append(lines, body...)
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	return lipgloss.NewStyle().Width(width).Height(height).Render(strings.Join(lines, "\n"))
}

func activityTargetForEntry(entry activityEntry) activityActionTarget {
	eventID := strings.ToLower(strings.TrimSpace(entry.EventID))
	text := strings.ToLower(strings.TrimSpace(entry.Text))
	switch {
	case strings.HasPrefix(eventID, "provider:"):
		return activityTargetProviders
	case strings.HasPrefix(eventID, "drive:"),
		strings.HasPrefix(eventID, "agent:autonomy:"),
		strings.HasPrefix(eventID, "agent:subagent:"):
		return activityTargetPlans
	case strings.HasPrefix(eventID, "security:"),
		strings.Contains(eventID, "secret"),
		strings.Contains(eventID, "vuln"),
		strings.Contains(text, "secret"),
		strings.Contains(text, "vulnerability"):
		return activityTargetSecurity
	case strings.HasPrefix(eventID, "context:"),
		strings.HasPrefix(eventID, "ctx:"):
		return activityTargetContext
	case strings.HasPrefix(eventID, "index:"):
		return activityTargetCodeMap
	case strings.HasPrefix(eventID, "tool:") && isMutationTool(entry.Tool):
		return activityTargetPatch
	case strings.HasPrefix(eventID, "tool:") && strings.TrimSpace(entry.Path) != "":
		return activityTargetFiles
	case strings.HasPrefix(eventID, "tool:"):
		return activityTargetTools
	case strings.HasPrefix(eventID, "config:"),
		strings.HasPrefix(eventID, "engine:"):
		return activityTargetStatus
	case strings.TrimSpace(entry.Path) != "":
		return activityTargetFiles
	default:
		return activityTargetStatus
	}
}

func activityTargetLabel(target activityActionTarget) string {
	switch target {
	case activityTargetFiles:
		return "Files"
	case activityTargetPatch:
		return "Patch"
	case activityTargetTools:
		return "Tools"
	case activityTargetPlans:
		return "Plans"
	case activityTargetContext:
		return "Context"
	case activityTargetCodeMap:
		return "CodeMap"
	case activityTargetSecurity:
		return "Security"
	case activityTargetProviders:
		return "Providers"
	default:
		return "Status"
	}
}

func activityTargetSupportsRefresh(target activityActionTarget) bool {
	switch target {
	case activityTargetStatus,
		activityTargetPatch,
		activityTargetPlans,
		activityTargetContext,
		activityTargetCodeMap,
		activityTargetSecurity,
		activityTargetProviders:
		return true
	default:
		return false
	}
}

func renderActivityInspector(entry activityEntry, width, height int) string {
	target := activityTargetForEntry(entry)
	body := []string{
		boldStyle.Render(truncateSingleLine(entry.Text, width-2)),
		subtleStyle.Render("event: " + blankFallback(strings.TrimSpace(entry.EventID), "(unknown)")),
		subtleStyle.Render("kind: " + string(entry.Kind)),
		subtleStyle.Render("time: " + entry.At.Format("15:04:05")),
		subtleStyle.Render("open: enter/o -> " + activityTargetLabel(target)),
	}
	if source := strings.TrimSpace(entry.Source); source != "" {
		body = append(body, subtleStyle.Render("source: "+source))
	}
	if provider := strings.TrimSpace(entry.Provider); provider != "" {
		body = append(body, subtleStyle.Render("provider: "+provider))
	}
	if path := strings.TrimSpace(entry.Path); path != "" {
		body = append(body, subtleStyle.Render("file: f -> "+truncateSingleLine(path, width-14)))
	}
	if activityTargetSupportsRefresh(target) {
		body = append(body, subtleStyle.Render("refresh: r -> reopen target with fresh data"))
	}
	body = append(body, subtleStyle.Render("copy: y -> snapshot details to clipboard"))
	if entry.Count > 1 {
		body = append(body, subtleStyle.Render(fmt.Sprintf("repeats: %d consecutive", entry.Count)))
	}
	body = append(body, "")
	for _, line := range entry.Details {
		body = append(body, truncateSingleLine(line, width-2))
	}
	return renderActivityPane("INSPECTOR", body, width, height)
}

func renderActivityTimeline(entries []activityEntry, selected, width, height int) string {
	if height < 4 {
		height = 4
	}
	rowsHeight := height - 3
	if rowsHeight < 1 {
		rowsHeight = 1
	}
	if len(entries) == 0 {
		return renderActivityPane("TIMELINE", []string{subtleStyle.Render("No matching events.")}, width, height)
	}
	end := selected + 1
	if end < 1 {
		end = 1
	}
	if end > len(entries) {
		end = len(entries)
	}
	start := end - rowsHeight
	if start < 0 {
		start = 0
	}
	hiddenOlder := start
	hiddenNewer := len(entries) - end

	body := make([]string, 0, rowsHeight+1)
	body = append(body, subtleStyle.Render(fmt.Sprintf(
		"%d shown · selected %d/%d · older %d · newer %d",
		len(entries), selected+1, len(entries), hiddenOlder, hiddenNewer,
	)))
	for idx := start; idx < end; idx++ {
		body = append(body, formatActivityLine(entries[idx], width-2, idx == selected))
	}
	return renderActivityPane("TIMELINE", body, width, height)
}

func activityKindCounts(entries []activityEntry) map[activityKind]int {
	counts := map[activityKind]int{}
	for _, entry := range entries {
		counts[entry.Kind] += entry.Count
	}
	return counts
}

func (m Model) renderActivityView(width int) string {
	return m.renderActivityViewSized(width, activityDefaultRenderHeight)
}

func (m Model) renderActivityViewSized(width int, height int) string {
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
	followState := okStyle.Render("live")
	if !m.activity.follow {
		followState = warnStyle.Render("paused")
	}

	hint := "j/k older-newer · pgup/pgdn page · enter/o open · r refresh · f file · y copy · 1-6 filter"
	if m.activity.searchActive {
		hint = "typing search · enter commit · esc stop · backspace delete"
	}
	queryLine := subtleStyle.Render("view: ") +
		accentStyle.Render(activityModeLabel(mode)) +
		subtleStyle.Render(" ["+activityModeShortcut(mode)+"] · query: ")
	if query != "" {
		queryLine += boldStyle.Render(query)
	} else {
		queryLine += subtleStyle.Render("(none)")
	}
	queryLine += subtleStyle.Render(" · follow: ") + followState

	summary := fmt.Sprintf(
		"%d total · %d shown · tool %d · agent %d · err %d · ctx %d",
		len(m.activity.entries),
		len(filtered),
		allCounts[activityKindTool],
		allCounts[activityKindAgent],
		allCounts[activityKindError],
		allCounts[activityKindCtx]+allCounts[activityKindIndex],
	)

	lines := []string{
		sectionHeader("✦", "Activity"),
		subtleStyle.Render(hint),
		queryLine,
		subtleStyle.Render(summary),
		renderDivider(width - 2),
	}

	if len(m.activity.entries) == 0 {
		lines = append(lines,
			"",
			subtleStyle.Render("No events yet."),
			subtleStyle.Render("Tool calls, subagent fan-out, drive progress, provider retries, and context lifecycle stream here live."),
		)
		return strings.Join(lines, "\n")
	}
	if len(filtered) == 0 {
		lines = append(lines,
			"",
			warnStyle.Render("No events match this filter/query."),
			subtleStyle.Render("Press c to clear the query or v / 1-6 to change the view."),
		)
		return strings.Join(lines, "\n")
	}

	remainingHeight := height - len(lines)
	if remainingHeight < 4 {
		remainingHeight = 4
	}

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
		timelineHeight := remainingHeight / 2
		if timelineHeight < 5 {
			timelineHeight = 5
		}
		inspectorHeight := remainingHeight - timelineHeight - 1
		if inspectorHeight < 4 {
			inspectorHeight = 4
		}
		lines = append(lines, renderActivityTimeline(filtered, selected, width-2, timelineHeight))
		lines = append(lines, renderDivider(width-2))
		lines = append(lines, renderActivityInspector(selectedEntry, width-2, inspectorHeight))
	}

	if !m.activity.follow {
		lines = append(lines, warnStyle.Render("paused - press G to jump to tail and resume follow"))
	}
	return strings.Join(lines, "\n")
}
