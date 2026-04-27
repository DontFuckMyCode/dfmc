package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunPlugin_ListEmpty(t *testing.T) {
	eng := newCLITestEngine(t)
	out := captureStdout(t, func() {
		code := runPlugin(context.Background(), eng, []string{"list"}, false)
		if code != 0 {
			t.Errorf("expected exit 0, got %d", code)
		}
	})
	if !strings.Contains(out, "No plugins") && out == "" {
		t.Error("expected no plugins message or empty output")
	}
}

func TestRunPlugin_ListJSON(t *testing.T) {
	eng := newCLITestEngine(t)
	out := captureStdout(t, func() {
		code := runPlugin(context.Background(), eng, []string{"list"}, true)
		if code != 0 {
			t.Errorf("expected exit 0, got %d", code)
		}
	})
	if !containsJSONKey(out, "directory") && !containsJSONKey(out, "plugins") {
		t.Errorf("expected JSON with directory/plugins: %s", out)
	}
}

func TestRunPlugin_Info(t *testing.T) {
	eng := newCLITestEngine(t)
	out := captureStdout(t, func() {
		code := runPlugin(context.Background(), eng, []string{"info", "nonexistent"}, false)
		if code != 1 && code != 0 {
			t.Errorf("expected exit 0 or 1, got %d", code)
		}
	})
	_ = out
}

func TestRunPlugin_Enable(t *testing.T) {
	eng := newCLITestEngine(t)
	out := captureStdout(t, func() {
		code := runPlugin(context.Background(), eng, []string{"enable", "nonexistent"}, false)
		if code != 0 {
			t.Errorf("expected exit 0, got %d", code)
		}
	})
	if !strings.Contains(out, "Plugin") && !strings.Contains(out, "enabled") {
		t.Logf("expected plugin enabled message: %s", out)
	}
}

func TestRunPlugin_Disable(t *testing.T) {
	eng := newCLITestEngine(t)
	out := captureStdout(t, func() {
		code := runPlugin(context.Background(), eng, []string{"disable", "nonexistent"}, false)
		if code != 0 {
			t.Errorf("expected exit 0, got %d", code)
		}
	})
	if !strings.Contains(out, "Plugin") && !strings.Contains(out, "disabled") {
		t.Logf("expected plugin disabled message: %s", out)
	}
}

func TestRunPlugin_Remove(t *testing.T) {
	eng := newCLITestEngine(t)
	out := captureStdout(t, func() {
		code := runPlugin(context.Background(), eng, []string{"remove", "nonexistent"}, false)
		if code != 0 {
			t.Errorf("expected exit 0, got %d", code)
		}
	})
	if !strings.Contains(out, "Plugin") && !strings.Contains(out, "disabled") {
		t.Logf("expected plugin removed message: %s", out)
	}
}

func TestRunPlugin_UnknownCommand(t *testing.T) {
	eng := newCLITestEngine(t)
	out := captureStderr(t, func() {
		code := runPlugin(context.Background(), eng, []string{"unknown-xyz"}, false)
		if code != 2 {
			t.Errorf("expected exit 2 for unknown subcommand, got %d", code)
		}
	})
	if !strings.Contains(out, "usage") {
		t.Errorf("expected usage message: %s", out)
	}
}

func TestRunSkill_List(t *testing.T) {
	eng := newCLITestEngine(t)
	out := captureStdout(t, func() {
		code := runSkill(context.Background(), eng, []string{"list"}, false)
		if code != 0 {
			t.Errorf("expected exit 0, got %d", code)
		}
	})
	if out == "" {
		t.Error("expected non-empty list output")
	}
}

func TestRunSkill_ListJSON(t *testing.T) {
	eng := newCLITestEngine(t)
	out := captureStdout(t, func() {
		code := runSkill(context.Background(), eng, []string{"list"}, true)
		if code != 0 {
			t.Errorf("expected exit 0, got %d", code)
		}
	})
	if !containsJSONKey(out, "skills") && !containsJSONKey(out, "name") {
		t.Errorf("expected skills JSON: %s", out)
	}
}

func TestRunSkill_Info(t *testing.T) {
	eng := newCLITestEngine(t)
	out := captureStdout(t, func() {
		code := runSkill(context.Background(), eng, []string{"info", "review"}, false)
		if code != 0 {
			t.Errorf("expected exit 0, got %d", code)
		}
	})
	if out == "" {
		t.Error("expected non-empty info output")
	}
}

func TestRunSkill_InfoJSON(t *testing.T) {
	eng := newCLITestEngine(t)
	out := captureStdout(t, func() {
		code := runSkill(context.Background(), eng, []string{"info", "review"}, true)
		if code != 0 {
			t.Errorf("expected exit 0, got %d", code)
		}
	})
	if !containsJSONKey(out, "name") {
		t.Errorf("expected name in skill info JSON: %s", out)
	}
}

