package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeBridge is an in-memory stand-in for the tool registry. Call behaviour
// is driven by the callFn field so each test can shape the response.
type fakeBridge struct {
	tools   []ToolDescriptor
	callFn  func(ctx context.Context, name string, args []byte) (CallToolResult, error)
	calls   atomic.Int32
	lastRaw atomic.Value // []byte
}

func (b *fakeBridge) List() []ToolDescriptor { return b.tools }

func (b *fakeBridge) Call(ctx context.Context, name string, args []byte) (CallToolResult, error) {
	b.calls.Add(1)
	cp := append([]byte(nil), args...)
	b.lastRaw.Store(cp)
	if b.callFn == nil {
		return CallToolResult{Content: []ContentBlock{TextContent("ok")}}, nil
	}
	return b.callFn(ctx, name, args)
}

// serverHarness wires a Server onto a pair of io.Pipes so tests can write
// framed requests and read framed responses. Returned `stop` cancels Serve
// and waits for the goroutine to return, making leak detection trivial.
type serverHarness struct {
	t      *testing.T
	stdin  *io.PipeWriter
	stdout *bufio.Reader
	done   chan error
	cancel context.CancelFunc
}

func newHarness(t *testing.T, bridge ToolBridge) *serverHarness {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	srv := NewServer(inR, outW, bridge, ServerInfo{Name: "dfmc-test", Version: "0.0.0"})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		err := srv.Serve(ctx)
		_ = outW.Close()
		done <- err
	}()
	return &serverHarness{
		t:      t,
		stdin:  inW,
		stdout: bufio.NewReader(outR),
		done:   done,
		cancel: cancel,
	}
}

func (h *serverHarness) send(v any) {
	h.t.Helper()
	buf, err := json.Marshal(v)
	if err != nil {
		h.t.Fatalf("marshal: %v", err)
	}
	// io.Pipe is synchronous: two back-to-back writes deadlock once the
	// server pauses to emit a response. Send body + newline as a single
	// Write so the server drains it in one Read before replying.
	frame := append(buf, '\n')
	if _, err := h.stdin.Write(frame); err != nil {
		h.t.Fatalf("stdin write: %v", err)
	}
}

func (h *serverHarness) recv() Response {
	h.t.Helper()
	line, err := h.stdout.ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		h.t.Fatalf("stdout read: %v", err)
	}
	line = []byte(strings.TrimSpace(string(line)))
	if len(line) == 0 {
		h.t.Fatalf("empty response frame")
	}
	var r Response
	if err := json.Unmarshal(line, &r); err != nil {
		h.t.Fatalf("decode response %q: %v", string(line), err)
	}
	return r
}

func (h *serverHarness) close() {
	h.t.Helper()
	_ = h.stdin.Close()
	select {
	case <-h.done:
	case <-time.After(2 * time.Second):
		h.cancel()
		<-h.done
		h.t.Fatalf("server did not exit after stdin close")
	}
}

func rawID(v any) json.RawMessage {
	buf, _ := json.Marshal(v)
	return buf
}

// decodeResult copies the Response.Result payload into `dst`. Needed because
// json.Unmarshal into `any` makes every nested map map[string]any; tests
// want their strongly-typed result.
func decodeResult(t *testing.T, resp Response, dst any) {
	t.Helper()
	if resp.Result == nil {
		t.Fatalf("response has no result: %+v", resp)
	}
	buf, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("remarshal result: %v", err)
	}
	if err := json.Unmarshal(buf, dst); err != nil {
		t.Fatalf("decode result: %v", err)
	}
}

// ---------- tests ----------

