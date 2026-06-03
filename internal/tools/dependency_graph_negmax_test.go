package tools

import (
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/codemap"
)

// TestDependencyGraphQueriesNegativeMaxNoPanic guards the dependency-graph
// query helpers against a negative max. They truncate matched edges with
// `if len(edges) > max { edges = edges[:max] }`; for a negative max,
// len(edges) > max is true and edges[:max] panics with "slice bounds out of
// range". max flows from the `max_results` tool arg (asInt, default 50, no
// clamp), so a dependency_graph call with max_results=-1 hit it. The
// lifecycle panic guard would recover it, but the tool should return a
// graceful result, not panic. max<=0 now means "no limit".
func TestDependencyGraphQueriesNegativeMaxNoPanic(t *testing.T) {
	g := codemap.NewGraph()
	g.AddNode(codemap.Node{ID: "a.go", Name: "a.go"})
	g.AddNode(codemap.Node{ID: "b.go", Name: "b.go"})
	g.AddEdge(codemap.Edge{From: "a.go", To: "module:foo", Type: "imports"})
	g.AddEdge(codemap.Edge{From: "b.go", To: "module:foo", Type: "imports"})
	g.AddEdge(codemap.Edge{From: "a.go", To: "pkg.DoThing", Type: "calls"})
	g.AddEdge(codemap.Edge{From: "a.go", To: "other/file.go", Type: "imports"})

	tool := &DependencyGraphTool{}

	for _, m := range []int{-1, -100, 0} {
		r1 := tool.queryImporters(g, "foo", "", m)
		if len(r1.Edges) != 2 {
			t.Fatalf("queryImporters(max=%d): expected 2 importers, got %d", m, len(r1.Edges))
		}
		r2 := tool.queryCallers(g, "dothing", m)
		if len(r2.Edges) != 1 {
			t.Fatalf("queryCallers(max=%d): expected 1 caller, got %d", m, len(r2.Edges))
		}
		// queryImports walks the other direction; just assert it doesn't panic.
		_ = tool.queryImports(g, "a.go", "", m)
	}
}
