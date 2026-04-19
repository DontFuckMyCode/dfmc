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

	rootReq := httptest.NewRequest(http.MethodGet, "/", nil)
	rootRec := httptest.NewRecorder()
	handler.ServeHTTP(rootRec, rootReq)
	if rootRec.Code != http.StatusOK {
		t.Fatalf("GET / should stay public, got %d", rootRec.Code)
	}

	apiReq := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	apiRec := httptest.NewRecorder()
	handler.ServeHTTP(apiRec, apiReq)
	if apiRec.Code != http.StatusUnauthorized {
		t.Fatalf("missing auth should 401, got %d", apiRec.Code)
	}

	apiReq = httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	apiReq.Header.Set("Authorization", "Bearer secret-token")
	apiRec = httptest.NewRecorder()
	handler.ServeHTTP(apiRec, apiReq)
	if apiRec.Code != http.StatusOK {
		t.Fatalf("bearer token should authorize, got %d", apiRec.Code)
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

func TestClientIPKeyStripsPortAndPrefersForwardedFor(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	if got := clientIPKey(req); got != "127.0.0.1" {
		t.Fatalf("expected remote addr port stripped, got %q", got)
	}
	req.Header.Set("X-Forwarded-For", "198.51.100.7, 10.0.0.5")
	if got := clientIPKey(req); got != "198.51.100.7" {
		t.Fatalf("expected forwarded for to win, got %q", got)
	}
}