// TestServer_FrameSizeCapRejectsOversized regresses a memory-exhaustion
// risk: pre-fix Serve used json.NewDecoder which would happily allocate
// up to RAM-exhaustion for a single multi-GB document. A buggy or
// hostile MCP server emitting unbounded JSON could OOM DFMC.
//
// We override maxFrameBytes to 256 bytes so the test doesn't have to
// allocate the production 16 MiB to exercise the cap, then send a 1 KiB
// frame. The server must:
//   - reply with a parse-error response carrying the cap-mention message
//   - exit Serve with bufio.ErrTooLong (transport unrecoverable past a
//     truncated frame; we can't re-sync mid-stream)
//
// io.Pipe is synchronous so a single 1 KiB Write would deadlock against
// a 256-byte Scanner buffer (Scanner errors after reading the cap and
// stops reading; the writer blocks forever on the unread tail). We do
// the write in a goroutine and let it leak if Scanner abandons the
// stream — that's the production failure mode anyway.
func TestServer_FrameSizeCapRejectsOversized(t *testing.T) {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	srv := NewServer(inR, outW, &fakeBridge{}, ServerInfo{Name: "test", Version: "0.0.0"})
	srv.maxFrameBytes = 256
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		err := srv.Serve(ctx)
		_ = outW.Close()
		done <- err
	}()

	// Push the oversized frame in a goroutine — the writer will block
	// forever on the tail bytes once Scanner gives up and stops reading.
	// We unblock it via inW.Close in the cleanup at the end.
	big := bytes.Repeat([]byte("A"), 1024)
	frame := append([]byte(`{"jsonrpc":"2.0","id":1,"method":"x","params":"`), big...)
	frame = append(frame, []byte(`"}`)...)
	frame = append(frame, '\n')
	go func() {
		_, _ = inW.Write(frame)
	}()

	// The server should emit one error response then close out.
	br := bufio.NewReader(outR)
	type readResult struct {
		line []byte
		err  error
	}
	rch := make(chan readResult, 1)
	go func() {
		l, e := br.ReadBytes('\n')
		rch <- readResult{l, e}
	}()
	var rr readResult
	select {
	case rr = <-rch:
	case <-time.After(2 * time.Second):
		t.Fatalf("server did not emit a parse-error response")
	}
	if rr.err != nil && !errors.Is(rr.err, io.EOF) {
		t.Fatalf("read response: %v", rr.err)
	}
	if len(bytes.TrimSpace(rr.line)) == 0 {
		t.Fatalf("expected parse-error response, got empty")
	}
	var resp Response
	if err := json.Unmarshal(bytes.TrimSpace(rr.line), &resp); err != nil {
		t.Fatalf("decode response %q: %v", string(rr.line), err)
	}
	if resp.Error == nil {
		t.Fatalf("expected error response, got success: %+v", resp)
	}
	if resp.Error.Code != ErrParseError {
		t.Fatalf("expected ErrParseError (%d), got %d", ErrParseError, resp.Error.Code)
	}
	if !strings.Contains(resp.Error.Message, "frame exceeded") {
		t.Fatalf("error message should mention frame cap, got %q", resp.Error.Message)
	}

	// Closing inW unblocks the goroutine writer (returns ErrClosedPipe).
	_ = inW.Close()
	select {
	case serveErr := <-done:
		if serveErr == nil {
			t.Fatalf("Serve returned nil; expected bufio.ErrTooLong propagation")
		}
		if !errors.Is(serveErr, bufio.ErrTooLong) {
			t.Fatalf("Serve returned %v, want bufio.ErrTooLong", serveErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("Serve did not exit after oversized-frame rejection")
	}
}

func TestServerInitializeHandshake(t *testing.T) {
	h := newHarness(t, &fakeBridge{})
	defer h.close()

	h.send(Request{
		JSONRPC: "2.0",
		ID:      rawID(1),
		Method:  "initialize",
		Params: mustMarshal(t, InitializeParams{
			ProtocolVersion: "2024-11-05",
			ClientInfo:      ClientInfo{Name: "test-client", Version: "1.0.0"},
		}),
	})

	resp := h.recv()
	if resp.Error != nil {
		t.Fatalf("initialize error: %+v", resp.Error)
	}
	var r InitializeResult
	decodeResult(t, resp, &r)
	if r.ProtocolVersion != ProtocolVersion {
		t.Fatalf("protocol version=%q want %q", r.ProtocolVersion, ProtocolVersion)
	}
	if r.ServerInfo.Name != "dfmc-test" {
		t.Fatalf("server name=%q want dfmc-test", r.ServerInfo.Name)
	}
	if r.Capabilities.Tools == nil {
		t.Fatalf("tools capability missing")
	}
}

func TestServerToolsListAfterInit(t *testing.T) {
	bridge := &fakeBridge{
		tools: []ToolDescriptor{
			{Name: "echo", Description: "repeat input", InputSchema: map[string]any{"type": "object"}},
			{Name: "ping", Description: "ping", InputSchema: map[string]any{"type": "object"}},
		},
	}
	h := newHarness(t, bridge)
	defer h.close()

	h.send(Request{JSONRPC: "2.0", ID: rawID(1), Method: "initialize"})
	_ = h.recv()
	h.send(Request{JSONRPC: "2.0", ID: rawID(2), Method: "tools/list"})

	resp := h.recv()
	if resp.Error != nil {
		t.Fatalf("tools/list error: %+v", resp.Error)
	}
	var lr ListToolsResult
	decodeResult(t, resp, &lr)
	if len(lr.Tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(lr.Tools))
	}
	if lr.Tools[0].Name != "echo" || lr.Tools[1].Name != "ping" {
		t.Fatalf("unexpected tool order: %+v", lr.Tools)
	}
}

