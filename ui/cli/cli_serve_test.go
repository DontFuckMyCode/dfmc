package cli

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBrowserCommandForOS(t *testing.T) {
	target := "http://127.0.0.1:7788"

	name, args, ok := browserCommandForOS("windows", target)
	if !ok || name != "cmd" {
		t.Fatalf("windows command mismatch: ok=%v name=%s args=%v", ok, name, args)
	}
	if len(args) < 4 || args[0] != "/c" || args[1] != "start" {
		t.Fatalf("windows args mismatch: %v", args)
	}

	name, args, ok = browserCommandForOS("darwin", target)
	if !ok || name != "open" || len(args) != 1 || args[0] != target {
		t.Fatalf("darwin command mismatch: ok=%v name=%s args=%v", ok, name, args)
	}

	name, args, ok = browserCommandForOS("linux", target)
	if !ok || name != "xdg-open" || len(args) != 1 || args[0] != target {
		t.Fatalf("linux command mismatch: ok=%v name=%s args=%v", ok, name, args)
	}

	_, _, ok = browserCommandForOS("plan9", target)
	if ok {
		t.Fatal("expected unsupported platform to return ok=false")
	}
}

// bearerTokenMiddleware tests

func TestBearerTokenMiddleware_Healthz(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mw := bearerTokenMiddleware(h, "secret-token")
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("healthz: got %d; want 200", rec.Code)
	}
}

func TestBearerTokenMiddleware_WebRoot_GET(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mw := bearerTokenMiddleware(h, "secret-token")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("GET /: got %d; want 200 (web root always allowed)", rec.Code)
	}
}

func TestBearerTokenMiddleware_Unauthorized(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mw := bearerTokenMiddleware(h, "secret-token")

	// No token
	req := httptest.NewRequest(http.MethodGet, "/api/v1/foo", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no token: got %d; want 401", rec.Code)
	}

	// Wrong token
	req = httptest.NewRequest(http.MethodGet, "/api/v1/foo", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	rec = httptest.NewRecorder()
	mw.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong token: got %d; want 401", rec.Code)
	}
}

func TestBearerTokenMiddleware_Authorized(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mw := bearerTokenMiddleware(h, "secret-token")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/foo", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("correct token: got %d; want 200", rec.Code)
	}
}

func TestBearerTokenMiddleware_WSTokenQueryParam(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mw := bearerTokenMiddleware(h, "secret-token")
	req := httptest.NewRequest(http.MethodGet, "/ws?token=secret-token", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("/ws with query token: got %d; want 200", rec.Code)
	}
}

func TestBearerTokenMiddleware_EmptyTokenRejectsNonHealthz(t *testing.T) {
	// With rawToken="" the middleware still checks rawToken!="" before
	// the header comparison, so non-/healthz requests get 401.
	// This behaviour is intentional — see bearerTokenMiddleware source.
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mw := bearerTokenMiddleware(h, "")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/foo", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("empty token on /api/v1/foo: got %d; want 401 (middleware rejects when rawToken is empty)", rec.Code)
	}
}

// writeRemoteJSON tests

func TestWriteRemoteJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	writeRemoteJSON(rec, http.StatusOK, map[string]any{"status": "ok", "count": 42})
	if rec.Code != http.StatusOK {
		t.Errorf("status code: got %d; want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("content-type: got %q; want %q", ct, "application/json; charset=utf-8")
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"status":"ok"`) {
		t.Errorf("body: got %q; want contains status:ok", body)
	}
}
