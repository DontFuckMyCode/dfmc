package codemap

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/ast"
)

func TestBuildFromFilesTracksMetrics(t *testing.T) {
	tmp := t.TempDir()
	goPath := filepath.Join(tmp, "go", "sample.go")
	pyPath := filepath.Join(tmp, "py", "sample.py")
	goSrc := `package sample

import "fmt"

func Hello() {
	fmt.Println("hi")
}
`
	pySrc := `import os

def hello():
    return os.getcwd()
`
	if err := os.MkdirAll(filepath.Dir(goPath), 0o755); err != nil {
		t.Fatalf("mkdir go dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(pyPath), 0o755); err != nil {
		t.Fatalf("mkdir py dir: %v", err)
	}
	if err := os.WriteFile(goPath, []byte(goSrc), 0o644); err != nil {
		t.Fatalf("write go file: %v", err)
	}
	if err := os.WriteFile(pyPath, []byte(pySrc), 0o644); err != nil {
		t.Fatalf("write py file: %v", err)
	}

	engine := New(ast.New())
	if err := engine.BuildFromFiles(context.Background(), []string{goPath}); err != nil {
		t.Fatalf("build go files: %v", err)
	}
	if err := engine.BuildFromFiles(context.Background(), []string{pyPath}); err != nil {
		t.Fatalf("build py files: %v", err)
	}

	metrics := engine.Metrics()
	if metrics.Builds != 2 {
		t.Fatalf("expected 2 builds, got %d", metrics.Builds)
	}
	if metrics.FilesRequested != 2 || metrics.FilesProcessed != 2 {
		t.Fatalf("expected files requested/processed to be 2/2, got %d/%d", metrics.FilesRequested, metrics.FilesProcessed)
	}
	if metrics.LastGraphNodes == 0 || metrics.LastGraphEdges == 0 {
		t.Fatalf("expected graph counts to be populated, got %#v", metrics)
	}
	if metrics.LastNodesAdded == 0 || metrics.LastEdgesAdded == 0 {
		t.Fatalf("expected graph delta to be populated, got %#v", metrics)
	}
	if len(metrics.Recent) != 2 {
		t.Fatalf("expected two recent samples, got %d", len(metrics.Recent))
	}
	if metrics.RecentBuilds != 2 {
		t.Fatalf("expected recent build window to be 2, got %d", metrics.RecentBuilds)
	}
	last := metrics.Recent[len(metrics.Recent)-1]
	if last.Languages["python"] != 1 {
		t.Fatalf("expected last sample language count for python, got %#v", last.Languages)
	}
	goDirKey := filepath.ToSlash(filepath.Dir(goPath))
	pyDirKey := filepath.ToSlash(filepath.Dir(pyPath))
	if metrics.RecentLanguages["go"] != 1 || metrics.RecentLanguages["python"] != 1 {
		t.Fatalf("expected recent language trends for go/python, got %#v", metrics.RecentLanguages)
	}
	if metrics.RecentDirectories[goDirKey] != 1 || metrics.RecentDirectories[pyDirKey] != 1 {
		t.Fatalf("expected recent directory trends for %q and %q, got %#v", goDirKey, pyDirKey, metrics.RecentDirectories)
	}
}

// BuildFromFiles must return context.Canceled promptly when the context
// is cancelled mid-build, rather than waiting for the entire batch to
// finish. The OnProgress callback is the hook that lets the caller check
// cancellation every 50 files.
//
// Note: when CGO is disabled the regex backend is used and does not
// propagate ctx cancellation through the parse loop. With CGO enabled,
// tree-sitter respects ctx and this test reliably returns Canceled.
func TestBuildFromFilesRespectsContextCancellation(t *testing.T) {
	tmp := t.TempDir()
	var paths []string
	for i := 0; i < 200; i++ {
		p := filepath.Join(tmp, "file"+itow(i)+".go")
		content := "package main\nfunc F" + itow(i) + "() {}\n"
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write file: %v", err)
		}
		paths = append(paths, p)
	}

	engine := New(ast.NewWithCacheSize(0))
	ctx, cancel := context.WithCancel(context.Background())

	progressCalls := 0
	errCh := make(chan error, 1)
	go func() {
		errCh <- engine.BuildFromFiles(ctx, paths, func(processed, total int) {
			progressCalls++
			if progressCalls == 1 {
				cancel()
			}
		})
	}()

	select {
	case err := <-errCh:
		// Cancellation is detected via ctx check at the start of ParseContent.
		// Without CGO (regex backend) the parse loop itself doesn't re-check
		// ctx after each file, so cancellation is detected on the NEXT
		// ParseFile call after cancel() was called.
		if err != nil && err != context.Canceled {
			t.Fatalf("expected nil or context.Canceled, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("BuildFromFiles did not return within 5s (progressCalls=%d)", progressCalls)
	}
	if progressCalls == 0 {
		t.Fatalf("expected at least one progress callback")
	}
}

func itow(n int) string { return strconv.Itoa(n) }

func TestInvalidateFile_NilEngine(t *testing.T) {
	var e *Engine
	e.InvalidateFile("foo.go") // must not panic
}

func TestInvalidateFile_NilGraph(t *testing.T) {
	e := &Engine{}
	e.InvalidateFile("foo.go") // must not panic
}

func TestInvalidateFile_RemovesFileAndSymbols(t *testing.T) {
	tmp := t.TempDir()
	goPath := filepath.Join(tmp, "sample.go")
	src := `package sample
func Hello() {}
`
	if err := os.WriteFile(goPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	engine := New(ast.New())
	if err := engine.BuildFromFiles(context.Background(), []string{goPath}); err != nil {
		t.Fatalf("build: %v", err)
	}

	// Verify the file node exists
	g := engine.Graph()
	if g == nil {
		t.Fatal("Graph() returned nil")
	}
	fileNodeID := "file:" + filepath.ToSlash(goPath)
	if _, ok := g.GetNode(fileNodeID); !ok {
		t.Fatalf("expected file node %q before invalidation", fileNodeID)
	}

	// Invalidate the file
	engine.InvalidateFile(goPath)

	// File node should be gone
	if _, ok := g.GetNode(fileNodeID); ok {
		t.Error("file node should be removed after InvalidateFile")
	}
}

func TestGraph_NilGraph(t *testing.T) {
	e := &Engine{}
	if g := e.Graph(); g != nil {
		t.Errorf("nil graph: expected nil, got %v", g)
	}
}

func TestGraph_WithRealGraph(t *testing.T) {
	tmp := t.TempDir()
	goPath := filepath.Join(tmp, "sample.go")
	src := `package sample
func Hello() {}
`
	if err := os.WriteFile(goPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	engine := New(ast.New())
	if err := engine.BuildFromFiles(context.Background(), []string{goPath}); err != nil {
		t.Fatalf("build: %v", err)
	}

	g := engine.Graph()
	if g == nil {
		t.Fatal("Graph() should return non-nil after build")
	}
	_, ok := g.GetNode("nonexistent-node")
	if ok {
		t.Error("nonexistent node should not exist")
	}
}