func TestServerToolsListRequiresInit(t *testing.T) {
	h := newHarness(t, &fakeBridge{})
	defer h.close()

	h.send(Request{JSONRPC: "2.0", ID: rawID(1), Method: "tools/list"})
	resp := h.recv()
	if resp.Error == nil {
		t.Fatalf("expected error before init, got %+v", resp)
	}
	if resp.Error.Code != ErrInvalidRequest {
		t.Fatalf("code=%d want %d", resp.Error.Code, ErrInvalidRequest)
	}
}

func TestServerToolsCallHappy(t *testing.T) {
	bridge := &fakeBridge{
		callFn: func(ctx context.Context, name string, args []byte) (CallToolResult, error) {
			if name != "echo" {
				t.Fatalf("unexpected tool name=%q", name)
			}
			// The server forwards raw argument bytes verbatim; confirm the
			// payload we sent round-trips unchanged.
			var argsMap map[string]any
			if err := json.Unmarshal(args, &argsMap); err != nil {
				t.Fatalf("decode args: %v", err)
			}
			if argsMap["msg"] != "hello" {
				t.Fatalf("unexpected args: %v", argsMap)
			}
			return CallToolResult{Content: []ContentBlock{TextContent("echo: hello")}}, nil
		},
	}
	h := newHarness(t, bridge)
	defer h.close()

	h.send(Request{JSONRPC: "2.0", ID: rawID(1), Method: "initialize"})
	_ = h.recv()
	h.send(Request{
		JSONRPC: "2.0",
		ID:      rawID(2),
		Method:  "tools/call",
		Params: mustMarshal(t, CallToolParams{
			Name:      "echo",
			Arguments: json.RawMessage(`{"msg":"hello"}`),
		}),
	})

	resp := h.recv()
	if resp.Error != nil {
		t.Fatalf("tools/call error: %+v", resp.Error)
	}
	var cr CallToolResult
	decodeResult(t, resp, &cr)
	if cr.IsError {
		t.Fatalf("unexpected IsError=true; content=%v", cr.Content)
	}
	if len(cr.Content) != 1 || cr.Content[0].Text != "echo: hello" {
		t.Fatalf("unexpected content: %+v", cr.Content)
	}
	if bridge.calls.Load() != 1 {
		t.Fatalf("bridge call count=%d want 1", bridge.calls.Load())
	}
}

