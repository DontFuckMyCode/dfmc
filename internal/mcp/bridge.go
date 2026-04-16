package mcp

import "context"

// ToolBridge is the adapter between the MCP server and whatever tool
// registry it's hosting. Kept as an interface so `internal/mcp` does not
// import `internal/tools` directly — the engine-side adapter lives in
// `ui/cli/mcp.go` where the Engine is already in scope.
type ToolBridge interface {
	// List returns every tool the bridge wants to expose over MCP.
	List() []ToolDescriptor
	// Call dispatches one tool invocation. Raw arguments are passed through
	// so the bridge can normalise them the same way the native agent loop
	// does. Return a non-nil error only for true invocation failures (tool
	// missing, runtime panic); tool-reported failures should come back as a
	// CallToolResult with IsError:true and the error text in Content.
	Call(ctx context.Context, name string, arguments []byte) (CallToolResult, error)
}
