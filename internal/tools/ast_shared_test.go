package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestSharedASTEngine_AcrossTools pins the cross-tool sharing contract
// from dfmc_report_ast.md §R2: ast_query, codemap, and find_symbol all
// pull from the same process-wide ast.Engine, so a parse warmed by one
// tool produces a cache hit for the others on the same file. Without
// this guarantee a session that calls codemap → find_symbol on the same
// file pays the parse cost twice.
func TestSharedASTEngine_AcrossTools(t *testing.T) {
	q := NewASTQueryTool()
	c := NewCodemapTool()
	f := NewFindSymbolTool()

	if q.getEngine() != c.getEngine() {
		t.Fatalf("ast_query and codemap should share one engine; got %p vs %p",
			q.getEngine(), c.getEngine())
	}
	if c.getEngine() != f.getEngine() {
		t.Fatalf("codemap and find_symbol should share one engine; got %p vs %p",
			c.getEngine(), f.getEngine())
	}
}

// TestSharedASTEngine_CloseDoesNotEvictPeerCaches guards against
// re-introducing the old per-tool Close() that called engine.Close().
// With the shared singleton, one tool's Close MUST NOT clear parse
// caches that other tools still depend on. We warm the cache via
// codemap, then Close codemap, then verify ast_query gets a valid
// result against the same engine.
func TestSharedASTEngine_CloseDoesNotEvictPeerCaches(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "shared.go")
	if err := os.WriteFile(src, []byte("package main\nfunc SharedFn() {}\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cm := NewCodemapTool()
	if _, err := cm.getEngine().ParseFile(context.Background(), src); err != nil {
		t.Fatalf("warm via codemap: %v", err)
	}
	if err := cm.Close(); err != nil {
		t.Fatalf("Close on codemap: %v", err)
	}

	// A peer tool (ast_query) on the same singleton must still parse.
	q := NewASTQueryTool()
	res, err := q.getEngine().ParseFile(context.Background(), src)
	if err != nil {
		t.Fatalf("post-Close peer ParseFile: %v", err)
	}
	if res == nil || len(res.Symbols) == 0 {
		t.Fatalf("expected SharedFn in result, got %+v", res)
	}

	// And find_symbol — third member of the shared trio — also works.
	fs := NewFindSymbolTool()
	if fs.getEngine() != q.getEngine() {
		t.Fatalf("find_symbol drifted from singleton after peer Close: %p vs %p",
			fs.getEngine(), q.getEngine())
	}
}
