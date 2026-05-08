package tui

// codemap_render.go — view-layer rendering for the CodeMap panel:
// banner, body switch (overview/hotspots/orphans/cycles), per-row
// formatters, count-map pretty-printer, scroll clip, and the
// over-long-cycle-path truncator. Sibling to codemap.go which keeps
// the snapshot type, the loadCodemapCmd graph snapshotter, view-cycle
// constants, action-menu wiring, and key dispatch.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/dontfuckmycode/dfmc/internal/codemap"
)

// truncateCyclePath reduces an over-long dependency-cycle path down
// to `limit` entries while keeping both endpoints visible. When a
// path has 200 nodes and limit=32, the result is
// `[p0, p1, ..., p15, "…", p184, ..., p199]` — the user still sees
// where the cycle starts and ends, but the middle is collapsed to a
// single ellipsis marker. Returns `path` untouched if it already
// fits under the limit.
func truncateCyclePath(path []string, limit int) []string {
	if limit <= 0 || len(path) <= limit {
		return path
	}
	// Reserve one slot for the ellipsis marker; split the rest evenly
	// between head and tail. Odd limits prefer a bigger head.
	keep := limit - 1
	head := (keep + 1) / 2
	tail := keep - head
	out := make([]string, 0, limit)
	out = append(out, path[:head]...)
	out = append(out, "…")
	out = append(out, path[len(path)-tail:]...)
	return out
}

func (m Model) renderCodemapView(width int) string {
	width = clampInt(width, 24, 1000)
	view := m.codemap.view
	if view == "" {
		view = codemapViewOverview
	}
	banner := m.codemapTopBanner(width, view)
	hint := subtleStyle.Render("j/k scroll · v cycle view · r refresh · g/G top/bottom")
	lines := []string{banner, hint, renderDivider(width - 2)}

	if m.codemap.err != "" {
		lines = append(lines, "", warnStyle.Render("error · "+m.codemap.err))
		return strings.Join(lines, "\n")
	}
	if m.codemap.loading {
		lines = append(lines, "", subtleStyle.Render("loading..."))
		return strings.Join(lines, "\n")
	}

	snap := m.codemap.snap
	if snap.Nodes == 0 {
		lines = append(lines, "",
			subtleStyle.Render("CodeMap is empty."),
			subtleStyle.Render("CodeMap is the project's symbol/dependency graph — used by /find and the `codemap` tool to give the agent a project outline without reading every file."),
			subtleStyle.Render("Run `dfmc analyze` or `dfmc init` to build it. CGO_ENABLED=1 enables AST parsing; without it the regex backend gives a coarser graph."),
		)
		return strings.Join(lines, "\n")
	}

	body := renderCodemapBody(view, snap, m.codemap.scroll, width-2)
	lines = append(lines, body...)
	summaryParts := []string{
		accentStyle.Render(fmt.Sprintf("%d nodes", snap.Nodes)),
		infoStyle.Render(fmt.Sprintf("%d edges", snap.Edges)),
		warnStyle.Render(fmt.Sprintf("%d cycles", len(snap.Cycles))),
		subtleStyle.Render(fmt.Sprintf("%d orphans", len(snap.Orphans))),
	}
	lines = append(lines, "", strings.Join(summaryParts, subtleStyle.Render(" · ")))
	out := strings.Join(lines, "\n")
	if m.actionMenu.open && m.actionMenu.owner == "CodeMap" {
		out += "\n\n" + m.renderActionMenu(width)
	}
	return out
}

// codemapTopBanner — title + view chip + status chip on the right.
// Status: HEALTHY / EMPTY / ERROR / LOADING.
func (m Model) codemapTopBanner(width int, view string) string {
	title := titleStyle.Bold(true).Render("⌘ CODEMAP")
	chipText, chipStyle := " HEALTHY ", okStyle
	switch {
	case m.codemap.err != "":
		chipText, chipStyle = " ERROR ", warnStyle
	case m.codemap.loading:
		chipText, chipStyle = " LOADING ", infoStyle
	case m.codemap.snap.Nodes == 0:
		chipText, chipStyle = " EMPTY ", subtleStyle
	}
	chip := chipStyle.Render(chipText)
	viewChip := accentStyle.Render(" view=" + view + " ")
	chipStrip := viewChip + " " + chip
	gap := max(width-lipgloss.Width(title)-lipgloss.Width(chipStrip)-4, 1)
	return title + strings.Repeat(" ", gap) + chipStrip
}

