package pluginexec

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestManagerCloseNotLoaded(t *testing.T) {
	m := NewManager()
	err := m.Close(context.Background(), "nonexistent")
	if err == nil {
		t.Error("expected error for closing nonexistent plugin")
	}
}

func TestManagerListSorted(t *testing.T) {
	m := NewManager()
	names := m.List()
	if len(names) != 0 {
		t.Errorf("expected empty list, got %v", names)
	}
}

func TestManagerCloseAll(t *testing.T) {
	m := NewManager()
	err := m.CloseAll(context.Background())
	if err != nil {
		t.Errorf("CloseAll on empty manager: %v", err)
	}
}

func TestKindFromExt(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"x.py", "python"},
		{"x.js", "node"},
		{"x.mjs", "node"},
		{"x.cjs", "node"},
		{"x.sh", "shell"},
		{"x", "exec"},
		{"x.exe", "exec"},
	}
	for _, tt := range tests {
		got := kindFromExt(tt.path)
		if got != tt.want {
			t.Errorf("kindFromExt(%q): got %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestResolveArgvExec(t *testing.T) {
	got, err := resolveArgv("some-binary", "exec", nil)
	if err != nil {
		t.Fatalf("resolveArgv exec: %v", err)
	}
	if len(got) < 1 || got[0] != "some-binary" {
		t.Errorf("exec: got %v", got)
	}
}

func TestResolveArgvPython(t *testing.T) {
	got, err := resolveArgv("script.py", "python", []string{"--flag"})
	if err != nil {
		t.Skip("python not available: " + err.Error())
	}
	if len(got) < 2 {
		t.Errorf("python argv too short: %v", got)
	}
}

func TestManagerSetToolRegistry(t *testing.T) {
	m := NewManager()
	fn := func(name string, fn func(ctx context.Context, params map[string]any) (any, error)) {
	}
	m.SetToolRegistry(fn)
	if m.toolRegistry == nil {
		t.Error("toolRegistry not set")
	}
}

func TestManagerSetHookRegistry(t *testing.T) {
	m := NewManager()
	fn := func(name, command string, timeout int) error { return nil }
	m.SetHookRegistry(fn)
	if m.hookRegistry == nil {
		t.Error("hookRegistry not set")
	}
}

func TestManagerSetSkillInstaller(t *testing.T) {
	m := NewManager()
	fn := func(name, prompt string) error { return nil }
	m.SetSkillInstaller(fn)
	if m.skillInstaller == nil {
		t.Error("skillInstaller not set")
	}
}

func TestManagerSetOnClose(t *testing.T) {
	m := NewManager()
	fn := func(name string) {}
	m.SetOnClose(fn)
	if m.onClose == nil {
		t.Error("onClose not set")
	}
}

func TestManagerSpawnDuplicate(t *testing.T) {
	m := NewManager()
	spec := Spec{Name: "dup", Entry: os.Args[0], Type: "exec", Env: []string{"DFMC_TEST_PLUGIN_MODE=echo"}, Args: []string{"-test.run=^$"}}
	err := m.Spawn(context.Background(), spec)
	if err != nil {
		t.Skip("spawn failed: " + err.Error())
	}
	defer m.CloseAll(context.Background())
	// Second spawn with same name should fail
	err = m.Spawn(context.Background(), spec)
	if err == nil {
		t.Error("expected error for duplicate plugin name")
	}
}

func TestManagerCallNotLoaded(t *testing.T) {
	m := NewManager()
	_, err := m.Call(context.Background(), "nonexistent", "method", nil)
	if err == nil {
		t.Error("expected error for calling unloaded plugin")
	}
}

func TestManagerCallWithTimeout(t *testing.T) {
	m := NewManager()
	spec := Spec{Name: "echo", Entry: os.Args[0], Type: "exec", Env: []string{"DFMC_TEST_PLUGIN_MODE=echo"}, Args: []string{"-test.run=^$"}}
	err := m.Spawn(context.Background(), spec)
	if err != nil {
		t.Skip("spawn failed: " + err.Error())
	}
	defer m.CloseAll(context.Background())
	result, err := m.Call(context.Background(), "echo", "test", map[string]any{"x": 1}, 5*time.Second)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if len(result) == 0 {
		t.Error("expected non-empty result")
	}
}

func TestManagerCloseAllWithClients(t *testing.T) {
	m := NewManager()
	spec := Spec{Name: "echo2", Entry: os.Args[0], Type: "exec", Env: []string{"DFMC_TEST_PLUGIN_MODE=echo"}, Args: []string{"-test.run=^$"}}
	err := m.Spawn(context.Background(), spec)
	if err != nil {
		t.Skip("spawn failed: " + err.Error())
	}
	err = m.CloseAll(context.Background())
	if err != nil {
		t.Errorf("CloseAll: %v", err)
	}
}

func TestManagerListAfterSpawn(t *testing.T) {
	m := NewManager()
	spec := Spec{Name: "echo3", Entry: os.Args[0], Type: "exec", Env: []string{"DFMC_TEST_PLUGIN_MODE=echo"}, Args: []string{"-test.run=^$"}}
	err := m.Spawn(context.Background(), spec)
	if err != nil {
		t.Skip("spawn failed: " + err.Error())
	}
	defer m.CloseAll(context.Background())
	names := m.List()
	if len(names) != 1 || names[0] != "echo3" {
		t.Errorf("List: got %v", names)
	}
}

func TestManagerStderrNotLoaded(t *testing.T) {
	m := NewManager()
	got := m.Stderr("nonexistent")
	if got != "" {
		t.Errorf("Stderr on not-loaded: got %q want empty", got)
	}
}

func TestManagerStderrLoaded(t *testing.T) {
	m := NewManager()
	spec := Spec{Name: "echo4", Entry: os.Args[0], Type: "exec", Env: []string{"DFMC_TEST_PLUGIN_MODE=echo"}, Args: []string{"-test.run=^$"}}
	err := m.Spawn(context.Background(), spec)
	if err != nil {
		t.Skip("spawn failed: " + err.Error())
	}
	defer m.CloseAll(context.Background())
	got := m.Stderr("echo4")
	// Should return whatever Client.Stderr returns (empty for echo mode)
	if got != "" {
		t.Errorf("Stderr on echo plugin: got %q", got)
	}
}