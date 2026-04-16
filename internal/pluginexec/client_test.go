package pluginexec

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
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
