package pluginexec

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestMain doubles the test binary as a JSON-RPC plugin when the
// DFMC_TEST_PLUGIN_MODE env var is set. This gives us a cross-platform
// plugin process for the tests without requiring python/node/bash on the
// test host.
func TestMain(m *testing.M) {
	switch os.Getenv("DFMC_TEST_PLUGIN_MODE") {
	case "echo":
		runEchoPlugin()
		return
	case "slow":
		runSlowPlugin()
		return
	case "error":
		runErrorPlugin()
		return
	case "malformed":
		runMalformedPlugin()
		return
	case "stderr":
		runStderrPlugin()
		return
	case "noexit":
		runNoexitPlugin()
		return
	}
	os.Exit(m.Run())
}

func runEchoPlugin() {
	dec := json.NewDecoder(bufio.NewReader(os.Stdin))
	enc := json.NewEncoder(os.Stdout)
	for {
		var req rpcRequest
		if err := dec.Decode(&req); err != nil {
			return
		}
		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result": map[string]any{
				"method": req.Method,
				"params": req.Params,
			},
		}
		_ = enc.Encode(resp)
	}
}

func runSlowPlugin() {
	dec := json.NewDecoder(bufio.NewReader(os.Stdin))
	for {
		var req rpcRequest
		if err := dec.Decode(&req); err != nil {
			return
		}
		// Sleep longer than any reasonable test timeout, never respond.
		time.Sleep(10 * time.Second)
		_ = req
	}
}

func runErrorPlugin() {
	dec := json.NewDecoder(bufio.NewReader(os.Stdin))
	enc := json.NewEncoder(os.Stdout)
	for {
		var req rpcRequest
		if err := dec.Decode(&req); err != nil {
			return
		}
		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"error": map[string]any{
				"code":    -32601,
				"message": "method not found: " + req.Method,
			},
		}
		_ = enc.Encode(resp)
	}
}

func runMalformedPlugin() {
	// Write garbage that is not valid JSON and exit.
	_, _ = os.Stdout.Write([]byte("this is not json\n"))
}

func runStderrPlugin() {
	dec := json.NewDecoder(bufio.NewReader(os.Stdin))
	enc := json.NewEncoder(os.Stdout)
	_, _ = os.Stderr.Write([]byte("hello from plugin stderr\n"))
	for {
		var req rpcRequest
		if err := dec.Decode(&req); err != nil {
			return
		}
		_ = enc.Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result":  "ok",
		})
	}
}

func runNoexitPlugin() {
	// Ignore stdin close, just sleep. Forces Close to kill the process.
	time.Sleep(60 * time.Second)
}

func spawnSelf(t *testing.T, mode string) *Client {
	t.Helper()
	spec := Spec{
		Name:  "test-" + mode,
		Entry: os.Args[0],
		Type:  "exec",
		Env:   []string{"DFMC_TEST_PLUGIN_MODE=" + mode},
		// Pass "-test.run=^$" so the binary doesn't try to run tests.
		Args: []string{"-test.run=^$"},
	}
	c, err := Spawn(context.Background(), spec)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	return c
}

func TestCallRoundTrip(t *testing.T) {
	c := spawnSelf(t, "echo")
	defer func() { _ = c.Close(context.Background()) }()

	raw, err := c.Call(context.Background(), "hello", map[string]any{"x": 42}, 5*time.Second)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["method"] != "hello" {
		t.Fatalf("unexpected method echo: %#v", got)
	}
	params, ok := got["params"].(map[string]any)
	if !ok {
		t.Fatalf("params missing or wrong shape: %#v", got["params"])
	}
	if fmt.Sprint(params["x"]) != "42" {
		t.Fatalf("param x not echoed: %#v", params)
	}
}

