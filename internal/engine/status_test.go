package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

func TestStatusIncludesASTBackend(t *testing.T) {
	cfg := config.DefaultConfig()
	eng, err := New(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.Init(context.Background()); err != nil {
		t.Fatalf("init engine: %v", err)
	}
	t.Cleanup(func() { eng.Shutdown() })

	if _, err := eng.AST.ParseContent(context.Background(), "sample.go", []byte("package sample\n\nfunc Hello() {}\n")); err != nil {
		t.Fatalf("seed ast parse: %v", err)
	}
	tmp := t.TempDir()
	path := filepath.Join(tmp, "sample.go")
	if err := os.WriteFile(path, []byte("package sample\n\nfunc Hello() {}\n"), 0o644); err != nil {
		t.Fatalf("write sample: %v", err)
	}
	if err := eng.CodeMap.BuildFromFiles(context.Background(), []string{path}); err != nil {
		t.Fatalf("seed codemap build: %v", err)
	}

	st := eng.Status()
	if st.ASTBackend == "" {
		t.Fatal("expected ast backend to be populated")
	}
	if st.ASTReason == "" {
		t.Fatal("expected ast backend reason to be populated")
	}
	if len(st.ASTLanguages) == 0 {
		t.Fatal("expected ast language capability matrix to be populated")
	}
	expected := map[string]bool{
		"go":         false,
		"javascript": false,
		"typescript": false,
		"python":     false,
	}
	for _, item := range st.ASTLanguages {
		if _, ok := expected[item.Language]; ok {
			expected[item.Language] = true
		}
		if item.Active == "" {
			t.Fatalf("expected active backend for language %q", item.Language)
		}
	}
	for lang, seen := range expected {
		if !seen {
			t.Fatalf("expected ast language %q in status matrix: %#v", lang, st.ASTLanguages)
		}
	}
	if st.ASTMetrics.Requests == 0 || st.ASTMetrics.Parsed == 0 {
		t.Fatalf("expected ast metrics to capture parse activity: %#v", st.ASTMetrics)
	}
	if st.ASTMetrics.LastLanguage != "go" {
		t.Fatalf("expected last ast language go, got %q", st.ASTMetrics.LastLanguage)
	}
	if st.CodeMap.Builds == 0 || st.CodeMap.FilesProcessed == 0 {
		t.Fatalf("expected codemap metrics to capture build activity: %#v", st.CodeMap)
	}
	if st.CodeMap.RecentBuilds == 0 {
		t.Fatalf("expected codemap recent trend window to be populated: %#v", st.CodeMap)
	}
	if st.CodeMap.RecentLanguages["go"] == 0 {
		t.Fatalf("expected codemap recent language trend for go: %#v", st.CodeMap.RecentLanguages)
	}
}
