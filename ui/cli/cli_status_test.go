package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunStatusJSON(t *testing.T) {
	eng := newCLITestEngine(t)
	if code := runStatus(eng, "dev", []string{"--query", "security audit auth middleware"}, true); code != 0 {
		t.Fatalf("runStatus json exit=%d", code)
	}
	if code := runStatus(eng, "dev", []string{
		"--query", "security audit auth middleware",
		"--runtime-tool-style", "function-calling",
		"--runtime-max-context", "1000",
	}, true); code != 0 {
		t.Fatalf("runStatus json runtime override exit=%d", code)
	}
}

func TestRunStatusText(t *testing.T) {
	eng := newCLITestEngine(t)
	if code := runStatus(eng, "dev", []string{}, false); code != 0 {
		t.Fatalf("runStatus text exit=%d", code)
	}
}

func TestFormatASTLanguageSummary(t *testing.T) {
	eng := newCLITestEngine(t)
	summary := formatASTLanguageSummary(eng.Status().ASTLanguages)
	if summary == "" {
		t.Fatal("expected ast language summary to be populated")
	}
	if want := "go="; !containsASTEntry(summary, want) {
		t.Fatalf("expected go entry in ast summary, got %q", summary)
	}
}

func containsASTEntry(summary, prefix string) bool {
	return strings.HasPrefix(summary, prefix) || strings.Contains(summary, ", "+prefix)
}

func TestFormatASTMetricsSummary(t *testing.T) {
	eng := newCLITestEngine(t)
	if _, err := eng.AST.ParseContent(t.Context(), "sample.go", []byte("package sample\n\nfunc Hello() {}\n")); err != nil {
		t.Fatalf("seed ast parse: %v", err)
	}

	summary := formatASTMetricsSummary(eng.Status().ASTMetrics)
	if summary == "" {
		t.Fatal("expected ast metrics summary to be populated")
	}
	if !strings.Contains(summary, "requests=") {
		t.Fatalf("expected requests count in ast metrics summary, got %q", summary)
	}
	if !strings.Contains(summary, "backend[") {
		t.Fatalf("expected backend distribution in ast metrics summary, got %q", summary)
	}
}

func TestFormatCodeMapMetricsSummary(t *testing.T) {
	eng := newCLITestEngine(t)
	tmp := t.TempDir()
	path := filepath.Join(tmp, "sample.go")
	if err := os.WriteFile(path, []byte("package sample\n\nfunc Hello() {}\n"), 0o644); err != nil {
		t.Fatalf("write sample: %v", err)
	}
	if err := eng.CodeMap.BuildFromFiles(t.Context(), []string{path}); err != nil {
		t.Fatalf("seed codemap build: %v", err)
	}

	summary := formatCodeMapMetricsSummary(eng.Status().CodeMap)
	if summary == "" {
		t.Fatal("expected codemap metrics summary to be populated")
	}
	if !strings.Contains(summary, "builds=") {
		t.Fatalf("expected builds count in codemap summary, got %q", summary)
	}
	if !strings.Contains(summary, "graph=") {
		t.Fatalf("expected graph counts in codemap summary, got %q", summary)
	}
	if !strings.Contains(summary, "recent_langs=go") {
		t.Fatalf("expected recent language summary in codemap summary, got %q", summary)
	}
	if !strings.Contains(summary, "recent_dirs=") {
		t.Fatalf("expected recent directory summary in codemap summary, got %q", summary)
	}
	if !strings.Contains(summary, "trend_langs=go") {
		t.Fatalf("expected trend language summary in codemap summary, got %q", summary)
	}
	if !strings.Contains(summary, "hot_dirs=") {
		t.Fatalf("expected hot directory summary in codemap summary, got %q", summary)
	}
}
