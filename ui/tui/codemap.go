package tui

// codemap.go — the CodeMap panel is a read-only view over the symbol/dep
// graph that internal/codemap maintains. It rotates through four modes:
//
//   1. Overview — counts + a tiny language breakdown
//   2. Hotspots — top nodes by (incoming + outgoing) degree
//   3. Orphans  — nodes with zero incoming edges (unreferenced from the rest)
//   4. Cycles   — each detected strongly-connected cycle
//
// The graph is rebuilt elsewhere (init / analyze / file watch) — this
// panel just takes a snapshot via eng.CodeMap.Graph() when loaded or
// when the user presses `r`.

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/codemap"
	"github.com/dontfuckmycode/dfmc/internal/engine"
)

const (
	// codemapHotspotLimit caps how many hotspots we keep in memory. Well
	// beyond what fits on screen, so scrolling still feels responsive.
	codemapHotspotLimit = 200
	codemapOrphanLimit  = 200
	codemapCycleLimit   = 50
	// codemapCyclePathLimit caps the length of each individual cycle
	// path. Without it a pathological dependency chain in a generated
	// file can produce a single cycle with thousands of nodes, which
	// balloons the snapshot's memory and the panel's render cost. 32
	// is well past the longest real cycle any well-maintained codebase
	// would have; chains longer than that get truncated with an
	// ellipsis so the user still sees both endpoints.
	codemapCyclePathLimit = 32

	codemapViewOverview = "overview"
	codemapViewHotspots = "hotspots"
	codemapViewOrphans  = "orphans"
	codemapViewCycles   = "cycles"
)

// codemapSnapshot is the data the panel actually renders. Holding a
// snapshot (vs. re-querying the Graph on every View()) keeps rendering
// cheap even when the codemap is large.
type codemapSnapshot struct {
	Nodes     int
	Edges     int
	Languages map[string]int
	Kinds     map[string]int
	Hotspots  []codemap.Node
	Orphans   []codemap.Node
	Cycles    [][]string
}

type codemapLoadedMsg struct {
	snap codemapSnapshot
	err  error
}

func loadCodemapCmd(eng *engine.Engine) tea.Cmd {
	return func() tea.Msg {
		if eng == nil || eng.CodeMap == nil || eng.CodeMap.Graph() == nil {
			return codemapLoadedMsg{}
		}
		g := eng.CodeMap.Graph()
		counts := g.Counts()
		nodes := g.Nodes()

		langs := map[string]int{}
		kinds := map[string]int{}
		for _, n := range nodes {
			if n.Language != "" {
				langs[n.Language]++
			}
			if n.Kind != "" {
				kinds[n.Kind]++
			}
		}

		orphans := g.Orphans()
		// Orphans and Hotspots return Go-map iteration order; sort by ID
		// so the panel is stable across reloads.
		sort.Slice(orphans, func(i, j int) bool { return orphans[i].ID < orphans[j].ID })
		if len(orphans) > codemapOrphanLimit {
			orphans = orphans[:codemapOrphanLimit]
		}

		hotspots := g.HotSpots(codemapHotspotLimit)

		cycles := g.Cycles()
		if len(cycles) > codemapCycleLimit {
			cycles = cycles[:codemapCycleLimit]
		}
		// Bound each cycle's path length too — see codemapCyclePathLimit.
		// Keep the first and last N/2 nodes with an ellipsis marker so
		// the user can still see the cycle's endpoints.
		for i, path := range cycles {
			if len(path) > codemapCyclePathLimit {
				cycles[i] = truncateCyclePath(path, codemapCyclePathLimit)
			}
		}

		return codemapLoadedMsg{snap: codemapSnapshot{
			Nodes:     counts.Nodes,
			Edges:     counts.Edges,
			Languages: langs,
			Kinds:     kinds,
			Hotspots:  hotspots,
			Orphans:   orphans,
			Cycles:    cycles,
		}}
	}
}

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
	view := m.codemapView
	if view == "" {
		view = codemapViewOverview
	}
	hint := subtleStyle.Render("j/k scroll · v cycle view · r refresh · g/G top/bottom")
	header := sectionHeader("⌘", "CodeMap")
	viewLine := subtleStyle.Render("view: ") + accentStyle.Render(view)
	lines := []string{header, hint, viewLine, renderDivider(width - 2)}

	if m.codemapErr != "" {
		lines = append(lines, "", warnStyle.Render("error · "+m.codemapErr))
		return strings.Join(lines, "\n")
	}
	if m.codemapLoading {
		lines = append(lines, "", subtleStyle.Render("loading..."))
		return strings.Join(lines, "\n")
	}

	snap := m.codemapSnap
	if snap.Nodes == 0 {
		lines = append(lines, "",
			subtleStyle.Render("CodeMap is empty."),
			subtleStyle.Render("Run `dfmc analyze` or `dfmc init` to populate the graph."),
		)
		return strings.Join(lines, "\n")
	}

	body := renderCodemapBody(view, snap, m.codemapScroll, width-2)
	lines = append(lines, body...)
	summary := fmt.Sprintf(
		"%d nodes · %d edges · %d cycles · %d orphans",
		snap.Nodes, snap.Edges, len(snap.Cycles), len(snap.Orphans),
	)
	lines = append(lines, "", subtleStyle.Render(summary))
	return strings.Join(lines, "\n")
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

// nextCodemapView cycles overview → hotspots → orphans → cycles → overview.
func nextCodemapView(current string) string {
	switch current {
	case codemapViewOverview:
		return codemapViewHotspots
	case codemapViewHotspots:
		return codemapViewOrphans
	case codemapViewOrphans:
		return codemapViewCycles
	default:
		return codemapViewOverview
	}
}

func (m Model) handleCodemapKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Total for scroll clamping depends on active view.
	total := codemapViewRowCount(m.codemapView, m.codemapSnap)
	step := 1
	pageStep := 10
	switch msg.String() {
	case "j", "down":
		if m.codemapScroll+step < total {
			m.codemapScroll += step
		}
	case "k", "up":
		if m.codemapScroll >= step {
			m.codemapScroll -= step
		} else {
			m.codemapScroll = 0
		}
	case "pgdown":
		if m.codemapScroll+pageStep < total {
			m.codemapScroll += pageStep
		} else if total > 0 {
			m.codemapScroll = total - 1
		}
	case "pgup":
		if m.codemapScroll >= pageStep {
			m.codemapScroll -= pageStep
		} else {
			m.codemapScroll = 0
		}
	case "g":
		m.codemapScroll = 0
	case "G":
		if total > 0 {
			m.codemapScroll = total - 1
		}
	case "v":
		m.codemapView = nextCodemapView(m.codemapView)
		m.codemapScroll = 0
	case "r":
		m.codemapLoading = true
		m.codemapErr = ""
		return m, loadCodemapCmd(m.eng)
	}
	return m, nil
}

func codemapViewRowCount(view string, snap codemapSnapshot) int {
	switch view {
	case codemapViewHotspots:
		return len(snap.Hotspots)
	case codemapViewOrphans:
		return len(snap.Orphans)
	case codemapViewCycles:
		return len(snap.Cycles)
	default:
		// Overview is fixed-length-ish; scrolling through it isn't useful.
		return 0
	}
}
