package config

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestT5_LoadWithOptions_ExplicitGlobalPath_UsesDefaults verifies that
// when a global config file explicitly sets primary:minimax and version:99,
// LoadWithOptions respects those values and the minimax profile is present
// with model MiniMax-M2.7 (merged from defaults).
func TestT5_LoadWithOptions_ExplicitGlobalPath_UsesDefaults(t *testing.T) {
	tmp := t.TempDir()

	fakeGlobal := filepath.Join(tmp, "config.yaml")
	yamlContent := `version: 99
providers:
  primary: minimax
  profiles:
    minimax:
      model: MiniMax-M2.7
`
	if err := os.WriteFile(fakeGlobal, []byte(yamlContent), 0o644); err != nil {
		t.Fatalf("write fake config: %v", err)
	}

	// Confirm the raw YAML parses correctly with a standalone struct.
	type configForTest struct {
		Version   int `yaml:"version"`
		Providers struct {
			Primary string `yaml:"primary"`
		} `yaml:"providers"`
	}
	var direct configForTest
	if err := yaml.Unmarshal([]byte(yamlContent), &direct); err != nil {
		t.Fatalf("yaml.Unmarshal directly: %v", err)
	}
	t.Logf("direct unmarshal: Version=%d, Providers.Primary=%q", direct.Version, direct.Providers.Primary)
	if direct.Version != 99 {
		t.Fatalf("direct unmarshal gave Version=%d; want 99", direct.Version)
	}
	if direct.Providers.Primary != "minimax" {
		t.Fatalf("direct unmarshal gave Primary=%q; want minimax", direct.Providers.Primary)
	}

	// Now test the full LoadWithOptions path.
	cfg, err := LoadWithOptions(LoadOptions{
		GlobalPath:  fakeGlobal,
		ProjectPath: fakeGlobal,
		CWD:        tmp,
	})
	if err != nil {
		t.Fatalf("LoadWithOptions: %v", err)
	}

	t.Logf("cfg.Version = %d", cfg.Version)
	t.Logf("cfg.Providers.Primary = %q", cfg.Providers.Primary)

	// Version check: if this fails, loadYAML isn't running.
	if cfg.Version != 99 {
		t.Errorf("cfg.Version = %d; want 99 (loadYAML not running?)", cfg.Version)
	}
	// Primary check.
	if cfg.Providers.Primary != "minimax" {
		t.Errorf("cfg.Providers.Primary = %q; want 'minimax'", cfg.Providers.Primary)
	}

	prof, ok := cfg.Providers.Profiles["minimax"]
	if !ok {
		t.Fatal("LoadWithOptions missing minimax profile")
	}
	if prof.Model != "MiniMax-M2.7" {
		t.Errorf("LoadWithOptions minimax model = %q; want 'MiniMax-M2.7'", prof.Model)
	}
}

// TestT5_DefaultConfig_SetsMinimaxProvider verifies that DefaultConfig()
// selects minimax as the primary provider and MiniMax-M2.7 as its model.
func TestT5_DefaultConfig_SetsMinimaxProvider(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Providers.Primary != "minimax" {
		t.Errorf("DefaultConfig primary provider = %q; want 'minimax'", cfg.Providers.Primary)
	}

	prof, ok := cfg.Providers.Profiles["minimax"]
	if !ok {
		t.Fatalf("DefaultConfig has no profile for 'minimax'")
	}

	if prof.Model != "MiniMax-M2.7" {
		t.Errorf("DefaultConfig minimax model = %q; want 'MiniMax-M2.7'", prof.Model)
	}
}

// TestT5_UserConfigDir_IsReadable ensures UserConfigDir() works.
func TestT5_UserConfigDir_IsReadable(t *testing.T) {
	dir := UserConfigDir()
	if dir == "" {
		t.Fatal("UserConfigDir returned empty string")
	}
	if filepath.Base(dir) != ".dfmc" {
		t.Errorf("UserConfigDir = %q; expected it to end in '.dfmc'", dir)
	}
}

// TestT5_ConfigSetKeyGetKey_RoundTrip verifies that
// SetKey / GetKey round-trips correctly for minimax.
func TestT5_ConfigSetKeyGetKey_RoundTrip(t *testing.T) {
	cfg := DefaultConfig()

	wantKey := "test-minimax-key-123"
	cfg.SetKey("minimax", wantKey)

	gotKey := cfg.GetKey("minimax")
	if gotKey != wantKey {
		t.Errorf("GetKey(minimax) = %q; want %q", gotKey, wantKey)
	}
}

// TestT5_BothKeysSet_BothPopulated verifies that when both
// ANTHROPIC_API_KEY and MINIMAX_API_KEY are set, both keys are
// correctly sourced from env vars. Uses LoadWithOptions so applyEnv runs.
func TestT5_BothKeysSet_BothPopulated(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("ANTHROPIC_API_KEY", "anthropic-key-xyz")
	t.Setenv("MINIMAX_API_KEY", "minimax-key-abc")

	fakeGlobal := filepath.Join(tmp, "config.yaml")
	if err := os.WriteFile(fakeGlobal, []byte("version: 1\nproviders:\n  primary: minimax\n"), 0o644); err != nil {
		t.Fatalf("write fake config: %v", err)
	}

	cfg, err := LoadWithOptions(LoadOptions{
		GlobalPath:  fakeGlobal,
		ProjectPath: fakeGlobal,
		CWD:        tmp,
	})
	if err != nil {
		t.Fatalf("LoadWithOptions: %v", err)
	}

	minimaxKey := cfg.GetKey("minimax")
	anthropicKey := cfg.GetKey("anthropic")

	if minimaxKey != "minimax-key-abc" {
		t.Errorf("GetKey(minimax) = %q; want env-sourced 'minimax-key-abc'", minimaxKey)
	}
	if anthropicKey != "anthropic-key-xyz" {
		t.Errorf("GetKey(anthropic) = %q; want env-sourced 'anthropic-key-xyz'", anthropicKey)
	}
	if cfg.Providers.Primary != "minimax" {
		t.Errorf("primary provider = %q; want 'minimax'", cfg.Providers.Primary)
	}
}

// TestT5_DefaultFallback_IsNonEmptyOrWarns verifies the default
// config has fallback providers defined; logs a warning if none are set.
func TestT5_DefaultFallback_IsNonEmptyOrWarns(t *testing.T) {
	cfg := DefaultConfig()

	if len(cfg.Providers.Fallback) == 0 {
		t.Log("WARNING: DefaultConfig has no fallback providers")
	}

	for _, fb := range cfg.Providers.Fallback {
		if _, ok := cfg.Providers.Profiles[fb]; !ok {
			t.Logf("WARNING: fallback provider %q has no profile in DefaultConfig", fb)
		}
	}
}

// TestT5_ConfigSaveProducesYAMLWithPrimary verifies that Save()
// produces valid YAML that explicitly contains primary: minimax.
func TestT5_ConfigSaveProducesYAMLWithPrimary(t *testing.T) {
	tmp := t.TempDir()
	cfg := DefaultConfig()

	savePath := filepath.Join(tmp, "config.yaml")
	if err := cfg.Save(savePath); err != nil {
		t.Fatalf("cfg.Save: %v", err)
	}

	data, err := os.ReadFile(savePath)
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}

	if !bytes.Contains(data, []byte("primary: minimax")) {
		t.Errorf("saved YAML does not contain 'primary: minimax'; got:\n%s", string(data))
	}
}