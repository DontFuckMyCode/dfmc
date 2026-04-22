package cli

import (
	"context"
	"os"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/engine"
)

// newProviderCLITestEngine builds a real engine so the Providers router
// is populated — runProviderCLI and runProvidersList read eng.Providers
// which the zero-value engine doesn't have.
func newProviderCLITestEngine(t *testing.T) *engine.Engine {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("HOME", tmp)
	if err := os.WriteFile(tmp+"/hello.txt", []byte("hi"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cfg := config.DefaultConfig()
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	if err := eng.Init(context.Background()); err != nil {
		t.Fatalf("engine.Init: %v", err)
	}
	t.Cleanup(func() { _ = eng.Shutdown() })
	eng.ProjectRoot = tmp
	return eng
}

func TestRunProviderCLI_NoArgsPrintsCurrent(t *testing.T) {
	eng := newProviderCLITestEngine(t)
	rc := runProviderCLI(eng, nil, true)
	if rc != 0 {
		t.Fatalf("provider show should exit 0, got %d", rc)
	}
}

func TestRunProviderCLI_SetUpdatesEngine(t *testing.T) {
	eng := newProviderCLITestEngine(t)
	rc := runProviderCLI(eng, []string{"offline"}, true)
	if rc != 0 {
		t.Fatalf("setting provider to offline should exit 0, got %d", rc)
	}
	if got := eng.Status().Provider; got != "offline" {
		t.Fatalf("engine provider should now be offline, got %q", got)
	}
}

func TestRunProviderCLI_RejectsUnknownProvider(t *testing.T) {
	eng := newProviderCLITestEngine(t)
	rc := runProviderCLI(eng, []string{"definitely-not-a-real-provider"}, true)
	if rc != 1 {
		t.Fatalf("unknown provider should exit 1, got %d", rc)
	}
}

func TestRunModelCLI_SetUpdatesEngine(t *testing.T) {
	eng := newProviderCLITestEngine(t)
	rc := runModelCLI(eng, []string{"custom-model-name"}, true)
	if rc != 0 {
		t.Fatalf("setting model should exit 0, got %d", rc)
	}
	if got := eng.Status().Model; got != "custom-model-name" {
		t.Fatalf("engine model should now be custom-model-name, got %q", got)
	}
}

func TestRunProvidersList(t *testing.T) {
	eng := newProviderCLITestEngine(t)
	rc := runProvidersList(eng, true)
	if rc != 0 {
		t.Fatalf("providers list should exit 0, got %d", rc)
	}
}
