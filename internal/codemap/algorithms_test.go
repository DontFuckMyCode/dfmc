// Pin tests for the codemap graph algorithms (SCC / Orphans /
// HotSpots / FindSymbol). graph_test.go covers Cycles + descendants
// at a basic level; this file targets edge cases the codemap relies
// on for cycle detection, hotspot ranking, and symbol jump-to.

package codemap

import (
	"sort"
	"strings"
	"testing"
)

// SCC must return one component per disconnected island, with
// multi-node SCCs intact. The Tarjan implementation in algorithms.go
// uses recursion + an explicit stack; we want the cycle a→b→c→a to
// land in a single 3-element component.
func TestStronglyConnectedComponents_MultiNodeCycle(t *testing.T) {
	g := NewGraph()
	for _, id := range []string{"a", "b", "c", "d"} {
		g.AddNode(Node{ID: id, Name: id})
	}
	// Three-node cycle a→b→c→a, plus an isolated tail d.
	g.AddEdge(Edge{From: "a", To: "b"})
	g.AddEdge(Edge{From: "b", To: "c"})
	g.AddEdge(Edge{From: "c", To: "a"})
	g.AddEdge(Edge{From: "c", To: "d"})

	sccs := g.StronglyConnectedComponents()

	// d is a sink in its own component; {a,b,c} must coalesce.
	var bigCycle []string
	for _, comp := range sccs {
		if len(comp) == 3 {
			bigCycle = append([]string(nil), comp...)
			sort.Strings(bigCycle)
			break
		}
	}
	if got := strings.Join(bigCycle, ","); got != "a,b,c" {
		t.Fatalf("expected SCC {a,b,c}; got %v (full sccs=%v)", bigCycle, sccs)
	}
}

// Self-loops are a degenerate but real case in dependency graphs
// (a function that calls itself). Cycles() must surface them; SCC
// is the underlying primitive that needs to handle the self-edge.
func TestStronglyConnectedComponents_SelfLoop(t *testing.T) {
	g := NewGraph()
	g.AddNode(Node{ID: "self", Name: "self"})
	g.AddEdge(Edge{From: "self", To: "self"})

	cycles := g.Cycles()
	if len(cycles) != 1 || len(cycles[0]) != 1 || cycles[0][0] != "self" {
		t.Fatalf("self-loop cycle missing: %v", cycles)
	}
}

// Empty graph must not panic and must return empty SCC set.
// Tarjan walks g.nodes directly, so an empty map needs to short-circuit.
func TestStronglyConnectedComponents_EmptyGraph(t *testing.T) {
	g := NewGraph()
	sccs := g.StronglyConnectedComponents()
	if len(sccs) != 0 {
		t.Fatalf("empty graph should produce 0 SCCs; got %d", len(sccs))
	}
	if cycles := g.Cycles(); len(cycles) != 0 {
		t.Fatalf("empty graph should produce 0 cycles; got %d", len(cycles))
	}
}

// Orphans: nodes with no incoming edges. Includes truly isolated
// nodes AND nodes that only have outgoing edges. The hotspot panel
// uses this to surface "entry-point candidates".
func TestOrphans_DistinguishesIsolatedFromSinks(t *testing.T) {
	g := NewGraph()
	for _, id := range []string{"root", "child", "leaf", "isolated"} {
		g.AddNode(Node{ID: id, Name: id})
	}
	// root → child → leaf. isolated stands alone.
	g.AddEdge(Edge{From: "root", To: "child"})
	g.AddEdge(Edge{From: "child", To: "leaf"})

	orphans := g.Orphans()
	gotNames := nodeNames(orphans)
	sort.Strings(gotNames)

	// root has no incoming, isolated has no incoming. child + leaf do.
	want := []string{"isolated", "root"}
	if strings.Join(gotNames, ",") != strings.Join(want, ",") {
		t.Fatalf("orphans=%v want %v", gotNames, want)
	}
}

func TestOrphans_EmptyGraph(t *testing.T) {
	g := NewGraph()
	if len(g.Orphans()) != 0 {
		t.Fatalf("empty graph orphans should be empty")
	}
}

