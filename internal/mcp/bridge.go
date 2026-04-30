package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

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
	if servers == nil || len(servers) == 0 {
		return nil, nil
	}
	out := make([]*Client, 0, len(servers))
	for _, s := range servers {
		c, err := NewClient(s.Name, s.Command, s.Args, s.Env)
		if err != nil {
			return nil, fmt.Errorf("mcp server %q: %w", s.Name, err)
		}
		out = append(out, c)
	}
	return out, nil
}
