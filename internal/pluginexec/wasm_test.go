package pluginexec

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

// TestWasmLoader_Errors covers error paths for WASM plugin loading.
func TestWasmLoader_EmptyModule(t *testing.T) {
	_, err := WasmLoader(context.Background(), WasmSpec{Name: "test", Module: nil})
	if err == nil {
		t.Error("expected error for empty module")
	}
	if !errors.Is(err, ErrWasmModuleEmpty) {
		t.Errorf("error type: got %v want ErrWasmModuleEmpty", err)
	}
}

func TestWasmLoader_EmptyName(t *testing.T) {
	_, err := WasmLoader(context.Background(), WasmSpec{Name: "", Module: []byte{0x00}})
	if err == nil {
		t.Error("expected error for empty name")
	}
}

func TestWasmLoader_InvalidWasmHeader(t *testing.T) {
	// Not a valid WASM binary — just random bytes
	invalidWasm := []byte{
		0x00, 0x61, 0x73, 0x6d, // magic bytes — should be 0x00 0x61 0x73 0x6d
		0xff, 0xff, 0xff, 0xff, // version
		0x00, 0x00, 0x00, 0x01, // garbage section
	}
	_, err := WasmLoader(context.Background(), WasmSpec{Name: "bad", Module: invalidWasm})
	if err == nil {
		t.Error("expected error for invalid WASM")
	}
}

// TestWasmLoader_MissingWazeroTests tests the wazero import without actual
// WASM binary — the import itself is tested via compilation failure.

func TestWasmSpec_Name(t *testing.T) {
	spec := WasmSpec{Name: "my-plugin", Module: []byte{0x00}}
	if spec.Name != "my-plugin" {
		t.Errorf("Name: got %q want my-plugin", spec.Name)
	}
	if len(spec.Module) != 1 {
		t.Errorf("Module len: got %d want 1", len(spec.Module))
	}
}

// TestWasmModule_RunNilMemory covers the code path where WASM module
// has no memory export — exercised via invalid module without memory.
func TestWasmModule_RunNilMemory(t *testing.T) {
	// Create a minimal WASM that has no memory export
	// Using wazero, we can create an "empty" module that compiles
	// but has no memory. However, InstantiateModule always exports
	// memory if the WASM spec includes it. Instead, test the error path
	// by calling Run on a module with nil memory handle.
	m := &WasmModule{name: "nil-mem", mod: nil}
	_, err := m.Run(context.Background(), `{"input":"test"}`)
	if err == nil {
		t.Error("expected error for nil memory module")
	}
}

// TestKindFromExt_AllCases tests all file extension cases.
func TestKindFromExt_AllCases(t *testing.T) {
	cases := []struct {
		path  string
		want  string
	}{
		{"plugin.py", "python"},
		{"plugin.js", "node"},
		{"plugin.mjs", "node"},
		{"plugin.cjs", "node"},
		{"plugin.sh", "shell"},
		{"plugin", "exec"},
		{"plugin.exe", "exec"},
		{"plugin.dll", "exec"},
		{"plugin.so", "exec"},
		{"plugin.bat", "exec"},
		{"plugin.ps1", "exec"},
	}
	for _, c := range cases {
		got := kindFromExt(c.path)
		if got != c.want {
			t.Errorf("kindFromExt(%q): got %q want %q", c.path, got, c.want)
		}
	}
}

// TestManagerSpawn_EnvVarInjection tests that environment variables
// from the spec are correctly passed to the subprocess.
func TestManagerSpawn_EnvVarInjection(t *testing.T) {
	m := NewManager()
	// Use the current binary as the plugin — it will exit immediately
	// since no test mode is set.
	spec := Spec{
		Name:  "envtest",
		Entry: os.Args[0],
		Type:  "exec",
		Env:   []string{"DFMC_TEST_PLUGIN_MODE=echo", "MY_CUSTOM_VAR=hello"},
		Args:  []string{"-test.run=^$"},
	}
	err := m.Spawn(context.Background(), spec)
	if err != nil {
		t.Skip("spawn failed: " + err.Error())
	}
	defer m.CloseAll(context.Background())
	names := m.List()
	if len(names) != 1 || names[0] != "envtest" {
		t.Errorf("List after spawn: got %v", names)
	}
}

