package tools

import (
	"context"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/codemap"
	"github.com/dontfuckmycode/dfmc/internal/config"
)

func TestDependencyGraph_QueryRequiresQuery(t *testing.T) {
	tmp := t.TempDir()
	codemapEng := codemap.New(nil)
	eng := New(*config.DefaultConfig())
	eng.SetCodemap(codemapEng)

	_, err := eng.Execute(context.Background(), "dependency_graph", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{},
	})
	if err == nil {
		t.Fatalf("expected error for missing query")
	}
}

func TestDependencyGraph_InvalidQueryType(t *testing.T) {
	tmp := t.TempDir()
	codemapEng := codemap.New(nil)
	eng := New(*config.DefaultConfig())
	eng.SetCodemap(codemapEng)

	_, err := eng.Execute(context.Background(), "dependency_graph", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"query": "not_a_query"},
	})
	if err == nil {
		t.Fatalf("expected error for invalid query type")
	}
}

func TestDependencyGraph_EmptyGraph(t *testing.T) {
	tmp := t.TempDir()
	codemapEng := codemap.New(nil)
	eng := New(*config.DefaultConfig())
	eng.SetCodemap(codemapEng)

	res, err := eng.Execute(context.Background(), "dependency_graph", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"query": "importers", "module": "foo"},
	})
	if err != nil {
		t.Fatalf("expected graceful handling of empty graph: %v", err)
	}
	if res.Data == nil {
		t.Fatalf("expected data in result")
	}
}

func TestDependencyGraph_Importers(t *testing.T) {
	tmp := t.TempDir()
	codemapEng := codemap.New(nil)
	g := codemapEng.Graph()
	g.AddNode(codemap.Node{ID: "file:pkg/a.go", Name: "a.go", Kind: "file", Path: "pkg/a.go"})
	g.AddNode(codemap.Node{ID: "file:pkg/b.go", Name: "b.go", Kind: "file", Path: "pkg/b.go"})
	g.AddNode(codemap.Node{ID: "module:foo/bar", Name: "foo/bar", Kind: "module"})
	g.AddEdge(codemap.Edge{From: "file:pkg/a.go", To: "module:foo/bar", Type: "imports"})
	g.AddEdge(codemap.Edge{From: "file:pkg/b.go", To: "module:foo/bar", Type: "imports"})

	eng := New(*config.DefaultConfig())
	eng.SetCodemap(codemapEng)

	res, err := eng.Execute(context.Background(), "dependency_graph", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"query": "importers", "module": "foo/bar"},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	edgesRaw, ok := res.Data["edges"]
	if !ok {
		t.Fatalf("expected edges in result")
	}
	edges, ok := edgesRaw.([]depEdge)
	if !ok {
		t.Fatalf("expected []depEdge in edges, got %T", edgesRaw)
	}
	if len(edges) != 2 {
		t.Errorf("want 2 importers, got %d", len(edges))
	}
}

func TestDependencyGraph_Imports(t *testing.T) {
	tmp := t.TempDir()
	codemapEng := codemap.New(nil)
	g := codemapEng.Graph()
	g.AddNode(codemap.Node{ID: "file:pkg/a.go", Name: "a.go", Kind: "file", Path: "pkg/a.go"})
	g.AddNode(codemap.Node{ID: "module:foo/bar", Name: "foo/bar", Kind: "module"})
	g.AddEdge(codemap.Edge{From: "file:pkg/a.go", To: "module:foo/bar", Type: "imports"})

	eng := New(*config.DefaultConfig())
	eng.SetCodemap(codemapEng)

	res, err := eng.Execute(context.Background(), "dependency_graph", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"query": "imports", "file": "pkg/a.go"},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	edgesRaw, ok := res.Data["edges"]
	if !ok {
		t.Fatalf("expected edges in result")
	}
	edges, ok := edgesRaw.([]depEdge)
	if !ok || len(edges) == 0 {
		t.Errorf("expected at least 1 edge, got %T", edgesRaw)
	}
}

func TestDependencyGraph_FanOut(t *testing.T) {
	tmp := t.TempDir()
	codemapEng := codemap.New(nil)
	g := codemapEng.Graph()
	g.AddNode(codemap.Node{ID: "file:main.go", Name: "main.go", Kind: "file", Path: "main.go"})
	g.AddNode(codemap.Node{ID: "file:util.go", Name: "util.go", Kind: "file", Path: "util.go"})
	g.AddNode(codemap.Node{ID: "module:fmt", Name: "fmt", Kind: "module"})
	g.AddEdge(codemap.Edge{From: "file:main.go", To: "file:util.go", Type: "calls"})
	g.AddEdge(codemap.Edge{From: "file:main.go", To: "module:fmt", Type: "imports"})
	g.AddEdge(codemap.Edge{From: "file:util.go", To: "module:fmt", Type: "imports"})

	eng := New(*config.DefaultConfig())
	eng.SetCodemap(codemapEng)

	res, err := eng.Execute(context.Background(), "dependency_graph", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"query": "fan_out", "file": "main.go", "max_depth": 2},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	nodesRaw, ok := res.Data["nodes"]
	if !ok {
		t.Fatalf("expected nodes in result")
	}
	nodes, ok := nodesRaw.([]depNode)
	if !ok || len(nodes) < 2 {
		t.Errorf("want at least 2 nodes in fan_out, got %T", nodesRaw)
	}
}

