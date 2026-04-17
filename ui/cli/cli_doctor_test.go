package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func TestRunDoctorJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
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

	cfg := config.DefaultConfig()
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	if err := eng.Init(context.Background()); err != nil {
		t.Fatalf("eng.Init: %v", err)
	}
	t.Cleanup(func() { eng.Shutdown() })

	out := captureStdout(t, func() {
		code := runDoctor(context.Background(), eng, []string{"--network=false"}, true)
		if code != 0 {
			t.Fatalf("expected doctor exit code 0, got %d", code)
		}
	})

	var payload struct {
		Checks []doctorCheck `json:"checks"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal doctor json: %v\n%s", err, out)
	}
	if !hasDoctorCheck(payload.Checks, "modelsdev.cache") {
		t.Fatalf("expected modelsdev.cache check in doctor payload: %#v", payload.Checks)
	}
	if !hasDoctorCheck(payload.Checks, "provider.anthropic.profile") {
		t.Fatalf("expected provider profile check in doctor payload: %#v", payload.Checks)
	}
	// Memory-tier health surfaces a silent-killer class of issue
	// (bbolt load failure → empty episodic/semantic recall). It must
	// always appear in the doctor payload so users can tell a degraded
	// startup from a healthy one without reading TUI-only banners.
	if !hasDoctorCheck(payload.Checks, "memory.tier") {
		t.Fatalf("expected memory.tier check in doctor payload: %#v", payload.Checks)
	}
}

func hasDoctorCheck(checks []doctorCheck, name string) bool {
	for _, check := range checks {
		if check.Name == name {
			return true
		}
	}
	return false
}

func TestProviderEndpointFromBaseURL(t *testing.T) {
	target, err := providerEndpoint("generic", config.ModelConfig{
		BaseURL: "http://localhost:11434/v1",
	})
	if err != nil {
		t.Fatalf("providerEndpoint: %v", err)
	}
	if target != "localhost:11434" {
		t.Fatalf("unexpected target: %s", target)
	}
}

func TestRunDoctorProvidersOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	cfg := config.DefaultConfig()
	cfg.Web.Auth = "token"
	cfg.Remote.Auth = "mtls"

	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	if err := eng.Init(context.Background()); err != nil {
		t.Fatalf("eng.Init: %v", err)
	}
	t.Cleanup(func() { eng.Shutdown() })

	code := runDoctor(context.Background(), eng, []string{"--providers-only"}, true)
	if code != 0 {
		t.Fatalf("expected doctor exit code 0, got %d", code)
	}
}

func TestRunDoctorFixRepairsProjectConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	project := t.TempDir()
	dfmcDir := filepath.Join(project, ".dfmc")
	if err := os.MkdirAll(dfmcDir, 0o755); err != nil {
		t.Fatalf("mkdir .dfmc: %v", err)
	}
	badCfg := "" +
		"version: 1\n" +
		"providers:\n" +
		"  primary: missing-provider\n" +
		"  profiles:\n" +
		"    generic:\n" +
		"      base_url: http://localhost:11434/v1\n" +
		"web:\n" +
		"  auth: invalid\n" +
		"remote:\n" +
		"  auth: invalid\n"
	if err := os.WriteFile(filepath.Join(dfmcDir, "config.yaml"), []byte(badCfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(project); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(oldWd) }()

	cfg := config.DefaultConfig()
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	if err := eng.Init(context.Background()); err != nil {
		t.Fatalf("eng.Init: %v", err)
	}
	t.Cleanup(func() { eng.Shutdown() })

	code := runDoctor(context.Background(), eng, []string{"--fix"}, true)
	if code != 0 {
		t.Fatalf("expected doctor exit code 0 after fix, got %d", code)
	}

	reloaded, err := config.LoadWithOptions(config.LoadOptions{CWD: project})
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if strings.TrimSpace(reloaded.Providers.Primary) == "" {
		t.Fatal("expected providers.primary to be set")
	}
	if _, ok := reloaded.Providers.Profiles[reloaded.Providers.Primary]; !ok {
		t.Fatalf("primary provider %q not present in profiles", reloaded.Providers.Primary)
	}
	if reloaded.Web.Auth != "none" && reloaded.Web.Auth != "token" {
		t.Fatalf("web.auth was not fixed: %s", reloaded.Web.Auth)
	}
	if reloaded.Remote.Auth != "none" && reloaded.Remote.Auth != "token" && reloaded.Remote.Auth != "mtls" {
		t.Fatalf("remote.auth was not fixed: %s", reloaded.Remote.Auth)
	}
}

func TestAddMagicDocHealthCheck(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		root := t.TempDir()
		checks := []doctorCheck{}
		addMagicDocHealthCheck(&checks, root, 24*time.Hour)
		if len(checks) != 1 {
			t.Fatalf("expected one check, got %d", len(checks))
		}
		if checks[0].Status != "warn" || !strings.Contains(checks[0].Details, "missing") {
			t.Fatalf("unexpected missing check: %+v", checks[0])
		}
	})

	t.Run("fresh", func(t *testing.T) {
		root := t.TempDir()
		path := resolveMagicDocPath(root, "")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir magic dir: %v", err)
		}
		if err := os.WriteFile(path, []byte("# MAGIC DOC: Test\n"), 0o644); err != nil {
			t.Fatalf("write magic doc: %v", err)
		}
		checks := []doctorCheck{}
		addMagicDocHealthCheck(&checks, root, 24*time.Hour)
		if len(checks) != 1 {
			t.Fatalf("expected one check, got %d", len(checks))
		}
		if checks[0].Status != "pass" || !strings.Contains(checks[0].Details, "fresh") {
			t.Fatalf("unexpected fresh check: %+v", checks[0])
		}
	})

	t.Run("stale", func(t *testing.T) {
		root := t.TempDir()
		path := resolveMagicDocPath(root, "")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir magic dir: %v", err)
		}
		if err := os.WriteFile(path, []byte("# MAGIC DOC: Test\n"), 0o644); err != nil {
			t.Fatalf("write magic doc: %v", err)
		}
		old := time.Now().Add(-72 * time.Hour)
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
		checks := []doctorCheck{}
		addMagicDocHealthCheck(&checks, root, 24*time.Hour)
		if len(checks) != 1 {
			t.Fatalf("expected one check, got %d", len(checks))
		}
		if checks[0].Status != "warn" || !strings.Contains(checks[0].Details, "stale") {
			t.Fatalf("unexpected stale check: %+v", checks[0])
		}
	})
}

func TestAddPromptHealthCheck(t *testing.T) {
	t.Run("pass_default_templates", func(t *testing.T) {
		root := t.TempDir()
		checks := []doctorCheck{}
		addPromptHealthCheck(&checks, root, 1000)
		if len(checks) != 1 {
			t.Fatalf("expected one check, got %d", len(checks))
		}
		if checks[0].Name != "prompt.health" || checks[0].Status != "pass" {
			t.Fatalf("unexpected prompt health check: %+v", checks[0])
		}
	})

	t.Run("warn_on_unknown_placeholder", func(t *testing.T) {
		root := t.TempDir()
		dir := filepath.Join(root, ".dfmc", "prompts")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir prompts dir: %v", err)
		}
		override := `
id: system.general.bad_var
type: system
task: general
priority: 999
body: |
  Broken var {{doctor_unknown_var}}
`
		if err := os.WriteFile(filepath.Join(dir, "bad_var.yaml"), []byte(override), 0o644); err != nil {
			t.Fatalf("write override: %v", err)
		}

		checks := []doctorCheck{}
		addPromptHealthCheck(&checks, root, 1000)
		if len(checks) != 1 {
			t.Fatalf("expected one check, got %d", len(checks))
		}
		if checks[0].Status != "warn" || !strings.Contains(checks[0].Details, "warnings=") {
			t.Fatalf("unexpected prompt warn check: %+v", checks[0])
		}
	})
}
