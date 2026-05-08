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
	"sort"

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

// openCodemapActionMenu — arrow-driven action surface for CodeMap.
func (m Model) openCodemapActionMenu() Model {
	actions := []panelAction{
		{Label: "Cycle view (overview → hotspots → orphans → cycles)", Accel: "v",
			Handler: func(m Model) (Model, tea.Cmd) {
				m.codemap.view = nextCodemapView(m.codemap.view)
				m.codemap.scroll = 0
				return m, nil
			}},
		{Label: "Refresh graph", Accel: "r",
			Handler: func(m Model) (Model, tea.Cmd) {
				m.codemap.loading = true
				m.codemap.err = ""
				return m, loadCodemapCmd(m.eng)
			}},
		{Label: "Jump to top", Accel: "g",
			Handler: func(m Model) (Model, tea.Cmd) {
				m.codemap.scroll = 0
				return m, nil
			}},
		{Label: "Jump to bottom", Accel: "G",
			Handler: func(m Model) (Model, tea.Cmd) {
				total := codemapViewRowCount(m.codemap.view, m.codemap.snap)
				if total > 0 {
					m.codemap.scroll = total - 1
				}
				return m, nil
			}},
	}
	return m.openActionMenu("CodeMap", "CodeMap actions", actions)
}

func (m Model) handleCodemapKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if nm, cmd, handled := m.handleActionMenuKey(msg); handled {
		return nm, cmd
	}
	if s := msg.String(); s == "enter" || s == "right" || s == "l" {
		return m.openCodemapActionMenu(), nil
	}
	// Total for scroll clamping depends on active view.
	total := codemapViewRowCount(m.codemap.view, m.codemap.snap)
	step := 1
	pageStep := 10
	switch msg.String() {
	case "j", "down":
		if m.codemap.scroll+step < total {
			m.codemap.scroll += step
		}
	case "k", "up":
		if m.codemap.scroll >= step {
			m.codemap.scroll -= step
		} else {
			m.codemap.scroll = 0
		}
	case "pgdown":
		if m.codemap.scroll+pageStep < total {
			m.codemap.scroll += pageStep
		} else if total > 0 {
			m.codemap.scroll = total - 1
		}
	case "pgup":
		if m.codemap.scroll >= pageStep {
			m.codemap.scroll -= pageStep
		} else {
			m.codemap.scroll = 0
		}
	case "g":
		m.codemap.scroll = 0
	case "G":
		if total > 0 {
			m.codemap.scroll = total - 1
		}
	case "v":
		m.codemap.view = nextCodemapView(m.codemap.view)
		m.codemap.scroll = 0
	case "r":
		m.codemap.loading = true
		m.codemap.err = ""
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
