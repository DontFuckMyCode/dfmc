package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// /api/v1/context/gc (GET) returns the dominance preview without
// mutating the active branch. Mirrors /context gc in the TUI.
func TestContextGC_PreviewListsDominatedTurns(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	defer srv.Close()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Seed a conversation where a-1 (empty content, failed read_file)
	// is dominated by a-2 (successful read on the same path).
	eng.Conversation.AddMessage("offline", "offline-v1", types.Message{
		ID: "u-1", Role: types.RoleUser, Content: "read foo.go",
	})
	eng.Conversation.AddMessage("offline", "offline-v1", types.Message{
		ID: "a-1", Role: types.RoleAssistant, Content: "",
		ToolCalls: []types.ToolCallRecord{{Name: "read_file", Params: map[string]any{"path": "foo.go"}}},
		Results:   []types.ToolResultRecord{{Name: "read_file", Success: false}},
	})
	eng.Conversation.AddMessage("offline", "offline-v1", types.Message{
		ID: "a-2", Role: types.RoleAssistant, Content: "got it",
		ToolCalls: []types.ToolCallRecord{{Name: "read_file", Params: map[string]any{"path": "foo.go"}}},
		Results:   []types.ToolResultRecord{{Name: "read_file", Success: true}},
	})

	resp, err := http.Get(ts.URL + "/api/v1/context/gc")
	if err != nil {
		t.Fatalf("GET preview: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var payload struct {
		DropIDs []string          `json:"drop_ids"`
		Reasons map[string]string `json:"reasons"`
		Count   int               `json:"count"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Count != 1 || len(payload.DropIDs) != 1 || payload.DropIDs[0] != "a-1" {
		t.Fatalf("expected exactly a-1 in preview, got %#v", payload)
	}
	if payload.Reasons["a-1"] != "failed_retry_superseded" {
		t.Errorf("expected failed_retry_superseded reason, got %q", payload.Reasons["a-1"])
	}
	// Preview must NOT mutate the active branch.
	if got := len(eng.Conversation.Active().Messages()); got != 3 {
		t.Errorf("preview must not mutate; %d messages remain", got)
	}
}

// POST /api/v1/context/gc actually prunes and reports dropped count.
func TestContextGC_RunPrunesDominated(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	defer srv.Close()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	eng.Conversation.AddMessage("offline", "offline-v1", types.Message{
		ID: "u-1", Role: types.RoleUser, Content: "read foo.go",
	})
	eng.Conversation.AddMessage("offline", "offline-v1", types.Message{
		ID: "a-1", Role: types.RoleAssistant, Content: "",
		ToolCalls: []types.ToolCallRecord{{Name: "read_file", Params: map[string]any{"path": "foo.go"}}},
		Results:   []types.ToolResultRecord{{Name: "read_file", Success: false}},
	})
	eng.Conversation.AddMessage("offline", "offline-v1", types.Message{
		ID: "a-2", Role: types.RoleAssistant, Content: "got it",
		ToolCalls: []types.ToolCallRecord{{Name: "read_file", Params: map[string]any{"path": "foo.go"}}},
		Results:   []types.ToolResultRecord{{Name: "read_file", Success: true}},
	})

	resp, err := http.Post(ts.URL+"/api/v1/context/gc", "application/json", strings.NewReader(""))
	if err != nil {
		t.Fatalf("POST run: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var payload struct {
		DropIDs []string          `json:"drop_ids"`
		Reasons map[string]string `json:"reasons"`
		Count   int               `json:"count"`
		Dropped int               `json:"dropped"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Dropped != 1 {
		t.Errorf("expected dropped=1, got %d", payload.Dropped)
	}
	// Active branch must have a-1 removed.
	for _, msg := range eng.Conversation.Active().Messages() {
		if msg.ID == "a-1" {
			t.Errorf("expected a-1 to be pruned, but it survived")
		}
	}
}

// Empty conversation → empty decision, no error.
func TestContextGC_EmptyConversationNoOp(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	defer srv.Close()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/context/gc")
	if err != nil {
		t.Fatalf("GET preview: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK on empty branch, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var payload struct {
		Count int `json:"count"`
	}
	_ = json.Unmarshal(body, &payload)
	if payload.Count != 0 {
		t.Errorf("expected count=0 on empty branch, got %d", payload.Count)
	}
}
