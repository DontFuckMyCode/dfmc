package engine

import (
	"context"
	"encoding/json"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/mcp"
	"github.com/dontfuckmycode/dfmc/internal/tools"
)

// loadMCPClients starts MCP external servers from config and registers
// their tools with the engine. Called from engine.Init after Tools is
// constructed and all native tools are registered.
func loadMCPClients(cfg *config.Config, toolsEngine *tools.Engine) error {
	if cfg.MCP.Servers == nil || len(cfg.MCP.Servers) == 0 {
		return nil
	}
	clients, err := mcp.LoadClientsFromConfig(cfg.MCP.Servers)
	if err != nil {
		return err
	}
	if len(clients) == 0 {
		return nil
	}
	bridge := mcp.NewMCPToolBridge(clients)
	toolsEngine.SetMCPBridge(bridge)
	return nil
}

// mcpToolBridgeAdapter adapts mcp.ToolBridge to the tools.mcpToolBridge
// interface expected by the engine. This avoids importing the tools
// package into mcp, which would create a cycle.
type mcpToolBridgeAdapter struct {
	bridge mcp.ToolBridge
}

func (a *mcpToolBridgeAdapter) ListTools() []string {
	descriptors := a.bridge.List()
	names := make([]string, 0, len(descriptors))
	for _, d := range descriptors {
		names = append(names, d.Name)
	}
	return names
}

func (a *mcpToolBridgeAdapter) CallTool(ctx context.Context, name string, args map[string]any) (tools.Result, error) {
	var argBytes []byte
	if args != nil {
		argBytes, _ = json.Marshal(args)
	}
	result, err := a.bridge.Call(ctx, name, argBytes)
	if err != nil {
		return tools.Result{}, err
	}
	output := ""
	isError := result.IsError
	if len(result.Content) > 0 {
		for _, block := range result.Content {
			output += block.Text
		}
	}
	return tools.Result{
		Success: !isError,
		Output:  output,
	}, nil
}
