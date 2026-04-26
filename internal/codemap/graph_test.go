package codemap

import (
	"fmt"
	"sync"
	"testing"
)

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

// TestGraphConcurrentAccess exercises Graph under a mixed read/write
// workload with the race detector on. Regression guard against future
// changes that drop or reorder the RWMutex acquisitions in the public
// Graph surface. Run via `go test -race ./internal/codemap`.
func TestGraphConcurrentAccess(t *testing.T) {
	g := NewGraph()
	g.AddNode(Node{ID: "seed", Name: "seed"})

	const workers = 32
	const opsPerWorker = 200

	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func(w int) {
			defer wg.Done()
			for i := 0; i < opsPerWorker; i++ {
				switch i % 6 {
				case 0:
					id := fmt.Sprintf("n-%d-%d", w, i)
					g.AddNode(Node{ID: id, Name: id})
				case 1:
					from := fmt.Sprintf("n-%d-%d", w, i-1)
					g.AddEdge(Edge{From: from, To: "seed", Type: "calls"})
				case 2:
					_ = g.Counts()
				case 3:
					_ = g.Nodes()
				case 4:
					_ = g.Edges()
				case 5:
					_, _ = g.GetNode("seed")
				}
			}
		}(w)
	}
	wg.Wait()

	// Sanity — the seed node must survive the mixed workload, and each
	// worker's AddNode cases (ops 0, 6, 12, ...) should have landed
	// without any serialization hazard.
	if _, ok := g.GetNode("seed"); !ok {
		t.Fatal("seed node disappeared under concurrent load")
	}
	counts := g.Counts()
	if counts.Nodes < workers {
		t.Fatalf("expected at least %d nodes after concurrent load, got %d", workers, counts.Nodes)
	}
}

func TestGraph_RemoveEdge(t *testing.T) {
	g := NewGraph()
	g.AddNode(Node{ID: "A", Name: "A"})
	g.AddNode(Node{ID: "B", Name: "B"})
	g.AddEdge(Edge{From: "A", To: "B", Type: "calls"})

	if c := g.Counts().Edges; c != 1 {
		t.Fatalf("expected 1 edge, got %d", c)
	}

	removed := g.RemoveEdge(Edge{From: "A", To: "B", Type: "calls"})
	if !removed {
		t.Error("RemoveEdge should return true for existing edge")
	}
	if c := g.Counts().Edges; c != 0 {
		t.Errorf("expected 0 edges after remove, got %d", c)
	}

	removed = g.RemoveEdge(Edge{From: "A", To: "B", Type: "calls"})
	if removed {
		t.Error("RemoveEdge should return false for non-existent edge")
	}
}

func TestGraph_RemoveEdge_MultiType(t *testing.T) {
	g := NewGraph()
	g.AddNode(Node{ID: "A", Name: "A"})
	g.AddNode(Node{ID: "B", Name: "B"})
	g.AddEdge(Edge{From: "A", To: "B", Type: "imports"})
	g.AddEdge(Edge{From: "A", To: "B", Type: "calls"})

	if c := g.Counts().Edges; c != 2 {
		t.Fatalf("expected 2 edges, got %d", c)
	}

	g.RemoveEdge(Edge{From: "A", To: "B", Type: "imports"})
	if c := g.Counts().Edges; c != 1 {
		t.Errorf("expected 1 edge after removing imports, got %d", c)
	}
}
