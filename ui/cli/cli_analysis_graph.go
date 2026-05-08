package cli

// cli_analysis_graph.go — codemap rendering helpers used by `dfmc map`
// and the `--deps` flag of `dfmc analyze`. Pulls the dependency-graph
// summary out of the engine's CodeMap and renders it as DOT (Graphviz)
// or a self-contained SVG with a circular layout. Kept separate from
// the analyze/map dispatcher (cli_analysis.go) so the rendering math
// doesn't bloat the command flow.

import (
	"fmt"
	"html"
	"math"
	"sort"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/codemap"
	"github.com/dontfuckmycode/dfmc/internal/engine"
)

type depStat struct {
	Module string `json:"module"`
	Count  int    `json:"count"`
}

func collectDependencyStats(eng *engine.Engine, limit int) []depStat {
	if eng == nil || eng.CodeMap == nil || eng.CodeMap.Graph() == nil {
		return nil
	}
	counts := map[string]int{}
	for _, e := range eng.CodeMap.Graph().Edges() {
		if e.Type != "imports" {
			continue
		}
		mod := strings.TrimPrefix(e.To, "module:")
		mod = strings.TrimSpace(mod)
		if mod == "" {
			continue
		}
		counts[mod]++
	}
	out := make([]depStat, 0, len(counts))
	for mod, count := range counts {
		out = append(out, depStat{Module: mod, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count == out[j].Count {
			return out[i].Module < out[j].Module
		}
		return out[i].Count > out[j].Count
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func graphToDOT(nodes []codemap.Node, edges []codemap.Edge) string {
	var b strings.Builder
	b.WriteString("digraph DFMC {\n")
	for _, n := range nodes {
		label := n.Name
		if strings.TrimSpace(label) == "" {
			label = n.ID
		}
		if n.Kind != "" {
			label = label + "\\n(" + n.Kind + ")"
		}
		fmt.Fprintf(&b, "  \"%s\" [label=\"%s\"];\n", escapeDOT(n.ID), escapeDOT(label))
	}
	for _, e := range edges {
		fmt.Fprintf(&b, "  \"%s\" -> \"%s\" [label=\"%s\"];\n",
			escapeDOT(e.From), escapeDOT(e.To), escapeDOT(e.Type))
	}
	b.WriteString("}\n")
	return b.String()
}

func graphToSVG(nodes []codemap.Node, edges []codemap.Edge) string {
	const (
		width     = 1200.0
		height    = 800.0
		margin    = 90.0
		nodeR     = 14.0
		fontSize  = 12
		labelDy   = 24.0
		strokeW   = 1.2
		centerPad = 20.0
	)

	var b strings.Builder
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" width="1200" height="800" viewBox="0 0 1200 800">` + "\n")
	b.WriteString(`  <defs><style>
    .edge { stroke: #64748b; stroke-width: 1.2; opacity: 0.8; }
    .node { fill: #0ea5e9; stroke: #075985; stroke-width: 1.2; }
    .label { fill: #0f172a; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 12px; text-anchor: middle; }
    .kind { fill: #334155; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 10px; text-anchor: middle; }
  </style></defs>` + "\n")
	b.WriteString(`  <rect x="0" y="0" width="1200" height="800" fill="#f8fafc"/>` + "\n")

	if len(nodes) == 0 {
		b.WriteString(`  <text x="600" y="400" class="label">No codemap nodes</text>` + "\n")
		b.WriteString(`</svg>` + "\n")
		return b.String()
	}

	type pt struct {
		x float64
		y float64
	}
	pos := make(map[string]pt, len(nodes))
	cx := width / 2
	cy := height / 2
	r := math.Min(width, height)/2 - margin
	if len(nodes) == 1 {
		pos[nodes[0].ID] = pt{x: cx, y: cy}
	} else {
		for i, n := range nodes {
			angle := (2 * math.Pi * float64(i) / float64(len(nodes))) - math.Pi/2
			x := cx + (r-centerPad)*math.Cos(angle)
			y := cy + (r-centerPad)*math.Sin(angle)
			pos[n.ID] = pt{x: x, y: y}
		}
	}

	for _, e := range edges {
		from, okFrom := pos[e.From]
		to, okTo := pos[e.To]
		if !okFrom || !okTo {
			continue
		}
		fmt.Fprintf(&b, `  <line class="edge" x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f"/>`+"\n",
			from.x, from.y, to.x, to.y)
	}

	for _, n := range nodes {
		p := pos[n.ID]
		label := strings.TrimSpace(n.Name)
		if label == "" {
			label = n.ID
		}
		kind := strings.TrimSpace(n.Kind)
		fmt.Fprintf(&b, `  <circle class="node" cx="%.1f" cy="%.1f" r="%.1f"/>`+"\n", p.x, p.y, nodeR)
		fmt.Fprintf(&b, `  <text class="label" x="%.1f" y="%.1f">%s</text>`+"\n", p.x, p.y+labelDy, xmlEscape(label))
		if kind != "" {
			fmt.Fprintf(&b, `  <text class="kind" x="%.1f" y="%.1f">%s</text>`+"\n", p.x, p.y+labelDy+12, xmlEscape(kind))
		}
	}

	b.WriteString(`</svg>` + "\n")
	return b.String()
}

func xmlEscape(s string) string {
	return html.EscapeString(s)
}

func escapeDOT(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return s
}
