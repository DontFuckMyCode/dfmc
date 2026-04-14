package cli

import (
	"context"
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

func TestParseSSEJSONLine(t *testing.T) {
	ev, ok, err := parseSSEJSONLine(`data: {"type":"ping","ts":"2026-01-01T00:00:00Z"}`)
	if err != nil {
		t.Fatalf("parseSSEJSONLine error: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true")
	}
	if ev["type"] != "ping" {
		t.Fatalf("unexpected event: %#v", ev)
	}

	_, ok, err = parseSSEJSONLine(`: keepalive`)
	if err != nil {
		t.Fatalf("unexpected error for comment line: %v", err)
	}
	if ok {
		t.Fatal("expected ok=false for non-data line")
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

func TestRemoteCollectEvents(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ws" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"connected\"}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"event\",\"event\":\"index:done\"}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"ping\"}\n\n")
	}))
	defer ts.Close()

	events, err := remoteCollectEvents(ts.URL+"/ws", "", 2*time.Second, 3)
	if err != nil {
		t.Fatalf("remoteCollectEvents error: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	if events[1]["type"] != "event" {
		t.Fatalf("unexpected second event: %#v", events[1])
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

func TestRemotePathEscape(t *testing.T) {
	got := remotePathEscape("dir with space/file#.go")
	if got != "dir%20with%20space/file%23.go" {
		t.Fatalf("unexpected escaped path: %s", got)
	}
}

func TestDecodeCodemapPayload(t *testing.T) {
	payload := map[string]any{
		"nodes": []map[string]any{
			{"id": "n1", "name": "A", "kind": "file", "path": "a.go"},
		},
		"edges": []map[string]any{
			{"from": "n1", "to": "n2", "type": "imports"},
		},
	}
	nodes, edges, err := decodeCodemapPayload(payload)
	if err != nil {
		t.Fatalf("decodeCodemapPayload error: %v", err)
	}
	if len(nodes) != 1 || nodes[0].ID != "n1" {
		t.Fatalf("unexpected nodes: %#v", nodes)
	}
	if len(edges) != 1 || edges[0].Type != "imports" {
		t.Fatalf("unexpected edges: %#v", edges)
	}
}

func TestRunRemoteConversationLifecycle(t *testing.T) {
	eng := newCLITestEngine(t)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/conversation" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"id":"conv_1","branch":"main","messages":2}`))
		case r.URL.Path == "/api/v1/conversation/new" && r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`{"id":"conv_2","branch":"main","messages":0}`))
		case r.URL.Path == "/api/v1/conversation/save" && r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case r.URL.Path == "/api/v1/conversation/load" && r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`{"id":"conv_1","branch":"main","messages":2}`))
		case r.URL.Path == "/api/v1/conversation/branches" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"branches":["main","alt"]}`))
		case r.URL.Path == "/api/v1/conversation/branches/create" && r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`{"status":"ok","branch":"alt"}`))
		case r.URL.Path == "/api/v1/conversation/branches/switch" && r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`{"status":"ok","branch":"alt"}`))
		case r.URL.Path == "/api/v1/conversation/branches/compare" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"branch_a":"main","branch_b":"alt","shared_prefix_count":2}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
		}
	}))
	defer ts.Close()

	cases := [][]string{
		{"conversation", "active", "--url", ts.URL},
		{"conversation", "new", "--url", ts.URL},
		{"conversation", "save", "--url", ts.URL},
		{"conversation", "load", "--url", ts.URL, "--id", "conv_1"},
		{"conversation", "branch", "list", "--url", ts.URL},
		{"conversation", "branch", "create", "--url", ts.URL, "--name", "alt"},
		{"conversation", "branch", "switch", "--url", ts.URL, "--name", "alt"},
		{"conversation", "branch", "compare", "--url", ts.URL, "--a", "main", "--b", "alt"},
	}
	for _, args := range cases {
		if code := runRemote(context.Background(), eng, args, true); code != 0 {
			t.Fatalf("runRemote %v exit=%d", args, code)
		}
	}
}

func TestRunRemotePromptLifecycle(t *testing.T) {
	eng := newCLITestEngine(t)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/prompts" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"prompts":[{"id":"system.base","type":"system","task":"general"}]}`))
		case r.URL.Path == "/api/v1/prompts/render" && r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`{"type":"system","task":"security","language":"go","prompt":"SECURITY PROMPT"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
		}
	}))
	defer ts.Close()

	cases := [][]string{
		{"prompt", "list", "--url", ts.URL},
		{"prompt", "render", "--url", ts.URL, "--task", "security", "--query", "auth audit"},
	}
	for _, args := range cases {
		if code := runRemote(context.Background(), eng, args, true); code != 0 {
			t.Fatalf("runRemote %v exit=%d", args, code)
		}
	}
}

func TestRunRemoteContextBudget(t *testing.T) {
	eng := newCLITestEngine(t)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/context/budget" || r.Method != http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
			return
		}
		if got := r.URL.Query().Get("q"); got != "security audit auth middleware" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"unexpected query"}`))
			return
		}
		_, _ = w.Write([]byte(`{"provider":"zai","model":"glm-5.1","task":"security","max_tokens_total":12000}`))
	}))
	defer ts.Close()

	cases := [][]string{
		{"context", "budget", "--url", ts.URL, "--query", "security audit auth middleware"},
		{"context", "--url", ts.URL, "--query", "security audit auth middleware"},
	}
	for _, args := range cases {
		if code := runRemote(context.Background(), eng, args, true); code != 0 {
			t.Fatalf("runRemote %v exit=%d", args, code)
		}
	}
}

func TestRunRemoteAnalyzeWithMagicDoc(t *testing.T) {
	eng := newCLITestEngine(t)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/analyze" || r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
			return
		}
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"bad request"}`))
			return
		}
		if req["magicdoc"] != true {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"magicdoc flag missing"}`))
			return
		}
		_, _ = w.Write([]byte(`{"report":{"files":12},"magicdoc":{"status":"ok","updated":true}}`))
	}))
	defer ts.Close()

	args := []string{"analyze", "--url", ts.URL, "--full", "--magicdoc", "--magicdoc-title", "Remote Analyze Brief"}
	if code := runRemote(context.Background(), eng, args, true); code != 0 {
		t.Fatalf("runRemote %v exit=%d", args, code)
	}
}

func TestRunRemoteMagicDocLifecycle(t *testing.T) {
	eng := newCLITestEngine(t)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/magicdoc" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"path":".dfmc/magic/MAGIC_DOC.md","exists":true,"content":"# MAGIC DOC: X"}`))
		case r.URL.Path == "/api/v1/magicdoc/update" && r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`{"status":"ok","path":".dfmc/magic/MAGIC_DOC.md","updated":true,"bytes":1200}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
		}
	}))
	defer ts.Close()

	cases := [][]string{
		{"magicdoc", "show", "--url", ts.URL},
		{"magicdoc", "update", "--url", ts.URL, "--title", "Remote Brief"},
	}
	for _, args := range cases {
		if code := runRemote(context.Background(), eng, args, true); code != 0 {
			t.Fatalf("runRemote %v exit=%d", args, code)
		}
	}
}
