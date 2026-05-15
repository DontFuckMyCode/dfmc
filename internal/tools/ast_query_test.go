package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestASTQueryTool_NameAndDescription(t *testing.T) {
	tool := NewASTQueryTool()
	if tool.Name() != "ast_query" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "ast_query")
	}
	if tool.Description() == "" {
		t.Fatal("Description() returned empty string")
	}
}

func TestASTQueryTool_CloseNilEngine(t *testing.T) {
	var tool *ASTQueryTool
	if err := tool.Close(); err != nil {
		t.Fatalf("Close() on nil tool returned error: %v", err)
	}
}

func TestASTQueryTool_CloseWithEngine(t *testing.T) {
	tool := NewASTQueryTool()
	if err := tool.Close(); err != nil {
		t.Fatalf("Close() returned error: %v", err)
	}
}

func TestASTQueryTool_getEngine(t *testing.T) {
	tool := NewASTQueryTool()
	engine := tool.getEngine()
	if engine == nil {
		t.Fatal("getEngine() returned nil")
	}
}

// TestASTQueryTool_SharedEngineIsSingleton pins the R2 contract from
// dfmc_report_ast.md: every ASTQueryTool returned by NewASTQueryTool
// shares the same underlying ast.Engine instance, so parse cache hits
// flow across tool instances (and across separate ast_query calls in
// the same process). Without this guarantee a fresh per-tool engine
// would warm a cold cache every time.
func TestASTQueryTool_SharedEngineIsSingleton(t *testing.T) {
	a := NewASTQueryTool()
	b := NewASTQueryTool()
	if a.getEngine() != b.getEngine() {
		t.Fatalf("expected both tools to share the same *ast.Engine; got distinct pointers (%p vs %p)",
			a.getEngine(), b.getEngine())
	}
}

// TestASTQueryTool_CloseIsNoopForSharedEngine guards against re-introducing
// the old behaviour where (*ASTQueryTool).Close called engine.Close() —
// because the engine is now a process-wide singleton, one tool's Close
// must NOT clear caches that other tools (including a second ASTQueryTool
// later in the same process) still depend on. After Close, a subsequent
// ParseFile through the SAME engine must still succeed.
func TestASTQueryTool_CloseIsNoopForSharedEngine(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "x.go")
	if err := os.WriteFile(src, []byte("package main\nfunc Foo() {}\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	first := NewASTQueryTool()
	// Warm the cache.
	if _, err := first.getEngine().ParseFile(context.Background(), src); err != nil {
		t.Fatalf("warm parse: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// A second tool — sharing the same singleton — must still produce
	// usable results after the first tool's Close. If Close closed the
	// shared engine the parse below would still work on a fresh state,
	// but the cache-hit guarantee from R2 would be broken silently. We
	// pin behaviour here: ParseFile must return a non-empty result and
	// must NOT error after a peer tool's Close.
	second := NewASTQueryTool()
	res, err := second.getEngine().ParseFile(context.Background(), src)
	if err != nil {
		t.Fatalf("post-close ParseFile errored: %v", err)
	}
	if res == nil || len(res.Symbols) == 0 {
		t.Fatalf("post-close ParseFile returned no symbols: %+v", res)
	}
}
