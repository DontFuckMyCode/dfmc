package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func TestRunDoctorJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	cfg := config.DefaultConfig()
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	if err := eng.Init(context.Background()); err != nil {
		t.Fatalf("eng.Init: %v", err)
	}
	t.Cleanup(func() { eng.Shutdown() })

	code := runDoctor(context.Background(), eng, []string{"--network=false"}, true)
	if code != 0 {
		t.Fatalf("expected doctor exit code 0, got %d", code)
	}
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
