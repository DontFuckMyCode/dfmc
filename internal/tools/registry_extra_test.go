package tools

import (
	"context"
	"errors"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

func TestEngine_Register_NilTool(t *testing.T) {
	cfg := *config.DefaultConfig()
	eng := NewFromConfig(&cfg)
	before := len(eng.ListAll())
	eng.Register(nil)
	after := len(eng.ListAll())
	if before != after {
		t.Error("Register(nil) should not change registry")
	}
}

func TestEngine_Register_AfterClose(t *testing.T) {
	cfg := *config.DefaultConfig()
	eng := NewFromConfig(&cfg)
	eng.Close()
	// Register after close should be a no-op
	before := len(eng.ListAll())
	eng.Register(&stubTool{name: "post_close_tool"})
	after := len(eng.ListAll())
	if before != after {
		t.Error("Register after Close should be a no-op")
	}
}

func TestEngine_Register_CustomTool(t *testing.T) {
	cfg := *config.DefaultConfig()
	eng := NewFromConfig(&cfg)
	eng.Register(&stubTool{name: "custom_test_tool"})
	all := eng.ListAll()
	found := false
	for _, n := range all {
		if n == "custom_test_tool" {
			found = true
		}
	}
	if !found {
		t.Error("custom tool should appear in ListAll")
	}
	tool, ok := eng.Get("custom_test_tool")
	if !ok {
		t.Fatal("Get should find custom tool")
	}
	if tool.Name() != "custom_test_tool" {
		t.Errorf("tool name = %q, want custom_test_tool", tool.Name())
	}
}

func TestEngine_ListDisabled_Empty(t *testing.T) {
	cfg := *config.DefaultConfig()
	eng := NewFromConfig(&cfg)
	disabled := eng.ListDisabled()
	if len(disabled) != 0 {
		t.Errorf("expected no disabled tools, got %v", disabled)
	}
}

func TestEngine_ListDisabled_AfterDisable(t *testing.T) {
	cfg := *config.DefaultConfig()
	eng := NewFromConfig(&cfg)
	if err := eng.SetEnabled("think", false); err != nil {
		t.Fatalf("disable think: %v", err)
	}
	disabled := eng.ListDisabled()
	if len(disabled) != 1 || disabled[0] != "think" {
		t.Errorf("expected [think], got %v", disabled)
	}
}

func TestEngine_ListDisabled_Multiple(t *testing.T) {
	cfg := *config.DefaultConfig()
	cfg.Tools.Disabled = []string{"hunt", "audit"}
	eng := NewFromConfig(&cfg)
	disabled := eng.ListDisabled()
	if len(disabled) != 2 {
		t.Fatalf("expected 2 disabled, got %d: %v", len(disabled), disabled)
	}
	// Should be sorted
	if disabled[0] != "audit" || disabled[1] != "hunt" {
		t.Errorf("expected sorted [audit hunt], got %v", disabled)
	}
}

func TestEngine_SetEnabled_NonexistentTool(t *testing.T) {
	cfg := *config.DefaultConfig()
	eng := NewFromConfig(&cfg)
	// Disabling a non-existent tool should succeed (it might be registered later)
	err := eng.SetEnabled("future_tool", false)
	if err != nil {
		t.Fatalf("disabling non-existent tool should not error: %v", err)
	}
	if !eng.IsDisabled("future_tool") {
		t.Error("future_tool should be disabled")
	}
}

func TestEngine_SetEnabled_EmptyName(t *testing.T) {
	cfg := *config.DefaultConfig()
	eng := NewFromConfig(&cfg)
	err := eng.SetEnabled("", false)
	if err != nil {
		t.Fatalf("empty name should be a no-op, got: %v", err)
	}
}

func TestEngine_SetEnabled_CaseInsensitive(t *testing.T) {
	cfg := *config.DefaultConfig()
	eng := NewFromConfig(&cfg)
	if err := eng.SetEnabled("Think", false); err != nil {
		t.Fatalf("disable Think: %v", err)
	}
	if !eng.IsDisabled("think") {
		t.Error("lowercase think should be disabled")
	}
	if !eng.IsDisabled("THINK") {
		t.Error("THINK should be disabled")
	}
}

func TestEngine_Register_DisabledToolStillInRegistry(t *testing.T) {
	cfg := *config.DefaultConfig()
	cfg.Tools.Disabled = []string{"custom_tool"}
	eng := NewFromConfig(&cfg)

	eng.Register(&stubTool{name: "custom_tool"})
	all := eng.ListAll()
	found := false
	for _, n := range all {
		if n == "custom_tool" {
			found = true
		}
	}
	if !found {
		t.Error("disabled tool should still be in ListAll")
	}
	// But not in List
	for _, n := range eng.List() {
		if n == "custom_tool" {
			t.Error("disabled tool should not appear in List")
		}
	}
}

func TestEngine_Disabled_ExecuteBlocked(t *testing.T) {
	cfg := *config.DefaultConfig()
	cfg.Tools.Disabled = []string{"think"}
	eng := NewFromConfig(&cfg)
	_, err := eng.Execute(context.Background(), "think", Request{
		ProjectRoot: t.TempDir(),
		Params:      map[string]any{},
	})
	if !errors.Is(err, ErrToolDisabled) {
		t.Errorf("expected ErrToolDisabled, got %v", err)
	}
}

// stubTool is a minimal Tool implementation for registry tests.
type stubTool struct {
	name string
}

func (s *stubTool) Name() string        { return s.name }
func (s *stubTool) Description() string { return "stub: " + s.name }
func (s *stubTool) Execute(_ context.Context, _ Request) (Result, error) {
	return Result{Output: "ok"}, nil
}
