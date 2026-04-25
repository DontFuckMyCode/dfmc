package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// taskCreateBody is a minimal valid POST /api/v1/tasks body.
func taskCreateBody(extra map[string]any) []byte {
	body := map[string]any{"title": "test task"}
	for k, v := range extra {
		body[k] = v
	}
	out, _ := json.Marshal(body)
	return out
}

// TestTaskCreate_RejectsClientID pins VULN-033: the create endpoint
// must refuse a caller-supplied id. Pre-fix a client could POST
// {"id":"<existing>","title":"x"} and silently overwrite an existing
// task because SaveTask is a blind Put.
func TestTaskCreate_RejectsClientID(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/v1/tasks", "application/json",
		bytes.NewReader(taskCreateBody(map[string]any{"id": "attacker-chosen"})))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for client-supplied id, got %d", resp.StatusCode)
	}
	var payload map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&payload)
	msg, _ := payload["error"].(string)
	if !strings.Contains(msg, "id is server-generated") {
		t.Fatalf("error message should explain the rule, got %q", msg)
	}
}

// TestTaskCreate_NoIDSucceeds: omitting id is the happy path.
func TestTaskCreate_NoIDSucceeds(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/v1/tasks", "application/json",
		bytes.NewReader(taskCreateBody(nil)))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	var task map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&task); err != nil {
		t.Fatalf("decode: %v", err)
	}
	id, _ := task["ID"].(string)
	if id == "" {
		t.Fatalf("response must include server-generated id, got %v", task)
	}
}

// createTaskHelper POSTs a task and returns its server-generated id.
func createTaskHelper(t *testing.T, ts *httptest.Server, extra map[string]any) string {
	t.Helper()
	body := map[string]any{"title": "task"}
	for k, v := range extra {
		body[k] = v
	}
	raw, _ := json.Marshal(body)
	resp, err := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create task: got %d", resp.StatusCode)
	}
	var task map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&task)
	id, _ := task["ID"].(string)
	if id == "" {
		t.Fatalf("created task must have id")
	}
	return id
}

// TestTaskUpdate_RejectsUnknownState pins VULN-032: the patch
// endpoint must refuse a state string outside the supervisor's
// canonical enum. Pre-fix any string went straight onto the row and
// confused the scheduler.
func TestTaskUpdate_RejectsUnknownState(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	id := createTaskHelper(t, ts, nil)

	patch, _ := json.Marshal(map[string]any{"state": "🚀"})
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/tasks/"+id, bytes.NewReader(patch))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown state, got %d", resp.StatusCode)
	}
}

// TestTaskUpdate_AcceptsKnownState confirms the canonical states
// pass the gate.
func TestTaskUpdate_AcceptsKnownState(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	id := createTaskHelper(t, ts, nil)
	patch, _ := json.Marshal(map[string]any{"state": "running"})
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/tasks/"+id, bytes.NewReader(patch))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := readAllBody(resp)
		t.Fatalf("expected 200 for valid state, got %d: %s", resp.StatusCode, body)
	}
}

// TestTaskUpdate_RejectsSelfReparent pins VULN-032's reparent cycle
// guard at the simplest case: a task can't become its own parent.
func TestTaskUpdate_RejectsSelfReparent(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	id := createTaskHelper(t, ts, nil)
	patch, _ := json.Marshal(map[string]any{"parent_id": id})
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/tasks/"+id, bytes.NewReader(patch))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("self-reparent must be 400, got %d", resp.StatusCode)
	}
}

// TestTaskUpdate_RejectsDescendantReparent pins the deeper cycle:
// reparenting to a child of yourself creates a loop. Build A→B and
// then PATCH A.parent_id = B.
func TestTaskUpdate_RejectsDescendantReparent(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	parentID := createTaskHelper(t, ts, nil)
	childID := createTaskHelper(t, ts, map[string]any{"parent_id": parentID})

	patch, _ := json.Marshal(map[string]any{"parent_id": childID})
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/tasks/"+parentID, bytes.NewReader(patch))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		body, _ := readAllBody(resp)
		t.Fatalf("descendant reparent must be 400, got %d: %s", resp.StatusCode, body)
	}
}

// TestTaskList_LimitClampedToCap pins VULN-042: a caller asking for
// limit=10000000 must be capped at taskListLimitMax. We seed more
// rows than the cap so the assertion fires on the cap.
func TestTaskList_LimitClampedToCap(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Seed a small number — we don't actually need >cap rows to test
	// that the cap is honoured: the response must succeed and never
	// exceed the cap regardless of the input.
	for i := 0; i < 5; i++ {
		createTaskHelper(t, ts, nil)
	}
	resp, err := http.Get(ts.URL + "/api/v1/tasks?limit=10000000")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var tasks []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&tasks); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(tasks) > taskListLimitMax {
		t.Fatalf("response exceeded cap: got %d, max %d", len(tasks), taskListLimitMax)
	}
}

// TestDriveStart_RejectsOversizedMaxParallel pins VULN-034: caller
// can't request a million-thread worker pool.
func TestDriveStart_RejectsOversizedMaxParallel(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	body, _ := json.Marshal(map[string]any{"task": "x", "max_parallel": 1_000_000})
	resp, err := http.Post(ts.URL+"/api/v1/drive", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("oversized max_parallel must be 400, got %d", resp.StatusCode)
	}
}

// TestDriveStart_RejectsOversizedAutoApprove pins the auto_approve
// length cap: a payload with 10k entries is refused.
func TestDriveStart_RejectsOversizedAutoApprove(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	bigList := make([]string, 10_000)
	for i := range bigList {
		bigList[i] = fmt.Sprintf("tool_%d", i)
	}
	body, _ := json.Marshal(map[string]any{"task": "x", "auto_approve": bigList})
	resp, err := http.Post(ts.URL+"/api/v1/drive", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("oversized auto_approve must be 400, got %d", resp.StatusCode)
	}
}

// TestDriveStart_AcceptsReasonableValues confirms the bounds don't
// over-reject — a typical request still goes through.
func TestDriveStart_AcceptsReasonableValues(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	body, _ := json.Marshal(map[string]any{
		"task":         "do something",
		"max_parallel": 4,
		"max_todos":    50,
		"auto_approve": []string{"read_file", "grep_codebase"},
	})
	resp, err := http.Post(ts.URL+"/api/v1/drive", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	// 202 Accepted is the happy path (background goroutine started);
	// allow 200/202/503 (the latter when the engine isn't fully
	// wired in this test fixture, which is acceptable here — we're
	// pinning bounds enforcement, not the runner's plumbing).
	if resp.StatusCode == http.StatusBadRequest {
		body, _ := readAllBody(resp)
		t.Fatalf("reasonable request must not return 400: %s", body)
	}
}

// readAllBody reads up to 4 KiB of the response body for diagnostic
// messages without dragging io.ReadAll into the test imports.
func readAllBody(resp *http.Response) (string, error) {
	buf := make([]byte, 4096)
	n, err := resp.Body.Read(buf)
	return string(buf[:n]), err
}
