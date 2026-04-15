package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

func TestStatusIncludesASTBackend(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

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
	if st.ProviderProfile.Name != st.Provider {
		t.Fatalf("expected provider profile name %q, got %#v", st.Provider, st.ProviderProfile)
	}
	if st.ProviderProfile.Model == "" {
		t.Fatalf("expected provider profile model to be populated: %#v", st.ProviderProfile)
	}
	if st.ProviderProfile.Protocol == "" {
		t.Fatalf("expected provider profile protocol to be populated: %#v", st.ProviderProfile)
	}
	if st.ProviderProfile.MaxContext == 0 || st.ProviderProfile.MaxTokens == 0 {
		t.Fatalf("expected provider profile limits to be populated: %#v", st.ProviderProfile)
	}
	if st.ModelsDevCache.Path == "" {
		t.Fatal("expected models.dev cache path to be populated")
	}
}

func TestStatusIncludesModelsDevCacheMetadata(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	catalog := config.ModelsDevCatalog{
		"anthropic": {
			ID:   "anthropic",
			Name: "Anthropic",
			Models: map[string]config.ModelsDevModel{
				"claude-sonnet-4-6": {ID: "claude-sonnet-4-6", Name: "Claude Sonnet 4.6"},
			},
		},
	}
	if err := config.SaveModelsDevCatalog(config.ModelsDevCachePath(), catalog); err != nil {
		t.Fatalf("save models.dev cache: %v", err)
	}

	cfg, err := config.LoadWithOptions(config.LoadOptions{})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	eng, err := New(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.Init(context.Background()); err != nil {
		t.Fatalf("init engine: %v", err)
	}
	t.Cleanup(func() { eng.Shutdown() })

	st := eng.Status()
	if !st.ModelsDevCache.Exists {
		t.Fatalf("expected models.dev cache to exist: %#v", st.ModelsDevCache)
	}
	if st.ModelsDevCache.Path != config.ModelsDevCachePath() {
		t.Fatalf("expected models.dev cache path %q, got %#v", config.ModelsDevCachePath(), st.ModelsDevCache)
	}
	if st.ModelsDevCache.SizeBytes == 0 {
		t.Fatalf("expected models.dev cache size to be populated: %#v", st.ModelsDevCache)
	}
	if st.ModelsDevCache.UpdatedAt.IsZero() {
		t.Fatalf("expected models.dev cache timestamp to be populated: %#v", st.ModelsDevCache)
	}
}

func TestStatusIncludesLastContextInReport(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	project := t.TempDir()
	mainPath := filepath.Join(project, "main.go")
	authPath := filepath.Join(project, "internal", "auth", "service.go")
	if err := os.MkdirAll(filepath.Dir(authPath), 0o755); err != nil {
		t.Fatalf("mkdir auth dir: %v", err)
	}
	if err := os.WriteFile(mainPath, []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	if err := os.WriteFile(authPath, []byte("package auth\n\nfunc VerifyToken(token string) bool { return token != \"\" }\n"), 0o644); err != nil {
		t.Fatalf("write service.go: %v", err)
	}

	cfg := config.DefaultConfig()
	eng, err := New(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.Init(context.Background()); err != nil {
		t.Fatalf("init engine: %v", err)
	}
	t.Cleanup(func() { eng.Shutdown() })

	eng.ProjectRoot = project
	if err := eng.CodeMap.BuildFromFiles(context.Background(), []string{mainPath, authPath}); err != nil {
		t.Fatalf("seed codemap build: %v", err)
	}

	chunks := eng.buildContextChunks("review [[file:internal/auth/service.go]] token verification")
	if len(chunks) == 0 {
		t.Fatal("expected context chunks from buildContextChunks")
	}

	st := eng.Status()
	if st.ContextIn == nil {
		t.Fatal("expected context_in report in status")
	}
	if st.ContextIn.FileCount == 0 || st.ContextIn.TokenCount == 0 {
		t.Fatalf("expected context_in files/tokens, got %#v", st.ContextIn)
	}
	if st.ContextIn.ExplicitFileMentions == 0 {
		t.Fatalf("expected explicit file mention count in context_in, got %#v", st.ContextIn)
	}
	if len(st.ContextIn.Reasons) == 0 {
		t.Fatalf("expected context_in reasons, got %#v", st.ContextIn)
	}
	if len(st.ContextIn.Files) == 0 {
		t.Fatalf("expected context_in file list, got %#v", st.ContextIn)
	}
}
