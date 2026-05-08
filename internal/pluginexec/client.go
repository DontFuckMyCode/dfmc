// Package pluginexec spawns script plugins as child processes and talks to
// them over line-delimited JSON-RPC 2.0 on stdin/stdout.
//
// A plugin is any executable or interpreted script. The interpreter is
// resolved from the manifest `type` field (python|node|shell|exec) or, when
// unset, from the entry file extension. The plugin process is expected to
// read JSON-RPC requests from stdin and write responses to stdout, one JSON
// object per line. stderr is captured by the client and made available for
// diagnostic logging — stderr is never parsed as data.
//
// The wire shape matches the MCP server in internal/mcp: DFMC is the client
// here, plugins are the server. Request IDs are int64 and monotonically
// increasing; responses may arrive out of order.
package pluginexec

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultCallTimeout caps a single Call if the caller passes 0.
const DefaultCallTimeout = 30 * time.Second

// ShutdownGrace is how long Close waits for the plugin to exit after
// stdin is closed before sending SIGKILL.
const ShutdownGrace = 2 * time.Second

// stderrBufferCap bounds how much of the plugin's stderr we retain.
// Anything past this is dropped to keep memory steady for long-running
// or noisy plugins.
const stderrBufferCap = 64 * 1024

// MaxFrameBytes caps a single plugin response frame. Plugins emit
// "one JSON object per line" per the package contract; 16 MiB is well
// past any legitimate tool result while bounding memory if a buggy or
// hostile plugin streams an unbounded document. A frame past the cap
// fails all pending Calls and stops the readLoop — the stream is
// unrecoverable because we can't re-sync after a truncated frame.
const MaxFrameBytes = 16 * 1024 * 1024

// Spec describes one plugin launch. Entry is the absolute (or workdir-
// relative) path to the script. Type overrides the extension-based
// interpreter pick — use it when the extension is ambiguous or the file
// has no extension.
type Spec struct {
	Name    string   // plugin name, used in error messages
	Entry   string   // path to the entry script/binary
	Type    string   // "python" | "node" | "shell" | "exec" | "" (auto)
	WorkDir string   // child cwd; defaults to dir of Entry
	Env     []string // extra env vars appended to the minimal allowlist
	Args    []string // extra args appended after the entry
}

// Client is a live connection to a running plugin process. Concurrent
// Call invocations are safe; responses are dispatched by request ID.
type Client struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser

	nextID   atomic.Int64
	writeMu  sync.Mutex
	pendMu   sync.Mutex
	pending  map[int64]chan rpcResponse
	readDone chan struct{}
	errDone  chan struct{}
	closed   atomic.Bool

	name      string
	stderrMu  sync.Mutex
	stderrBuf []byte

	// maxFrameBytes is the per-instance read cap, defaulting to
	// MaxFrameBytes via Spawn. Tests override directly to avoid having
	// to allocate 16+ MiB to exercise the cap path.
	maxFrameBytes int
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

// RPCError is the body of a JSON-RPC error reply. Exposed so callers can
// inspect the numeric code.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("plugin rpc error %d: %s", e.Code, e.Message)
}

// Call invokes `method` on the plugin with the given params and waits for
// the reply. A zero timeout uses DefaultCallTimeout. Returns the raw
// Result JSON on success; an *RPCError if the plugin reported a
// structured failure; or a transport/timeout error otherwise.
func (c *Client) Call(ctx context.Context, method string, params any, timeout time.Duration) (json.RawMessage, error) {
	if c.closed.Load() {
		return nil, fmt.Errorf("plugin %q: client closed", c.name)
	}
	if timeout <= 0 {
		timeout = DefaultCallTimeout
	}
	id := c.nextID.Add(1)
	req := rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	buf, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	ch := make(chan rpcResponse, 1)
	c.pendMu.Lock()
	c.pending[id] = ch
	c.pendMu.Unlock()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	// Bounded write phase. The naive `c.stdin.Write` blocks indefinitely
	// when the plugin has stopped reading its stdin (OS pipe buffer fills,
	// typically 4KB-64KB). Holding writeMu through that block used to
	// freeze EVERY subsequent Call on the client — the calling goroutine
	// never reached its select, so the per-Call timeout couldn't fire,
	// and the writeMu it held cascaded to block all other Calls' Lock()
	// at the head of this function. A single hung plugin took down the
	// entire client.
	//
	// We now run the write in a sub-goroutine and select against the
	// timer / ctx. If the timeout fires while the write is in flight we
	// close stdin: that unblocks the orphan goroutine (Write returns
	// ErrClosedPipe), it drains its err to the buffered werrCh and
	// exits. Subsequent Calls hit ErrClosedPipe immediately rather than
	// cascading on writeMu — the client is effectively dead but the
	// damage is bounded to one timeout window. The user is expected to
	// call Close to reap the process; stdin.Close is idempotent so the
	// double-close in Close is harmless.
	c.writeMu.Lock()
	payload := append(buf, '\n')
	werrCh := make(chan error, 1)
	go func() {
		_, err := c.stdin.Write(payload)
		werrCh <- err
	}()
	select {
	case werr := <-werrCh:
		c.writeMu.Unlock()
		if werr != nil {
			c.clearPending(id)
			return nil, fmt.Errorf("write request: %w", werr)
		}
	case <-ctx.Done():
		_ = c.stdin.Close()
		c.writeMu.Unlock()
		c.clearPending(id)
		return nil, ctx.Err()
	case <-timer.C:
		_ = c.stdin.Close()
		c.writeMu.Unlock()
		c.clearPending(id)
		return nil, fmt.Errorf("plugin %q method %q write timed out after %s", c.name, method, timeout)
	}

	// Response phase. Same timer covers the whole Call so total latency
	// honours the caller's `timeout`, not 2× of it.
	select {
	case <-ctx.Done():
		c.clearPending(id)
		return nil, ctx.Err()
	case <-timer.C:
		c.clearPending(id)
		return nil, fmt.Errorf("plugin %q method %q timed out after %s", c.name, method, timeout)
	case resp := <-ch:
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	}
}

// Close asks the plugin to exit by closing stdin. If the process does not
// exit within ShutdownGrace it is killed. Returns any wait error.
func (c *Client) Close(ctx context.Context) error {
	if c.closed.Swap(true) {
		return nil
	}
	_ = c.stdin.Close()

	done := make(chan error, 1)
	go func() { done <- c.cmd.Wait() }()

	timer := time.NewTimer(ShutdownGrace)
	defer timer.Stop()

	var waitErr error
	select {
	case waitErr = <-done:
	case <-timer.C:
		_ = c.cmd.Process.Kill()
		waitErr = fmt.Errorf("plugin %q did not exit within %s; killed", c.name, ShutdownGrace)
		<-done
	case <-ctx.Done():
		_ = c.cmd.Process.Kill()
		<-done
		waitErr = ctx.Err()
	}

	<-c.readDone
	<-c.errDone
	c.failAll(fmt.Errorf("plugin %q closed", c.name))
	return waitErr
}

// Stderr returns the bytes captured from the plugin's stderr so far
// (bounded to stderrBufferCap). Safe to call during or after the plugin's
// lifetime.
func (c *Client) Stderr() string {
	c.stderrMu.Lock()
	defer c.stderrMu.Unlock()
	return string(c.stderrBuf)
}


