package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRunContextBudgetAndRecent(t *testing.T) {
	eng := newCLITestEngine(t)

	if code := runContext(context.Background(), eng, []string{"budget", "--query", "security audit auth"}, true); code != 0 {
		t.Fatalf("context budget exit=%d", code)
	}
	if code := runContext(context.Background(), eng, []string{"budget", "--query", "security audit auth", "--runtime-max-context", "1000"}, true); code != 0 {
		t.Fatalf("context budget runtime override exit=%d", code)
	}

	if code := runContext(context.Background(), eng, []string{"recent"}, true); code != 0 {
		t.Fatalf("context recent exit=%d", code)
	}

	if code := runContext(context.Background(), eng, []string{"recommend", "--query", "debug [[file:internal/auth/service.go]]"}, true); code != 0 {
		t.Fatalf("context recommend exit=%d", code)
	}
	if code := runContext(context.Background(), eng, []string{"recommend", "--query", "debug [[file:internal/auth/service.go]]", "--runtime-max-context", "1000"}, true); code != 0 {
		t.Fatalf("context recommend runtime override exit=%d", code)
	}
}

func TestRunContextBrief(t *testing.T) {
	eng := newCLITestEngine(t)
	root := t.TempDir()
	eng.ProjectRoot = root

	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "BRIEF.md"), []byte("# MAGIC DOC: Brief\n\nContext brief smoke line.\n"), 0o644); err != nil {
		t.Fatalf("write brief: %v", err)
	}

	if code := runContext(context.Background(), eng, []string{"brief", "--path", "docs/BRIEF.md", "--max-words", "24"}, true); code != 0 {
		t.Fatalf("context brief exit=%d", code)
	}
}

func TestRunContextUsageForUnknownAction(t *testing.T) {
	eng := newCLITestEngine(t)
	if code := runContext(context.Background(), eng, []string{"unknown"}, true); code != 2 {
		t.Fatalf("expected exit=2 for unknown action, got %d", code)
	}
}
