package tools

// tools_config_test.go pins ToToolsConfigSubset against the regression that
// shipped briefly before the May 2026 audit: the helper takes `any`, type-
// asserts to ConfigLike, and silently returns an empty subset on a mismatch.
// That meant callers passing a **config.Config (the common typo when the
// surrounding field is already a *Config) got a fully zeroed subset —
// AllowShell=false, BlockedCommands=nil, Disabled=nil, Layers=nil — without
// any compile-time or test-time signal. run_command was disabled, tools the
// user had disabled became live again, and the shell blocked-command list was
// dropped. The bug was caught by a TUI integration test that happened to call
// run_command, not by anything pinning the subset itself.
//
// The two tests below are deliberately narrow and run cheaply:
//   - the wiring test pins that every security-relevant field round-trips
//     through *config.Config without losing fidelity;
//   - the double-pointer test pins that the helper does NOT silently return
//     empty when called with **config.Config — it falls through to default
//     but the assertion catches it explicitly.

import (
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

// TestToToolsConfigSubset_WiringFidelity confirms the subset carries the
// security-relevant fields end-to-end. Each field is a separate t.Run so a
// future regression names exactly which field stopped wiring.
func TestToToolsConfigSubset_WiringFidelity(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Tools.Disabled = []string{"web_fetch"}
	cfg.Tools.RequireApproval = []string{"write_file"}
	cfg.Tools.Layers = []string{"core", "skill"}
	cfg.Tools.Shell.BlockedCommands = []string{"rm", "format"}
	cfg.Security.Sandbox.AllowShell = true

	got := ToToolsConfigSubset(cfg)

	t.Run("AllowShell", func(t *testing.T) {
		if !got.Security.Sandbox.AllowShell {
			t.Fatal("AllowShell dropped — run_command would silently refuse to run")
		}
	})
	t.Run("Disabled", func(t *testing.T) {
		if len(got.Tools.Disabled) != 1 || got.Tools.Disabled[0] != "web_fetch" {
			t.Fatalf("Disabled dropped — user-disabled tools would silently re-enable: %v", got.Tools.Disabled)
		}
	})
	t.Run("RequireApproval", func(t *testing.T) {
		if len(got.Tools.RequireApproval) != 1 || got.Tools.RequireApproval[0] != "write_file" {
			t.Fatalf("RequireApproval dropped: %v", got.Tools.RequireApproval)
		}
	})
	t.Run("Layers", func(t *testing.T) {
		if len(got.Tools.Layers) != 2 {
			t.Fatalf("Layers dropped — layer gating would silently disable: %v", got.Tools.Layers)
		}
	})
	t.Run("BlockedCommands", func(t *testing.T) {
		if len(got.Tools.Shell.BlockedCommands) != 2 {
			t.Fatalf("BlockedCommands dropped — shell blocklist would silently empty: %v", got.Tools.Shell.BlockedCommands)
		}
	})
}

// TestToToolsConfigSubset_DoublePointerReturnsEmpty pins the exact failure
// mode of the May 2026 bug: when the helper receives **config.Config (the
// common typo when the caller's local is already a *Config and they reach
// for & out of habit) the ConfigLike assertion fails and the default branch
// returns a zero subset. This is the silent-failure shape we want a future
// reader to see called out explicitly — if anyone changes the helper to
// auto-dereference, this test should be updated, not deleted.
func TestToToolsConfigSubset_DoublePointerReturnsEmpty(t *testing.T) {
	cfg := config.DefaultConfig() // *config.Config
	got := ToToolsConfigSubset(&cfg)
	if got.Security.Sandbox.AllowShell {
		t.Fatal("double-pointer started returning a populated subset — re-evaluate the assertion path")
	}
	if len(got.Tools.Disabled) != 0 || len(got.Tools.RequireApproval) != 0 {
		t.Fatal("double-pointer started returning a populated subset")
	}
}
