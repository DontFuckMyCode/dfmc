package engine

import (
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
	// MCPToolBridge implements mcp.ToolBridge directly — no adapter needed.
	toolsEngine.SetMCPBridge(bridge)
	return nil
}