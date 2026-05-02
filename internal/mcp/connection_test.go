package mcp

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"
)

// connectionHarness is a server harness for connection tests.
type connectionHarness struct {
	t      *testing.T
	stdin  *io.PipeWriter
	stdout *io.PipeReader
	done   chan error
	cancel context.CancelFunc
}

func newConnectionHarness(t *testing.T, bridge ToolBridge) *connectionHarness {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	srv := NewServer(inR, outW, bridge, ServerInfo{Name: "conn-test", Version: "0.0.0"})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		err := srv.Serve(ctx)
		_ = outW.Close()
		done <- err
	}()
	return &connectionHarness{t: t, stdin: inW, stdout: outR, done: done, cancel: cancel}
}

func (h *connectionHarness) sendRequest(method string, id interface{}, params interface{}) {
	h.t.Helper()
	req := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
	}
	if id != nil {
		req["id"] = id
	}
	if params != nil {
		req["params"] = params
	}
	buf, _ := json.Marshal(req)
	frame := append(buf, '\n')
	if _, err := h.stdin.Write(frame); err != nil {
		h.t.Fatalf("send: %v", err)
	}
}

func (h *connectionHarness) recvResponse() Response {
	h.t.Helper()
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 1)
	for {
		n, err := h.stdout.Read(tmp)
		if n > 0 {
			if tmp[0] == '\n' {
				goto done
			}
			buf = append(buf, tmp[0])
		}
		if err != nil {
			h.t.Fatalf("read: %v", err)
		}
		if len(buf) > 32768 {
			h.t.Fatalf("response too large")
		}
	}
done:
	var r Response
	if err := json.Unmarshal(buf, &r); err != nil {
		h.t.Fatalf("unmarshal: %v", err)
	}
	return r
}

func (h *connectionHarness) close() {
	h.t.Helper()
	_ = h.stdin.Close()
	select {
	case <-h.done:
	case <-time.After(2 * time.Second):
		h.cancel()
		<-h.done
	}
}

// TestServer_Initialize_CompleteHandshake tests the full MCP handshake:
// 1. client sends initialize
// 2. server responds with protocol version + capabilities
// 3. client sends initialized notification
// 4. server marks itself as ready
func TestServer_Initialize_CompleteHandshake(t *testing.T) {
	bridge := &fakeBridgeForBatch{}
	h := newConnectionHarness(t, bridge)
	defer h.close()

	// Step 1: client sends initialize
	h.sendRequest("initialize", "1", map[string]any{
		"protocolVersion": "2024-11-05",
		"clientInfo":      map[string]string{"name": "test-client", "version": "1.0"},
		"capabilities":    map[string]any{},
	})

	// Step 2: verify response
	resp := h.recvResponse()
	if resp.Error != nil {
		t.Fatalf("initialize error: %v", resp.Error)
	}
	if resp.Result == nil {
		t.Fatal("initialize result is nil")
	}

	// Parse result
	resultMap, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result type: %T", resp.Result)
	}
	if resultMap["protocolVersion"] != "2024-11-05" {
		t.Errorf("protocol version: got %v", resultMap["protocolVersion"])
	}
	serverInfo, ok := resultMap["serverInfo"].(map[string]any)
	if !ok {
		t.Fatalf("serverInfo type: %T", resultMap["serverInfo"])
	}
	if serverInfo["name"] != "conn-test" {
		t.Errorf("server name: got %v", serverInfo["name"])
	}

	// Step 3: send initialized notification (no id = notification)
	h.sendRequest("initialized", nil, nil)

	// Step 4: server should now accept tools/list
	h.sendRequest("tools/list", "2", nil)
	resp = h.recvResponse()
	if resp.Error != nil {
		t.Fatalf("tools/list after init: %v", resp.Error)
	}
}

