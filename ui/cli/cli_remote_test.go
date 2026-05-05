package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/engine"
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

	// GET / is the workbench HTML — the operator needs to load it to enter
	// their token in the browser. Gating it would create a chicken-and-egg
	// lockout where the only way to log in is via the page they can't load.
	t.Run("root html bypasses auth", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET / must be reachable without a token (got %d)", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "ok") {
			t.Fatalf("expected handler body to flow through, got %q", rec.Body.String())
		}
	})

	// EventSource cannot set custom headers, so /ws (the SSE activity stream)
	// would 401 forever in token mode if we only accepted the Authorization
	// header. The query-param fallback is the documented workaround.
	t.Run("query param token authorizes", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/ws?token=secret-token", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("query-param token should authorize (got %d)", rec.Code)
		}
	})

	t.Run("query param token rejected on api routes", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/status?token=secret-token", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("query-param token must stay scoped to /ws (got %d)", rec.Code)
		}
	})

	t.Run("wrong query param token rejected", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/ws?token=nope", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("bad query token must 401 (got %d)", rec.Code)
		}
	})

	// POST / (or any other method on /) is NOT the static-HTML bypass and
	// must still require auth — the bypass is scoped to GET only so a
	// crafted POST against the root can't slip past the gate.
	t.Run("non-GET root still gated", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("POST / must require auth (got %d)", rec.Code)
		}
	})
}

// The "refuse --auth=none when bound off-loopback" guard in runServe
// depends entirely on this classifier. If someone tightens the list
// (e.g. drops ::1) or loosens it (e.g. adds 0.0.0.0), the guard's
// behaviour flips silently — these cases pin the expected semantic.
func TestIsLoopbackBindHost(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		// Loopback: fine to leave auth=none.
		{"127.0.0.1", true},
		{"localhost", true},
		{"LocalHost", true}, // case-insensitive
		{"::1", true},
		{"[::1]", true},     // bracketed IPv6 literal (URL-style)
		{"127.0.0.2", true}, // anything in 127.0.0.0/8 is loopback per RFC
		{" 127.0.0.1 ", true},
		// Not loopback: auth=none would expose the API.
		{"0.0.0.0", false}, // bind-all — explicitly off-box
		{"", false},        // empty == bind-all in net.Listen
		{"192.168.1.10", false},
		{"10.0.0.5", false},
		{"::", false},
		{"example.com", false},
	}
	for _, c := range cases {
		if got := isLoopbackBindHost(c.in); got != c.want {
			t.Errorf("isLoopbackBindHost(%q) = %v, want %v", c.in, got, c.want)
		}
	}
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
		case r.URL.Path == "/api/v1/prompts/recommend" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"query":"auth audit","recommendation":{"task":"security","profile":"deep","prompt_budget_tokens":1200}}`))
		case r.URL.Path == "/api/v1/prompts/stats" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"template_count":12,"warning_count":0,"max_template_tokens":450}`))
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
		{"prompt", "recommend", "--url", ts.URL, "--query", "auth audit"},
		{"prompt", "stats", "--url", ts.URL, "--max-template-tokens", "450"},
	}
	for _, args := range cases {
		if code := runRemote(context.Background(), eng, args, true); code != 0 {
			t.Fatalf("runRemote %v exit=%d", args, code)
		}
	}
}

func TestRunRemotePromptStatsFailOnWarning(t *testing.T) {
	eng := newCLITestEngine(t)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/prompts/stats" || r.Method != http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
			return
		}
		_, _ = w.Write([]byte(`{"template_count":12,"warning_count":2,"max_template_tokens":450}`))
	}))
	defer ts.Close()

	args := []string{"prompt", "stats", "--url", ts.URL, "--fail-on-warning"}
	if code := runRemote(context.Background(), eng, args, true); code != 1 {
		t.Fatalf("expected fail-on-warning exit=1, got %d", code)
	}
}