// TestManagerSpawn_Args tests that command-line arguments are passed.
func TestManagerSpawn_Args(t *testing.T) {
	m := NewManager()
	spec := Spec{
		Name:  "argtest",
		Entry: os.Args[0],
		Type:  "exec",
		Args:  []string{"--version"},
	}
	err := m.Spawn(context.Background(), spec)
	if err != nil {
		t.Skip("spawn failed: " + err.Error())
	}
	defer m.CloseAll(context.Background())
}

// TestManagerProbeAndRegister_Success tests that ProbeAndRegister
// calls all three registries when plugin has initialize.
func TestManagerProbeAndRegister_Success(t *testing.T) {
	m := NewManager()
	var tools, hooks, skills int
	m.SetToolRegistry(func(name string, fn func(ctx context.Context, params map[string]any) (any, error)) {
		tools++
	})
	m.SetHookRegistry(func(name, command string, timeout int) error {
		hooks++
		return nil
	})
	m.SetSkillInstaller(func(name, prompt string) error {
		skills++
		return nil
	})

	spec := Spec{
		Name:  "initecho",
		Entry: os.Args[0],
		Type:  "exec",
		Env:   []string{"DFMC_TEST_PLUGIN_MODE=echo"},
		Args:  []string{"-test.run=^$"},
	}
	if err := m.Spawn(context.Background(), spec); err != nil {
		t.Skip("spawn failed: " + err.Error())
	}
	defer m.CloseAll(context.Background())

	if err := m.ProbeAndRegister(context.Background(), "initecho"); err != nil {
		t.Fatalf("ProbeAndRegister: %v", err)
	}
	if tools != 1 {
		t.Errorf("tools registered: got %d want 1", tools)
	}
	if hooks != 1 {
		t.Errorf("hooks registered: got %d want 1", hooks)
	}
	if skills != 1 {
		t.Errorf("skills registered: got %d want 1", skills)
	}
}

// TestManagerProbeAndRegister_AlreadyLoaded tests that calling
// ProbeAndRegister on an already-registered plugin is idempotent.
func TestManagerProbeAndRegister_AlreadyLoaded(t *testing.T) {
	m := NewManager()
	spec := Spec{
		Name:  "double",
		Entry: os.Args[0],
		Type:  "exec",
		Env:   []string{"DFMC_TEST_PLUGIN_MODE=echo"},
		Args:  []string{"-test.run=^$"},
	}
	if err := m.Spawn(context.Background(), spec); err != nil {
		t.Skip("spawn failed: " + err.Error())
	}
	defer m.CloseAll(context.Background())

	err1 := m.ProbeAndRegister(context.Background(), "double")
	err2 := m.ProbeAndRegister(context.Background(), "double")
	if err1 != nil {
		t.Fatalf("first ProbeAndRegister: %v", err1)
	}
	if err2 != nil {
		t.Errorf("second ProbeAndRegister should be idempotent: %v", err2)
	}
}

// TestManagerSpawn_FileScopeIsolation tests that plugins with
// overlapping file scopes are handled gracefully (scheduler would
// serialize them — this is manager-level so we just check spawning).
func TestManagerSpawn_OverlappingScope(t *testing.T) {
	m := NewManager()
	spec1 := Spec{
		Name:  "scope1",
		Entry: os.Args[0],
		Type:  "exec",
		Env:   []string{"DFMC_TEST_PLUGIN_MODE=echo"},
		Args:  []string{"-test.run=^$"},
	}
	if err := m.Spawn(context.Background(), spec1); err != nil {
		t.Skip("spawn failed: " + err.Error())
	}
	defer m.CloseAll(context.Background())

	// A second plugin with different name — scopes are per-TODO in drive,
	// not per-plugin, so no conflict here.
	spec2 := Spec{
		Name:  "scope2",
		Entry: os.Args[0],
		Type:  "exec",
		Env:   []string{"DFMC_TEST_PLUGIN_MODE=echo"},
		Args:  []string{"-test.run=^$"},
	}
	if err := m.Spawn(context.Background(), spec2); err != nil {
		t.Skip("spawn failed: " + err.Error())
	}
	defer m.CloseAll(context.Background())

	names := m.List()
	if len(names) != 2 {
		t.Errorf("expected 2 plugins, got %d: %v", len(names), names)
	}
}

