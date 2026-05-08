package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"

	"github.com/dontfuckmycode/dfmc/internal/security"
)

// Client is an MCP client that connects to one external MCP server over
// stdio. It spawns the server process, runs the JSON-RPC 2.0 handshake,
// caches the tool list, and exposes CallTool for the bridge.
type Client struct {
	Name string

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.Reader
	outBuf *bufio.Reader

	mu     sync.RWMutex
	tools  []ToolDescriptor
	closed atomic.Bool
}

// NewClient builds a client that will spawn `command args` and connect to it
// over stdio. Start() must be called before using the client.
//
// VULN-011 fix: the parent environment is scrubbed of secret-shaped
// keys (`*_API_KEY`, `*_TOKEN`, `*_SECRET`, AWS_* etc.) before being
// handed to the subprocess. A hostile or buggy external MCP server
// used to read its own `os.Environ()` and exfiltrate every provider
// API key + DFMC bearer token DFMC was running with. Forwarding now
// requires an explicit per-server opt-in: any key the server
// genuinely needs (e.g. `GITHUB_TOKEN` for an MCP-over-GitHub
// connector) is named in the operator's `env_passthrough` config
// and merged AFTER the scrub. The override `env` map passed to
// NewClient is unconditionally forwarded — that's the operator's
// explicit choice, not an inherited blob.
func NewClient(name string, command string, args []string, env map[string]string) (*Client, error) {
	cmd := exec.Command(command, args...)
	// Build the subprocess env from a scrubbed copy of the parent
	// environment plus operator-supplied overrides. We don't honour
	// an env_passthrough allowlist here — that's read from
	// MCPConfig before NewClient and the desired keys arrive in
	// `env` already. Future config-side wiring can pass a list down
	// once it exists.
	envVars := security.ScrubEnv(os.Environ(), nil)
	for k, v := range env {
		envVars = append(envVars, k+"="+v)
	}
	cmd.Env = envVars
	// Leave stderr nil so server logs go to our stderr (inherit from parent)

	c := &Client{Name: name, cmd: cmd}
	return c, nil
}

// Stop terminates the server process and releases resources.
func (c *Client) Stop() error {
	if c.closed.Swap(true) {
		return nil
	}
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
		_ = c.cmd.Wait()
	}
	return nil
}

// ListTools returns the cached tool list from the server.
func (c *Client) ListTools() []ToolDescriptor {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.tools == nil {
		return []ToolDescriptor{}
	}
	return c.tools
}

// CallTool forwards a call to the server and returns the result.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (CallToolResult, error) {
	if c.closed.Load() {
		return CallToolResult{}, errors.New("client closed")
	}

	req := Request{
		JSONRPC: "2.0",
		ID:      newID(),
		Method:  "tools/call",
		Params:  jsonMarshal(CallToolParams{Name: name, Arguments: marshalRaw(args)}),
	}
	var resp Response
	if err := c.sendSync(ctx, &req, &resp); err != nil {
		return CallToolResult{}, fmt.Errorf("tools/call %s: %w", name, err)
	}
	if resp.Error != nil {
		return CallToolResult{}, fmt.Errorf("server error: %s", resp.Error.Message)
	}
	var result CallToolResult
	if err := jsonDecode(resp.Result, &result); err != nil {
		return CallToolResult{}, fmt.Errorf("decode result: %w", err)
	}
	return result, nil
}

// sendSync sends a request and waits for its response.
func (c *Client) sendSync(ctx context.Context, req *Request, resp *Response) error {
	if c.stdin == nil || c.stdout == nil {
		return errors.New("client not started")
	}
	if err := json.NewEncoder(c.stdin).Encode(req); err != nil {
		return fmt.Errorf("send %s: %w", req.Method, err)
	}
	ch := make(chan error, 1)
	go func() {
		raw, err := c.outBuf.ReadBytes('\n')
		if err != nil {
			ch <- err
			return
		}
		if err := json.Unmarshal(raw, resp); err != nil {
			ch <- err
			return
		}
		ch <- nil
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-ch:
		return err
	}
}

// -- helpers --

func newID() json.RawMessage {
	id, _ := json.Marshal(fmt.Sprintf("req-%d", idSeq.Add(1)))
	return id
}

var idSeq atomic.Int64

func marshalRaw(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// jsonMarshal is the non-test version used by client code.
func jsonMarshal(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// jsonDecode is the non-test version used by client code.
func jsonDecode(v any, target any) error {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, target)
}