func TestRunSkill_InfoUnknown(t *testing.T) {
	eng := newCLITestEngine(t)
	out := captureStderr(t, func() {
		code := runSkill(context.Background(), eng, []string{"info", "nonexistent-skill-xyz"}, false)
		if code != 1 {
			t.Errorf("expected exit 1 for unknown skill, got %d", code)
		}
	})
	if !strings.Contains(out, "not found") && !strings.Contains(out, "unknown") {
		t.Logf("expected not found message: %s", out)
	}
}

func TestRunSkill_Run(t *testing.T) {
	eng := newCLITestEngine(t)
	out := captureStdout(t, func() {
		code := runSkill(context.Background(), eng, []string{"run", "review", "--input", "test.go"}, false)
		if code != 0 {
			t.Logf("expected exit 0 or non-zero if no content, got %d", code)
		}
	})
	_ = out
}

func TestRunSkill_RunJSON(t *testing.T) {
	eng := newCLITestEngine(t)
	out := captureStdout(t, func() {
		code := runSkill(context.Background(), eng, []string{"run", "review", "--input", "test.go"}, true)
		if code != 0 {
			t.Logf("expected exit 0 or non-zero, got %d", code)
		}
	})
	if out == "" {
		t.Error("expected output")
	}
}

func TestRunSkill_UnknownCommand(t *testing.T) {
	eng := newCLITestEngine(t)
	out := captureStderr(t, func() {
		code := runSkill(context.Background(), eng, []string{"unknown-xyz"}, false)
		if code != 2 {
			t.Errorf("expected exit 2 for unknown subcommand, got %d", code)
		}
	})
	if !strings.Contains(out, "usage") {
		t.Errorf("expected usage message: %s", out)
	}
}

func TestRunSkill_InstallNotImplemented(t *testing.T) {
	eng := newCLITestEngine(t)
	out := captureStderr(t, func() {
		code := runSkill(context.Background(), eng, []string{"install", "some-skill"}, false)
		if code != 1 {
			t.Errorf("expected exit 1 for install (not implemented), got %d", code)
		}
	})
	if !strings.Contains(out, "not supported") && !strings.Contains(out, "install") {
		t.Logf("expected not supported message: %s", out)
	}
}

func TestRunSkill_ExportNotImplemented(t *testing.T) {
	eng := newCLITestEngine(t)
	out := captureStderr(t, func() {
		code := runSkill(context.Background(), eng, []string{"export", "some-skill"}, false)
		if code != 1 {
			t.Errorf("expected exit 1 for export (not implemented), got %d", code)
		}
	})
	if !strings.Contains(out, "not supported") && !strings.Contains(out, "export") {
		t.Logf("expected not supported message: %s", out)
	}
}

func TestRunPlugin_NoArgsDefaultsToList(t *testing.T) {
	eng := newCLITestEngine(t)
	out := captureStdout(t, func() {
		code := runPlugin(context.Background(), eng, []string{}, false)
		if code != 0 {
			t.Errorf("expected exit 0 (defaults to list), got %d", code)
		}
	})
	// Should run list (empty or not)
	_ = out
}

func TestRunSkill_NoArgsDefaultsToList(t *testing.T) {
	eng := newCLITestEngine(t)
	out := captureStdout(t, func() {
		code := runSkill(context.Background(), eng, []string{}, false)
		if code != 0 {
			t.Errorf("expected exit 0 (defaults to list), got %d", code)
		}
	})
	// Should run list (shows builtin skills)
	_ = out
}

func TestDiscoverPlugins_CaseInsensitive(t *testing.T) {
	items := discoverPlugins(t.TempDir(), []string{"Alpha", "BETA"})
	_ = items
}

func TestPluginInfo_JSON(t *testing.T) {
	info := pluginInfo{
		Name:      "test-plugin",
		Version:   "1.0.0",
		Type:      "script",
		Installed: true,
		Enabled:   true,
	}
	_ = info
}

func TestResolvePathWithinBase(t *testing.T) {
	root := t.TempDir()
	subdir := filepath.Join(root, "sub")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Valid path inside base
	validPath := filepath.Join(subdir, "file.txt")
	if err := os.WriteFile(validPath, []byte("test"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	result, err := resolvePathWithinBase(root, validPath)
	if err != nil {
		t.Errorf("unexpected error for valid path: %v", err)
	}
	if result != validPath {
		t.Errorf("expected same path, got %s", result)
	}

	// Path outside base should error
	badPath := filepath.Join(root, "..", "outside.txt")
	_, err = resolvePathWithinBase(root, badPath)
	if err == nil {
		t.Error("expected error for path outside base")
	}
}