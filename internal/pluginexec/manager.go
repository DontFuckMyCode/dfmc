// Package pluginexec hosts the plugin executor and the plugin Manager that
// owns all loaded plugin clients. Callers outside this package use Manager
// exclusively — raw Client operations are internal.
package pluginexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"
)

// ManageReq is the request shape for dfmc plugin run <name>.
type ManageReq struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// ManageResp is the response shape for dfmc plugin run.
type ManageResp struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  *RPCError       `json:"error,omitempty"`
}

// PluginManager owns all running plugin clients and provides lifecycle ops.
type Manager struct {
	mu      sync.RWMutex
	clients map[string]*Client // keyed by plugin name

	// engine-side registries that plugins can extend
	toolRegistry   func(name string, fn func(ctx context.Context, params map[string]any) (any, error))
	hookRegistry   func(name, command string, timeout int) error
	skillInstaller func(name, prompt string) error

	onClose func(name string)
}

func NewManager() *Manager {
	return &Manager{clients: make(map[string]*Client)}
}

// SetToolRegistry injects the engine's tool registry setter.
// Must be called before any plugin loads.
func (m *Manager) SetToolRegistry(fn func(name string, fn func(ctx context.Context, params map[string]any) (any, error))) {
	m.toolRegistry = fn
}

// SetHookRegistry injects the engine's hook registration function.
func (m *Manager) SetHookRegistry(fn func(name, command string, timeout int) error) {
	m.hookRegistry = fn
}

// SetSkillInstaller injects the engine's skill installer.
func (m *Manager) SetSkillInstaller(fn func(name, prompt string) error) {
	m.skillInstaller = fn
}

// SetOnClose registers a callback invoked (without the manager lock held)
// after each plugin's Close completes.
func (m *Manager) SetOnClose(fn func(name string)) {
	m.onClose = fn
}

// Spawn starts a new plugin client from the given spec and stores it under
// its assigned name. Returns an error if a plugin with that name is already
// running. The plugin process is left to run independently; callers must
// call Close(name) to stop it.
func (m *Manager) Spawn(ctx context.Context, spec Spec) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.clients[spec.Name]; ok {
		return fmt.Errorf("plugin %q already running", spec.Name)
	}
	c, err := Spawn(ctx, spec)
	if err != nil {
		return err
	}
	m.clients[spec.Name] = c
	return nil
}

// Call sends a JSON-RPC request to the named plugin and returns the raw
// result. It returns an error if the plugin is not loaded or the call fails.
func (m *Manager) Call(ctx context.Context, name, method string, params any, timeout ...time.Duration) (json.RawMessage, error) {
	m.mu.RLock()
	c, ok := m.clients[name]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("plugin %q not loaded", name)
	}
	var t time.Duration
	if len(timeout) > 0 {
		t = timeout[0]
	}
	return c.Call(ctx, method, params, t)
}

// Close stops a running plugin by name and removes it from the manager.
func (m *Manager) Close(ctx context.Context, name string) error {
	m.mu.Lock()
	c, ok := m.clients[name]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("plugin %q not loaded", name)
	}
	delete(m.clients, name)
	m.mu.Unlock()

	err := c.Close(ctx)
	if m.onClose != nil {
		m.onClose(name)
	}
	return err
}