func TestRunRemotePromptRenderWithRuntimeOverrides(t *testing.T) {
	eng := newCLITestEngine(t)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/prompts/render" || r.Method != http.MethodPost {
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
		if req["runtime_tool_style"] != "function-calling" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"runtime_tool_style missing"}`))
			return
		}
		if req["runtime_max_context"] != float64(1000) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"runtime_max_context missing"}`))
			return
		}
		if req["role"] != "security_auditor" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"role missing"}`))
			return
		}
		_, _ = w.Write([]byte(`{"type":"system","task":"security","language":"go","prompt":"SECURITY PROMPT"}`))
	}))
	defer ts.Close()

	args := []string{
		"prompt", "render", "--url", ts.URL, "--task", "security", "--query", "auth audit",
		"--role", "security_auditor",
		"--runtime-tool-style", "function-calling",
		"--runtime-max-context", "1000",
	}
	if code := runRemote(context.Background(), eng, args, true); code != 0 {
		t.Fatalf("runRemote %v exit=%d", args, code)
	}
}

func TestRunRemotePromptRecommendWithRuntimeOverrides(t *testing.T) {
	eng := newCLITestEngine(t)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/prompts/recommend" || r.Method != http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
			return
		}
		if got := r.URL.Query().Get("runtime_tool_style"); got != "function-calling" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"runtime_tool_style missing"}`))
			return
		}
		if got := r.URL.Query().Get("runtime_max_context"); got != "1000" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"runtime_max_context missing"}`))
			return
		}
		_, _ = w.Write([]byte(`{"query":"auth audit","recommendation":{"task":"security","profile":"compact","tool_style":"function-calling","max_context":1000}}`))
	}))
	defer ts.Close()

	args := []string{
		"prompt", "recommend", "--url", ts.URL, "--query", "auth audit",
		"--runtime-tool-style", "function-calling",
		"--runtime-max-context", "1000",
	}
	if code := runRemote(context.Background(), eng, args, true); code != 0 {
		t.Fatalf("runRemote %v exit=%d", args, code)
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

func TestRunRemoteContextRecommend(t *testing.T) {
	eng := newCLITestEngine(t)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/context/recommend" || r.Method != http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
			return
		}
		if got := r.URL.Query().Get("q"); got != "debug [[file:internal/auth/service.go]]" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"unexpected query"}`))
			return
		}
		_, _ = w.Write([]byte(`{"query":"debug [[file:internal/auth/service.go]]","preview":{"task":"debug"},"recommendations":[{"severity":"info","code":"balanced_budget","message":"ok"}]}`))
	}))
	defer ts.Close()

	args := []string{"context", "recommend", "--url", ts.URL, "--query", "debug [[file:internal/auth/service.go]]"}
	if code := runRemote(context.Background(), eng, args, true); code != 0 {
		t.Fatalf("runRemote %v exit=%d", args, code)
	}
}

func TestRunRemoteContextBudgetWithRuntimeOverrides(t *testing.T) {
	eng := newCLITestEngine(t)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/context/budget" || r.Method != http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
			return
		}
		if got := r.URL.Query().Get("runtime_tool_style"); got != "function-calling" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"runtime_tool_style missing"}`))
			return
		}
		if got := r.URL.Query().Get("runtime_max_context"); got != "1000" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"runtime_max_context missing"}`))
			return
		}
		_, _ = w.Write([]byte(`{"provider":"zai","model":"glm-5.1","task":"security","provider_max_context":1000,"max_tokens_total":512}`))
	}))
	defer ts.Close()

	args := []string{
		"context", "budget", "--url", ts.URL, "--query", "security audit auth middleware",
		"--runtime-tool-style", "function-calling",
		"--runtime-max-context", "1000",
	}
	if code := runRemote(context.Background(), eng, args, true); code != 0 {
		t.Fatalf("runRemote %v exit=%d", args, code)
	}
}

