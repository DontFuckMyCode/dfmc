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

func TestRunStatusJSONIncludesApprovalGateAndHooks(t *testing.T) {
	eng := newCLITestEngine(t)
	eng.Config.Tools.RequireApproval = []string{"write_file", "run_command", "edit_file"}

	out := captureStdout(t, func() {
		if code := runStatus(eng, "dev", []string{}, true); code != 0 {
			t.Fatalf("runStatus json exit=%d", code)
		}
	})

	var payload struct {
		ApprovalGate struct {
			Active   bool     `json:"active"`
			Wildcard bool     `json:"wildcard"`
			Count    int      `json:"count"`
			Tools    []string `json:"tools"`
		} `json:"approval_gate"`
		Hooks struct {
			Total    int            `json:"total"`
			PerEvent map[string]int `json:"per_event"`
		} `json:"hooks"`
		RecentDenials int `json:"recent_denials"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal status json: %v\n%s", err, out)
	}
	if !payload.ApprovalGate.Active {
		t.Fatalf("gate should be active with 3 tools configured: %#v", payload.ApprovalGate)
	}
	if payload.ApprovalGate.Count != 3 {
		t.Fatalf("gate count should be 3, got %d", payload.ApprovalGate.Count)
	}
	if payload.ApprovalGate.Wildcard {
		t.Fatalf("wildcard should be false for explicit list")
	}
	wantTools := map[string]bool{"write_file": true, "run_command": true, "edit_file": true}
	for _, tool := range payload.ApprovalGate.Tools {
		if !wantTools[tool] {
			t.Fatalf("unexpected gated tool %q in payload", tool)
		}
	}
	if payload.Hooks.Total < 0 {
		t.Fatalf("hooks total must never be negative: %d", payload.Hooks.Total)
	}
	if payload.RecentDenials != 0 {
		t.Fatalf("fresh engine should have zero recent denials, got %d", payload.RecentDenials)
	}
}

func TestRunStatusJSONWildcardApprovalGate(t *testing.T) {
	eng := newCLITestEngine(t)
	eng.Config.Tools.RequireApproval = []string{"*"}

	out := captureStdout(t, func() {
		if code := runStatus(eng, "dev", []string{}, true); code != 0 {
			t.Fatalf("runStatus json exit=%d", code)
		}
	})

	var payload struct {
		ApprovalGate struct {
			Active   bool `json:"active"`
			Wildcard bool `json:"wildcard"`
			Count    int  `json:"count"`
		} `json:"approval_gate"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal status json: %v\n%s", err, out)
	}
	if !payload.ApprovalGate.Wildcard {
		t.Fatalf("wildcard must be true when RequireApproval is [\"*\"], got %#v", payload.ApprovalGate)
	}
	if !payload.ApprovalGate.Active {
		t.Fatalf("wildcard gate must be active")
	}
}

func TestRunStatusTextMentionsGateAndHooks(t *testing.T) {
	eng := newCLITestEngine(t)
	eng.Config.Tools.RequireApproval = []string{"write_file", "run_command"}

	out := captureStdout(t, func() {
		if code := runStatus(eng, "dev", []string{}, false); code != 0 {
			t.Fatalf("runStatus text exit=%d", code)
		}
	})

	if !strings.Contains(out, "approval gate:") {
		t.Fatalf("status text should mention approval gate, got:\n%s", out)
	}
	if !strings.Contains(out, "write_file") || !strings.Contains(out, "run_command") {
		t.Fatalf("status text should list gated tools, got:\n%s", out)
	}
}

func TestFormatApprovalGateSummary(t *testing.T) {
	cases := []struct {
		name string
		in   approvalGateSummary
		want string
	}{
		{"off", approvalGateSummary{}, "off"},
		{"wildcard", approvalGateSummary{Active: true, Wildcard: true, Count: -1}, "on (*)"},
		{"small list", approvalGateSummary{Active: true, Count: 2, Tools: []string{"edit_file", "write_file"}}, "on (edit_file, write_file)"},
		{"long list", approvalGateSummary{Active: true, Count: 6, Tools: []string{"a", "b", "c", "d", "e", "f"}}, "on (6: a, b, c, d, …)"},
	}
	for _, c := range cases {
		if got := formatApprovalGateSummary(c.in); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestFormatHooksSummary(t *testing.T) {
	if got := formatHooksSummary(hooksSummary{PerEvent: map[string]int{}}); got != "none registered" {
		t.Fatalf("empty summary should read 'none registered', got %q", got)
	}
	s := hooksSummary{Total: 3, PerEvent: map[string]int{"pre_tool": 2, "post_tool": 1}}
	got := formatHooksSummary(s)
	if !strings.Contains(got, "3 (") || !strings.Contains(got, "pre_tool=2") || !strings.Contains(got, "post_tool=1") {
		t.Fatalf("unexpected hooks summary %q", got)
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
