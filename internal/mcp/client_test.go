package mcp

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

// Tests for client.go public API surface.

// Tests for LoadClientsFromConfig — uses real config parsing via NewClient.
// These live here (client_test.go) because NewClient lives in client.go
// and the LoadClientsFromConfig tests need the same harness.

func TestLoadClientsFromConfig_EmptySlice(t *testing.T) {
	clients, err := LoadClientsFromConfig([]config.MCPServerConfig{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if clients == nil {
		t.Error("empty slice should return empty slice, not nil")
	}
	if len(clients) != 0 {
		t.Errorf("len: got %d", len(clients))
	}
}

// LoadClientsFromConfig now spawns AND handshakes each server. A command
// that isn't a real MCP server (here `echo`, which ignores stdin and exits)
// fails the initialize handshake and must be skipped — best-effort, no error,
// so one bad server never aborts engine startup or the other servers.
func TestLoadClientsFromConfig_NonMCPServerIsSkipped(t *testing.T) {
	cfg := []config.MCPServerConfig{
		{Name: "test-echo", Command: "echo", Args: []string{"ok"}, Env: nil},
	}
	clients, err := LoadClientsFromConfig(cfg)
	if err != nil {
		t.Fatalf("LoadClientsFromConfig must not error on a bad server: %v", err)
	}
	if len(clients) != 0 {
		t.Fatalf("a non-MCP command must be skipped, got %d clients", len(clients))
	}
}

// TestClient_HandshakeAndCall is the end-to-end proof that the client
// integration actually works: connect it (over in-memory pipes) to an
// in-process MCP Server, and verify the handshake populates the tool list
// and CallTool round-trips to the server's bridge. Before Start()/handshake
// existed, external MCP servers were silently dead (no tools, no process).
func TestClient_HandshakeAndCall(t *testing.T) {
	bridge := &fakeBridge{
		tools: []ToolDescriptor{
			{Name: "echo_tool", Description: "echoes", InputSchema: map[string]any{"type": "object"}},
		},
		callFn: func(_ context.Context, name string, args []byte) (CallToolResult, error) {
			return CallToolResult{Content: []ContentBlock{TextContent("called " + name + " with " + string(args))}}, nil
		},
	}

	// Wire: client.stdin -> server.in ; server.out -> client.stdout
	srvIn, cliOut := io.Pipe() // server reads srvIn, client writes cliOut
	cliIn, srvOut := io.Pipe() // client reads cliIn, server writes srvOut
	srv := NewServer(srvIn, srvOut, bridge, ServerInfo{Name: "fake-mcp", Version: "0.0.0"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srvDone := make(chan struct{})
	go func() { _ = srv.Serve(ctx); _ = srvOut.Close(); close(srvDone) }()

	c := &Client{Name: "fake"}
	hctx, hcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer hcancel()
	if err := c.connectStreams(hctx, cliOut, cliIn); err != nil {
		t.Fatalf("connectStreams/handshake failed: %v", err)
	}

	tools := c.ListTools()
	if len(tools) != 1 || tools[0].Name != "echo_tool" {
		t.Fatalf("tools/list not populated by handshake, got %+v", tools)
	}

	res, err := c.CallTool(context.Background(), "echo_tool", map[string]any{"x": 1})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if len(res.Content) == 0 || !strings.Contains(res.Content[0].Text, "called echo_tool") {
		t.Fatalf("CallTool result did not round-trip: %+v", res)
	}
	if bridge.calls.Load() != 1 {
		t.Fatalf("expected exactly 1 bridge call, got %d", bridge.calls.Load())
	}

	_ = c.Stop()
}

func TestClientName(t *testing.T) {
	c := &Client{Name: "my-client"}
	if c.Name != "my-client" {
		t.Errorf("got %s", c.Name)
	}
}

func TestClientClosedDefault(t *testing.T) {
	c := &Client{}
	if c.closed.Load() {
		t.Error("default closed should be false")
	}
}

func TestNewClient_Name(t *testing.T) {
	c, err := NewClient("server-fs", "echo", []string{"arg"}, nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if c.Name != "server-fs" {
		t.Errorf("got %s", c.Name)
	}
}

func TestClientStop_Idempotent(t *testing.T) {
	c := &Client{Name: "test", cmd: nil}
	c.closed.Store(false)
	if err := c.Stop(); err != nil {
		t.Errorf("first Stop: %v", err)
	}
	if !c.closed.Load() {
		t.Error("should be closed after Stop")
	}
	if err := c.Stop(); err != nil {
		t.Errorf("second Stop: %v", err)
	}
}

func TestClientStop_AlreadyClosed(t *testing.T) {
	c := &Client{Name: "test", cmd: nil}
	c.closed.Store(true)
	if err := c.Stop(); err != nil {
		t.Errorf("Stop on already-closed: %v", err)
	}
}

func TestClientCallTool_ClosedClient(t *testing.T) {
	c := &Client{Name: "test", cmd: nil}
	c.closed.Store(true)
	_, err := c.CallTool(context.Background(), "foo", nil)
	if err == nil {
		t.Error("expected error on closed client")
	}
}

func TestClientListTools_Empty(t *testing.T) {
	c := &Client{Name: "test"}
	got := c.ListTools()
	if len(got) != 0 {
		t.Errorf("got %v", got)
	}
}

// CallTool on unstarted client returns "client not started".
func TestClientCallTool_UnstartedClient(t *testing.T) {
	c := &Client{Name: "test"} // stdin/stdout nil
	_, err := c.CallTool(context.Background(), "foo", nil)
	if err == nil {
		t.Fatal("expected error on unstarted client")
	}
	if !strings.Contains(err.Error(), "not started") {
		t.Errorf("expected 'not started' error, got: %v", err)
	}
}

// jsonDecode with nil v returns nil.
func TestJsonDecode_NilValue(t *testing.T) {
	err := jsonDecode(nil, nil)
	if err != nil {
		t.Errorf("jsonDecode(nil, nil): %v", err)
	}
}

// marshalRaw produces valid JSON.
func TestMarshalRaw(t *testing.T) {
	got := marshalRaw(map[string]string{"key": "val"})
	if len(got) == 0 {
		t.Error("marshalRaw returned empty")
	}
}

func TestMCPToolBridge_NilClients(t *testing.T) {
	b := NewMCPToolBridge(nil)
	if got := b.List(); len(got) != 0 {
		t.Errorf("List: got %v", got)
	}
	_, err := b.Call(context.Background(), "anything", nil)
	if err == nil {
		t.Error("Call on nil bridge: expected error")
	}
}

func TestMCPToolBridge_UnknownTool(t *testing.T) {
	b := NewMCPToolBridge(nil)
	_, err := b.Call(context.Background(), "no-such-tool", nil)
	if err == nil {
		t.Error("expected error")
	}
	if e, ok := err.(*unknownToolError); !ok {
		t.Errorf("got %T", err)
	} else if e.Name != "no-such-tool" {
		t.Errorf("got %q", e.Name)
	}
}

func TestLoadClientsFromConfig_Nil(t *testing.T) {
	clients, err := LoadClientsFromConfig(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(clients) != 0 {
		t.Errorf("got %d", len(clients))
	}
}

func TestNewID_Unique(t *testing.T) {
	id1, id2 := newID(), newID()
	if string(id1) == string(id2) {
		t.Error("IDs should be unique")
	}
}

func TestJSONMarshal(t *testing.T) {
	v := map[string]any{"k": float64(1)}
	b := jsonMarshal(v)
	if len(b) == 0 {
		t.Error("empty result")
	}
}

func TestJSONDecode(t *testing.T) {
	v := map[string]any{"x": float64(1)}
	var target map[string]any
	if err := jsonDecode(v, &target); err != nil {
		t.Fatalf("jsonDecode: %v", err)
	}
	if target["x"] != float64(1) {
		t.Errorf("got %v", target)
	}
}

func TestJSONDecode_Nil(t *testing.T) {
	if err := jsonDecode(nil, nil); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestMCPToolBridge_ToolIndex(t *testing.T) {
	c1 := &Client{Name: "c1"}
	c1.mu.Lock()
	c1.tools = []ToolDescriptor{
		{Name: "tool_a"},
		{Name: "tool_b"},
	}
	c1.mu.Unlock()

	c2 := &Client{Name: "c2"}
	c2.mu.Lock()
	c2.tools = []ToolDescriptor{
		{Name: "tool_c"},
	}
	c2.mu.Unlock()

	b := NewMCPToolBridge([]*Client{c1, c2})
	if len(b.toolIndex) != 3 {
		t.Errorf("toolIndex size: got %d, want 3", len(b.toolIndex))
	}
}

func TestMCPToolBridge_ListUnion(t *testing.T) {
	c1 := &Client{Name: "c1"}
	c1.mu.Lock()
	c1.tools = []ToolDescriptor{{Name: "tool_a"}, {Name: "tool_b"}}
	c1.mu.Unlock()
	c2 := &Client{Name: "c2"}
	c2.mu.Lock()
	c2.tools = []ToolDescriptor{{Name: "tool_c"}}
	c2.mu.Unlock()

	b := NewMCPToolBridge([]*Client{c1, c2})
	list := b.List()
	if len(list) != 3 {
		t.Errorf("got %d", len(list))
	}
}

func TestUnknownToolError(t *testing.T) {
	e := &unknownToolError{Name: "my-tool"}
	if e.Error() != "mcp: unknown tool: my-tool" {
		t.Errorf("got %q", e.Error())
	}
}
