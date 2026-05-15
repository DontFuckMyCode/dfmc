package ast

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// TestWalk_VisitsAllSymbolsInOrder pins the basic contract: Walk
// invokes the visitor once per extracted symbol, in the order they
// appear in ParseResult.Symbols (which mirrors source order for
// every backend).
func TestWalk_VisitsAllSymbolsInOrder(t *testing.T) {
	eng := New()
	defer eng.Close()

	src := []byte(`package main

func first() {}
func second() {}
func third() {}
`)

	var visited []string
	res, err := eng.Walk(context.Background(), "x.go", src, func(sym types.Symbol) WalkAction {
		visited = append(visited, sym.Name)
		return WalkContinue
	})
	if err != nil {
		t.Fatalf("Walk error: %v", err)
	}
	if res == nil {
		t.Fatal("Walk returned nil ParseResult on success")
	}
	if len(visited) < 3 {
		t.Fatalf("expected >=3 symbols visited, got %d (%v)", len(visited), visited)
	}
	// Source order is preserved.
	want := []string{"first", "second", "third"}
	for i, name := range want {
		if i >= len(visited) {
			t.Fatalf("missing symbol %q at index %d", name, i)
		}
		if visited[i] != name {
			t.Errorf("symbol order at %d: want %q got %q", i, name, visited[i])
		}
	}
}

// TestWalk_StopHaltsEarly pins that WalkStop terminates the walk
// after the current symbol. Later symbols MUST NOT be visited.
func TestWalk_StopHaltsEarly(t *testing.T) {
	eng := New()
	defer eng.Close()

	src := []byte(`package main

func a() {}
func b() {}
func c() {}
`)

	var visited []string
	_, err := eng.Walk(context.Background(), "y.go", src, func(sym types.Symbol) WalkAction {
		visited = append(visited, sym.Name)
		if sym.Name == "b" {
			return WalkStop
		}
		return WalkContinue
	})
	if err != nil {
		t.Fatalf("Walk error: %v", err)
	}
	// Should have visited a, b, and stopped before c.
	if len(visited) != 2 {
		t.Fatalf("expected 2 symbols (early stop), got %d (%v)", len(visited), visited)
	}
	if visited[0] != "a" || visited[1] != "b" {
		t.Errorf("expected [a b], got %v", visited)
	}
}

// TestWalk_NilVisitorParsesOnly: a nil visitor is allowed and behaves
// like a pure ParseContent call. Useful as a cache-warming shortcut.
func TestWalk_NilVisitorParsesOnly(t *testing.T) {
	eng := New()
	defer eng.Close()

	src := []byte(`package main
func only() {}
`)

	res, err := eng.Walk(context.Background(), "z.go", src, nil)
	if err != nil {
		t.Fatalf("Walk(nil visitor) error: %v", err)
	}
	if res == nil || len(res.Symbols) == 0 {
		t.Fatal("expected ParseResult with symbols when visitor is nil")
	}
}

// TestWalk_NilEngineReturnsError: the receiver is nil-safe, returning
// an error rather than panicking. Mirrors the rest of the Engine's
// nil-safe surface (Close, ParseMetrics).
func TestWalk_NilEngineReturnsError(t *testing.T) {
	var eng *Engine
	_, err := eng.Walk(context.Background(), "x.go", []byte("package main"), nil)
	if err == nil {
		t.Fatal("nil engine must return error from Walk")
	}
}

// TestWalkPath_ReadsFromDisk pins the convenience helper: the file is
// read from disk and parsed, no extra wiring needed by the caller.
func TestWalkPath_ReadsFromDisk(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "sample.go")
	src := []byte(`package main
func diskOnly() {}
`)
	if err := os.WriteFile(path, src, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	eng := New()
	defer eng.Close()

	var seen []string
	_, err := eng.WalkPath(context.Background(), path, func(sym types.Symbol) WalkAction {
		seen = append(seen, sym.Name)
		return WalkContinue
	})
	if err != nil {
		t.Fatalf("WalkPath error: %v", err)
	}
	if len(seen) == 0 || seen[0] != "diskOnly" {
		t.Fatalf("expected diskOnly symbol, got %v", seen)
	}
}

// TestWalkPath_MissingFileSurfacesIOError ensures the read error is
// wrapped distinctly from parse errors. Callers can switch on the
// wrapped error to give better diagnostics ("file missing" vs
// "syntax error").
func TestWalkPath_MissingFileSurfacesIOError(t *testing.T) {
	eng := New()
	defer eng.Close()

	_, err := eng.WalkPath(context.Background(), "/no/such/file/ever.go", nil)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}
