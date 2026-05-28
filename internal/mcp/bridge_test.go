package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

// TestMCPToolBridge_FirstWinsOnNameCollision regresses a bug where two
// MCP clients exposing the same tool name produced:
//   - List(): two ToolDescriptor entries with identical names (host UI
//     showed the same tool twice).
//   - Call(): routed to whichever client was iterated last (last-wins),
//     which contradicted whatever the host had cached from List().
//
// The fix de-duplicates List() and locks Call to first-wins so the surface
// the host sees matches the routing it gets. A warning is logged so the
// operator notices the collision and decides which side to drop or rename.
//
// We construct synthetic Clients with pre-set tool slices (no Start, no
// subprocess) — the bridge only reads ListTools() output, so this is
// sufficient and keeps the test hermetic.
func TestMCPToolBridge_FirstWinsOnNameCollision(t *testing.T) {
	primary := &Client{Name: "primary", tools: []ToolDescriptor{
		{Name: "search", Description: "primary's search"},
		{Name: "primary_only", Description: "unique to primary"},
	}}
	secondary := &Client{Name: "secondary", tools: []ToolDescriptor{
		{Name: "search", Description: "secondary's search (should be dropped)"},
		{Name: "secondary_only", Description: "unique to secondary"},
	}}

	// Capture the warning log so we can assert the collision was reported.
	var logBuf bytes.Buffer
	prevFlags := log.Flags()
	prevOut := log.Writer()
	log.SetFlags(0)
	log.SetOutput(&logBuf)
	defer func() {
		log.SetFlags(prevFlags)
		log.SetOutput(prevOut)
	}()

	b := NewMCPToolBridge([]*Client{primary, secondary})

	// List should expose 3 distinct names, with primary's "search" winning.
	list := b.List()
	if len(list) != 3 {
		t.Fatalf("List() should de-dupe to 3 entries, got %d: %+v", len(list), list)
	}
	seen := map[string]string{}
	for _, td := range list {
		seen[td.Name] = td.Description
	}
	if seen["search"] != "primary's search" {
		t.Fatalf("first-wins violated: List()'s search points to %q, want primary's", seen["search"])
	}
	if _, ok := seen["primary_only"]; !ok {
		t.Fatalf("primary_only missing from List()")
	}
	if _, ok := seen["secondary_only"]; !ok {
		t.Fatalf("secondary_only missing from List()")
	}

	// toolIndex routing must agree with List(): "search" goes to primary,
	// secondary_only still routes to secondary.
	if owner := b.toolIndex["search"]; owner != primary {
		t.Fatalf("Call routing for collided name went to %v, want primary", owner)
	}
	if owner := b.toolIndex["secondary_only"]; owner != secondary {
		t.Fatalf("Call routing for unique secondary tool went to %v, want secondary", owner)
	}

	// The collision warning must name both clients and the tool. We don't
	// pin exact wording — only the load-bearing identifiers — so future
	// rephrasing doesn't break the test.
	logged := logBuf.String()
	for _, want := range []string{"search", "primary", "secondary"} {
		if !strings.Contains(logged, want) {
			t.Fatalf("collision log missing %q. Full log: %s", want, logged)
		}
	}
}

// TestMCPToolBridge_CallRoutesToFirstWins double-checks that the Call path
// honours the same first-wins rule. We shadow Call by going through the
// public method with a JSON arguments shape and assert it lands on
// primary's CallTool. Since the synthetic Client has no live process,
// we can't actually invoke; instead we verify the index lookup at the
// bridge layer succeeds against primary, which is the routing decision.
func TestMCPToolBridge_CallRoutesToFirstWins(t *testing.T) {
	primary := &Client{Name: "first", tools: []ToolDescriptor{{Name: "shared"}}}
	secondary := &Client{Name: "second", tools: []ToolDescriptor{{Name: "shared"}}}
	// Silence the warning during this assertion-focused test.
	prevOut := log.Writer()
	log.SetOutput(&bytes.Buffer{})
	defer log.SetOutput(prevOut)

	b := NewMCPToolBridge([]*Client{primary, secondary})

	owner, ok := b.toolIndex["shared"]
	if !ok {
		t.Fatalf("expected 'shared' in toolIndex")
	}
	if owner != primary {
		t.Fatalf("Call should route to first-registered client (primary), got %s", owner.Name)
	}

	// And Call against an unknown tool still surfaces the typed error.
	_, err := b.Call(context.Background(), "no-such-tool", json.RawMessage(`{}`))
	if err == nil {
		t.Fatalf("expected unknownToolError for missing tool")
	}
	if _, ok := err.(*unknownToolError); !ok {
		t.Fatalf("expected *unknownToolError, got %T: %v", err, err)
	}
}

