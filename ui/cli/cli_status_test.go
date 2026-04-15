package cli

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/engine"
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

func TestRunStatusJSONIncludesProviderProfileAndModelsDevCache(t *testing.T) {
	eng := newCLITestEngine(t)
	if err := config.SaveModelsDevCatalog(config.ModelsDevCachePath(), config.ModelsDevCatalog{
		"anthropic": {
			ID:   "anthropic",
			Name: "Anthropic",
			Models: map[string]config.ModelsDevModel{
				"claude-sonnet-4-6": {ID: "claude-sonnet-4-6", Name: "Claude Sonnet 4.6"},
			},
		},
	}); err != nil {
		t.Fatalf("save models.dev cache: %v", err)
	}

	out := captureStdout(t, func() {
		if code := runStatus(eng, "dev", []string{}, true); code != 0 {
			t.Fatalf("runStatus json exit=%d", code)
		}
	})

	var payload struct {
		ProviderProfile engine.ProviderProfileStatus `json:"provider_profile"`
		ModelsDevCache  engine.ModelsDevCacheStatus  `json:"models_dev_cache"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal status json: %v\n%s", err, out)
	}
	if payload.ProviderProfile.Protocol == "" || payload.ProviderProfile.MaxContext == 0 {
		t.Fatalf("expected provider profile in status payload: %#v", payload.ProviderProfile)
	}
	if !payload.ModelsDevCache.Exists || payload.ModelsDevCache.Path == "" {
		t.Fatalf("expected models.dev cache in status payload: %#v", payload.ModelsDevCache)
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

func TestFormatProviderProfileSummary(t *testing.T) {
	summary := formatProviderProfileSummary(engine.ProviderProfileStatus{
		Name:       "anthropic",
		Model:      "claude-sonnet-4-6",
		Protocol:   "anthropic",
		MaxContext: 1000000,
		MaxTokens:  64000,
		Configured: true,
	})
	if !strings.Contains(summary, "proto=anthropic") {
		t.Fatalf("expected protocol in provider profile summary, got %q", summary)
	}
	if !strings.Contains(summary, "ctx=1000000") || !strings.Contains(summary, "out=64000") {
		t.Fatalf("expected limits in provider profile summary, got %q", summary)
	}
	if !strings.Contains(summary, "configured=true") {
		t.Fatalf("expected configured flag in provider profile summary, got %q", summary)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stdout: %v", err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = old
	}()

	done := make(chan []byte, 1)
	go func() {
		data, _ := io.ReadAll(r)
		done <- data
	}()

	fn()

	_ = w.Close()
	data := <-done
	return string(data)
}
