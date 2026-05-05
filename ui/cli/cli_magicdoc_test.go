package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunMagicDocUpdateAndShow(t *testing.T) {
	eng := newCLITestEngine(t)
	project := t.TempDir()
	eng.ProjectRoot = project

	src := filepath.Join(project, "main.go")
	if err := os.WriteFile(src, []byte("package main\nfunc main(){}\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	target := filepath.Join(project, ".dfmc", "magic", "MAGIC_DOC.md")
	if code := runMagicDoc(context.Background(), eng, []string{"update", "--path", target, "--title", "Test Brief"}, true); code != 0 {
		t.Fatalf("magicdoc update exit=%d", code)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read magicdoc: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "# MAGIC DOC: Test Brief") {
		t.Fatalf("expected title in magicdoc, got: %s", text)
	}
	if !strings.Contains(text, "## Current State") {
		t.Fatalf("expected current state section, got: %s", text)
	}

	if code := runMagicDoc(context.Background(), eng, []string{"show", "--path", target}, true); code != 0 {
		t.Fatalf("magicdoc show exit=%d", code)
	}
}

func TestRunMagicDocUnknownAction(t *testing.T) {
	eng := newCLITestEngine(t)
	if code := runMagicDoc(context.Background(), eng, []string{"unknown"}, true); code != 2 {
		t.Fatalf("expected exit=2 for unknown action, got %d", code)
	}
}
