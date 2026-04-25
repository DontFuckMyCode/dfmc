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

	mu      sync.RWMutex
	tools   []ToolDescriptor
	closed  atomic.Bool
	initErr error
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

// Start spawns the server process and runs the MCP handshake:
//  1. Create stdout pipe
//  2. Start the process
//  3. Bind stdin/stdout pipes
//  4. Send initialize, read response, send notifications/initialized
//  5. Send tools/list and cache the result
func (c *Client) Start(ctx context.Context) error {
	if c.closed.Load() {
		return errors.New("client already closed")
	}

	// Create stdout pipe before process start
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	c.cmd.Stdout = stdoutW

	if err := c.cmd.Start(); err != nil {
		stdoutR.Close()
		stdoutW.Close()
		return fmt.Errorf("start %s: %w", c.Name, err)
	}

	var err2 error
	c.stdin, err2 = c.cmd.StdinPipe()
	if err2 != nil {
		stdoutR.Close()
		stdoutW.Close()
		c.cmd.Process.Kill()
		return fmt.Errorf("stdin pipe: %w", err2)
	}
	c.stdout = stdoutR
	c.outBuf = bufio.NewReader(c.stdout)

	if err := c.handshake(ctx); err != nil {
		stdoutR.Close()
		stdoutW.Close()
		c.cmd.Process.Kill()
		c.stdin.Close()
		return fmt.Errorf("handshake: %w", err)
	}
	// stdoutW ownership transferred to the child process; only close on error paths above
	_ = stdoutW
	return nil
}

// Stop terminates the server process and releases resources.
func (c *Client) Stop() error {
	if c.closed.Swap(true) {
		return nil
	}
	if c.stdin != nil {
		c.stdin.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		c.cmd.Process.Kill()
		c.cmd.Wait()
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

// handshake runs the MCP initialize/initialized handshake and caches tools.
func (c *Client) handshake(ctx context.Context) error {
	// Use raw params to set serverInfo (the param name in the spec,
	// even though it's the CLIENT's own info — the struct field in
	// protocol.go is mislabeled ServerInfo but the JSON key is serverInfo)
	initParams := map[string]any{
		"protocolVersion": ProtocolVersion,
		"serverInfo": map[string]string{
			"name":    "dfmc-client",
			"version": "1.0",
		},
	}
	initReq := Request{
		JSONRPC: "2.0",
		ID:      newID(),
		Method:  "initialize",
		Params:  jsonMarshal(initParams),
	}
	var initResp Response
	if err := c.sendSync(ctx, &initReq, &initResp); err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	if initResp.Error != nil {
		return fmt.Errorf("server rejected initialize: %s", initResp.Error.Message)
	}

	// Client is ready — send the notifications/initialized notification
	c.sendNotification(Request{JSONRPC: "2.0", Method: "notifications/initialized"})

	// Fetch tool list
	var listResp Response
	listReq := Request{JSONRPC: "2.0", ID: newID(), Method: "tools/list"}
	if err := c.sendSync(ctx, &listReq, &listResp); err != nil {
		return fmt.Errorf("tools/list: %w", err)
	}
	if listResp.Error != nil {
		return fmt.Errorf("tools/list error: %s", listResp.Error.Message)
	}

	var listResult ListToolsResult
	if err := jsonDecode(listResp.Result, &listResult); err != nil {
		return fmt.Errorf("decode tools/list: %w", err)
	}

	c.mu.Lock()
	c.tools = listResult.Tools
	c.mu.Unlock()
	return nil
}

// sendSync sends a request and waits for its response.
func (c *Client) sendSync(ctx context.Context, req *Request, resp *Response) error {
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

// sendNotification sends a one-way JSON-RPC notification (no ID, no response).
func (c *Client) sendNotification(req Request) {
	_ = json.NewEncoder(c.stdin).Encode(req)
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