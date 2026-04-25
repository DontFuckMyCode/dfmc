package mcp

import (
	"context"
	"encoding/json"
	"testing"
)

// Tests for client.go public API surface.

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

func TestMarshalRaw(t *testing.T) {
	v := []string{"a", "b"}
	b := marshalRaw(v)
	var out []string
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out) != 2 || out[0] != "a" {
		t.Errorf("got %v", out)
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