func TestConcurrentCallsByID(t *testing.T) {
	c := spawnSelf(t, "echo")
	defer func() { _ = c.Close(context.Background()) }()

	const n = 16
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			raw, err := c.Call(context.Background(), fmt.Sprintf("m%d", i), nil, 5*time.Second)
			if err != nil {
				errs <- err
				return
			}
			var got map[string]any
			if err := json.Unmarshal(raw, &got); err != nil {
				errs <- err
				return
			}
			want := fmt.Sprintf("m%d", i)
			if got["method"] != want {
				errs <- fmt.Errorf("mismatched echo: want %s got %v", want, got["method"])
				return
			}
			errs <- nil
		}()
	}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent call %d: %v", i, err)
		}
	}
}

func TestCallTimeout(t *testing.T) {
	c := spawnSelf(t, "slow")
	defer func() { _ = c.Close(context.Background()) }()

	_, err := c.Call(context.Background(), "anything", nil, 100*time.Millisecond)
	if err == nil {
		t.Fatalf("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout, got %v", err)
	}
}

func TestCallRPCError(t *testing.T) {
	c := spawnSelf(t, "error")
	defer func() { _ = c.Close(context.Background()) }()

	_, err := c.Call(context.Background(), "missing", nil, 5*time.Second)
	if err == nil {
		t.Fatalf("expected RPC error, got nil")
	}
	rpcErr, ok := err.(*RPCError)
	if !ok {
		t.Fatalf("want *RPCError, got %T: %v", err, err)
	}
	if rpcErr.Code != -32601 {
		t.Fatalf("want code -32601, got %d", rpcErr.Code)
	}
	if !strings.Contains(rpcErr.Message, "missing") {
		t.Fatalf("want message to contain method, got %q", rpcErr.Message)
	}
}

func TestRPCError_Error(t *testing.T) {
	// Nil RPCError should not panic
	var nilErr *RPCError
	if nilErr.Error() != "" {
		t.Error("nil Error() should return empty string")
	}

	// Non-nil error
	e := &RPCError{Code: -32601, Message: "method not found"}
	got := e.Error()
	if got == "" {
		t.Error("Error() returned empty string")
	}
	if !strings.Contains(got, "plugin rpc error") {
		t.Errorf("expected 'plugin rpc error' in message, got %q", got)
	}
	if !strings.Contains(got, "-32601") {
		t.Errorf("expected code in message, got %q", got)
	}
	if !strings.Contains(got, "method not found") {
		t.Errorf("expected message in output, got %q", got)
	}
}

func TestCallAfterClose(t *testing.T) {
	c := spawnSelf(t, "echo")
	if err := c.Close(context.Background()); err != nil {
		// Non-nil is fine if the process exited cleanly on stdin close;
		// but an error here should not be a wait failure. Accept any exit.
		t.Logf("close returned: %v", err)
	}
	_, err := c.Call(context.Background(), "foo", nil, 1*time.Second)
	if err == nil {
		t.Fatalf("expected error after close")
	}
}

func TestMalformedOutputFailsPending(t *testing.T) {
	c := spawnSelf(t, "malformed")
	defer func() { _ = c.Close(context.Background()) }()

	_, err := c.Call(context.Background(), "foo", nil, 2*time.Second)
	if err == nil {
		t.Fatalf("expected error from malformed stream")
	}
}

func TestStderrCaptured(t *testing.T) {
	c := spawnSelf(t, "stderr")
	_, err := c.Call(context.Background(), "ping", nil, 2*time.Second)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	// Give the drain goroutine a moment to swallow the stderr line.
	time.Sleep(100 * time.Millisecond)
	_ = c.Close(context.Background())
	if !strings.Contains(c.Stderr(), "hello from plugin stderr") {
		t.Fatalf("stderr not captured, got %q", c.Stderr())
	}
}

func TestCloseKillsStuckPlugin(t *testing.T) {
	c := spawnSelf(t, "noexit")
	start := time.Now()
	err := c.Close(context.Background())
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("expected kill-path error from unresponsive plugin")
	}
	if elapsed > ShutdownGrace+3*time.Second {
		t.Fatalf("close took too long: %s", elapsed)
	}
}

