package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
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

	// stdinCloser wraps stdin so that ctx cancellation in sendSync can
	// unblock the reader goroutine by closing the connection from our
	// side. Without this, a ctx cancel would leave the reader goroutine
	// blocked on outBuf.ReadBytes('\n') forever if the server was slow
	// or unresponsive.
	stdinCloser io.Closer

	// callMu serializes a full request→response round-trip on the shared
	// stdio pipes. Without it, two concurrent CallTool invocations would
	// interleave their writes and each grab whichever response line
	// arrived first, mis-correlating responses to requests.
	callMu sync.Mutex

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
//
// VULN-032 fix: env values are validated for shell metacharacters
// before being passed to the subprocess to prevent command injection
// via malicious config (e.g. TERM=;rm -rf /).
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
		if hasShellMeta(v) {
			return nil, fmt.Errorf("env value for %q contains shell metacharacters", k)
		}
		envVars = append(envVars, k+"="+v)
	}
	cmd.Env = envVars
	// Leave stderr nil so server logs go to our stderr (inherit from parent)

	c := &Client{Name: name, cmd: cmd}
	return c, nil
}

// Start spawns the server process, wires the stdio pipes, runs the
// JSON-RPC initialize handshake (+ the `notifications/initialized`
// follow-up the spec requires before tool calls), and caches the tool
// list. After Start returns nil, ListTools/CallTool work. On any failure
// the process is torn down so a half-started client never lingers.
func (c *Client) Start(ctx context.Context) error {
	if c.closed.Load() {
		return errors.New("client closed")
	}
	c.mu.Lock()
	if c.stdin != nil {
		c.mu.Unlock()
		return nil // already started
	}
	stdin, err := c.cmd.StdinPipe()
	if err != nil {
		c.mu.Unlock()
		return fmt.Errorf("mcp %s: stdin pipe: %w", c.Name, err)
	}
	stdout, err := c.cmd.StdoutPipe()
	if err != nil {
		c.mu.Unlock()
		return fmt.Errorf("mcp %s: stdout pipe: %w", c.Name, err)
	}
	if err := c.cmd.Start(); err != nil {
		c.mu.Unlock()
		return fmt.Errorf("mcp %s: start process: %w", c.Name, err)
	}
	c.mu.Unlock()
	return c.connectStreams(ctx, stdin, stdout)
}

// connectStreams wires an already-open transport and runs the handshake +
// tools/list. Split out from Start so tests can drive the client over
// in-memory pipes against an in-process Server without spawning a real
// subprocess. On handshake failure it tears the client down.
func (c *Client) connectStreams(ctx context.Context, stdin io.WriteCloser, stdout io.Reader) error {
	c.mu.Lock()
	c.stdin = stdin
	c.stdout = stdout
	c.stdinCloser = stdin
	c.outBuf = bufio.NewReader(stdout)
	c.mu.Unlock()

	if err := c.handshake(ctx); err != nil {
		_ = c.Stop()
		return err
	}
	if err := c.refreshTools(ctx); err != nil {
		_ = c.Stop()
		return err
	}
	return nil
}

// handshake performs the MCP initialize exchange and sends the required
// `notifications/initialized` follow-up.
func (c *Client) handshake(ctx context.Context) error {
	req := Request{
		JSONRPC: "2.0",
		ID:      newID(),
		Method:  "initialize",
		Params: jsonMarshal(InitializeParams{
			ProtocolVersion: ProtocolVersion,
			ClientInfo:      ClientInfo{Name: "dfmc", Version: ProtocolVersion},
		}),
	}
	var resp Response
	if err := c.sendSync(ctx, &req, &resp); err != nil {
		return fmt.Errorf("mcp %s: initialize: %w", c.Name, err)
	}
	if resp.Error != nil {
		return fmt.Errorf("mcp %s: initialize error: %s", c.Name, resp.Error.Message)
	}
	// notifications/initialized — fire-and-forget; servers gate tools/* on it.
	note := Request{JSONRPC: "2.0", Method: "notifications/initialized"}
	c.callMu.Lock()
	c.mu.RLock()
	stdin := c.stdin
	c.mu.RUnlock()
	if stdin != nil {
		_ = json.NewEncoder(stdin).Encode(&note)
	}
	c.callMu.Unlock()
	return nil
}

// refreshTools issues tools/list and caches the result.
func (c *Client) refreshTools(ctx context.Context) error {
	req := Request{JSONRPC: "2.0", ID: newID(), Method: "tools/list"}
	var resp Response
	if err := c.sendSync(ctx, &req, &resp); err != nil {
		return fmt.Errorf("mcp %s: tools/list: %w", c.Name, err)
	}
	if resp.Error != nil {
		return fmt.Errorf("mcp %s: tools/list error: %s", c.Name, resp.Error.Message)
	}
	var lt ListToolsResult
	if err := jsonDecode(resp.Result, &lt); err != nil {
		return fmt.Errorf("mcp %s: decode tools/list: %w", c.Name, err)
	}
	c.mu.Lock()
	c.tools = lt.Tools
	c.mu.Unlock()
	return nil
}

// hasShellMeta reports whether s contains characters that have special
// meaning in POSIX shells and could be used for injection if the value
// is blindly passed to a shell subprocess. We deliberately exclude "="
// and whitespace so that valid values like "TERM=xterm-256color" or
// "PATH=/usr/local/bin:/usr/bin" are not flagged.
func hasShellMeta(s string) bool {
	return strings.ContainsAny(s, ";&|$`()<>\\|")
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

// sendSync sends a request and waits for its response. If ctx is
// cancelled, stdin is closed to unblock the reader goroutine, preventing
// a goroutine leak when the caller abandons the request mid-flight.
func (c *Client) sendSync(ctx context.Context, req *Request, resp *Response) error {
	// One round-trip at a time on the shared pipes (response correlation).
	c.callMu.Lock()
	defer c.callMu.Unlock()

	c.mu.RLock()
	stdin, outBuf, closer := c.stdin, c.outBuf, c.stdinCloser
	c.mu.RUnlock()
	if stdin == nil || outBuf == nil {
		return errors.New("client not started")
	}
	if err := json.NewEncoder(stdin).Encode(req); err != nil {
		return fmt.Errorf("send %s: %w", req.Method, err)
	}
	ch := make(chan error, 1)
	go func() {
		// Read lines until the response whose id matches our request.
		// Skip notifications (no id) and unrelated lines (server log
		// noise or a stray response) instead of mis-correlating them.
		for {
			raw, err := outBuf.ReadBytes('\n')
			if err != nil {
				ch <- err
				return
			}
			var probe Response
			if err := json.Unmarshal(raw, &probe); err != nil {
				continue // not a JSON-RPC line — ignore
			}
			if len(probe.ID) == 0 || string(probe.ID) == "null" {
				continue // notification — not our response
			}
			if len(req.ID) != 0 && !bytes.Equal(probe.ID, req.ID) {
				continue // a different request's response
			}
			*resp = probe
			ch <- nil
			return
		}
	}()
	select {
	case <-ctx.Done():
		// Closing stdin signals EOF to the server and unblocks the reader
		// goroutine (ReadBytes returns an error once the pipe closes),
		// preventing the goroutine leak that occurred when ctx cancelled
		// before the server responded.
		if closer != nil {
			_ = closer.Close()
		}
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