func TestDependencyGraph_FanIn(t *testing.T) {
	tmp := t.TempDir()
	codemapEng := codemap.New(nil)
	g := codemapEng.Graph()
	g.AddNode(codemap.Node{ID: "file:main.go", Name: "main.go", Kind: "file", Path: "main.go"})
	g.AddNode(codemap.Node{ID: "file:util.go", Name: "util.go", Kind: "file", Path: "util.go"})
	g.AddNode(codemap.Node{ID: "file:lib.go", Name: "lib.go", Kind: "file", Path: "lib.go"})
	g.AddEdge(codemap.Edge{From: "file:main.go", To: "file:util.go", Type: "calls"})
	g.AddEdge(codemap.Edge{From: "file:lib.go", To: "file:util.go", Type: "calls"})

	eng := New(*config.DefaultConfig())
	eng.SetCodemap(codemapEng)

	res, err := eng.Execute(context.Background(), "dependency_graph", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"query": "fan_in", "file": "util.go"},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	nodesRaw, ok := res.Data["nodes"]
	if !ok {
		t.Fatalf("expected nodes in result")
	}
	nodes, ok := nodesRaw.([]depNode)
	if !ok || len(nodes) < 2 {
		t.Errorf("want at least 2 nodes in fan_in, got %T", nodesRaw)
	}
}

func TestDependencyGraph_MaxResults(t *testing.T) {
	tmp := t.TempDir()
	codemapEng := codemap.New(nil)
	g := codemapEng.Graph()
	g.AddNode(codemap.Node{ID: "module:foo", Name: "foo", Kind: "module"})
	letters := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
	for _, l := range letters {
		fname := "dir/file" + l + ".go"
		g.AddNode(codemap.Node{ID: "file:" + fname, Name: "file.go", Kind: "file"})
		g.AddEdge(codemap.Edge{From: "file:" + fname, To: "module:foo", Type: "imports"})
	}

	eng := New(*config.DefaultConfig())
	eng.SetCodemap(codemapEng)

	res, err := eng.Execute(context.Background(), "dependency_graph", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"query": "importers", "module": "foo", "max_results": 3},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	edgesRaw, ok := res.Data["edges"]
	if !ok {
		t.Fatalf("expected edges in result")
	}
	edges, ok := edgesRaw.([]depEdge)
	if !ok {
		t.Fatalf("expected []depEdge, got %T", edgesRaw)
	}
	if len(edges) > 3 {
		t.Errorf("want at most 3 edges, got %d", len(edges))
	}
}

func TestDependencyGraph_PathNotFound(t *testing.T) {
	tmp := t.TempDir()
	codemapEng := codemap.New(nil)
	g := codemapEng.Graph()
	g.AddNode(codemap.Node{ID: "file:a.go", Name: "a.go", Kind: "file", Path: "a.go"})
	g.AddNode(codemap.Node{ID: "file:b.go", Name: "b.go", Kind: "file", Path: "b.go"})

	eng := New(*config.DefaultConfig())
	eng.SetCodemap(codemapEng)

	res, err := eng.Execute(context.Background(), "dependency_graph", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"query": "path", "file": "a.go", "fileB": "b.go"},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Data["summary"] == nil {
		t.Errorf("expected non-empty summary")
	}
}

func TestDependencyGraph_EdgeTypeFilter(t *testing.T) {
	tmp := t.TempDir()
	codemapEng := codemap.New(nil)
	g := codemapEng.Graph()
	g.AddNode(codemap.Node{ID: "file:a.go", Name: "a.go", Kind: "file", Path: "a.go"})
	g.AddNode(codemap.Node{ID: "file:b.go", Name: "b.go", Kind: "file", Path: "b.go"})
	g.AddEdge(codemap.Edge{From: "file:a.go", To: "file:b.go", Type: "imports"})

	eng := New(*config.DefaultConfig())
	eng.SetCodemap(codemapEng)

	res, err := eng.Execute(context.Background(), "dependency_graph", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"query": "imports", "file": "a.go", "edge_type": "calls"},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	edgesRaw, ok := res.Data["edges"]
	if !ok {
		t.Fatalf("expected edges in result")
	}
	edges, ok := edgesRaw.([]depEdge)
	if !ok {
		t.Fatalf("expected []depEdge, got %T", edgesRaw)
	}
	if len(edges) != 0 {
		t.Errorf("want 0 edges when filtering by 'calls', got %d", len(edges))
	}
}

func TestDependencyGraph_NoCodemapEngine(t *testing.T) {
	tmp := t.TempDir()
	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	_, err := eng.Execute(context.Background(), "dependency_graph", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"query": "importers", "module": "foo"},
	})
	if err == nil {
		t.Fatalf("expected error when codemap is nil")
	}
}