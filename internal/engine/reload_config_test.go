package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/tools"
)

func TestReloadConfig_RewiresToolReasoningPublisher(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	cfg := config.DefaultConfig()
	cfg.Agent.ToolReasoning = "auto"
	eng, err := New(cfg)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	if err := eng.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { eng.Shutdown() })

	if err := eng.ReloadConfig(home); err != nil {
		t.Fatalf("ReloadConfig: %v", err)
	}

	ch := eng.EventBus.Subscribe("tool:reasoning")
	defer eng.EventBus.Unsubscribe("tool:reasoning", ch)

	_, err = eng.CallTool(context.Background(), "list_dir", map[string]any{
		"path":            ".",
		tools.ReasonField: "verify reasoning publisher survived reload",
	})
	if err != nil {
		t.Fatalf("CallTool after reload: %v", err)
	}

	select {
	case ev := <-ch:
		payload, ok := ev.Payload.(map[string]any)
		if !ok {
			t.Fatalf("payload is not map[string]any: %T", ev.Payload)
		}
		if payload["tool"] != "list_dir" {
			t.Fatalf("unexpected tool field after reload: %#v", payload["tool"])
		}
	case <-time.After(time.Second):
		t.Fatal("did not receive tool:reasoning event after ReloadConfig")
	}
}

func TestMaybeAutoReloadProjectConfig_ReloadsModifiedProjectConfig(t *testing.T) {
	project := t.TempDir()
	configDir := filepath.Join(project, ".dfmc")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	configPath := filepath.Join(configDir, "config.yaml")
	initial := strings.TrimSpace(`
version: 1
providers:
  primary: generic
  profiles:
    generic:
      base_url: http://localhost:11434/v1
      model: qwen-initial
      protocol: openai-compatible
`)
	if err := os.WriteFile(configPath, []byte(initial+"\n"), 0o644); err != nil {
		t.Fatalf("write initial config: %v", err)
	}

	cfg, err := config.LoadWithOptions(config.LoadOptions{CWD: project})
	if err != nil {
		t.Fatalf("LoadWithOptions: %v", err)
	}
	eng, err := New(cfg)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	eng.ProjectRoot = project
	eng.refreshProjectConfigSnapshot(eng.projectConfigPath())

	updated := strings.TrimSpace(`
version: 1
providers:
  primary: generic
  profiles:
    generic:
      base_url: http://localhost:11434/v1
      model: qwen-updated
      protocol: openai-compatible
`)
	if err := os.WriteFile(configPath, []byte(updated+"\n"), 0o644); err != nil {
		t.Fatalf("write updated config: %v", err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(configPath, future, future); err != nil {
		t.Fatalf("chtimes updated config: %v", err)
	}

	ch := eng.EventBus.Subscribe("config:reload:auto")
	defer eng.EventBus.Unsubscribe("config:reload:auto", ch)

	if err := eng.maybeAutoReloadProjectConfig(); err != nil {
		t.Fatalf("maybeAutoReloadProjectConfig: %v", err)
	}
	if got := eng.Status().Model; got != "qwen-updated" {
		t.Fatalf("reloaded model = %q, want qwen-updated", got)
	}

	select {
	case ev := <-ch:
		payload, ok := ev.Payload.(map[string]any)
		if !ok {
			t.Fatalf("payload is not map[string]any: %T", ev.Payload)
		}
		if payload["path"] != configPath {
			t.Fatalf("config reload path = %#v, want %q", payload["path"], configPath)
		}
	case <-time.After(time.Second):
		t.Fatal("did not receive config:reload:auto event")
	}
}
