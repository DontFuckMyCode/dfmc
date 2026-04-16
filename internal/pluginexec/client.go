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
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// Spawn starts the plugin process and wires its stdin/stdout/stderr to the
// returned Client. Callers must invoke Close when done; a forgotten plugin
// leaks a child process.
func Spawn(ctx context.Context, spec Spec) (*Client, error) {
	if strings.TrimSpace(spec.Entry) == "" {
		return nil, fmt.Errorf("plugin entry is required")
	}
	abs, err := filepath.Abs(spec.Entry)
	if err != nil {
		return nil, fmt.Errorf("resolve entry: %w", err)
	}
	if _, err := os.Stat(abs); err != nil {
		return nil, fmt.Errorf("entry not found: %w", err)
	}
	argv, err := resolveArgv(abs, spec.Type, spec.Args)
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	if strings.TrimSpace(spec.WorkDir) != "" {
		cmd.Dir = spec.WorkDir
	} else {
		cmd.Dir = filepath.Dir(abs)
	}
	cmd.Env = buildEnv(spec.Env)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("plugin stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("plugin stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("plugin stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start plugin: %w", err)
	}

	c := &Client{
		cmd:      cmd,
		stdin:    stdin,
		stdout:   stdout,
		stderr:   stderr,
		pending:  map[int64]chan rpcResponse{},
		readDone: make(chan struct{}),
		errDone:  make(chan struct{}),
		name:     strings.TrimSpace(spec.Name),
	}
	go c.readLoop()
	go c.drainStderr()
	return c, nil
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

	c.writeMu.Lock()
	_, werr := c.stdin.Write(append(buf, '\n'))
	c.writeMu.Unlock()
	if werr != nil {
		c.clearPending(id)
		return nil, fmt.Errorf("write request: %w", werr)
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
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

func (c *Client) readLoop() {
	defer close(c.readDone)
	dec := json.NewDecoder(bufio.NewReader(c.stdout))
	for {
		var resp rpcResponse
		if err := dec.Decode(&resp); err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			// Decode failure — can't recover the stream without re-framing.
			// Fail all outstanding calls and stop reading.
			c.failAll(fmt.Errorf("plugin decode: %w", err))
			return
		}
		c.pendMu.Lock()
		ch, ok := c.pending[resp.ID]
		if ok {
			delete(c.pending, resp.ID)
		}
		c.pendMu.Unlock()
		if !ok {
			// Stray or out-of-order frame (e.g. notification, unexpected id).
			continue
		}
		ch <- resp
	}
}

func (c *Client) drainStderr() {
	defer close(c.errDone)
	buf := make([]byte, 4096)
	for {
		n, err := c.stderr.Read(buf)
		if n > 0 {
			c.stderrMu.Lock()
			remaining := stderrBufferCap - len(c.stderrBuf)
			if remaining > 0 {
				if n > remaining {
					n = remaining
				}
				c.stderrBuf = append(c.stderrBuf, buf[:n]...)
			}
			c.stderrMu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func (c *Client) clearPending(id int64) {
	c.pendMu.Lock()
	delete(c.pending, id)
	c.pendMu.Unlock()
}

func (c *Client) failAll(err error) {
	c.pendMu.Lock()
	defer c.pendMu.Unlock()
	for id, ch := range c.pending {
		select {
		case ch <- rpcResponse{ID: id, Error: &RPCError{Code: -32000, Message: err.Error()}}:
		default:
		}
		delete(c.pending, id)
	}
}

// resolveArgv picks the interpreter and builds the argv slice for the
// given entry. Explicit `kind` wins over file extension.
func resolveArgv(entry, kind string, extra []string) ([]string, error) {
	k := strings.ToLower(strings.TrimSpace(kind))
	if k == "" {
		k = kindFromExt(entry)
	}
	switch k {
	case "exec", "binary", "executable":
		return append([]string{entry}, extra...), nil
	case "python", "py":
		interp := firstAvailable("python3", "python")
		if interp == "" {
			return nil, fmt.Errorf("python interpreter not found on PATH")
		}
		return append([]string{interp, entry}, extra...), nil
	case "node", "javascript", "js":
		interp := firstAvailable("node")
		if interp == "" {
			return nil, fmt.Errorf("node interpreter not found on PATH")
		}
		return append([]string{interp, entry}, extra...), nil
	case "shell", "sh", "bash":
		interp := firstAvailable("bash", "sh")
		if interp == "" {
			return nil, fmt.Errorf("bash/sh interpreter not found on PATH")
		}
		return append([]string{interp, entry}, extra...), nil
	default:
		return append([]string{entry}, extra...), nil
	}
}

func kindFromExt(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".py":
		return "python"
	case ".js", ".mjs", ".cjs":
		return "node"
	case ".sh", ".bash":
		return "shell"
	case ".exe", "":
		return "exec"
	}
	return "exec"
}

func firstAvailable(candidates ...string) string {
	for _, c := range candidates {
		if p, err := exec.LookPath(c); err == nil {
			return p
		}
	}
	return ""
}

func buildEnv(extra []string) []string {
	passthrough := []string{
		"PATH", "HOME", "USERPROFILE", "SYSTEMROOT", "TEMP", "TMP",
		"LANG", "LC_ALL", "LC_CTYPE",
	}
	base := make([]string, 0, len(passthrough)+len(extra)+1)
	for _, k := range passthrough {
		if v, ok := os.LookupEnv(k); ok {
			base = append(base, k+"="+v)
		}
	}
	base = append(base, "DFMC_PLUGIN=1")
	base = append(base, extra...)
	return base
}
