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
		path  string
		want  []string
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
