package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/engine"
)

// TestHostAllowlist_RejectsForeignHost pins VULN-003: a request whose
// Host header isn't on the allowlist is rejected with 421 Misdirected
// Request before it can reach a handler. Defends against DNS
// rebinding from a foreign hostname → loopback.
func TestHostAllowlist_RejectsForeignHost(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	srv.SetAllowedHosts([]string{"127.0.0.1:7777", "localhost:7777"})

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/status", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Host = "evil.example.com"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMisdirectedRequest {
		t.Fatalf("expected 421 Misdirected Request for foreign Host, got %d", resp.StatusCode)
	}
}

// TestHostAllowlist_AcceptsAllowedHost confirms the allowlist doesn't
// over-reject — a Host that matches the configured allowlist passes.
func TestHostAllowlist_AcceptsAllowedHost(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	// Use a precise host string and send the request with a matching
	// Host header. httptest assigns its own port but we only need the
	// allowlist match to succeed so the middleware passes.
	srv.SetAllowedHosts([]string{"approved.local"})

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/status", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Host = "approved.local"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for allowed Host, got %d", resp.StatusCode)
	}
}

// TestHostAllowlist_WildcardDisablesCheck — explicit "*" in the list
// must let any Host pass (escape hatch for edge deployments).
func TestHostAllowlist_WildcardDisablesCheck(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	srv.SetAllowedHosts([]string{"*"})

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/status", nil)
	req.Host = "any.weird.thing"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 with wildcard host allowlist, got %d", resp.StatusCode)
	}
}

// TestCheckWebSocketOrigin_AcceptsAllowed — direct unit-level check
// of the Origin allowlist; covers the WS upgrade path without the
// websocket protocol overhead.
func TestCheckWebSocketOrigin_AcceptsAllowed(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Web.AllowedOrigins = []string{"http://127.0.0.1:7777"}
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	t.Cleanup(func() { _ = eng.Shutdown() })
	s := New(eng, "127.0.0.1", 7777)

	cases := []struct {
		origin string
		want   bool
	}{
		{"", true},                                   // native client (curl, dfmc remote)
		{"http://127.0.0.1:7777", true},              // workbench
		{"http://evil.example.com", false},           // cross-origin browser tab
		{"https://127.0.0.1:7777", false},            // wrong scheme
		{"HTTP://127.0.0.1:7777", true},              // case-insensitive
	}
	for _, c := range cases {
		req, _ := http.NewRequest(http.MethodGet, "/", nil)
		if c.origin != "" {
			req.Header.Set("Origin", c.origin)
		}
		got := s.checkWebSocketOrigin(req)
		if got != c.want {
			t.Errorf("checkWebSocketOrigin(%q) = %v, want %v", c.origin, got, c.want)
		}
	}
}

// TestCheckWebSocketOrigin_WildcardEscapeHatch confirms "*" in the
// list lets every cross-origin request through. Documented escape
// hatch for users who deliberately want the old wide-open behaviour.
func TestCheckWebSocketOrigin_WildcardEscapeHatch(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Web.AllowedOrigins = []string{"*"}
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	t.Cleanup(func() { _ = eng.Shutdown() })
	s := New(eng, "127.0.0.1", 7777)

	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "http://anything.example.com")
	if !s.checkWebSocketOrigin(req) {
		t.Fatalf("wildcard origin allowlist must accept any cross-origin request")
	}
}

// TestResolveAllowedOrigins_DefaultIncludesBindAndLocalhost asserts
// the default allowlist covers the workbench's own URL plus localhost
// alias when no config override is provided. Closes a regression
// where users could be locked out of their own embedded UI.
func TestResolveAllowedOrigins_DefaultIncludesBindAndLocalhost(t *testing.T) {
	got := resolveAllowedOrigins(nil, "127.0.0.1", 7777)
	want := []string{
		"http://127.0.0.1:7777",
		"http://localhost:7777",
		"http://127.0.0.1:7777",
	}
	if !equalAfterNormalize(got, want) {
		t.Fatalf("default origins = %v, want superset of %v", got, want)
	}
}

func equalAfterNormalize(a, b []string) bool {
	seen := make(map[string]bool, len(a))
	for _, s := range a {
		seen[strings.ToLower(strings.TrimSpace(s))] = true
	}
	for _, s := range b {
		if !seen[strings.ToLower(strings.TrimSpace(s))] {
			return false
		}
	}
	return true
}