func TestServerToolsCallBridgeErrorReported(t *testing.T) {
	bridge := &fakeBridge{
		callFn: func(ctx context.Context, name string, args []byte) (CallToolResult, error) {
			return CallToolResult{}, errors.New("boom")
		},
	}
	h := newHarness(t, bridge)
	defer h.close()

	h.send(Request{JSONRPC: "2.0", ID: rawID(1), Method: "initialize"})
	_ = h.recv()
	h.send(Request{
		JSONRPC: "2.0",
		ID:      rawID(2),
		Method:  "tools/call",
		Params:  mustMarshal(t, CallToolParams{Name: "nope"}),
	})

	resp := h.recv()
	if resp.Error != nil {
		t.Fatalf("expected success frame with IsError:true, got rpc error: %+v", resp.Error)
	}
	var cr CallToolResult
	decodeResult(t, resp, &cr)
	if !cr.IsError {
		t.Fatalf("expected IsError=true")
	}
	if len(cr.Content) == 0 || !strings.Contains(cr.Content[0].Text, "boom") {
		t.Fatalf("error text missing: %+v", cr.Content)
	}
}

func TestServerToolsCallMissingName(t *testing.T) {
	h := newHarness(t, &fakeBridge{})
	defer h.close()

	h.send(Request{JSONRPC: "2.0", ID: rawID(1), Method: "initialize"})
	_ = h.recv()
	h.send(Request{
		JSONRPC: "2.0",
		ID:      rawID(2),
		Method:  "tools/call",
		Params:  json.RawMessage(`{}`),
	})

	resp := h.recv()
	if resp.Error == nil || resp.Error.Code != ErrInvalidParams {
		t.Fatalf("expected InvalidParams, got %+v", resp)
	}
}

func TestServerMethodNotFound(t *testing.T) {
	h := newHarness(t, &fakeBridge{})
	defer h.close()

	h.send(Request{JSONRPC: "2.0", ID: rawID(1), Method: "nosuch/method"})
	resp := h.recv()
	if resp.Error == nil || resp.Error.Code != ErrMethodNotFound {
		t.Fatalf("expected MethodNotFound, got %+v", resp)
	}
}

func TestServerNotificationProducesNoResponse(t *testing.T) {
	bridge := &fakeBridge{}
	h := newHarness(t, bridge)
	defer h.close()

	// Send initialize (request), read response, then send a notification and
	// a follow-up request. If the notification produced a response the
	// follow-up response would arrive second — we'd then see the
	// notification's frame first and fail the decode.
	h.send(Request{JSONRPC: "2.0", ID: rawID(1), Method: "initialize"})
	_ = h.recv()
	h.send(Request{JSONRPC: "2.0", Method: "notifications/initialized"})
	h.send(Request{JSONRPC: "2.0", ID: rawID(2), Method: "ping"})

	resp := h.recv()
	if resp.Error != nil {
		t.Fatalf("ping error: %+v", resp.Error)
	}
	if string(resp.ID) != "2" {
		t.Fatalf("expected ID=2 (notification must be silent), got %s", string(resp.ID))
	}
}

func TestServerInvalidJSONRPCVersionRejected(t *testing.T) {
	h := newHarness(t, &fakeBridge{})
	defer h.close()

	h.send(map[string]any{"jsonrpc": "1.0", "id": 1, "method": "initialize"})
	resp := h.recv()
	if resp.Error == nil || resp.Error.Code != ErrInvalidRequest {
		t.Fatalf("expected InvalidRequest for jsonrpc=1.0, got %+v", resp)
	}
}

func TestServerPingWorksBeforeInit(t *testing.T) {
	// ping is defined to work pre-init; everything else requires the handshake.
	h := newHarness(t, &fakeBridge{})
	defer h.close()

	h.send(Request{JSONRPC: "2.0", ID: rawID(1), Method: "ping"})
	resp := h.recv()
	if resp.Error != nil {
		t.Fatalf("ping should not error pre-init: %+v", resp.Error)
	}
}

func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	buf, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return buf
}
