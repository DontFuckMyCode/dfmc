package tools

import (
	"context"
	"errors"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

func TestDisabledState_SetEnabled(t *testing.T) {
	ds := newDisabledState(nil)

	// Disable a non-protected tool
	if err := ds.SetEnabled("think", false, func(string) bool { return true }); err != nil {
		t.Fatalf("disable think: %v", err)
	}
	if !ds.IsDisabled("think") {
		t.Fatal("think should be disabled")
	}

	// Re-enable
	if err := ds.SetEnabled("think", true, func(string) bool { return true }); err != nil {
		t.Fatalf("enable think: %v", err)
	}
	if ds.IsDisabled("think") {
		t.Fatal("think should be enabled")
	}
}

func TestDisabledState_ProtectedToolsCannotBeDisabled(t *testing.T) {
	ds := newDisabledState(nil)
	for name := range protectedTools {
		err := ds.SetEnabled(name, false, func(string) bool { return true })
		if !errors.Is(err, ErrToolProtected) {
			t.Errorf("expected ErrToolProtected for %q, got %v", name, err)
		}
	}
}

func TestDisabledState_Snapshot(t *testing.T) {
	ds := newDisabledState([]string{"think", "hunt"})
	snap := ds.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("expected 2 disabled, got %d: %v", len(snap), snap)
	}
	// Sorted
	if snap[0] != "hunt" || snap[1] != "think" {
		t.Fatalf("unexpected order: %v", snap)
	}
}

func TestDisabledState_CaseInsensitive(t *testing.T) {
	ds := newDisabledState([]string{"Think"})
	if !ds.IsDisabled("think") {
		t.Fatal("should match case-insensitively")
	}
	if !ds.IsDisabled("THINK") {
		t.Fatal("should match case-insensitively")
	}
}

func TestEngine_DisabledFiltering(t *testing.T) {
	cfg := *config.DefaultConfig()
	cfg.Tools.Disabled = []string{"think", "hunt"}
	eng := New(cfg)

	// think is disabled — should NOT appear in List, Specs, BackendSpecs
	for _, name := range eng.List() {
		if name == "think" {
			t.Error("disabled tool 'think' should not appear in List()")
		}
	}
	for _, s := range eng.Specs() {
		if s.Name == "think" {
			t.Error("disabled tool 'think' should not appear in Specs()")
		}
	}
	for _, s := range eng.BackendSpecs() {
		if s.Name == "think" {
			t.Error("disabled tool 'think' should not appear in BackendSpecs()")
		}
	}

	// But it should still be in the full inventory
	all := eng.ListAll()
	found := false
	for _, name := range all {
		if name == "think" {
			found = true
		}
	}
	if !found {
		t.Error("disabled tool 'think' should appear in ListAll()")
	}

	// Spec() should still return the spec (needed for tool_help)
	if _, ok := eng.Spec("think"); !ok {
		t.Error("Spec() should return spec for disabled tool")
	}

	// Execute should fail
	_, err := eng.Execute(context.Background(), "think", Request{
		ProjectRoot: t.TempDir(),
		Params:      map[string]any{},
	})
	if !errors.Is(err, ErrToolDisabled) {
		t.Errorf("expected ErrToolDisabled, got %v", err)
	}
}

func TestEngine_SetEnabled_Runtime(t *testing.T) {
	cfg := *config.DefaultConfig()
	eng := New(cfg)

	// think is enabled by default
	if eng.IsDisabled("think") {
		t.Fatal("think should be enabled initially")
	}

	// Disable
	if err := eng.SetEnabled("think", false); err != nil {
		t.Fatalf("disable think: %v", err)
	}
	if !eng.IsDisabled("think") {
		t.Fatal("think should be disabled after SetEnabled(false)")
	}

	// Verify filtered out of List
	for _, name := range eng.List() {
		if name == "think" {
			t.Error("disabled tool should not appear in List()")
		}
	}

	// Re-enable
	if err := eng.SetEnabled("think", true); err != nil {
		t.Fatalf("enable think: %v", err)
	}
	if eng.IsDisabled("think") {
		t.Fatal("think should be enabled after SetEnabled(true)")
	}

	// Now it should appear in List
	found := false
	for _, name := range eng.List() {
		if name == "think" {
			found = true
		}
	}
	if !found {
		t.Error("think should appear in List() after re-enable")
	}
}

func TestEngine_SetEnabled_Protected(t *testing.T) {
	cfg := *config.DefaultConfig()
	eng := New(cfg)

	for name := range protectedTools {
		err := eng.SetEnabled(name, false)
		if !errors.Is(err, ErrToolProtected) {
			t.Errorf("protected tool %q: expected ErrToolProtected, got %v", name, err)
		}
	}
}

func TestEngine_DisabledInSearch(t *testing.T) {
	cfg := *config.DefaultConfig()
	cfg.Tools.Disabled = []string{"think"}
	eng := New(cfg)

	hits := eng.Search("think", 10)
	for _, s := range hits {
		if s.Name == "think" {
			t.Error("disabled tool should not appear in Search results")
		}
	}
}

func TestEngine_DisabledSnapshot(t *testing.T) {
	cfg := *config.DefaultConfig()
	cfg.Tools.Disabled = []string{"think", "hunt"}
	eng := New(cfg)

	snap := eng.DisabledSnapshot()
	if len(snap) != 2 {
		t.Fatalf("expected 2 disabled, got %d", len(snap))
	}
}
