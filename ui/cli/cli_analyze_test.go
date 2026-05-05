package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRunAnalyzeWithMagicDoc(t *testing.T) {
	eng := newCLITestEngine(t)
	root := t.TempDir()
	eng.ProjectRoot = root

	src := filepath.Join(root, "main.go")
	if err := os.WriteFile(src, []byte("package main\nfunc main(){}\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	if code := runAnalyze(context.Background(), eng, []string{"--json", "--magicdoc"}, false); code != 0 {
		t.Fatalf("runAnalyze exit=%d", code)
	}

	target := filepath.Join(root, ".dfmc", "magic", "MAGIC_DOC.md")
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read magicdoc: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty magicdoc content")
	}
}
