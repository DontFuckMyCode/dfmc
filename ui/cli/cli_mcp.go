package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/internal/mcp"
	"github.com/dontfuckmycode/dfmc/internal/tools"
)

// runMCP serves the DFMC tool registry over MCP on stdio. IDE hosts launch
// the binary with `dfmc mcp`, pipe JSON-RPC 2.0 frames to stdin, and read
// frames from stdout. Diagnostics go to stderr so the transport stream
// stays clean.
func runMCP(ctx context.Context, eng *engine.Engine, args []string, version string) int {
	if len(args) > 0 {
		switch strings.ToLower(strings.TrimSpace(args[0])) {
		case "--help", "-h", "help":
			printMCPHelp()
			return 0
		default:
			fmt.Fprintf(os.Stderr, "mcp: unexpected argument %q (accepts no flags)\n", args[0])
			return 2
		}
	}
	if eng == nil || eng.Tools == nil {
		fmt.Fprintln(os.Stderr, "mcp: engine tools not initialized")
		return 1
	}

	bridge := &engineMCPBridge{eng: eng}
	srv := mcp.NewServer(os.Stdin, os.Stdout, bridge, mcp.ServerInfo{
		Name:    "dfmc",
		Version: version,
	})
	fmt.Fprintf(os.Stderr, "dfmc mcp: serving %d tools on stdio (proto %s)\n", len(bridge.List()), mcp.ProtocolVersion)
	if err := srv.Serve(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "mcp: %v\n", err)
		return 1
	}
	return 0
}

func printMCPHelp() {
	fmt.Println(`Usage: dfmc mcp

Serves DFMC's tool registry to MCP-compatible IDE hosts (Claude Desktop,
Cursor, VSCode) over stdio using JSON-RPC 2.0.

The command takes no flags — the host launches the binary, DFMC reads
requests from stdin and writes responses to stdout. Diagnostics are
printed to stderr.

Example Claude Desktop config snippet:

  {
    "mcpServers": {
      "dfmc": {
        "command": "dfmc",
        "args": ["mcp"]
      }
    }
  }`)
}

// engineMCPBridge exposes *engine.Engine as a mcp.ToolBridge. Routes Call
// through engine.CallTool so every MCP-driven invocation goes through the
// same approval gate, hooks dispatch, and panic guard as a CLI/TUI/web
// call. Without this routing, a tool panic raised by an MCP-initiated
// invocation would crash the dfmc mcp process and the IDE host would see
// a broken stdio transport instead of a tool_result with isError=true.
type engineMCPBridge struct {
	eng *engine.Engine
}

func (b *engineMCPBridge) List() []mcp.ToolDescriptor {
	// Expose backend tools only — the meta surface (tool_search/tool_help/
	// tool_call/tool_batch_call) exists to keep prompt-token cost flat for
	// DFMC's own agent loop. Over MCP the host already drives discovery,
	// so the indirection would just waste round-trips.
	if b.eng == nil || b.eng.Tools == nil {
		return nil
	}
	specs := b.eng.Tools.BackendSpecs()
	out := make([]mcp.ToolDescriptor, 0, len(specs))
	for _, s := range specs {
		out = append(out, mcp.ToolDescriptor{
			Name:        s.Name,
			Description: bridgeDescription(s),
			InputSchema: s.JSONSchema(),
		})
	}
	return out
}

func (b *engineMCPBridge) Call(ctx context.Context, name string, rawArgs []byte) (mcp.CallToolResult, error) {
	if b.eng == nil {
		return mcp.CallToolResult{}, fmt.Errorf("engine not initialized")
	}
	params := map[string]any{}
	if len(rawArgs) > 0 {
		if err := json.Unmarshal(rawArgs, &params); err != nil {
			return mcp.CallToolResult{}, fmt.Errorf("decode arguments: %w", err)
		}
	}
	// CallTool funnels through executeToolWithLifecycle → panic guard.
	// A panic inside the tool is converted to err here, so the MCP
	// transport never sees a process crash.
	res, err := b.eng.CallTool(ctx, name, params)
	if err != nil {
		return mcp.CallToolResult{
			Content: []mcp.ContentBlock{mcp.TextContent(err.Error())},
			IsError: true,
		}, nil
	}
	return mcp.CallToolResult{
		Content: []mcp.ContentBlock{mcp.TextContent(formatCallOutput(res))},
		IsError: !res.Success,
	}, nil
}

func bridgeDescription(s tools.ToolSpec) string {
	summary := strings.TrimSpace(s.Summary)
	if summary == "" {
		summary = strings.TrimSpace(s.Purpose)
	}
	if summary == "" {
		return s.Name
	}
	return summary
}

func formatCallOutput(res tools.Result) string {
	out := strings.TrimSpace(res.Output)
	if out == "" && len(res.Data) > 0 {
		if buf, err := json.MarshalIndent(res.Data, "", "  "); err == nil {
			out = string(buf)
		}
	}
	if out == "" {
		out = "(no output)"
	}
	return out
}
