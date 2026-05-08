package pluginexec

// client_io.go — process startup (Spawn) + the two background
// goroutines (readLoop, drainStderr) + pending-call ledger helpers
// (clearPending, failAll). Sibling of client.go which keeps the
// public types, the RPC Call surface, Close, and Stderr accessor.
// client_spawn.go owns the argv/env helpers (resolveArgv, kindFromExt,
// firstAvailable, buildEnv) used by Spawn here.
//
// readLoop owns the response-dispatch path with the strict per-frame
// cap; drainStderr captures stderr into a bounded buffer for diagnostic
// logging; failAll lets either of those goroutines (or Close) fan a
// fatal error out to every pending Call so callers don't hang on a
// broken stream.

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
)

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
		cmd:           cmd,
		stdin:         stdin,
		stdout:        stdout,
		stderr:        stderr,
		pending:       map[int64]chan rpcResponse{},
		readDone:      make(chan struct{}),
		errDone:       make(chan struct{}),
		name:          strings.TrimSpace(spec.Name),
		maxFrameBytes: MaxFrameBytes,
	}
	go c.readLoop()
	go c.drainStderr()
	return c, nil
}

func (c *Client) readLoop() {
	defer close(c.readDone)
	// Strict line-delimited framing with a per-frame cap. The previous
	// json.NewDecoder path would have allocated up to memory exhaustion
	// for a single oversized document, taking down DFMC alongside the
	// misbehaving plugin. With Scanner + MaxFrameBytes a frame past the
	// cap surfaces as bufio.ErrTooLong; we fail all pending Calls and
	// stop reading because we can't recover the stream after a truncated
	// frame.
	frameCap := c.maxFrameBytes
	if frameCap <= 0 {
		frameCap = MaxFrameBytes
	}
	// See note in mcp/server.go's Serve: bufio.Scanner.Buffer's effective
	// cap is max(maxArg, cap(initBuf)), so the initial buffer must be ≤
	// frameCap or the limit silently rises. Otherwise the test override
	// path is a lie.
	initSize := 64 * 1024
	if initSize > frameCap {
		initSize = frameCap
	}
	sc := bufio.NewScanner(c.stdout)
	sc.Buffer(make([]byte, 0, initSize), frameCap)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var resp rpcResponse
		if err := json.Unmarshal(line, &resp); err != nil {
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
	if err := sc.Err(); err != nil {
		if errors.Is(err, io.EOF) {
			return
		}
		if errors.Is(err, bufio.ErrTooLong) {
			c.failAll(fmt.Errorf("plugin %q frame exceeded %d bytes; stream terminated", c.name, frameCap))
			return
		}
		c.failAll(fmt.Errorf("plugin read: %w", err))
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
