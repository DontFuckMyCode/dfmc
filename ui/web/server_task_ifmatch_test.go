// HTTP-level tests for the If-Match / ETag opt-in CAS layer over
// PATCH /api/v1/tasks/{id}. Without the header behaviour is unchanged
// (last-writer-wins via UpdateTask); with the header a stale version
// is rejected with 412 Precondition Failed.

package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTaskUpdate_NoIfMatch_BehavesAsBefore(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	id := createTaskHelper(t, ts, nil)

	patch, _ := json.Marshal(map[string]any{"title": "renamed"})
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/tasks/"+id, bytes.NewReader(patch))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := readAllBody(resp)
		t.Fatalf("expected 200 without If-Match, got %d: %s", resp.StatusCode, body)
	}
	// ETag should still be set on the response so a follow-up PATCH can
	// adopt If-Match without a separate GET.
	if etag := resp.Header.Get("ETag"); etag == "" {
		t.Error("response should carry an ETag header even without If-Match")
	}
}

func TestTaskUpdate_IfMatch_StaleVersionGets412(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	id := createTaskHelper(t, ts, nil)

	// First PATCH bumps Version 0 -> 1.
	patch1, _ := json.Marshal(map[string]any{"title": "first edit"})
	req1, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/tasks/"+id, bytes.NewReader(patch1))
	req1.Header.Set("Content-Type", "application/json")
	resp1, err := http.DefaultClient.Do(req1)
	if err != nil {
		t.Fatalf("first patch: %v", err)
	}
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("first patch unexpected status %d", resp1.StatusCode)
	}

	// Second PATCH passes If-Match: 0 (stale). Must get 412.
	patch2, _ := json.Marshal(map[string]any{"title": "stale edit"})
	req2, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/tasks/"+id, bytes.NewReader(patch2))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("If-Match", `"0"`)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("stale patch: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusPreconditionFailed {
		body, _ := readAllBody(resp2)
		t.Fatalf("stale If-Match must be 412, got %d: %s", resp2.StatusCode, body)
	}
}

func TestTaskUpdate_IfMatch_FreshVersionSucceeds(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	id := createTaskHelper(t, ts, nil)

	// GET to read the current version via ETag.
	getResp, err := http.Get(ts.URL + "/api/v1/tasks/" + id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	getResp.Body.Close()
	etag := getResp.Header.Get("ETag")
	if etag == "" {
		t.Fatal("GET should expose ETag for If-Match round-trips")
	}

	// PATCH with the fresh ETag must succeed.
	patch, _ := json.Marshal(map[string]any{"title": "from fresh etag"})
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/tasks/"+id, bytes.NewReader(patch))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("If-Match", etag)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := readAllBody(resp)
		t.Fatalf("fresh If-Match must be 200, got %d: %s", resp.StatusCode, body)
	}
	// Version on the response should have advanced.
	newEtag := resp.Header.Get("ETag")
	if newEtag == "" || newEtag == etag {
		t.Errorf("PATCH response ETag should reflect bumped version (was %q, got %q)", etag, newEtag)
	}
}

func TestTaskUpdate_IfMatch_GarbageRejected(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	id := createTaskHelper(t, ts, nil)

	cases := []string{"abc", "-1", "", `"abc"`}
	for _, val := range cases {
		// Skip empty (would be no-header path).
		if val == "" {
			continue
		}
		patch, _ := json.Marshal(map[string]any{"title": "x"})
		req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/tasks/"+id, bytes.NewReader(patch))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("If-Match", val)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("patch %q: %v", val, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("If-Match=%q must be 400, got %d", val, resp.StatusCode)
		}
	}
}

// Compile-time hint that fmt is intended-imported (defensive against
// future edits dropping the only call site).
var _ = fmt.Sprintf
