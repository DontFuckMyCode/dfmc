package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

func TestAnalyzeWithOptions(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("HOME", tmp)
	src := `package main
const apiKey = "sk-abcdefghijklmnopqrstuvwxyz1234567890"
func unusedThing() { if true { } }
func usedThing() { unusedThing() }
`
	if err := os.WriteFile(filepath.Join(tmp, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	cfg := config.DefaultConfig()
	eng, err := New(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.Init(context.Background()); err != nil {
		t.Fatalf("init engine: %v", err)
	}
	t.Cleanup(func() { _ = eng.Shutdown() })
	eng.ProjectRoot = tmp

	report, err := eng.AnalyzeWithOptions(context.Background(), AnalyzeOptions{
		Full: true,
	})
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if report.Files == 0 {
		t.Fatal("expected scanned files")
	}
	if report.Security == nil {
		t.Fatal("expected security report")
	}
	if report.Complexity == nil {
		t.Fatal("expected complexity report")
	}
}
