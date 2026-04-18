package codemap

import "testing"

func TestGraphBasicOps(t *testing.T) {
	g := NewGraph()
	g.AddNode(Node{ID: "A", Name: "A"})
	g.AddNode(Node{ID: "B", Name: "B"})
	g.AddNode(Node{ID: "C", Name: "C"})
	g.AddEdge(Edge{From: "A", To: "B", Type: "calls"})
	g.AddEdge(Edge{From: "B", To: "C", Type: "calls"})

	if d := g.ShortestPathLength("A", "C"); d != 2 {
		t.Fatalf("expected distance 2, got %d", d)
	}
	des := g.Descendants("A", 2)
	if len(des) != 2 {
		t.Fatalf("expected 2 descendants, got %d", len(des))
	}
	anc := g.Ancestors("C", 2)
	if len(anc) != 2 {
		t.Fatalf("expected 2 ancestors, got %d", len(anc))
	}
}

// REPORT.md #4 regression: pre-fix the inner adjacency map was keyed
// by the other endpoint alone, so two edges sharing From/To but
// differing in Type silently overwrote each other. Post-fix the key
// is composite (Node, Type) and both edges coexist.
func TestGraph_AddEdge_MultiTypeBetweenSamePairCoexist(t *testing.T) {
	g := NewGraph()
	g.AddNode(Node{ID: "pkgA", Name: "A"})
	g.AddNode(Node{ID: "pkgB", Name: "B"})

	g.AddEdge(Edge{From: "pkgA", To: "pkgB", Type: "imports"})
	g.AddEdge(Edge{From: "pkgA", To: "pkgB", Type: "calls"})

	if c := g.Counts().Edges; c != 2 {
		t.Fatalf("two edges with different types must both survive, Counts.Edges=%d", c)
	}

	got := g.Edges()
	types := map[string]bool{}
	for _, e := range got {
		if e.From == "pkgA" && e.To == "pkgB" {
			types[e.Type] = true
		}
	}
	if !types["imports"] || !types["calls"] {
		t.Fatalf("both edge types must be visible in Edges(), got %v", types)
	}
}

// Confirms the inverse invariant: a duplicate of the EXACT same edge
// (same From/To/Type) must still deduplicate to a single entry rather
// than fan-multiplying. Pre-fix this happened by accident; post-fix
// the composite key still collapses identical edges.
func TestGraph_AddEdge_DuplicateExactEdgeDeduplicates(t *testing.T) {
	g := NewGraph()
	g.AddNode(Node{ID: "A"})
	g.AddNode(Node{ID: "B"})

	g.AddEdge(Edge{From: "A", To: "B", Type: "calls"})
	g.AddEdge(Edge{From: "A", To: "B", Type: "calls"})

	if c := g.Counts().Edges; c != 1 {
		t.Fatalf("identical edges must collapse, Counts.Edges=%d", c)
	}
}

func TestGraphCycles(t *testing.T) {
	g := NewGraph()
	g.AddNode(Node{ID: "A", Name: "A"})
	g.AddNode(Node{ID: "B", Name: "B"})
	g.AddEdge(Edge{From: "A", To: "B", Type: "imports"})
	g.AddEdge(Edge{From: "B", To: "A", Type: "imports"})

	cycles := g.Cycles()
	if len(cycles) == 0 {
		t.Fatal("expected at least one cycle")
	}
}