// List returns the names of all currently loaded plugins, sorted.
func (m *Manager) List() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, 0, len(m.clients))
	for n := range m.clients {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Stderr returns the accumulated stderr output for a named plugin.
func (m *Manager) Stderr(name string) string {
	m.mu.RLock()
	c, ok := m.clients[name]
	m.mu.RUnlock()
	if !ok {
		return ""
	}
	return c.Stderr()
}

// CloseAll stops all running plugins. Errors are collected but the first
// error is returned after attempting to close every plugin.
func (m *Manager) CloseAll(ctx context.Context) error {
	m.mu.Lock()
	var clients []*Client
	for _, c := range m.clients {
		clients = append(clients, c)
	}
	m.clients = make(map[string]*Client)
	m.mu.Unlock()

	var errs []error
	for _, c := range clients {
		if err := c.Close(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

// ProbeAndRegister sends the handshake to a newly spawned plugin and
// registers any announced tools/hooks/skills with the engine registries.
// It is safe to call on a plugin that has already started.
func (m *Manager) ProbeAndRegister(ctx context.Context, name string) error {
	m.mu.RLock()
	c, ok := m.clients[name]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("plugin %q not loaded", name)
	}

	var caps struct {
		Capabilities struct {
			Tools  []string `json:"tools"`
			Hooks  []string `json:"hooks"`
			Skills []string `json:"skills"`
		} `json:"capabilities"`
	}
	raw, err := c.Call(ctx, "initialize", nil, 5*time.Second)
	if err != nil {
		// A plugin that doesn't implement `initialize` returns a JSON-RPC
		// `-32601 method not found` reply. The intent has always been that
		// this is non-fatal — the plugin just exposes zero capabilities and
		// we move on. The previous code declared ErrPluginNoInitialize but
		// nothing ever returned it, so the errors.Is check below was dead
		// and a no-initialize plugin was rejected at registration. Convert
		// the *RPCError shape here so the sentinel becomes load-bearing.
		var rpcErr *RPCError
		if errors.As(err, &rpcErr) && rpcErr.Code == jsonRPCMethodNotFound {
			err = ErrPluginNoInitialize
		}
		if !errors.Is(err, ErrPluginNoInitialize) {
			return fmt.Errorf("plugin initialize: %w", err)
		}
	}
	if raw != nil {
		if err := json.Unmarshal(raw, &caps); err != nil {
			log.Printf("pluginexec: %s initialize response unparseable, registering 0 capabilities: %v", name, err)
		}
	}

	if m.toolRegistry != nil && len(caps.Capabilities.Tools) > 0 {
		for _, tool := range caps.Capabilities.Tools {
			// Defers to the engine's tool registry; the plugin function
			// is a closure that proxies through Manager.Call.
			toolName := tool
			m.toolRegistry(toolName, func(ctx context.Context, params map[string]any) (any, error) {
				return m.Call(ctx, name, "tool."+toolName, params)
			})
		}
	}

	if m.hookRegistry != nil && len(caps.Capabilities.Hooks) > 0 {
		for _, hook := range caps.Capabilities.Hooks {
			// Hooks are registered as shell commands that delegate to the plugin.
			h := hook
			if err := m.hookRegistry(h, fmt.Sprintf("dfmc plugin run %s hook.%s", name, h), 30); err != nil {
				log.Printf("pluginexec: %s hook %q registration failed: %v", name, h, err)
			}
		}
	}

	if m.skillInstaller != nil && len(caps.Capabilities.Skills) > 0 {
		for _, skill := range caps.Capabilities.Skills {
			s := skill
			if err := m.skillInstaller(s, fmt.Sprintf("[[skill:%s]]", s)); err != nil {
				log.Printf("pluginexec: %s skill %q install failed: %v", name, s, err)
			}
		}
	}

	return nil
}

// jsonRPCMethodNotFound is the standard JSON-RPC code for "method not
// found". Duplicated here as a private constant to avoid an import cycle
// from pluginexec to internal/mcp (which owns the canonical ErrMethodNotFound
// constant). Both packages honor the same JSON-RPC 2.0 wire convention.
const jsonRPCMethodNotFound = -32601

// ErrPluginNoInitialize is the wrapped error ProbeAndRegister substitutes
// for an `initialize` reply that came back as `-32601 method not found`.
// Use errors.Is(err, ErrPluginNoInitialize) to distinguish "plugin chose
// not to implement initialize" (non-fatal — capabilities are empty) from
// other transport / protocol failures (fatal). Before this sentinel was
// wired, ProbeAndRegister rejected any plugin without an initialize method.
var ErrPluginNoInitialize = errors.New("plugin does not implement initialize")
