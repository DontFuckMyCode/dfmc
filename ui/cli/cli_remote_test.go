package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBearerTokenMiddleware(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	handler := bearerTokenMiddleware(next, "secret-token")

	t.Run("healthz open", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("unexpected status: %d", rec.Code)
		}
		payload := map[string]any{}
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode json: %v", err)
		}
		if payload["status"] != "ok" {
			t.Fatalf("unexpected payload: %#v", payload)
		}
	})

	t.Run("unauthorized", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("authorized", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
		req.Header.Set("Authorization", "Bearer secret-token")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
	})
}

func TestParseEndpointList(t *testing.T) {
	got := parseEndpointList("healthz, /api/v1/status ,")
	if len(got) != 2 {
		t.Fatalf("expected 2 endpoints, got %d (%v)", len(got), got)
	}
	if got[0] != "/healthz" || got[1] != "/api/v1/status" {
		t.Fatalf("unexpected endpoints: %v", got)
	}
}

func TestProbeRemoteEndpoint(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ok" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	client := &http.Client{Timeout: 2 * time.Second}

	okRes := probeRemoteEndpoint(client, ts.URL, "/ok", "")
	if !okRes.OK || okRes.StatusCode != http.StatusOK {
		t.Fatalf("expected ok result, got: %+v", okRes)
	}
	if !strings.Contains(okRes.Body, "status") {
		t.Fatalf("unexpected ok body: %s", okRes.Body)
	}

	failRes := probeRemoteEndpoint(client, ts.URL, "/fail", "")
	if failRes.OK || failRes.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected fail result, got: %+v", failRes)
	}
	if !strings.Contains(failRes.Body, "unauthorized") {
		t.Fatalf("unexpected fail body: %s", failRes.Body)
	}
}

func TestParseSSEDataLine(t *testing.T) {
	ev, ok, err := parseSSEDataLine(`data: {"type":"delta","delta":"hi"}`)
	if err != nil {
		t.Fatalf("parseSSEDataLine error: %v", err)
	}
	if !ok || ev.Type != "delta" || ev.Delta != "hi" {
		t.Fatalf("unexpected event: ok=%v ev=%+v", ok, ev)
	}

	_, ok, err = parseSSEDataLine("event: ping")
	if err != nil {
		t.Fatalf("unexpected parse error for non-data line: %v", err)
	}
	if ok {
		t.Fatal("expected non-data line to return ok=false")
	}
}

func TestRemoteAsk(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/chat" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"delta\",\"delta\":\"A\"}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"delta\",\"delta\":\"B\"}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"done\"}\n\n")
	}))
	defer ts.Close()

	events, answer, err := remoteAsk(ts.URL, "", "hello", 3*time.Second, false)
	if err != nil {
		t.Fatalf("remoteAsk error: %v", err)
	}
	if answer != "AB" {
		t.Fatalf("unexpected answer: %q", answer)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
}

func TestParseKeyValueParams(t *testing.T) {
	params, err := parseKeyValueParams([]string{"path=main.go", "line_start=2", "raw=true"})
	if err != nil {
		t.Fatalf("parseKeyValueParams error: %v", err)
	}
	if params["path"] != "main.go" {
		t.Fatalf("unexpected path: %#v", params["path"])
	}
	if params["line_start"] != 2 {
		t.Fatalf("unexpected line_start: %#v", params["line_start"])
	}
	if params["raw"] != true {
		t.Fatalf("unexpected raw: %#v", params["raw"])
	}
}

func TestRemoteJSONRequest(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			_, _ = w.Write([]byte(`{"error":"method not allowed"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"output":"done"}`))
	}))
	defer ts.Close()

	out, status, err := remoteJSONRequest(http.MethodPost, ts.URL, "", map[string]any{"x": 1}, 2*time.Second)
	if err != nil {
		t.Fatalf("remoteJSONRequest error: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("unexpected status: %d", status)
	}
	if ok, _ := out["ok"].(bool); !ok {
		t.Fatalf("unexpected response: %#v", out)
	}
}
