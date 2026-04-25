package mcp

import (
	"context"
	"encoding/json"
	"fmt"
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
	clients    []*Client
	toolIndex map[string]*Client // tool name → owning client
}

// NewMCPToolBridge builds a bridge over the given clients. The bridge
// maintains a flat index of all tools so Call can dispatch by name.
// Nil clients slice is safe — the bridge still works but every Call fails.
func NewMCPToolBridge(clients []*Client) *MCPToolBridge {
	b := &MCPToolBridge{clients: clients, toolIndex: make(map[string]*Client)}
	if clients == nil {
		return b
	}
	for _, c := range clients {
		for _, td := range c.ListTools() {
			b.toolIndex[td.Name] = c
		}
	}
	return b
}

// List returns the union of all tool descriptors from all clients.
func (b *MCPToolBridge) List() []ToolDescriptor {
	if b.clients == nil {
		return nil
	}
	var out []ToolDescriptor
	for _, c := range b.clients {
		out = append(out, c.ListTools()...)
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
		_ = json.Unmarshal(arguments, &args)
	}
	return c.CallTool(ctx, name, args)
}

// LoadClientsFromConfig parses a list of MCP server configs and spawns
// clients for each. Returns empty slice on nil/empty config (no error).
func LoadClientsFromConfig(servers []MCPConfig) ([]*Client, error) {
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

// MCPConfig describes one external MCP server to connect to.
type MCPConfig struct {
	Name    string
	Command string
	Args    []string
	Env     map[string]string
}

// Ensure tool index stays in sync after client creation.