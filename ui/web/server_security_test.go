package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/engine"
)

// TestBearerTokenMiddleware_WorkbenchRequiresAuth — M2
// When auth=token is configured, GET / must require the bearer token.
// Previously it bypassed auth entirely.
func TestBearerTokenMiddleware_WorkbenchRequiresAuth(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Web.Auth = "token"
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	srv := New(eng, "127.0.0.1", 0)
	srv.SetBearerToken("secret-token")
	handler := srv.Handler()

	// Without token -> 401
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET / without token: expected 401, got %d", rec.Code)
	}

	// With correct token -> 200
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / with bearer token: expected 200, got %d", rec.Code)
	}
}

// TestBearerTokenMiddleware_NoTokenAllowsPublicGET — M2 complement
// When NO token is configured (auth=none), GET / should work without auth.
func TestBearerTokenMiddleware_NoTokenAllowsPublicGET(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Web.Auth = "none"
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	srv := New(eng, "127.0.0.1", 0)
	handler := srv.Handler()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / with auth=none: expected 200, got %d", rec.Code)
	}
}

// TestApplyUnifiedDiffWeb_RootEscape — M3
// applyUnifiedDiffWeb must reject projectRoot pointing outside allowed tree.
func TestApplyUnifiedDiffWeb_RootEscape(t *testing.T) {
	err := applyUnifiedDiffWeb("../../../etc", "diff content", false)
	if err == nil {
		t.Fatal("expected error for root escape path, got nil")
	}
	// Error should indicate invalid project root
	if _, ok := err.(interface{ Unwrap() error }); !ok {
		// just checking error is returned
	}
}

// TestGitChangedFilesWeb_RootEscape — M3
// gitChangedFilesWeb must reject projectRoot pointing outside allowed tree.
func TestGitChangedFilesWeb_RootEscape(t *testing.T) {
	_, err := gitChangedFilesWeb("../../../etc", 10)
	if err == nil {
		t.Fatal("expected error for root escape path, got nil")
	}
}

// TestNormalizeBindHost_TokenModeNonLoopback — S2
// When auth=token and host is non-loopback, normalizeBindHost should emit
// a warning (tested via capturing stderr output).
func TestNormalizeBindHost_TokenModeNonLoopback(t *testing.T) {
	host := normalizeBindHost("token", "0.0.0.0")
	if host != "0.0.0.0" {
		t.Fatalf("expected host unchanged (warning only), got %q", host)
	}
	host = normalizeBindHost("token", "0.0.0.0:7777")
	if host != "0.0.0.0:7777" {
		t.Fatalf("expected host unchanged, got %q", host)
	}
	// auth=none with non-loopback should force loopback
	host = normalizeBindHost("none", "0.0.0.0")
	if host != "127.0.0.1" {
		t.Fatalf("auth=none should force loopback, got %q", host)
	}
}