func TestResolveArgvByExt(t *testing.T) {
	cases := []struct {
		entry string
		want  string
	}{
		{"foo.py", "python"},
		{"foo.js", "node"},
		{"foo.mjs", "node"},
		{"foo.sh", "shell"},
		{"foo.exe", "exec"},
		{"foo", "exec"},
	}
	for _, tc := range cases {
		if got := kindFromExt(tc.entry); got != tc.want {
			t.Errorf("kindFromExt(%q) = %s, want %s", tc.entry, got, tc.want)
		}
	}
}

func TestResolveArgvExplicitTypeWins(t *testing.T) {
	// Type overrides extension — using "exec" for a .py file should NOT
	// route through python.
	argv, err := resolveArgv("/tmp/foo.py", "exec", nil)
	if err != nil {
		t.Fatalf("resolveArgv: %v", err)
	}
	if len(argv) != 1 || argv[0] != "/tmp/foo.py" {
		t.Fatalf("unexpected argv: %#v", argv)
	}
}

func TestSpawnRejectsMissingEntry(t *testing.T) {
	_, err := Spawn(context.Background(), Spec{Name: "x", Entry: ""})
	if err == nil {
		t.Fatalf("want error for empty entry")
	}
	_, err = Spawn(context.Background(), Spec{Name: "x", Entry: "/does/not/exist/here"})
	if err == nil {
		t.Fatalf("want error for missing entry")
	}
}

// blockingWriter is an io.WriteCloser that blocks Write forever until
// Close() is called. Used to simulate a plugin whose stdin pipe buffer
// is full because the plugin has stopped reading — the OS-level
// scenario the writeMu cascade fix addresses.
type blockingWriter struct {
	mu      sync.Mutex
	closed  bool
	release chan struct{}
}

func newBlockingWriter() *blockingWriter {
	return &blockingWriter{release: make(chan struct{})}
}

func (b *blockingWriter) Write(p []byte) (int, error) {
	<-b.release
	return 0, errClosedTestPipe
}

func (b *blockingWriter) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	close(b.release)
	return nil
}

var errClosedTestPipe = errClosedTestPipeT{}

type errClosedTestPipeT struct{}

func (errClosedTestPipeT) Error() string { return "test: stdin closed" }

// TestCall_WriteTimeoutDoesNotCascadeOnHungStdin regresses the writeMu
// freeze bug: pre-fix, holding writeMu during stdin.Write meant a hung
// plugin (one that stopped reading stdin, OS pipe buffer full) blocked
// the calling goroutine forever — it never reached its select on the
// timer, and the writeMu it held cascaded to every subsequent Call.
//
// We construct a minimal Client with a stdin that blocks forever and
// no readLoop running (the test only exercises the write path; response
// phase is expected to time out via the same timer). Two assertions:
//   - First Call: returns within ~timeout, not after the OS gives up.
//   - Second Call: also returns fast (writeMu was released by the
//     timeout branch closing stdin; the second Call's own write
//     attempt returns ErrClosedPipe immediately).
//
// If the cascade bug regressed, the second Call would block on
// writeMu acquisition until the orphan goroutine eventually returned —
// which in the original code path was "never". The 1s ceiling here is
// generous; the actual budget is 2x the per-Call 80ms.
func TestCall_WriteTimeoutDoesNotCascadeOnHungStdin(t *testing.T) {
	bw := newBlockingWriter()
	defer bw.Close()
	c := &Client{
		stdin:    bw,
		pending:  map[int64]chan rpcResponse{},
		readDone: make(chan struct{}),
		errDone:  make(chan struct{}),
		name:     "blocked",
	}

	const perCallTimeout = 80 * time.Millisecond
	start := time.Now()
	_, err := c.Call(context.Background(), "first", nil, perCallTimeout)
	firstElapsed := time.Since(start)
	if err == nil {
		t.Fatalf("first Call should have timed out, got nil")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("first Call: expected timeout error, got %v", err)
	}
	if firstElapsed > 1*time.Second {
		t.Fatalf("first Call took %v — write phase is not bounded", firstElapsed)
	}

	// Second Call: pre-fix this would block forever on writeMu (held by
	// the orphan goroutine of Call 1). Post-fix, the timeout branch in
	// Call 1 closed stdin and released writeMu, so this Call acquires
	// writeMu fast and its own Write returns ErrClosedPipe immediately.
	start = time.Now()
	_, err = c.Call(context.Background(), "second", nil, perCallTimeout)
	secondElapsed := time.Since(start)
	if err == nil {
		t.Fatalf("second Call should have errored on closed stdin, got nil")
	}
	if secondElapsed > 1*time.Second {
		t.Fatalf("second Call cascaded on writeMu: took %v", secondElapsed)
	}
}

