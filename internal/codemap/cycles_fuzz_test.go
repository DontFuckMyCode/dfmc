package codemap

import (
	"fmt"
	"testing"
)

// kahnAcyclic is an INDEPENDENT reference cycle detector (Kahn's algorithm)
// used to cross-check Graph.Cycles' Tarjan SCC implementation. A directed
// graph has a cycle iff Kahn cannot remove every node. Self-loops are
// handled naturally: a node with an edge to itself keeps in-degree >= 1 and
// is never removed, so Kahn reports it as cyclic — matching Cycles, which
// reports size-1 SCCs that carry a self-loop.
func kahnAcyclic(nodes []string, edges map[[2]string]bool) bool {
	indeg := make(map[string]int, len(nodes))
	adj := make(map[string][]string, len(nodes))
	for _, n := range nodes {
		indeg[n] = 0
	}
	for e := range edges {
		adj[e[0]] = append(adj[e[0]], e[1])
		indeg[e[1]]++
	}
	queue := make([]string, 0, len(nodes))
	for n, d := range indeg {
		if d == 0 {
			queue = append(queue, n)
		}
	}
	removed := 0
	for len(queue) > 0 {
		x := queue[0]
		queue = queue[1:]
		removed++
		for _, w := range adj[x] {
			indeg[w]--
			if indeg[w] == 0 {
				queue = append(queue, w)
			}
		}
	}
	return removed == len(nodes)
}

// FuzzCyclesMatchesKahn cross-checks Graph.Cycles (Tarjan strongly-connected
// components + self-loop detection) against the Kahn reference: the graph has
// at least one cycle iff Cycles returns a non-empty result iff Kahn cannot
// topologically order it. Tarjan SCC is subtle enough that an independent
// second algorithm is the standard way to trust it; any disagreement
// pinpoints a bug.
func FuzzCyclesMatchesKahn(f *testing.F) {
	f.Add([]byte{0, 1, 1, 2, 2, 0}) // 0->1->2->0 cycle
	f.Add([]byte{0, 1, 1, 2, 2, 3}) // chain, acyclic
	f.Add([]byte{3, 3})             // self-loop
	f.Add([]byte{0, 1, 2, 0, 1, 2}) // 0->1->2->0 cycle (reordered)
	f.Add([]byte{})                 // empty

	f.Fuzz(func(t *testing.T, raw []byte) {
		const n = 6
		nodes := make([]string, n)
		g := NewGraph()
		for i := range n {
			nodes[i] = fmt.Sprintf("n%d", i)
			g.AddNode(Node{ID: nodes[i], Name: nodes[i]})
		}
		// Pack byte pairs into a deduped edge set (self-loops allowed —
		// Cycles handles them, so the cross-check must too).
		edges := make(map[[2]string]bool)
		for i := 0; i+1 < len(raw); i += 2 {
			from := nodes[int(raw[i])%n]
			to := nodes[int(raw[i+1])%n]
			key := [2]string{from, to}
			if edges[key] {
				continue
			}
			edges[key] = true
			g.AddEdge(Edge{From: from, To: to})
		}

		tarjanCycle := len(g.Cycles()) > 0
		kahnCycle := !kahnAcyclic(nodes, edges)
		if tarjanCycle != kahnCycle {
			t.Fatalf("cycle detectors disagree: Cycles()=%v kahn=%v\n  edges=%v",
				tarjanCycle, kahnCycle, edges)
		}
	})
}
