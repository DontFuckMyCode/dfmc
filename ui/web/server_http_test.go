package web

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func TestNewHTTPServerAppliesTimeoutHardening(t *testing.T) {
	srv := NewHTTPServer("127.0.0.1:0", http.NewServeMux())
	if srv.ReadHeaderTimeout != serverReadHeaderTimeout {
		t.Fatalf("ReadHeaderTimeout=%v want %v", srv.ReadHeaderTimeout, serverReadHeaderTimeout)
	}
	if srv.ReadTimeout != serverReadTimeout {
		t.Fatalf("ReadTimeout=%v want %v", srv.ReadTimeout, serverReadTimeout)
	}
	if srv.WriteTimeout != serverWriteTimeout {
		t.Fatalf("WriteTimeout=%v want %v", srv.WriteTimeout, serverWriteTimeout)
	}
	if srv.IdleTimeout != serverIdleTimeout {
		t.Fatalf("IdleTimeout=%v want %v", srv.IdleTimeout, serverIdleTimeout)
	}
	if srv.MaxHeaderBytes != serverMaxHeaderBytes {
		t.Fatalf("MaxHeaderBytes=%d want %d", srv.MaxHeaderBytes, serverMaxHeaderBytes)
	}
}

func TestHandlerAppliesBearerAuthWhenConfigured(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Web.Auth = "token"
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	srv := New(eng, "127.0.0.1", 0)
	srv.SetBearerToken("secret-token")
	handler := srv.Handler()

	// Use http.Get against a live test server so the Host header is
	// correctly set to the actual server address (127.0.0.1:PORT).
	ts := httptest.NewServer(handler)
	defer ts.Close()

	// Without token -> 401
	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("get root: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET / without token: expected 401, got %d", resp.StatusCode)
	}

	// With correct token -> 200
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer secret-token")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get root with token: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("GET / with bearer token: expected 200, got %d", resp2.StatusCode)
	}

	// /api/v1/status also protected
	apiReq, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/status", nil)
	apiResp, err := http.DefaultClient.Do(apiReq)
	if err != nil {
		t.Fatalf("get status: %v", err)
	}
	defer apiResp.Body.Close()
	if apiResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing auth should 401, got %d", apiResp.StatusCode)
	}

	// With token -> 200
	apiReq2, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/status", nil)
	apiReq2.Header.Set("Authorization", "Bearer secret-token")
	apiResp2, err := http.DefaultClient.Do(apiReq2)
	if err != nil {
		t.Fatalf("get status with token: %v", err)
	}
	defer apiResp2.Body.Close()
	if apiResp2.StatusCode != http.StatusOK {
		t.Fatalf("bearer token should authorize, got %d", apiResp2.StatusCode)
	}
}

func TestNewClampsAuthNoneToLoopback(t *testing.T) {
	eng := newTestEngine(t)
	eng.Config.Web.Auth = "none"
	srv := New(eng, "0.0.0.0", 7777)
	if got := srv.addr; got != "127.0.0.1:7777" {
		t.Fatalf("expected auth=none bind to clamp to loopback, got %q", got)
	}
}

func TestSecurityHeadersCSPDisallowsInlineStyles(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(rec, req)

	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "style-src 'self'") {
		t.Fatalf("expected style-src self in CSP, got %q", csp)
	}
	if strings.Contains(csp, "unsafe-inline") {
		t.Fatalf("CSP must not allow unsafe-inline styles, got %q", csp)
	}
}

func TestHandlerRejectsWSQueryTokenWhenBearerAuthEnabled(t *testing.T) {
	eng := newTestEngine(t)
	eng.Config.Web.Auth = "token"
	srv := New(eng, "127.0.0.1", 0)
	srv.SetBearerToken("secret-token")
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/ws?type=test:event&token=secret-token")
	if err != nil {
		t.Fatalf("ws request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected ws query token to be rejected, got %d", resp.StatusCode)
	}
}

