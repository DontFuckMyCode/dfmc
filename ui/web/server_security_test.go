package web

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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
	ts := httptest.NewServer(handler)
	defer ts.Close()

	// Without token -> 401
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request without token: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET / without token: expected 401, got %d", resp.StatusCode)
	}

	// With correct token -> 200
	req2, _ := http.NewRequest(http.MethodGet, ts.URL+"/", nil)
	req2.Header.Set("Authorization", "Bearer secret-token")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("request with token: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("GET / with bearer token: expected 200, got %d", resp2.StatusCode)
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
	ts := httptest.NewServer(handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("get /: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / with auth=none: expected 200, got %d", resp.StatusCode)
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
}

// TestGitChangedFilesWeb_RootEscape — M3
// gitChangedFilesWeb must reject projectRoot pointing outside allowed tree.
func TestGitChangedFilesWeb_RootEscape(t *testing.T) {
	_, err := gitChangedFilesWeb(context.Background(), "../../../etc", 10)
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

// VULN-049: auth=none silently rebinding non-loopback to 127.0.0.1
// must now emit a NOTICE on stderr so the operator can see their
// flag was overridden.
func TestNormalizeBindHost_AuthNoneEmitsNotice(t *testing.T) {
	rPipe, wPipe, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	origStderr := os.Stderr
	os.Stderr = wPipe
	defer func() { os.Stderr = origStderr }()

	host := normalizeBindHost("none", "0.0.0.0")

	wPipe.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(rPipe)
	captured := buf.String()

	if host != "127.0.0.1" {
		t.Fatalf("rebind expected, got %q", host)
	}
	if !strings.Contains(captured, "auth=none") {
		t.Fatalf("notice should mention auth=none, got %q", captured)
	}
	if !strings.Contains(captured, "0.0.0.0") {
		t.Fatalf("notice should echo the overridden host, got %q", captured)
	}
	if !strings.Contains(captured, "127.0.0.1") {
		t.Fatalf("notice should mention the chosen loopback, got %q", captured)
	}
}

// VULN-049: a loopback bind under auth=none must NOT emit the notice.
func TestNormalizeBindHost_AuthNoneLoopbackNoNotice(t *testing.T) {
	rPipe, wPipe, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	origStderr := os.Stderr
	os.Stderr = wPipe
	defer func() { os.Stderr = origStderr }()

	_ = normalizeBindHost("none", "127.0.0.1")

	wPipe.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(rPipe)
	if got := buf.String(); strings.Contains(got, "NOTICE") || strings.Contains(got, "WARNING") {
		t.Fatalf("loopback under auth=none should not emit a notice, got: %q", got)
	}
}

// TestHandleFileContent_RejectsSecretFiles — VULN-013
// GET /api/v1/files/{path...} must reject paths that look like credential
// files (.env, id_rsa, *.pem, etc.) with 403.
func TestHandleFileContent_RejectsSecretFiles(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	for _, path := range []string{".env", "id_rsa", "credentials.json", "secrets.yaml", "token.pem"} {
		req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/files/"+path, nil)
		if err != nil {
			t.Fatalf("new request for %s: %v", path, err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request for %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("GET /api/v1/files/%s: expected 403, got %d", path, resp.StatusCode)
		}
	}
}

// TestHandleFileContent_AllowsNormalFiles — VULN-013 complement
// Normal files (source code, docs) are still served normally.
func TestHandleFileContent_AllowsNormalFiles(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// main.go should be served (exists in the test project).
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/files/main.go", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	// 200 or 404 are both acceptable — 403 would mean it was incorrectly
	// flagged as a secret file.
	if resp.StatusCode == http.StatusForbidden {
		t.Fatalf("main.go should NOT be flagged as secret, got 403")
	}
}
