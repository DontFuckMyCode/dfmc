package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

// mcpStartTimeout bounds the initialize + tools/list handshake per server
// so one slow/hung external MCP server can't stall engine startup.
const mcpStartTimeout = 15 * time.Second

// ToolBridge is the interface between the MCP server and whatever tool
// registry it's hosting.
type ToolBridge interface {
	List() []ToolDescriptor
	Call(ctx context.Context, name string, arguments []byte) (CallToolResult, error)
}

// unknownToolError is returned when a tool name isn't found in any client.
type unknownToolError struct {
	Name string
}

func (e *unknownToolError) Error() string {
	return fmt.Sprintf("mcp: unknown tool: %s", e.Name)
}

// MCPToolBridge exposes multiple MCP clients as a single tool registry
// for the engine's MCP bridge adapter.
type MCPToolBridge struct {
	clients   []*Client
	toolIndex map[string]*Client // tool name → owning client
}

// NewMCPToolBridge builds a bridge over the given clients. The bridge
// maintains a flat index of all tools so Call can dispatch by name.
// Nil clients slice is safe — the bridge still works but every Call fails.
//
// Tool name collisions across clients are resolved first-wins: the earliest
// configured client owns the name, later clients exposing the same tool
// have their bindings dropped from toolIndex (List() de-duplicates the
// same way). A warning is logged naming both clients and the tool so the
// operator can either rename the conflicting tool, drop one of the servers,
// or reorder them in config to control which one wins. Before this guard
// List() leaked duplicate ToolDescriptor entries to the host (clients saw
// the same name twice in their tool picker) and Call routed last-wins,
// which contradicted what tools/list advertised.
func NewMCPToolBridge(clients []*Client) *MCPToolBridge {
	b := &MCPToolBridge{clients: clients, toolIndex: make(map[string]*Client)}
	if clients == nil {
		return b
	}
	for _, c := range clients {
		for _, td := range c.ListTools() {
			if existing, dup := b.toolIndex[td.Name]; dup {
				log.Printf("mcp: tool %q exposed by both %q and %q; keeping first-wins binding to %q. Rename or reorder in config to change precedence.", td.Name, existing.Name, c.Name, existing.Name)
				continue
			}
			b.toolIndex[td.Name] = c
		}
	}
	return b
}

// List returns the de-duplicated union of all tool descriptors from all
// clients. Duplicate names (across clients) are reported once with the
// first-seen client's descriptor; subsequent occurrences are dropped to
// match Call's first-wins routing.
func (b *MCPToolBridge) List() []ToolDescriptor {
	if b.clients == nil {
		return nil
	}
	if len(b.clients) == 0 {
		return []ToolDescriptor{}
	}
	seen := make(map[string]struct{})
	var out []ToolDescriptor
	for _, c := range b.clients {
		for _, td := range c.ListTools() {
			if _, dup := seen[td.Name]; dup {
				continue
			}
			seen[td.Name] = struct{}{}
			out = append(out, td)
		}
	}
	return out
}

// Close terminates every backing MCP client subprocess. Idempotent per
// client (Client.Stop short-circuits on its own closed flag), so calling
// Close twice on the bridge is safe. Errors from individual clients are
// joined so the caller sees every failure without one masking the others.
//
// Without this, engine.Shutdown left MCP server subprocesses orphaned —
// they kept reading from a closed stdin until the OS reaped them, which
// on Windows manifested as zombie cmd.exe / node.exe processes that
// outlived the parent. ReloadConfig made this worse: every config edit
// spawned a fresh set of clients, but the old set was never stopped, so
// long-running TUI sessions accumulated one stale subprocess tree per
// reload.
func (b *MCPToolBridge) Close() error {
	if b == nil {
		return nil
	}
	var errs []error
	for _, c := range b.clients {
		if c == nil {
			continue
		}
		if err := c.Stop(); err != nil {
			errs = append(errs, fmt.Errorf("mcp client %q stop: %w", c.Name, err))
		}
	}
	// Drop references so the bridge can't accidentally route a Call to a
	// stopped client after Close — Client.CallTool already guards via
	// c.closed, but clearing here gives a clean error path (unknownToolError)
	// rather than the "client closed" string match.
	b.clients = nil
	b.toolIndex = nil
	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}

// Call dispatches a tool by name to the owning client.
func (b *MCPToolBridge) Call(ctx context.Context, name string, arguments []byte) (CallToolResult, error) {
	c, ok := b.toolIndex[name]
	if !ok {
		return CallToolResult{}, &unknownToolError{Name: name}
	}
	var args map[string]any
	if arguments != nil {
		if err := json.Unmarshal(arguments, &args); err != nil {
			return CallToolResult{}, fmt.Errorf("malformed tool arguments: %w", err)
		}
	}
	return c.CallTool(ctx, name, args)
}

// LoadClientsFromConfig parses a list of MCP server configs and spawns
// clients for each. Returns empty slice on nil/empty config (no error).
func LoadClientsFromConfig(servers []config.MCPServerConfig) ([]*Client, error) {
	if servers == nil {
		return nil, nil
	}
	if len(servers) == 0 {
		return []*Client{}, nil
	}
	out := make([]*Client, 0, len(servers))
	for _, s := range servers {
		c, err := NewClient(s.Name, s.Command, s.Args, s.Env)
		if err != nil {
			return nil, fmt.Errorf("mcp server %q: %w", s.Name, err)
		}
		// Spawn the process and run the handshake. Best-effort: a server
		// that fails to start (bad command, handshake error, timeout) is
		// logged and skipped rather than aborting engine startup — the
		// other servers and all native tools must still come up.
		ctx, cancel := context.WithTimeout(context.Background(), mcpStartTimeout)
		startErr := c.Start(ctx)
		cancel()
		if startErr != nil {
			log.Printf("mcp: server %q failed to start, skipping: %v", s.Name, startErr)
			_ = c.Stop()
			continue
		}
		out = append(out, c)
	}
	return out, nil
}
