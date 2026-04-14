package codemap

import (
	"context"
	"os"
	"path/filepath"
	"testing"

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
