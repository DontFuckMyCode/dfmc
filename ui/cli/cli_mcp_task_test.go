// MCP-side opt-in CAS for task updates. Mirrors the HTTP If-Match
// surface in server_task.go: when callers pass if_version, the update
// routes through UpdateTaskCAS and stale versions surface as a parseable
// "version_conflict" error token. Without if_version (or with a
// negative value), behaviour is unchanged.

package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/supervisor"
	"github.com/dontfuckmycode/dfmc/internal/taskstore"
)

// helperCreateTask sets up an engine + store + a single task and
// returns (handler, taskID). Each test gets its own engine so the
// in-process SQLite file is isolated.
func helperCreateTask(t *testing.T, title string) (*taskMCPHandler, string) {
	t.Helper()
	eng := newCLITestEngine(t)
	store := eng.Tools.TaskStore()
	if store == nil {
		t.Fatal("task store should be wired in test engine")
	}
	id := taskstore.NewTaskID()
	task := supervisor.Task{ID: id, Title: title, State: supervisor.TaskPending, Origin: "test"}
	if err := store.SaveTask(&task); err != nil {
		t.Fatalf("SaveTask: %v", err)
	}
	return &taskMCPHandler{eng: eng}, id
}

func TestMCPTaskUpdate_NoIfVersion_BehavesAsBefore(t *testing.T) {
	h, id := helperCreateTask(t, "original")
	args, _ := json.Marshal(map[string]any{"id": id, "title": "renamed"})
	res, err := h.callUpdate(args)
	if err != nil {
		t.Fatalf("callUpdate: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected ok, got error: %+v", res)
	}
}

func TestMCPTaskUpdate_StaleIfVersionRejected(t *testing.T) {
	h, id := helperCreateTask(t, "original")
	// First update bumps version 0 -> 1.
	first, _ := json.Marshal(map[string]any{"id": id, "title": "v1"})
	if _, err := h.callUpdate(first); err != nil {
		t.Fatalf("first update: %v", err)
	}
	// Second update with stale if_version=0 must be refused.
	stale, _ := json.Marshal(map[string]any{"id": id, "title": "v2", "if_version": 0})
	res, err := h.callUpdate(stale)
	if err != nil {
		t.Fatalf("callUpdate: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected stale if_version to fail, got ok")
	}
	// Error message must carry the parseable token so MCP clients can
	// branch on the conflict without text matching the rest.
	body := mcpResultText(res)
	if !strings.Contains(body, "version_conflict") {
		t.Errorf("expected 'version_conflict' token in error, got %q", body)
	}
}

func TestMCPTaskUpdate_FreshIfVersionSucceeds(t *testing.T) {
	h, id := helperCreateTask(t, "original")
	// Read current version (0 for a freshly-saved task).
	store := h.eng.Tools.TaskStore()
	task, err := store.LoadTask(id)
	if err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	args, _ := json.Marshal(map[string]any{
		"id":         id,
		"title":      "v1",
		"if_version": task.Version,
	})
	res, err := h.callUpdate(args)
	if err != nil {
		t.Fatalf("callUpdate: %v", err)
	}
	if res.IsError {
		t.Fatalf("fresh if_version should succeed, got error: %s", mcpResultText(res))
	}
	// Version on the stored task should have advanced.
	updated, _ := store.LoadTask(id)
	if updated.Version <= task.Version {
		t.Errorf("Version should bump after CAS update: before=%d after=%d", task.Version, updated.Version)
	}
}

func TestMCPTaskUpdate_NegativeIfVersionFallsBackToPlainUpdate(t *testing.T) {
	h, id := helperCreateTask(t, "original")
	// Negative if_version is the documented way to opt out — same effect
	// as omitting the field entirely.
	neg := -1
	rawArgs := map[string]any{"id": id, "title": "renamed", "if_version": neg}
	args, _ := json.Marshal(rawArgs)
	res, err := h.callUpdate(args)
	if err != nil {
		t.Fatalf("callUpdate: %v", err)
	}
	if res.IsError {
		t.Fatalf("negative if_version should fall back to UpdateTask, got error: %s", mcpResultText(res))
	}
}

// mcpResultText extracts the human-readable text body from an MCP
// CallToolResult. Used by tests to inspect error messages without
// importing the mcp package directly.
func mcpResultText(res any) string {
	type contentItem struct {
		Text string `json:"text"`
	}
	type result struct {
		Content []contentItem `json:"content"`
	}
	raw, _ := json.Marshal(res)
	var parsed result
	_ = json.Unmarshal(raw, &parsed)
	var parts []string
	for _, c := range parsed.Content {
		parts = append(parts, c.Text)
	}
	return strings.Join(parts, "\n")
}