func TestHandlerAllowsWSAuthorizationHeaderWhenConfigured(t *testing.T) {
	eng := newTestEngine(t)
	eng.Config.Web.Auth = "token"
	srv := New(eng, "127.0.0.1", 0)
	srv.SetBearerToken("secret-token")
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/ws?type=test:event", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer secret-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("ws request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected authorized ws stream, got %d", resp.StatusCode)
	}
	reader := bufio.NewReader(resp.Body)
	done := make(chan string, 1)
	go func() {
		line, _ := reader.ReadString('\n')
		done <- line
	}()
	select {
	case line := <-done:
		if !strings.Contains(line, `"type":"connected"`) {
			t.Fatalf("expected connected frame, got %q", line)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for connected frame")
	}
}

func TestHostAllowlistMiddlewareRejectsForeignHost(t *testing.T) {
	handler := hostAllowlistMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), []string{"127.0.0.1:7777", "localhost:7777"})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req.Host = "evil.example.com"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusMisdirectedRequest {
		t.Fatalf("expected 421 Misdirected Request for foreign Host, got %d", rec.Code)
	}
}

func TestHostAllowlistMiddlewareAcceptsAllowedHost(t *testing.T) {
	handler := hostAllowlistMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), []string{"127.0.0.1:7777", "localhost:7777"})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req.Host = "127.0.0.1:7777"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for allowed Host, got %d", rec.Code)
	}
}

func TestHostAllowlistMiddlewareAcceptsWildcard(t *testing.T) {
	handler := hostAllowlistMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), []string{"*"})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req.Host = "anything.example.com"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for wildcard allowlist, got %d", rec.Code)
	}
}

func TestHostAllowlistMiddlewareAcceptsLocalhost(t *testing.T) {
	handler := hostAllowlistMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), []string{"127.0.0.1:7777", "localhost:7777"})

	// localhost:7777 on allowed list
	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req.Host = "localhost:7777"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for localhost:7777, got %d", rec.Code)
	}

	// With host-stripping matching, "127.0.0.1" on allowlist matches
	// "127.0.0.1:9999" — port is stripped before comparison so ephemeral
	// port servers work without explicit port in allowlist.
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req2.Host = "127.0.0.1:9999"
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for host-strip match, got %d", rec2.Code)
	}
}

func TestClientIPKeyStripsPortAndPrefersForwardedFor(t *testing.T) {
	srv := &Server{trustedProxies: []string{"127.0.0.1", "localhost", "::1"}}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	if got := srv.clientIPKey(req); got != "127.0.0.1" {
		t.Fatalf("expected remote addr port stripped, got %q", got)
	}

	// When remote is loopback (trusted proxy), XFF is honored.
	// VULN-010 fix: rightmost entry wins, not leftmost.
	req.Header.Set("X-Forwarded-For", "198.51.100.7, 10.0.0.5")
	if got := srv.clientIPKey(req); got != "10.0.0.5" {
		t.Fatalf("expected rightmost XFF entry (10.0.0.5) when remote is trusted proxy, got %q", got)
	}
}

func TestClientIPKeyIgnoresXFFWhenRemoteNotTrusted(t *testing.T) {
	srv := &Server{trustedProxies: []string{"127.0.0.1", "localhost", "::1"}}

	// Remote is NOT a trusted proxy — XFF must be ignored.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req.RemoteAddr = "203.0.113.5:54321"
	req.Header.Set("X-Forwarded-For", "198.51.100.7, 10.0.0.5")
	if got := srv.clientIPKey(req); got != "203.0.113.5" {
		t.Fatalf("expected XFF ignored when remote is not trusted proxy, got %q", got)
	}
}

func TestClientIPKeyEmptyProxiesListIgnoresXFF(t *testing.T) {
	srv := &Server{trustedProxies: nil} // no proxies trusted

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Header.Set("X-Forwarded-For", "198.51.100.7, 10.0.0.5")
	if got := srv.clientIPKey(req); got != "127.0.0.1" {
		t.Fatalf("expected XFF ignored when no proxies configured, got %q", got)
	}
}