// renderCodemapBody returns the view-specific rows. It's split out from
// renderCodemapView so the scroll offset is applied uniformly and the
// test suite can exercise one view in isolation.
func renderCodemapBody(view string, snap codemapSnapshot, scroll, width int) []string {
	switch view {
	case codemapViewHotspots:
		rows := make([]string, 0, len(snap.Hotspots))
		for _, n := range snap.Hotspots {
			rows = append(rows, truncateSingleLine(formatCodemapNodeRow(n), width))
		}
		return applyScroll(rows, scroll)
	case codemapViewOrphans:
		rows := make([]string, 0, len(snap.Orphans))
		for _, n := range snap.Orphans {
			rows = append(rows, truncateSingleLine(formatCodemapNodeRow(n), width))
		}
		return applyScroll(rows, scroll)
	case codemapViewCycles:
		rows := make([]string, 0, len(snap.Cycles))
		for i, c := range snap.Cycles {
			label := fmt.Sprintf("%2d. %s", i+1, strings.Join(c, " → "))
			rows = append(rows, truncateSingleLine(label, width))
		}
		return applyScroll(rows, scroll)
	default:
		return renderCodemapOverview(snap, width)
	}
}

func renderCodemapOverview(snap codemapSnapshot, width int) []string {
	rows := []string{
		fmt.Sprintf("nodes  %d", snap.Nodes),
		fmt.Sprintf("edges  %d", snap.Edges),
		"",
		subtleStyle.Render("by language"),
	}
	rows = append(rows, formatCountMap(snap.Languages, 10, width)...)
	rows = append(rows, "", subtleStyle.Render("by kind"))
	rows = append(rows, formatCountMap(snap.Kinds, 10, width)...)
	return rows
}

// formatCountMap sorts a string->int map by count desc then emits at
// most `limit` "  key  N" rows, truncated to width.
func formatCountMap(m map[string]int, limit, width int) []string {
	type kv struct {
		k string
		v int
	}
	list := make([]kv, 0, len(m))
	for k, v := range m {
		list = append(list, kv{k, v})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].v == list[j].v {
			return list[i].k < list[j].k
		}
		return list[i].v > list[j].v
	})
	if len(list) == 0 {
		return []string{subtleStyle.Render("  (none)")}
	}
	if limit > 0 && len(list) > limit {
		list = list[:limit]
	}
	out := make([]string, 0, len(list))
	for _, e := range list {
		line := fmt.Sprintf("  %-16s %d", e.k, e.v)
		if width > 0 {
			line = truncateSingleLine(line, width)
		}
		out = append(out, line)
	}
	return out
}

// formatCodemapNodeRow renders one graph node as a single line. Name is
// highlighted, path stays subtle, kind/language ride along.
func formatCodemapNodeRow(n codemap.Node) string {
	name := strings.TrimSpace(n.Name)
	if name == "" {
		name = n.ID
	}
	head := accentStyle.Render(name)
	tags := []string{}
	if n.Kind != "" {
		tags = append(tags, n.Kind)
	}
	if n.Language != "" {
		tags = append(tags, n.Language)
	}
	tail := ""
	if len(tags) > 0 {
		tail = subtleStyle.Render(" (" + strings.Join(tags, ", ") + ")")
	}
	if strings.TrimSpace(n.Path) != "" {
		tail += "  " + subtleStyle.Render(n.Path)
	}
	return head + tail
}

// applyScroll clips the visible range to rows[scroll:]. We intentionally
// don't wrap — scrolling past the tail holds at the last item.
func applyScroll(rows []string, scroll int) []string {
	if scroll <= 0 || len(rows) == 0 {
		return rows
	}
	if scroll >= len(rows) {
		return rows[len(rows)-1:]
	}
	return rows[scroll:]
}