// TestReadLoop_FrameSizeCapFailsPending regresses the symmetric
// memory-exhaustion risk on the response side: a buggy or hostile
// plugin emitting an unbounded JSON document would have OOM-ed DFMC
// pre-fix because json.NewDecoder allocates as needed. Now the
// readLoop uses bufio.Scanner with a per-Client maxFrameBytes cap;
// an oversized frame fails all pending Calls with a clear error and
// stops the readLoop (transport unrecoverable past a truncated frame).
//
// We construct a minimal Client with stdout fed by an io.Pipe whose
// writer side gets an oversized frame, override maxFrameBytes to 256
// bytes (so we don't allocate the production 16 MiB to exercise the
// cap path), enqueue one pending Call's response channel, and start
// the readLoop. Assertions:
//   - the pending Call's channel receives an *RPCError whose message
//     mentions the frame cap (failAll wraps the read error)
//   - readLoop exits (readDone closes) so a subsequent Close has
//     something to wait on
func TestReadLoop_FrameSizeCapFailsPending(t *testing.T) {
	pr, pw := io.Pipe()
	c := &Client{
		stdout:        pr,
		pending:       map[int64]chan rpcResponse{},
		readDone:      make(chan struct{}),
		errDone:       make(chan struct{}),
		name:          "huge-emitter",
		maxFrameBytes: 256,
	}

	// Pre-register a pending Call so failAll has something to fail.
	respCh := make(chan rpcResponse, 1)
	c.pending[1] = respCh

	// Run readLoop in the background.
	go c.readLoop()

	// Push an oversized frame in another goroutine — same deadlock-avoidance
	// pattern as the MCP server cap test. Scanner buffers up to 256 bytes,
	// errors with bufio.ErrTooLong, exits without draining the rest.
	go func() {
		big := bytes.Repeat([]byte("B"), 1024)
		frame := append([]byte(`{"jsonrpc":"2.0","id":1,"result":"`), big...)
		frame = append(frame, []byte(`"}`)...)
		frame = append(frame, '\n')
		_, _ = pw.Write(frame)
	}()

	// Wait for the pending Call to be failed.
	select {
	case resp := <-respCh:
		if resp.Error == nil {
			t.Fatalf("expected RPCError on pending Call, got success: %+v", resp)
		}
		if !strings.Contains(resp.Error.Message, "frame exceeded") {
			t.Fatalf("error message should mention frame cap, got %q", resp.Error.Message)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("readLoop did not fail pending Call within timeout")
	}

	// Close the writer so readLoop's pending Read unblocks if it hasn't
	// already exited. readDone should be closed promptly.
	_ = pw.Close()
	select {
	case <-c.readDone:
	case <-time.After(2 * time.Second):
		t.Fatalf("readLoop did not exit after oversized frame")
	}
}

// TestCall_CtxCancelDuringWriteUnblocks confirms ctx cancellation
// reaches the write phase. Pre-fix, a cancelled ctx couldn't escape
// stdin.Write; post-fix the ctx.Done() branch of the write-phase
// select fires and closes stdin to release the orphan.
func TestCall_CtxCancelDuringWriteUnblocks(t *testing.T) {
	bw := newBlockingWriter()
	defer bw.Close()
	c := &Client{
		stdin:    bw,
		pending:  map[int64]chan rpcResponse{},
		readDone: make(chan struct{}),
		errDone:  make(chan struct{}),
		name:     "blocked-ctx",
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(40 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := c.Call(ctx, "blocked", nil, 5*time.Second)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("expected ctx.Err() from cancelled write")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if elapsed > 1*time.Second {
		t.Fatalf("Call did not honour ctx during write: ran %v", elapsed)
	}
}
