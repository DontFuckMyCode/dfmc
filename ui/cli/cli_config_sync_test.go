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
