package config

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestMergeProviderProfilesFromModelsDev(t *testing.T) {
	current := map[string]ModelConfig{
		"anthropic": {APIKey: "k-test", Model: "glm-5.1", BaseURL: "https://proxy.example/v1"},
		"openai":    {Model: "gpt-5.4"},
		"generic":   {Model: "qwen-local", BaseURL: "http://localhost:11434/v1"},
	}
	catalog := ModelsDevCatalog{
		"anthropic": {
			ID:  "anthropic",
			NPM: "@ai-sdk/anthropic",
			API: "",
			Models: map[string]ModelsDevModel{
				"claude-sonnet-4-6": {
					ID:       "claude-sonnet-4-6",
					ToolCall: true,
					Modalities: ModelsDevModes{
						Input:  []string{"text"},
						Output: []string{"text"},
					},
					Limit: ModelsDevLimits{Context: 1000000, Output: 64000},
				},
			},
		},
		"openai": {
			ID:  "openai",
			NPM: "@ai-sdk/openai",
			Models: map[string]ModelsDevModel{
				"gpt-5.4": {
					ID:       "gpt-5.4",
					ToolCall: true,
					Modalities: ModelsDevModes{
						Input:  []string{"text"},
						Output: []string{"text"},
					},
					Limit: ModelsDevLimits{Context: 1050000, Output: 128000},
				},
			},
		},
	}

	merged := MergeProviderProfilesFromModelsDev(current, catalog, ModelsDevMergeOptions{})

	if got := merged["anthropic"].Model; got != "claude-sonnet-4-6" {
		t.Fatalf("expected anthropic model to be corrected, got %q", got)
	}
	if got := merged["anthropic"].APIKey; got != "k-test" {
		t.Fatalf("expected anthropic api key preserved, got %q", got)
	}
	if got := merged["anthropic"].BaseURL; got != "https://proxy.example/v1" {
		t.Fatalf("expected anthropic base_url to be preserved without rewrite, got %q", got)
	}
	if got := merged["anthropic"].MaxContext; got != 1000000 {
		t.Fatalf("expected anthropic max context from catalog, got %d", got)
	}
	if got := merged["openai"].Protocol; got != "openai" {
		t.Fatalf("expected openai protocol to be set, got %q", got)
	}
	if got := merged["generic"].Model; got != "qwen-local" {
		t.Fatalf("expected generic profile unchanged, got %q", got)
	}
}

func TestFetchAndSaveModelsDevCatalog(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
  "openai": {
    "id": "openai",
    "npm": "@ai-sdk/openai",
    "models": {
      "gpt-5.4": {
        "id": "gpt-5.4",
        "tool_call": true,
        "modalities": {"input":["text"],"output":["text"]},
        "limit": {"context": 1050000, "output": 128000}
      }
    }
  }
}`))
	}))
	defer srv.Close()

	catalog, err := FetchModelsDevCatalog(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("FetchModelsDevCatalog: %v", err)
	}
	if _, ok := catalog["openai"]; !ok {
		t.Fatalf("expected openai provider in catalog: %#v", catalog)
	}

	path := filepath.Join(t.TempDir(), "models.dev.json")
	if err := SaveModelsDevCatalog(path, catalog); err != nil {
		t.Fatalf("SaveModelsDevCatalog: %v", err)
	}
	reloaded, err := LoadModelsDevCatalog(path)
	if err != nil {
		t.Fatalf("LoadModelsDevCatalog: %v", err)
	}
	if got := reloaded["openai"].Models["gpt-5.4"].Limit.Context; got != 1050000 {
		t.Fatalf("expected persisted context limit, got %d", got)
	}
}

func TestLoadWithOptions_AppliesModelsDevCache(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	projectRoot := filepath.Join(tmp, "project")
	projectPath := filepath.Join(projectRoot, ".dfmc", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(projectPath), 0o755); err != nil {
		t.Fatalf("mkdir project config dir: %v", err)
	}
	projectYAML := []byte(`
version: 1
providers:
  profiles:
    anthropic:
      model: glm-5.1
`)
	if err := os.WriteFile(projectPath, projectYAML, 0o644); err != nil {
		t.Fatalf("write project config: %v", err)
	}
	catalog := ModelsDevCatalog{
		"anthropic": {
			ID:  "anthropic",
			NPM: "@ai-sdk/anthropic",
			Models: map[string]ModelsDevModel{
				"claude-sonnet-4-6": {
					ID:       "claude-sonnet-4-6",
					ToolCall: true,
					Modalities: ModelsDevModes{
						Input:  []string{"text"},
						Output: []string{"text"},
					},
					Limit: ModelsDevLimits{Context: 1000000, Output: 64000},
				},
			},
		},
	}
	if err := SaveModelsDevCatalog(ModelsDevCachePath(), catalog); err != nil {
		t.Fatalf("SaveModelsDevCatalog: %v", err)
	}

	cfg, err := LoadWithOptions(LoadOptions{
		GlobalPath:  filepath.Join(tmp, "missing-global.yaml"),
		ProjectPath: projectPath,
		CWD:         projectRoot,
	})
	if err != nil {
		t.Fatalf("LoadWithOptions: %v", err)
	}
	if got := cfg.Providers.Profiles["anthropic"].Model; got != "claude-sonnet-4-6" {
		t.Fatalf("expected anthropic model corrected from cache, got %q", got)
	}
	if got := cfg.Providers.Profiles["anthropic"].MaxContext; got != 1000000 {
		t.Fatalf("expected anthropic max context from cache, got %d", got)
	}
}