func TestRunRemoteContextRecommendWithRuntimeOverrides(t *testing.T) {
	eng := newCLITestEngine(t)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/context/recommend" || r.Method != http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
			return
		}
		if got := r.URL.Query().Get("runtime_tool_style"); got != "function-calling" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"runtime_tool_style missing"}`))
			return
		}
		if got := r.URL.Query().Get("runtime_max_context"); got != "1000" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"runtime_max_context missing"}`))
			return
		}
		_, _ = w.Write([]byte(`{"query":"debug [[file:internal/auth/service.go]]","preview":{"task":"debug","provider_max_context":1000},"recommendations":[{"severity":"warn","code":"near_context_cap","message":"tight budget"}]}`))
	}))
	defer ts.Close()

	args := []string{
		"context", "recommend", "--url", ts.URL, "--query", "debug [[file:internal/auth/service.go]]",
		"--runtime-tool-style", "function-calling",
		"--runtime-max-context", "1000",
	}
	if code := runRemote(context.Background(), eng, args, true); code != 0 {
		t.Fatalf("runRemote %v exit=%d", args, code)
	}
}

func TestRunRemoteContextBrief(t *testing.T) {
	eng := newCLITestEngine(t)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/context/brief" || r.Method != http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
			return
		}
		if got := r.URL.Query().Get("max_words"); got != "180" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"unexpected max_words"}`))
			return
		}
		if got := r.URL.Query().Get("path"); got != "docs/BRIEF.md" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"unexpected path"}`))
			return
		}
		_, _ = w.Write([]byte(`{"path":"docs/BRIEF.md","exists":true,"max_words":180,"word_count":12,"brief":"Context brief line"}`))
	}))
	defer ts.Close()

	args := []string{"context", "brief", "--url", ts.URL, "--max-words", "180", "--path", "docs/BRIEF.md"}
	if code := runRemote(context.Background(), eng, args, true); code != 0 {
		t.Fatalf("runRemote %v exit=%d", args, code)
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

// runServe must refuse to start with --auth=none on a non-loopback
// host, since the web API exposes tool/file/shell endpoints. We build
// a minimal Engine (no Init — no bbolt, no providers) so the guard
// runs long before any listener touches the network, and we don't
// race with other tests that may hold the engine's bolt lock.
func TestRunServe_RefusesNoAuthOffLoopback(t *testing.T) {
	stub := &engine.Engine{Config: config.DefaultConfig()}

	// Redirect stderr to capture the refusal message. The guard fires
	// before any goroutines, so a plain pipe + sync read is enough.
	origStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w

	code := runServe(context.Background(), stub, []string{
		"--host", "0.0.0.0",
		"--port", "0",
		"--auth", "none",
	}, true)

	_ = w.Close()
	os.Stderr = origStderr
	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	msg := string(buf[:n])

	if code != 2 {
		t.Fatalf("want exit code 2 (refuse), got %d; stderr=%q", code, msg)
	}
	if !strings.Contains(msg, "refusing") || !strings.Contains(msg, "--auth=token") || !strings.Contains(msg, "--insecure") {
		t.Fatalf("stderr missing clear guidance, got: %q", msg)
	}
}

// Same guard applies to `dfmc remote start` — the attack surface is
// identical (both mount the web API), so the parallel test pins the
// parallel behaviour.
func TestRunRemoteStart_RefusesNoAuthOffLoopback(t *testing.T) {
	stub := &engine.Engine{Config: config.DefaultConfig()}

	origStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w

	code := runRemote(context.Background(), stub, []string{
		"start",
		"--host", "0.0.0.0",
		"--ws-port", "0",
		"--auth", "none",
	}, true)

	_ = w.Close()
	os.Stderr = origStderr
	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	msg := string(buf[:n])

	if code != 2 {
		t.Fatalf("want exit code 2 (refuse), got %d; stderr=%q", code, msg)
	}
	if !strings.Contains(msg, "refusing") || !strings.Contains(msg, "--insecure") {
		t.Fatalf("stderr missing clear guidance, got: %q", msg)
	}
}