// TestMCPToolBridge_NilClientsListOnly exercises the nil-safe path for List.
func TestMCPToolBridge_NilClientsListOnly(t *testing.T) {
	b := NewMCPToolBridge(nil)
	if b.List() != nil {
		t.Error("nil clients: List() should return nil")
	}
}

// TestMCPToolBridge_EmptyClients exercises the empty-safe path.
func TestMCPToolBridge_EmptyClients(t *testing.T) {
	b := NewMCPToolBridge([]*Client{})
	got := b.List()
	if got == nil {
		t.Error("empty clients: List() should return empty slice, not nil")
	}
}

// TestMCPToolBridge_CallMalformedArguments verifies that a JSON parse error
// in the arguments is surfaced as a well-formed error.
func TestMCPToolBridge_CallMalformedArguments(t *testing.T) {
	c := &Client{Name: "test"}
	b := NewMCPToolBridge([]*Client{c})
	b.toolIndex["bad-args"] = c

	_, err := b.Call(context.Background(), "bad-args", []byte(`{not json`))
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

// TestMCPToolBridge_CallNilArguments exercises the nil arguments path.
func TestMCPToolBridge_CallNilArguments(t *testing.T) {
	c := &Client{Name: "test"}
	b := NewMCPToolBridge([]*Client{c})
	b.toolIndex["echo"] = c

	// nil arguments should not cause an unmarshal error
	_, err := b.Call(context.Background(), "echo", nil)
	// err will be something like "client closed" since subprocess isn't started,
	// but the args parsing path should not fail
	if err != nil && err.Error() == "malformed tool arguments: unexpected end of JSON input" {
		t.Errorf("nil args should not trigger JSON parse error: %v", err)
	}
}

// TestUnknownToolError_Error checks the formatted message.
func TestUnknownToolError_Error(t *testing.T) {
	err := &unknownToolError{Name: "my-tool"}
	got := err.Error()
	want := "mcp: unknown tool: my-tool"
	if got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestLoadClientsFromConfig_NilInput(t *testing.T) {
	got, err := LoadClientsFromConfig(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("LoadClientsFromConfig(nil) = %v, want nil", got)
	}
}

func TestLoadClientsFromConfig_EmptyInput(t *testing.T) {
	got, err := LoadClientsFromConfig([]config.MCPServerConfig{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Error("LoadClientsFromConfig({}) = nil, want empty slice")
	}
}

// TestMCPToolBridge_Close pins that bridge.Close stops every backing
// client. The May 2026 regression left them running — engine.Shutdown
// teared the engine down but MCP server subprocesses stayed orphaned,
// and ReloadConfig spawned a fresh set on every config edit without
// stopping the old one. We use cmd=nil clients so Stop short-circuits
// to the closed-flag flip without needing a real subprocess.
func TestMCPToolBridge_Close(t *testing.T) {
	c1 := &Client{Name: "alpha", cmd: nil}
	c2 := &Client{Name: "beta", cmd: nil}
	b := NewMCPToolBridge([]*Client{c1, c2})

	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !c1.closed.Load() {
		t.Error("client alpha not stopped")
	}
	if !c2.closed.Load() {
		t.Error("client beta not stopped")
	}
}

// TestMCPToolBridge_CloseIdempotent confirms Close can be called twice
// safely. The double-defer pattern in cmd/dfmc/main.go means engine
// Shutdown runs twice in the LIFO unwind, and via Tools.Close in the
// reload swap. Both paths reach the bridge.
func TestMCPToolBridge_CloseIdempotent(t *testing.T) {
	c := &Client{Name: "alpha", cmd: nil}
	b := NewMCPToolBridge([]*Client{c})
	if err := b.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// TestMCPToolBridge_CloseNil keeps the nil receiver path covered so
// the cleanup paths in engine.Shutdown / ReloadConfig don't need to
// guard against a nil bridge themselves.
func TestMCPToolBridge_CloseNil(t *testing.T) {
	var b *MCPToolBridge
	if err := b.Close(); err != nil {
		t.Fatalf("nil bridge Close should be a no-op, got %v", err)
	}
}
