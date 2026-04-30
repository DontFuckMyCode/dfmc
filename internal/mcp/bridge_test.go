package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"strings"
	"testing"
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