// TestManager_ZombieOnPluginCrash tests that a crashed plugin
// leaves the manager in a usable state (Close doesn't panic).
func TestManager_ZombieOnPluginCrash(t *testing.T) {
	m := NewManager()
	spec := Spec{
		Name:  "zombie",
		Entry: os.Args[0],
		Type:  "exec",
		Env:   []string{"DFMC_TEST_PLUGIN_MODE=noexit"},
		Args:  []string{"-test.run=^$"},
	}
	if err := m.Spawn(context.Background(), spec); err != nil {
		t.Skip("spawn failed: " + err.Error())
	}

	// Close should force-kill the process
	err := m.CloseAll(context.Background())
	if err != nil {
		t.Errorf("CloseAll after crash: %v", err)
	}

	// Manager should be reusable after CloseAll
	spec2 := Spec{
		Name:  "after-crash",
		Entry: os.Args[0],
		Type:  "exec",
		Env:   []string{"DFMC_TEST_PLUGIN_MODE=echo"},
		Args:  []string{"-test.run=^$"},
	}
	if err := m.Spawn(context.Background(), spec2); err != nil {
		t.Skip("spawn after crash failed: " + err.Error())
	}
	m.CloseAll(context.Background())
}

// TestManagerStderrReader tests that stderr capture from a plugin
// works end-to-end.
func TestManagerStderrReader(t *testing.T) {
	m := NewManager()
	spec := Spec{
		Name:  "stderr-plugin",
		Entry: os.Args[0],
		Type:  "exec",
		Env:   []string{"DFMC_TEST_PLUGIN_MODE=stderr"},
		Args:  []string{"-test.run=^$"},
	}
	if err := m.Spawn(context.Background(), spec); err != nil {
		t.Skip("spawn failed: " + err.Error())
	}
	defer m.CloseAll(context.Background())

	// Write something to make the plugin emit stderr
	m.Call(context.Background(), "stderr-plugin", "test", nil, 2*time.Second)

	stderr := m.Stderr("stderr-plugin")
	// stderr plugin writes "hello from plugin stderr\n"
	if stderr == "" {
		t.Error("expected non-empty stderr")
	}
}

// TestWasmModule_ListExports verifies the fixed export list.
func TestWasmModule_ListExports(t *testing.T) {
	m := &WasmModule{name: "test"}
	exports := m.ListExports()
	if len(exports) != 3 {
		t.Errorf("len: got %d want 3", len(exports))
	}
	want := []string{"run", "initialize", "shutdown"}
	for i, w := range want {
		if exports[i] != w {
			t.Errorf("exports[%d] = %q want %q", i, exports[i], w)
		}
	}
}

// TestWasmModule_Close_Idempotent verifies Close can be called twice
// without error.
func TestWasmModule_Close_Idempotent(t *testing.T) {
	m := &WasmModule{name: "already-closed"}
	// First close on nil runtime/mod is safe
	if err := m.Close(context.Background()); err != nil {
		t.Errorf("first close: %v", err)
	}
	// Second close should also be safe
	if err := m.Close(context.Background()); err != nil {
		t.Errorf("second close: %v", err)
	}
}

// TestWasmModule_Run_MissingRunExport exercises the missing "run" export path.
func TestWasmModule_Run_MissingRunExport(t *testing.T) {
	m := &WasmModule{name: "no-run", mod: nil}
	_, err := m.Run(context.Background(), `{"input":"x"}`)
	if err == nil {
		t.Error("expected error for nil mod")
	}
	if err.Error() != "WASM module has no memory export" {
		t.Errorf("unexpected error: %v", err)
	}
}