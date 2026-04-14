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