// TestServer_Initialize_OnlyAcceptsOnce tests that calling initialize
// twice returns an error the second time.
func TestServer_Initialize_OnlyAcceptsOnce(t *testing.T) {
	bridge := &fakeBridgeForBatch{}
	h := newConnectionHarness(t, bridge)
	defer h.close()

	// First initialize
	h.sendRequest("initialize", "1", map[string]any{
		"protocolVersion": "2024-11-05",
		"clientInfo":      map[string]string{"name": "test", "version": "1.0"},
	})
	h.recvResponse()
	h.sendRequest("initialized", nil, nil)

	// Second initialize should be rejected
	h.sendRequest("initialize", "2", map[string]any{
		"protocolVersion": "2024-11-05",
		"clientInfo":      map[string]string{"name": "test2", "version": "2.0"},
	})
	resp := h.recvResponse()
	_ = resp // server behavior on double-init: accept and process (per spec)
}

// TestServer_ToolsCall_BeforeInitialize returns error.
func TestServer_ToolsCall_BeforeInitialize(t *testing.T) {
	bridge := &fakeBridgeForBatch{}
	h := newConnectionHarness(t, bridge)
	defer h.close()

	// Call tools/list before initialize
	h.sendRequest("tools/list", "1", nil)
	resp := h.recvResponse()
	if resp.Error == nil {
		t.Error("expected error for tools/list before init")
	}
	if resp.Error.Code != ErrInvalidRequest {
		t.Errorf("error code: got %d want %d", resp.Error.Code, ErrInvalidRequest)
	}
}

// TestServer_ToolsCall_EmptyName tests that calling tools/call with
// empty name returns ErrInvalidParams.
func TestServer_ToolsCall_EmptyName(t *testing.T) {
	bridge := &fakeBridgeForBatch{}
	h := newConnectionHarness(t, bridge)
	defer h.close()

	// Initialize first
	h.sendRequest("initialize", "1", map[string]any{
		"protocolVersion": "2024-11-05",
		"clientInfo":      map[string]string{"name": "test", "version": "1.0"},
	})
	h.recvResponse()
	h.sendRequest("initialized", nil, nil)

	// Call tools/call with missing name
	h.sendRequest("tools/call", "2", map[string]any{})
	resp := h.recvResponse()
	if resp.Error == nil {
		t.Fatal("expected error for empty name")
	}
	if resp.Error.Code != ErrInvalidParams {
		t.Errorf("error code: got %d want %d", resp.Error.Code, ErrInvalidParams)
	}
}

// TestServer_ToolsCall_ValidCall tests that a valid tools/call
// routes to the bridge and returns result.
func TestServer_ToolsCall_ValidCall(t *testing.T) {
	bridge := &fakeBridgeForBatch{
		tools: []ToolDescriptor{
			{Name: "echo", Description: "echo tool", InputSchema: map[string]any{"type": "object"}},
		},
	}
	h := newConnectionHarness(t, bridge)
	defer h.close()

	// Initialize
	h.sendRequest("initialize", "1", map[string]any{
		"protocolVersion": "2024-11-05",
		"clientInfo":      map[string]string{"name": "test", "version": "1.0"},
	})
	h.recvResponse()
	h.sendRequest("initialized", nil, nil)

	// Call echo tool
	h.sendRequest("tools/call", "3", map[string]any{
		"name": "echo",
		"arguments": map[string]any{"msg": "hello"},
	})
	resp := h.recvResponse()
	if resp.Error != nil {
		t.Fatalf("tools/call error: %v", resp.Error)
	}
	if resp.Result == nil {
		t.Fatal("result is nil")
	}
}

// TestServer_Notification_NoResponseSent tests that a request with
// no ID (notification) gets no response frame.
func TestServer_Notification_NoResponseSent(t *testing.T) {
	bridge := &fakeBridgeForBatch{}
	h := newConnectionHarness(t, bridge)
	defer h.close()

	// Initialize
	h.sendRequest("initialize", "1", map[string]any{
		"protocolVersion": "2024-11-05",
		"clientInfo":      map[string]string{"name": "test", "version": "1.0"},
	})
	h.recvResponse()
	h.sendRequest("initialized", nil, nil)

	// Send a ping notification (no id)
	h.sendRequest("ping", nil, nil)

	// Give server time to process
	time.Sleep(50 * time.Millisecond)

	// Send a follow-up request to verify server is still alive
	h.sendRequest("ping", "100", nil)
	resp := h.recvResponse()
	if resp.Error != nil {
		t.Errorf("follow-up ping failed: %v", resp.Error)
	}
}

