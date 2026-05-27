package codemap

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/ast"
)

// --- receiverTypeName ---

func TestReceiverTypeName(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"*Foo", "foo"},
		{"&Bar", "bar"},
		{"  *Baz  ", "baz"},
		{"()", ""},
		{"() MyType", "mytype"},
		{"MyType", "mytype"},
		{"  ", ""},
	}
	for _, tt := range tests {
		got := receiverTypeName(tt.input)
		if got != tt.want {
			t.Errorf("receiverTypeName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// --- HasNode ---

func TestGraph_HasNode(t *testing.T) {
	g := NewGraph()
	g.AddNode(Node{ID: "X", Name: "X"})
	if !g.HasNode("X") {
		t.Error("HasNode should return true for existing node")
	}
	if g.HasNode("missing") {
		t.Error("HasNode should return false for non-existent node")
	}
}

// --- AddNodeWithEdges ---

func TestGraph_AddNodeWithEdges(t *testing.T) {
	g := NewGraph()
	g.AddNodeWithEdges(
		Node{ID: "file:x.go", Name: "x.go"},
		[]Edge{
			{From: "file:x.go", To: "module:fmt", Type: "imports"},
		},
	)
	if !g.HasNode("file:x.go") {
		t.Error("node should be present")
	}
	if g.Counts().Edges != 1 {
		t.Errorf("expected 1 edge, got %d", g.Counts().Edges)
	}
}

func TestGraph_AddNodeWithEdges_EmptyEdges(t *testing.T) {
	g := NewGraph()
	g.AddNodeWithEdges(Node{ID: "solo", Name: "solo"}, nil)
	if !g.HasNode("solo") {
		t.Error("node should be present")
	}
	if g.Counts().Edges != 0 {
		t.Errorf("expected 0 edges, got %d", g.Counts().Edges)
	}
}

// --- Outgoing / copyOutgoing ---

func TestGraph_Outgoing(t *testing.T) {
	g := NewGraph()
	g.AddNode(Node{ID: "A"})
	g.AddNode(Node{ID: "B"})
	g.AddNode(Node{ID: "C"})
	g.AddEdge(Edge{From: "A", To: "B", Type: "calls"})
	g.AddEdge(Edge{From: "A", To: "C", Type: "imports"})

	out := g.Outgoing("A")
	if len(out) != 2 {
		t.Fatalf("expected 2 outgoing edges, got %d", len(out))
	}
	types := map[string]bool{}
	for _, e := range out {
		types[e.Type] = true
	}
	if !types["calls"] || !types["imports"] {
		t.Errorf("missing edge types in outgoing: %v", types)
	}
}

func TestGraph_Outgoing_NoEdges(t *testing.T) {
	g := NewGraph()
	g.AddNode(Node{ID: "A"})
	out := g.Outgoing("A")
	if len(out) != 0 {
		t.Errorf("expected 0 outgoing, got %d", len(out))
	}
}

func TestGraph_Outgoing_NonExistent(t *testing.T) {
	g := NewGraph()
	out := g.Outgoing("ghost")
	if len(out) != 0 {
		t.Errorf("expected 0 outgoing for missing node, got %d", len(out))
	}
}

// --- Incoming / copyIncoming ---

func TestGraph_Incoming(t *testing.T) {
	g := NewGraph()
	g.AddNode(Node{ID: "A"})
	g.AddNode(Node{ID: "B"})
	g.AddEdge(Edge{From: "A", To: "B", Type: "calls"})

	in := g.Incoming("B")
	if len(in) != 1 {
		t.Fatalf("expected 1 incoming edge, got %d", len(in))
	}
	if in[0].From != "A" {
		t.Errorf("incoming edge from = %q, want A", in[0].From)
	}
}

func TestGraph_Incoming_NoEdges(t *testing.T) {
	g := NewGraph()
	g.AddNode(Node{ID: "A"})
	in := g.Incoming("A")
	if len(in) != 0 {
		t.Errorf("expected 0 incoming, got %d", len(in))
	}
}

func TestGraph_Incoming_NonExistent(t *testing.T) {
	g := NewGraph()
	in := g.Incoming("ghost")
	if len(in) != 0 {
		t.Errorf("expected 0 incoming for missing node, got %d", len(in))
	}
}

// --- Clear ---

func TestGraph_Clear(t *testing.T) {
	g := NewGraph()
	g.AddNode(Node{ID: "A"})
	g.AddNode(Node{ID: "B"})
	g.AddEdge(Edge{From: "A", To: "B", Type: "calls"})

	g.Clear()
	if c := g.Counts(); c.Nodes != 0 || c.Edges != 0 {
		t.Fatalf("graph should be empty after Clear, got nodes=%d edges=%d", c.Nodes, c.Edges)
	}
	if g.HasNode("A") {
		t.Error("nodes should be gone after Clear")
	}
}

func TestGraph_Clear_Empty(t *testing.T) {
	g := NewGraph()
	g.Clear() // should not panic
	if c := g.Counts(); c.Nodes != 0 || c.Edges != 0 {
		t.Fatalf("empty graph should stay empty after Clear")
	}
}

// --- WalkDepthFirst ---

func TestGraph_WalkDepthFirst(t *testing.T) {
	g := NewGraph()
	g.AddNode(Node{ID: "A", Name: "A"})
	g.AddNode(Node{ID: "B", Name: "B"})
	g.AddNode(Node{ID: "C", Name: "C"})
	g.AddEdge(Edge{From: "A", To: "B", Type: "calls"})
	g.AddEdge(Edge{From: "B", To: "C", Type: "calls"})

	var visited []string
	g.WalkDepthFirst("A", func(n Node) {
		visited = append(visited, n.Name)
	})
	if len(visited) != 3 {
		t.Fatalf("expected 3 nodes visited, got %d: %v", len(visited), visited)
	}
	if visited[0] != "A" {
		t.Errorf("DFS first node = %q, want A", visited[0])
	}
}

func TestGraph_WalkDepthFirst_Cycle(t *testing.T) {
	g := NewGraph()
	g.AddNode(Node{ID: "A"})
	g.AddNode(Node{ID: "B"})
	g.AddEdge(Edge{From: "A", To: "B", Type: "calls"})
	g.AddEdge(Edge{From: "B", To: "A", Type: "calls"})

	count := 0
	g.WalkDepthFirst("A", func(n Node) {
		count++
		if count > 10 {
			t.Fatal("DFS should not loop infinitely on cycles")
		}
	})
	if count != 2 {
		t.Errorf("expected 2 nodes in cyclic graph, got %d", count)
	}
}

func TestGraph_WalkDepthFirst_NonExistentStart(t *testing.T) {
	g := NewGraph()
	count := 0
	g.WalkDepthFirst("missing", func(n Node) { count++ })
	if count != 0 {
		t.Error("DFS from non-existent node should visit nothing")
	}
}

// --- WalkBreadthFirst ---

func TestGraph_WalkBreadthFirst(t *testing.T) {
	g := NewGraph()
	g.AddNode(Node{ID: "A", Name: "A"})
	g.AddNode(Node{ID: "B1", Name: "B1"})
	g.AddNode(Node{ID: "B2", Name: "B2"})
	g.AddNode(Node{ID: "C", Name: "C"})
	g.AddEdge(Edge{From: "A", To: "B1", Type: "calls"})
	g.AddEdge(Edge{From: "A", To: "B2", Type: "calls"})
	g.AddEdge(Edge{From: "B1", To: "C", Type: "calls"})

	var visited []string
	g.WalkBreadthFirst("A", func(n Node) {
		visited = append(visited, n.Name)
	})
	if len(visited) != 4 {
		t.Fatalf("expected 4 nodes visited, got %d: %v", len(visited), visited)
	}
	if visited[0] != "A" {
		t.Errorf("BFS first = %q, want A", visited[0])
	}
}

func TestGraph_WalkBreadthFirst_Cycle(t *testing.T) {
	g := NewGraph()
	g.AddNode(Node{ID: "A"})
	g.AddNode(Node{ID: "B"})
	g.AddEdge(Edge{From: "A", To: "B", Type: "calls"})
	g.AddEdge(Edge{From: "B", To: "A", Type: "calls"})

	count := 0
	g.WalkBreadthFirst("A", func(n Node) {
		count++
		if count > 10 {
			t.Fatal("BFS should not loop infinitely on cycles")
		}
	})
	if count != 2 {
		t.Errorf("expected 2 in cyclic graph, got %d", count)
	}
}

func TestGraph_WalkBreadthFirst_NonExistentStart(t *testing.T) {
	g := NewGraph()
	count := 0
	g.WalkBreadthFirst("missing", func(n Node) { count++ })
	if count != 0 {
		t.Error("BFS from non-existent node should visit nothing")
	}
}

// --- SetMaxRecent ---

func TestBuildMetrics_SetMaxRecent(t *testing.T) {
	m := newBuildMetricsTracker()
	for i := 0; i < 10; i++ {
		m.recordBuild(BuildSample{FilesProcessed: int64(i)})
	}
	m.SetMaxRecent(3)
	snap := m.snapshot()
	if len(snap.Recent) > 3 {
		t.Errorf("expected at most 3 recent builds after SetMaxRecent(3), got %d", len(snap.Recent))
	}
	if len(snap.Recent) == 3 {
		if snap.Recent[0].FilesProcessed != 7 {
			t.Errorf("oldest kept = %d, want 7", snap.Recent[0].FilesProcessed)
		}
	}
}

func TestBuildMetrics_SetMaxRecent_ZeroResets(t *testing.T) {
	m := newBuildMetricsTracker()
	m.SetMaxRecent(0)
	// Should not panic; resets to default
	for i := 0; i < 5; i++ {
		m.recordBuild(BuildSample{})
	}
	snap := m.snapshot()
	if len(snap.Recent) > defaultMaxRecentBuildSamples {
		t.Errorf("expected at most %d recent, got %d", defaultMaxRecentBuildSamples, len(snap.Recent))
	}
}

func TestBuildMetrics_SetMaxRecent_NegativeResets(t *testing.T) {
	m := newBuildMetricsTracker()
	m.SetMaxRecent(-5)
	for i := 0; i < 5; i++ {
		m.recordBuild(BuildSample{})
	}
	snap := m.snapshot()
	if len(snap.Recent) > defaultMaxRecentBuildSamples {
		t.Errorf("expected at most %d recent, got %d", defaultMaxRecentBuildSamples, len(snap.Recent))
	}
}

// --- BuildFromFilesParallel ---

func TestBuildFromFilesParallel_NilASTEngine(t *testing.T) {
	e := New(nil, nil)
	err := e.BuildFromFilesParallel(context.Background(), []string{"test.go"}, 2)
	if err == nil {
		t.Fatal("expected error for nil AST engine")
	}
}

func TestBuildFromFilesParallel_SingleFile(t *testing.T) {
	tmp := t.TempDir()
	goFile := filepath.Join(tmp, "sample.go")
	content := []byte(`package main
import "fmt"
func main() {
	fmt.Println("hello")
}
`)
	if err := os.WriteFile(goFile, content, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	e := New(ast.New(), nil)
	err := e.BuildFromFilesParallel(context.Background(), []string{goFile}, 1)
	if err != nil {
		t.Fatalf("BuildFromFilesParallel: %v", err)
	}
	if e.Graph().Counts().Nodes == 0 {
		t.Error("expected at least one node from parsed file")
	}
}

func TestBuildFromFilesParallel_CancelledContext(t *testing.T) {
	tmp := t.TempDir()
	goFile := filepath.Join(tmp, "cancel_test.go")
	if err := os.WriteFile(goFile, []byte("package main\nfunc main(){}\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	e := New(ast.New(), nil)
	err := e.BuildFromFilesParallel(ctx, []string{goFile}, 1)
	if err == nil {
		t.Error("expected error from cancelled context")
	}
}

func TestBuildFromFilesParallel_ProgressCallback(t *testing.T) {
	tmp := t.TempDir()
	files := make([]string, 3)
	for i := 0; i < 3; i++ {
		name := filepath.Join(tmp, fmt.Sprintf("prog_%d.go", i))
		if err := os.WriteFile(name, []byte("package main\nfunc main(){}\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		files[i] = name
	}

	e := New(ast.New(), nil)
	var progressCalls int
	err := e.BuildFromFilesParallel(context.Background(), files, 2, func(processed, total int) {
		progressCalls++
	})
	if err != nil {
		t.Fatalf("BuildFromFilesParallel: %v", err)
	}
	if progressCalls == 0 {
		t.Error("expected at least one progress callback")
	}
}

func TestBuildFromFilesParallel_EmptyPathsSkipped(t *testing.T) {
	e := New(ast.New(), nil)
	err := e.BuildFromFilesParallel(context.Background(), []string{"", "  ", ""}, 1)
	if err != nil {
		t.Fatalf("empty paths should be skipped, got: %v", err)
	}
	if e.Graph().Counts().Nodes != 0 {
		t.Error("no real files = no nodes expected")
	}
}

// --- Outgoing/Incoming with tombstoned edges ---

func TestGraph_Outgoing_SkipsTombstoned(t *testing.T) {
	g := NewGraph()
	g.AddNode(Node{ID: "A"})
	g.AddNode(Node{ID: "B"})
	g.AddEdge(Edge{From: "A", To: "B", Type: "calls"})
	g.RemoveEdge(Edge{From: "A", To: "B", Type: "calls"})

	out := g.Outgoing("A")
	if len(out) != 0 {
		t.Errorf("expected 0 outgoing after removal, got %d", len(out))
	}
}

func TestGraph_Incoming_SkipsTombstoned(t *testing.T) {
	g := NewGraph()
	g.AddNode(Node{ID: "A"})
	g.AddNode(Node{ID: "B"})
	g.AddEdge(Edge{From: "A", To: "B", Type: "calls"})
	g.RemoveEdge(Edge{From: "A", To: "B", Type: "calls"})

	in := g.Incoming("B")
	if len(in) != 0 {
		t.Errorf("expected 0 incoming after removal, got %d", len(in))
	}
}

// --- Integration: Outgoing + Incoming after AddNodeWithEdges ---

func TestGraph_OutgoingIncoming_AfterAddNodeWithEdges(t *testing.T) {
	g := NewGraph()
	g.AddNode(Node{ID: "B"})
	g.AddNodeWithEdges(
		Node{ID: "A"},
		[]Edge{
			{From: "A", To: "B", Type: "calls"},
		},
	)
	out := g.Outgoing("A")
	if len(out) != 1 {
		t.Fatalf("expected 1 outgoing, got %d", len(out))
	}
	in := g.Incoming("B")
	if len(in) != 1 {
		t.Fatalf("expected 1 incoming, got %d", len(in))
	}
}
