// Package pluginexec hosts the plugin executor and manager. This file
// provides a WebAssembly plugin loader using wazero. WASM plugins are
// loaded from .wasm files and execute in a sandboxed environment with
// access to a limited host API (DFMC plugin host).
package pluginexec

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// WasmSpec describes a WASM plugin to load.
type WasmSpec struct {
	Name   string // plugin name
	Module []byte // raw WASM binary
}

// WasmModule wraps a wazero compiled module and provides a sandboxed
// execution environment for a single WASM plugin.
type WasmModule struct {
	name    string
	runtime wazero.Runtime
	mod     api.Module
	mu      sync.Mutex
}

// WasmLoader creates a wazero runtime, compiles the module, instantiates
// it, and returns a WasmModule handle. The caller must call Close when done.
func WasmLoader(ctx context.Context, spec WasmSpec) (*WasmModule, error) {
	if len(spec.Module) == 0 {
		return nil, errors.New("WASM module is empty")
	}
	if spec.Name == "" {
		return nil, errors.New("WASM plugin name is required")
	}

	runtime := wazero.NewRuntime(ctx)

	// Instantiate the module with an empty config.
	compiled, err := runtime.CompileModule(ctx, spec.Module)
	if err != nil {
		runtime.Close(ctx)
		return nil, fmt.Errorf("compile WASM module: %w", err)
	}

	mod, err := runtime.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().WithName(spec.Name))
	if err != nil {
		runtime.Close(ctx)
		return nil, fmt.Errorf("instantiate WASM module: %w", err)
	}

	return &WasmModule{
		name:    spec.Name,
		runtime: runtime,
		mod:     mod,
	}, nil
}

// Run calls the "run" export of the WASM module with the given argument JSON.
// The "run" export must have the signature (i32, i32) -> (i32, i32), where
// each pair is (pointer, length) into WASM linear memory for argument and result.
// It returns the result string or an error.
func (m *WasmModule) Run(ctx context.Context, arg string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	mem := m.mod.Memory()
	if mem == nil {
		return "", errors.New("WASM module has no memory export")
	}

	// Allocate argument in WASM linear memory at offset 0.
	// We write the string bytes directly.
	if !mem.Write(0, []byte(arg)) {
		return "", errors.New("WASM memory write failed for argument")
	}
	argLen := uint32(len(arg))

	// Write arg ptr (0) and argLen at offsets 0 and 4.
	_ = mem.WriteUint32Le(0, 0)
	_ = mem.WriteUint32Le(4, argLen)

	run := m.mod.ExportedFunction("run")
	if run == nil {
		return "", errors.New("WASM module has no \"run\" export")
	}

	results, err := run.Call(ctx, 0, uint64(argLen))
	if err != nil {
		return "", fmt.Errorf("WASM run: %w", err)
	}

	if len(results) < 2 {
		return "", nil
	}

	resPtr := uint32(results[0])
	resLen := uint32(results[1])

	if resPtr == 0 || resLen == 0 {
		return "", nil
	}

	buf, ok := mem.Read(resPtr, resLen)
	if !ok {
		return "", errors.New("WASM memory read failed for result")
	}
	return string(buf), nil
}

// Close releases the WASM module.
func (m *WasmModule) Close(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.runtime != nil {
		m.runtime.Close(ctx)
		m.runtime = nil
		m.mod = nil
	}
	return nil
}

// ListExports returns the names of known plugin exports.
func (m *WasmModule) ListExports() []string {
	return []string{"run", "initialize", "shutdown"}
}