// HotSpots ranks by degree (in + out). Tie-break is unspecified, so
// the test asserts the SET of top entries, not the order within ties.
func TestHotSpots_RanksByCombinedDegree(t *testing.T) {
	g := NewGraph()
	for _, id := range []string{"hub", "low", "mid", "leaf"} {
		g.AddNode(Node{ID: id, Name: id})
	}
	// hub: 3 out, 0 in => degree 3
	g.AddEdge(Edge{From: "hub", To: "low"})
	g.AddEdge(Edge{From: "hub", To: "mid"})
	g.AddEdge(Edge{From: "hub", To: "leaf"})
	// mid: 1 out, 1 in => degree 2
	g.AddEdge(Edge{From: "mid", To: "leaf"})
	// leaf: 0 out, 2 in => degree 2
	// low: 0 out, 1 in => degree 1

	top := g.HotSpots(2)
	if len(top) != 2 {
		t.Fatalf("expected top 2 hotspots; got %d", len(top))
	}
	if top[0].ID != "hub" {
		t.Fatalf("highest-degree node should be hub; got %s", top[0].ID)
	}
	// Second slot is either mid or leaf (both degree 2). Pin the SET.
	allowed := map[string]bool{"mid": true, "leaf": true}
	if !allowed[top[1].ID] {
		t.Fatalf("second hotspot should be mid or leaf (degree 2); got %s", top[1].ID)
	}
}

// HotSpots(0) means "all of them, sorted". Used by the codemap
// panel's "show every hotspot" view.
func TestHotSpots_LimitZeroReturnsAll(t *testing.T) {
	g := NewGraph()
	for _, id := range []string{"a", "b", "c"} {
		g.AddNode(Node{ID: id, Name: id})
	}
	g.AddEdge(Edge{From: "a", To: "b"})
	all := g.HotSpots(0)
	if len(all) != 3 {
		t.Fatalf("limit=0 should return all nodes; got %d", len(all))
	}
}

// FindSymbol on the Engine wraps graph node lookup with case-folded
// matching. Pin both the case-fold contract and the empty-name guard.
// We hand-roll a minimal Engine — codemap.New(nil) is enough because
// FindSymbol only walks the graph, never the AST engine.
func TestFindSymbol_CaseInsensitiveAndGuards(t *testing.T) {
	e := New(nil)
	e.graph.AddNode(Node{ID: "pkg/file.go::Foo", Name: "Foo"})
	e.graph.AddNode(Node{ID: "pkg/other.go::foo", Name: "foo"})
	e.graph.AddNode(Node{ID: "pkg/other.go::Bar", Name: "Bar"})

	// Case-insensitive: both "Foo" and "foo" should match.
	matches := e.FindSymbol("FOO")
	if len(matches) != 2 {
		t.Fatalf("case-folded lookup should match 2; got %d (%v)", len(matches), matches)
	}

	// Empty / whitespace name returns nil, not the entire graph.
	if got := e.FindSymbol(""); got != nil {
		t.Fatalf("empty name should return nil; got %v", got)
	}
	if got := e.FindSymbol("   "); got != nil {
		t.Fatalf("whitespace name should return nil; got %v", got)
	}

	// No match returns nil/empty.
	if got := e.FindSymbol("nonexistent"); len(got) != 0 {
		t.Fatalf("missing symbol should return empty; got %v", got)
	}
}

// Counts() must report nodes/edges including bidirectional edges
// counted as 2 (directed graph). Pin the contract.
func TestCounts_DirectedEdges(t *testing.T) {
	g := NewGraph()
	g.AddNode(Node{ID: "a", Name: "a"})
	g.AddNode(Node{ID: "b", Name: "b"})
	g.AddEdge(Edge{From: "a", To: "b"})
	g.AddEdge(Edge{From: "b", To: "a"})
	c := g.Counts()
	if c.Nodes != 2 {
		t.Fatalf("nodes: got %d want 2", c.Nodes)
	}
	if c.Edges != 2 {
		t.Fatalf("directed edges should both count; got %d want 2", c.Edges)
	}
}

// ShortestPathLength returns -1 for unreachable. Pin the negative
// sentinel so callers (codemap panel "no path" rendering) keep
// working.
func TestShortestPathLength_UnreachableReturnsMinusOne(t *testing.T) {
	g := NewGraph()
	g.AddNode(Node{ID: "a", Name: "a"})
	g.AddNode(Node{ID: "b", Name: "b"})
	// no edges
	if got := g.ShortestPathLength("a", "b"); got != -1 {
		t.Fatalf("unreachable should be -1; got %d", got)
	}
}

func nodeNames(nodes []Node) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.ID)
	}
	return out
}
