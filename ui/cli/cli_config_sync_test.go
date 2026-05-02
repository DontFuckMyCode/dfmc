package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

func TestRunConfigSyncModels(t *testing.T) {
	eng := newCLITestEngine(t)
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, ".dfmc"), 0o755); err != nil {
		t.Fatalf("mkdir .dfmc: %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, "go.mod"), []byte("module example.com/test\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(project); err != nil {
		t.Fatalf("chdir project: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWD) })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
  "anthropic": {
    "id": "anthropic",
    "npm": "@ai-sdk/anthropic",
    "models": {
      "claude-sonnet-4-6": {
        "id": "claude-sonnet-4-6",
        "tool_call": true,
        "modalities": {"input":["text"],"output":["text"]},
        "limit": {"context": 1000000, "output": 64000}
      }
    }
  },
  "zai": {
    "id": "zai",
    "npm": "@ai-sdk/openai-compatible",
    "api": "https://api.z.ai/api/paas/v4",
    "models": {
      "glm-5.1": {
        "id": "glm-5.1",
        "tool_call": true,
        "modalities": {"input":["text"],"output":["text"]},
        "limit": {"context": 200000, "output": 131072}
      }
    }
  }
}`))
	}))
	defer srv.Close()

	code := runConfig(context.Background(), eng, []string{"sync-models", "--url", srv.URL}, true)
	if code != 0 {
		t.Fatalf("runConfig sync-models exit=%d", code)
	}

	cfg, err := config.LoadWithOptions(config.LoadOptions{CWD: project})
	if err != nil {
		t.Fatalf("LoadWithOptions: %v", err)
	}
	if got := cfg.Providers.Profiles["anthropic"].Model; got != "claude-sonnet-4-6" {
		t.Fatalf("expected anthropic model synced, got %q", got)
	}
	if got := cfg.Providers.Profiles["zai"].Protocol; got != "openai-compatible" {
		t.Fatalf("expected zai protocol synced, got %q", got)
	}
	if got := cfg.Providers.Profiles["zai"].MaxContext; got != 200000 {
		t.Fatalf("expected zai max context 200000, got %d", got)
	}
}

func TestIsSensitivePath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"", false},
		{"providers.profiles.anthropic.api_key", true},
		{"providers.profiles.openai.apiKey", true},
		{"providers.profiles.github.secret", true},
		{"providers.profiles.aws.secret_key", true},
		{"providers.profiles.aws.client_secret", true},
		{"auth.password", true},
		{"ssh.passphrase", true},
		{"github.token", true},
		{"github.oauth_token", true},
		{"providers.profiles.anthropic.model", false},
		{"providers.profiles.openai.base_url", false},
		{"general.project_root", false},
		{"context_budget", false},
	}
	for _, c := range cases {
		got := isSensitivePath(c.path)
		if got != c.want {
			t.Errorf("isSensitivePath(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestSanitizeConfigValue(t *testing.T) {
	got := sanitizeConfigValue("secret123", "api_key", false)
	if got != "secret123" {
		t.Errorf("disabled: got %v want secret123", got)
	}

	got = sanitizeConfigValue("secret123", "api_key", true)
	if got != "***REDACTED***" {
		t.Errorf("api_key: got %v want ***REDACTED***", got)
	}

	got = sanitizeConfigValue("somevalue", "model", true)
	if got != "somevalue" {
		t.Errorf("model: got %v want somevalue", got)
	}

	nested := map[string]any{
		"api_key": "secret",
		"model":   "claude-sonnet",
	}
	gotMap := sanitizeConfigValue(nested, "providers.profiles.test", true).(map[string]any)
	if gotMap["api_key"] != "***REDACTED***" {
		t.Errorf("nested api_key: got %v want ***REDACTED***", gotMap["api_key"])
	}
	if gotMap["model"] != "claude-sonnet" {
		t.Errorf("nested model: got %v want claude-sonnet", gotMap["model"])
	}
}

func TestConfigToMap(t *testing.T) {
	cfg := &config.Config{
		Version: 1,
	}
	m, err := configToMap(cfg)
	if err != nil {
		t.Fatalf("configToMap: %v", err)
	}
	if m == nil {
		t.Fatal("expected non-nil map")
	}
}

func TestSplitConfigPath(t *testing.T) {
	cases := []struct {
		path string
		want []string
	}{
		{"", nil},
		{"a", []string{"a"}},
		{"a.b", []string{"a", "b"}},
		{"a.b.c", []string{"a", "b", "c"}},
		{"  a . b  ", []string{"a", "b"}},
		{"a..b", []string{"a", "b"}},
		{".a.", []string{"a"}},
	}
	for _, c := range cases {
		got := splitConfigPath(c.path)
		if len(got) != len(c.want) {
			t.Errorf("splitConfigPath(%q): len=%d want %d", c.path, len(got), len(c.want))
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("splitConfigPath(%q)[%d] = %q want %q", c.path, i, got[i], c.want[i])
			}
		}
	}
}

func TestGetConfigPath(t *testing.T) {
	root := map[string]any{
		"providers": map[string]any{
			"primary": "anthropic",
			"profiles": map[string]any{
				"anthropic": map[string]any{
					"model": "claude-sonnet-4-6",
					"api_key": "secret-key",
				},
			},
		},
		"top-level": "value",
		"list":      []any{10, 20, 30},
	}

	// Root access
	got, ok := getConfigPath(root, "")
	if !ok || got == nil {
		t.Errorf("root access: got %v ok=%v", got, ok)
	}

	// Dot-path access
	got, ok = getConfigPath(root, "providers.primary")
	if !ok || got != "anthropic" {
		t.Errorf("providers.primary: got %v ok=%v", got, ok)
	}

	got, ok = getConfigPath(root, "providers.profiles.anthropic.model")
	if !ok || got != "claude-sonnet-4-6" {
		t.Errorf("providers.profiles.anthropic.model: got %v ok=%v", got, ok)
	}

	// List index
	got, ok = getConfigPath(root, "list.1")
	if !ok || got != 20 {
		t.Errorf("list.1: got %v ok=%v", got, ok)
	}

	// Missing key
	_, ok = getConfigPath(root, "providers.missing")
	if ok {
		t.Error("expected ok=false for missing key")
	}

	// Missing index
	_, ok = getConfigPath(root, "list.99")
	if ok {
		t.Error("expected ok=false for missing index")
	}

	// Non-map path segment
	_, ok = getConfigPath(root, "top-level.nested")
	if ok {
		t.Error("expected ok=false for non-map intermediate")
	}
}

func TestSetConfigPath(t *testing.T) {
	// Shallow set
	root := map[string]any{}
	if err := setConfigPath(root, "model", "sonnet"); err != nil {
		t.Fatalf("set model: %v", err)
	}
	if root["model"] != "sonnet" {
		t.Errorf("model: got %v", root["model"])
	}

	// Nested set creates intermediate maps
	root2 := map[string]any{}
	if err := setConfigPath(root2, "providers.profiles.anthropic.model", "sonnet"); err != nil {
		t.Fatalf("set nested: %v", err)
	}
	nested := root2["providers"].(map[string]any)["profiles"].(map[string]any)["anthropic"].(map[string]any)
	if nested["model"] != "sonnet" {
		t.Errorf("nested model: got %v", nested["model"])
	}

	// Overwrite existing
	root3 := map[string]any{"model": "old"}
	if err := setConfigPath(root3, "model", "new"); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	if root3["model"] != "new" {
		t.Errorf("overwrite: got %v", root3["model"])
	}

	// Empty path is error
	if err := setConfigPath(root, "", "value"); err == nil {
		t.Error("expected error for empty path")
	}
}

func TestParseConfigValue(t *testing.T) {
	tests := []struct {
		input string
		want  any
	}{
		{"", ""},
		{"  ", ""},
		{"42", 42},
		{"3.14", 3.14},
		{"true", true},
		{"false", false},
		{"\"hello\"", "hello"},
		{"[1,2,3]", []any{float64(1), float64(2), float64(3)}},
		{"plain string", "plain string"},
	}
	for _, tc := range tests {
		got, err := parseConfigValue(tc.input)
		if err != nil {
			t.Errorf("parseConfigValue(%q): error %v", tc.input, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseConfigValue(%q) = %v (%T), want %v (%T)", tc.input, got, got, tc.want, tc.want)
		}
	}
}

func TestCloneConfig(t *testing.T) {
	cfg := &config.Config{
		Version: 1,
	}
	cloned, err := cloneConfig(cfg)
	if err != nil {
		t.Fatalf("cloneConfig: %v", err)
	}
	if cloned.Version != 1 {
		t.Errorf("Version: got %d want 1", cloned.Version)
	}
}

func TestDiffProviderProfiles(t *testing.T) {
	before := map[string]config.ModelConfig{
		"anthropic": {Model: "old-model", Protocol: "anthropic"},
	}
	after := map[string]config.ModelConfig{
		"anthropic": {Model: "new-model", Protocol: "anthropic"},
		"deepseek":  {Model: "deepseek-chat", Protocol: "openai-compatible"},
	}
	changes := diffProviderProfiles(before, after)
	if len(changes) != 2 {
		t.Errorf("expected 2 changes, got %d: %v", len(changes), changes)
	}
}

func TestRunConfig_List(t *testing.T) {
	eng := newCLITestEngine(t)
	code := runConfig(context.Background(), eng, []string{"list"}, false)
	if code != 0 {
		t.Fatalf("runConfig list exit=%d", code)
	}
}

func TestRunConfig_ListJSON(t *testing.T) {
	eng := newCLITestEngine(t)
	code := runConfig(context.Background(), eng, []string{"list"}, true)
	if code != 0 {
		t.Fatalf("runConfig list --json exit=%d", code)
	}
}

func TestRunConfig_ListRaw(t *testing.T) {
	eng := newCLITestEngine(t)
	code := runConfig(context.Background(), eng, []string{"list", "--raw"}, false)
	if code != 0 {
		t.Fatalf("runConfig list --raw exit=%d", code)
	}
}

func TestRunConfig_GetTopLevel(t *testing.T) {
	eng := newCLITestEngine(t)
	code := runConfig(context.Background(), eng, []string{"get", "version"}, false)
	if code != 0 {
		t.Fatalf("runConfig get version exit=%d", code)
	}
}

func TestRunConfig_GetNested(t *testing.T) {
	eng := newCLITestEngine(t)
	code := runConfig(context.Background(), eng, []string{"get", "providers.primary"}, false)
	if code != 0 {
		t.Fatalf("runConfig get providers.primary exit=%d", code)
	}
}

func TestRunConfig_GetJSON(t *testing.T) {
	eng := newCLITestEngine(t)
	code := runConfig(context.Background(), eng, []string{"get", "version"}, true)
	if code != 0 {
		t.Fatalf("runConfig get --json exit=%d", code)
	}
}

func TestRunConfig_GetRaw(t *testing.T) {
	eng := newCLITestEngine(t)
	code := runConfig(context.Background(), eng, []string{"get", "version", "--raw"}, false)
	if code != 0 {
		t.Fatalf("runConfig get --raw exit=%d", code)
	}
}

func TestRunConfig_GetNotFound(t *testing.T) {
	eng := newCLITestEngine(t)
	code := runConfig(context.Background(), eng, []string{"get", "nonexistent.path"}, false)
	if code != 1 {
		t.Fatalf("runConfig get missing path: got exit=%d want 1", code)
	}
}

func TestRunConfig_GetNoPath(t *testing.T) {
	eng := newCLITestEngine(t)
	code := runConfig(context.Background(), eng, []string{"get"}, false)
	if code != 2 {
		t.Fatalf("runConfig get no path: got exit=%d want 2", code)
	}
}

func TestRunConfig_EditUnknown(t *testing.T) {
	eng := newCLITestEngine(t)
	code := runConfig(context.Background(), eng, []string{"edit", "--global"}, false)
	// edit will try to open an editor - we just verify it doesn't panic
	// and exits non-zero (either 1 or 2 depending on whether editor fails)
	if code == 0 {
		t.Error("edit with no real editor should not exit 0")
	}
}
