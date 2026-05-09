package pluginexec

import (
	"context"
	"encoding/json"
	"time"
)

// PluginClient is the interface for interacting with a loaded plugin,
// regardless of whether it's a child process or a WASM module.
type PluginClient interface {
	// Call sends a JSON-RPC request to the plugin and returns the result.
	Call(ctx context.Context, method string, params any, timeout time.Duration) (json.RawMessage, error)

	// Close shuts down the plugin and releases resources.
	Close(ctx context.Context) error

	// Stderr returns any diagnostic output from the plugin.
	Stderr() string
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}
