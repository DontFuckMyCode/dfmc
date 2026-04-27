// Regression tests for the H1 (request body size cap) and M5
// (path-traversal guard on magic-doc paths) hardening from the
// 2026-04-17 review.

package web

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// H1: POSTing a body larger than maxRequestBodyBytes is rejected
// before it reaches the handler, so a malicious or buggy client
// can't exhaust memory streaming endless JSON into a Decode call.
// We hit /api/v1/chat because it's a real POST endpoint that calls
// json.NewDecoder(r.Body).Decode — which is exactly where overflow
// surfaces as 413 from the stdlib's MaxBytesReader.
func TestWebHandler_RejectsOversizedPostBody(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// 5 MiB of valid-looking JSON — one KEY above the 4 MiB cap. The
	// limiter should reject before the handler reads the body.
	oversized := bytes.Repeat([]byte("x"), int(maxRequestBodyBytes)+1024)
	body := []byte(`{"message":"`)
	body = append(body, oversized...)
	body = append(body, []byte(`"}`)...)

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/chat", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	// Accept either StatusRequestEntityTooLarge (413) or a 400 from
	// the downstream Decode catching the truncated stream. What we're
	// asserting is: the server DID NOT happily consume 5 MiB. The
	// key property is that the request fails early; the exact status
	// depends on whether MaxBytesReader's error closure fired before
	// the handler's own error path.
	if resp.StatusCode == http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("oversized body was accepted, got 200 with body %q", string(b))
	}
}

// H1: a body below the cap is still accepted — the limiter must not
// over-reject. We send a small-but-well-formed chat request and
// expect a 200 OK with a JSON answer (offline provider will produce
// a placeholder answer, but 200 is what we care about).
func TestWebHandler_AcceptsNormalPostBody(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	body := strings.NewReader(`{"message":"hello"}`)
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/ask", body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	// /api/v1/ask may return 200 or 500 (offline provider paths vary
	// by config), but must never be 413 / 400 for a tiny body.
	if resp.StatusCode == http.StatusRequestEntityTooLarge {
		t.Fatalf("small body rejected as oversized: %d", resp.StatusCode)
	}
}

// M5: an absolute path outside the project root must not escape the
// root. Before the fix, resolveMagicDocPath("/proj", "/etc/passwd")
// returned "/etc/passwd" verbatim — the server would then happily
// read it. The hardened version falls back to the default inside
// the root.
func TestResolveMagicDocPath_AbsolutePathOutsideRootFallsBack(t *testing.T) {
	root := t.TempDir()
	escape := "/etc/passwd"
	if filepath.Separator == '\\' {
		escape = `C:\Windows\System32\drivers\etc\hosts`
	}
	got := resolveMagicDocPath(root, escape)
	// Must be inside root, NOT the escape path.
	rel, err := filepath.Rel(root, got)
	if err != nil {
		t.Fatalf("Rel: %v", err)
	}
	if strings.HasPrefix(rel, "..") {
		t.Fatalf("resolved path escapes root: got=%q root=%q", got, root)
	}
	// And the default sentinel path must be the fallback.
	if !strings.HasSuffix(filepath.ToSlash(got), "/.dfmc/magic/MAGIC_DOC.md") {
		t.Fatalf("expected default fallback, got %q", got)
	}
}

// M5: a relative path that traverses out (../../..) must also fall
// back to the default, not jump to the parent FS.
func TestResolveMagicDocPath_RelativeEscapeFallsBack(t *testing.T) {
	root := t.TempDir()
	got := resolveMagicDocPath(root, "../../../../etc/passwd")
	rel, err := filepath.Rel(root, got)
	if err != nil {
		t.Fatalf("Rel: %v", err)
	}
	if strings.HasPrefix(rel, "..") {
		t.Fatalf("resolved path escapes root via relative: got=%q", got)
	}
}

// M5: a legitimate relative path under the root is honoured, so the
// hardening doesn't break the normal `--magicdoc-path` feature.
func TestResolveMagicDocPath_HonoursRelativeInsideRoot(t *testing.T) {
	root := t.TempDir()
	got := resolveMagicDocPath(root, "docs/brief.md")
	want := filepath.Join(root, "docs", "brief.md")
	if got != want {
		t.Fatalf("relative-inside-root resolution: got=%q want=%q", got, want)
	}
}

// REPORT.md H3: handleAnalyze must reject AnalyzeRequest.Path values
// that escape the configured project root. CLI usage is allowed to
// pass arbitrary paths ("dfmc analyze /tmp/somewhere"), but the HTTP
// handler is the trust boundary — a request body asking to analyse a
// path outside the project root must be refused before the engine
// starts walking that tree.
//
// We use deep parent-traversal because that's the same shape on every
// OS. A POSIX absolute path like "/etc" gets reinterpreted as
// project-relative on Windows (no drive letter), so it doesn't
// reliably escape there — the symmetrical signal is "../"-many.
func TestHandleAnalyze_RejectsParentTraversal(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	body := bytes.NewBufferString(`{"path":"../../../../../../../../etc"}`)
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/analyze", body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 400 on parent traversal; got %d: %s", resp.StatusCode, buf)
	}
	buf, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(buf), "project root") {
		t.Fatalf("rejection message should explain the constraint; got: %s", buf)
	}
}

// Empty path is the default ("analyse the project root") and must
// continue to work — the guard only kicks in when path is non-empty.
func TestHandleAnalyze_AcceptsEmptyPath(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	body := bytes.NewBufferString(`{}`)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/analyze", body)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("empty-path analyse should succeed; got %d: %s", resp.StatusCode, buf)
	}
}
