// Drive HTTP endpoint tests. The endpoints don't actually run a real
// drive (that requires provider plumbing); we focus on shape, status
// codes, and idempotency contracts so a frontend or remote client
// can be built against a stable surface.

package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/drive"
)

// TestDriveStartRejectsEmptyTask: an empty / missing task must
// return 400 with a hint, not a 500. The hint is what the workbench
// shows the user, so it has to be useful.
func TestDriveStartRejectsEmptyTask(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/v1/drive", "application/json",
		strings.NewReader(`{"task":""}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty task, got %d", resp.StatusCode)
	}
	var payload map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&payload)
	if payload["error"] == nil {
		t.Fatal("400 payload must include error field")
	}
	if payload["hint"] == nil {
		t.Fatal("400 payload must include hint so the user knows the correct shape")
	}
}

// TestDriveStartRejectsInvalidJSON: malformed body returns 400, not
// 500. (Standard Go decoder behavior, but worth pinning so a refactor
// to a streaming decoder doesn't accidentally regress.)
func TestDriveStartRejectsInvalidJSON(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/v1/drive", "application/json",
		strings.NewReader(`{not json`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed JSON, got %d", resp.StatusCode)
	}
}

func TestDriveStartReturnsRunID(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/v1/drive", "application/json",
		strings.NewReader(`{"task":"add smoke test"}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202 for valid task, got %d", resp.StatusCode)
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	runID := strings.TrimSpace(toTestString(payload["run_id"]))
	if runID == "" {
		t.Fatalf("expected run_id in response, got %#v", payload)
	}
	store, err := drive.NewStore(eng.Storage.DB())
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	run, err := store.Load(runID)
	if err != nil {
		t.Fatalf("load run: %v", err)
	}
	if run == nil {
		t.Fatalf("expected persisted run %q", runID)
	}
}

func toTestString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// TestDriveListEmptyReturnsArrayNotNull: a fresh project has no
// runs; the list endpoint must emit `[]` not `null` so the workbench
// frontend can iterate without a typeof check.
func TestDriveListEmptyReturnsArrayNotNull(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/drive")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body := new(bytes.Buffer)
	_, _ = body.ReadFrom(resp.Body)
	if strings.TrimSpace(body.String()) == "null" {
		t.Fatal("empty list must serialize as [] not null")
	}
	if !strings.HasPrefix(strings.TrimSpace(body.String()), "[") {
		t.Fatalf("expected JSON array, got %q", body.String())
	}
}

// TestDriveShowMissingReturns404: explicit 404 for a missing run, so
// the workbench can show "no such run" instead of "server error".
func TestDriveShowMissingReturns404(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/drive/drv-doesnotexist")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for missing run, got %d", resp.StatusCode)
	}
}

// TestDriveShowReturnsExistingRun: pre-seed a run via the underlying
// store, then GET it back. Verifies the round-trip works without
// running the actual driver loop (which would need a real provider).
func TestDriveShowReturnsExistingRun(t *testing.T) {
	eng := newTestEngine(t)
	store, err := drive.NewStore(eng.Storage.DB())
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	run := &drive.Run{
		ID:        "drv-test-show",
		Task:      "test task",
		Status:    drive.RunDone,
		CreatedAt: time.Now(),
		Todos: []drive.Todo{
			{ID: "T1", Title: "first", Detail: "do x", Status: drive.TodoDone, Brief: "did x"},
		},
	}
	if err := store.Save(run); err != nil {
		t.Fatalf("save seed: %v", err)
	}

	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/drive/drv-test-show")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var got drive.Run
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != "drv-test-show" {
		t.Fatalf("ID round-trip failed: got %q", got.ID)
	}
	if len(got.Todos) != 1 || got.Todos[0].Brief != "did x" {
		t.Fatalf("todos round-trip failed: %+v", got.Todos)
	}
}

// TestDriveResumeRejectsTerminalRun: GET sees the run done, POST
// resume must refuse with 409 instead of silently re-running.
func TestDriveResumeRejectsTerminalRun(t *testing.T) {
	eng := newTestEngine(t)
	store, _ := drive.NewStore(eng.Storage.DB())
	_ = store.Save(&drive.Run{
		ID:        "drv-terminal",
		Task:      "x",
		Status:    drive.RunDone,
		CreatedAt: time.Now(),
	})

	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/v1/drive/drv-terminal/resume", "application/json", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 for terminal run resume, got %d", resp.StatusCode)
	}
}

// TestDriveStopMissingReturns404: cancelling a non-existent run
// must return 404 with a hint pointing the user at the persisted
// state endpoint (so they can distinguish "wrong ID" from "already
// done").
func TestDriveStopMissingReturns404(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/v1/drive/drv-not-here/stop", "application/json", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for stop of missing run, got %d", resp.StatusCode)
	}
	var payload map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&payload)
	if payload["error"] == nil {
		t.Fatal("404 must include error field")
	}
	if payload["hint"] == nil {
		t.Fatal("404 must include hint pointing at GET /api/v1/drive/{id}")
	}
}

// TestDriveActiveEmptyReturnsArrayNotNull: matches the same contract
// as /api/v1/drive — empty list is `[]` so the workbench can iterate
// without nil-checking.
func TestDriveActiveEmptyReturnsArrayNotNull(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/drive/active")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body := new(bytes.Buffer)
	_, _ = body.ReadFrom(resp.Body)
	if strings.TrimSpace(body.String()) == "null" {
		t.Fatal("empty active list must serialize as [] not null")
	}
}

// TestDriveDeleteIdempotent: deleting a missing run is fine — same
// 200 response as a real delete. Lets cleanup automation be naive.
func TestDriveDeleteIdempotent(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/drive/drv-never-existed", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 idempotent delete, got %d", resp.StatusCode)
	}
}