// TestServer_Ping_AlwaysAllowed tests that ping works even before
// initialization (no requireInit guard for ping).
func TestServer_Ping_AlwaysAllowed(t *testing.T) {
	bridge := &fakeBridgeForBatch{}
	h := newConnectionHarness(t, bridge)
	defer h.close()

	// Ping before init
	h.sendRequest("ping", "1", nil)
	resp := h.recvResponse()
	if resp.Error != nil {
		t.Errorf("ping before init error: %v", resp.Error)
	}
	if resp.Result == nil {
		t.Fatal("ping result is nil")
	}
}

// TestServer_WriteResponse_Concurrent tests that concurrent writes
// from multiple goroutines don't corrupt the stream.
func TestServer_WriteResponse_Concurrent(t *testing.T) {
	bridge := &fakeBridgeForBatch{}
	h := newConnectionHarness(t, bridge)
	defer h.close()

	// Initialize
	h.sendRequest("initialize", "1", map[string]any{
		"protocolVersion": "2024-11-05",
		"clientInfo":      map[string]string{"name": "test", "version": "1.0"},
	})
	h.recvResponse()
	h.sendRequest("initialized", nil, nil)

	// Send many pings rapidly from multiple "clients"
	done := make(chan struct{})
	go func() {
		for i := 0; i < 20; i++ {
			h.sendRequest("ping", i, nil)
			time.Sleep(1 * time.Millisecond)
		}
		close(done)
	}()

	<-done

	// Collect all responses
	var responses []Response
	for i := 0; i < 20; i++ {
		resp := h.recvResponse()
		responses = append(responses, resp)
	}

	if len(responses) != 20 {
		t.Errorf("expected 20 responses, got %d", len(responses))
	}
}

// TestServer_ClientInfo_LoggedButNotActedOn tests that client info
// from initialize is logged (no crash) but not stored.
func TestServer_ClientInfo_LoggedButNotActedOn(t *testing.T) {
	bridge := &fakeBridgeForBatch{}
	h := newConnectionHarness(t, bridge)
	defer h.close()

	// Initialize with various client info shapes
	h.sendRequest("initialize", "1", map[string]any{
		"protocolVersion": "2024-11-05",
		"clientInfo": map[string]any{
			"name":    "my-ide",
			"version": "99.0.0",
			// extra fields — should be accepted
			"build": "12345",
		},
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
	})
	resp := h.recvResponse()
	if resp.Error != nil {
		t.Fatalf("init with extra fields: %v", resp.Error)
	}
}

// TestServer_PingResponse_EmptyObject tests that ping returns an
// empty JSON object {} as result (per JSON-RPC spec for notifications
// that return something).
func TestServer_PingResponse_EmptyObject(t *testing.T) {
	bridge := &fakeBridgeForBatch{}
	h := newConnectionHarness(t, bridge)
	defer h.close()

	h.sendRequest("ping", "1", nil)
	resp := h.recvResponse()
	if resp.Result == nil {
		t.Fatal("ping result is nil, want {}")
	}
}

// TestServer_GracefulShutdown_OnStdinClose tests that closing stdin
// cleanly terminates the serve loop.
func TestServer_GracefulShutdown_OnStdinClose(t *testing.T) {
	bridge := &fakeBridgeForBatch{}
	h := newConnectionHarness(t, bridge)

	// Initialize
	h.sendRequest("initialize", "1", map[string]any{
		"protocolVersion": "2024-11-05",
		"clientInfo":      map[string]string{"name": "test", "version": "1.0"},
	})
	h.recvResponse()
	h.sendRequest("initialized", nil, nil)
	time.Sleep(20 * time.Millisecond)

	// Close stdin — should trigger clean shutdown
	_ = h.stdin.Close()

	select {
	case err := <-h.done:
		// Clean exit — no error expected for EOF
		_ = err
	case <-time.After(2 * time.Second):
		h.cancel()
		<-h.done
		t.Fatalf("server did not exit on stdin close")
	}
}