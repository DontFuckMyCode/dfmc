package web

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestContentTypeMiddleware_RejectsNonJSONPost pins VULN-050: a
// CORS-simple POST whose Content-Type isn't application/json must
// be refused with 415 before the handler decodes the body. Pre-fix,
// `<form enctype="text/plain">` POSTs landed at the JSON decoder
// because Go's `json.NewDecoder` is content-type-blind.
func TestContentTypeMiddleware_RejectsNonJSONPost(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	cases := []struct {
		name        string
		contentType string
		want415     bool
	}{
		{"text/plain (CORS-simple form)", "text/plain", true},
		{"text/html", "text/html", true},
		{"application/x-www-form-urlencoded", "application/x-www-form-urlencoded", true},
		// Empty Content-Type on a body-bearing request is passed through
		// to the JSON decoder rather than hard-rejected. The decoder
		// itself will reject non-JSON content. We only hard-block types
		// we can definitively identify as non-JSON. This avoids rejecting
		// bodyless POSTs to endpoints like /conversation/undo.
		{"empty (default fetch())", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/tasks",
				bytes.NewBufferString(`{"title":"x"}`))
			if c.contentType != "" {
				req.Header.Set("Content-Type", c.contentType)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("post: %v", err)
			}
			defer resp.Body.Close()
			if c.want415 {
				if resp.StatusCode != http.StatusUnsupportedMediaType {
					t.Fatalf("expected 415 for Content-Type=%q, got %d", c.contentType, resp.StatusCode)
				}
			} else {
				// Not a hard-blocked type — pass through to decoder
				if resp.StatusCode == http.StatusUnsupportedMediaType {
					t.Fatalf("expected non-415 for Content-Type=%q, got 415", c.contentType)
				}
			}
		})
	}
}

// TestContentTypeMiddleware_AcceptsJSONWithCharset confirms the
// middleware doesn't over-reject — `application/json; charset=utf-8`
// is the canonical shape and must pass.
func TestContentTypeMiddleware_AcceptsJSONWithCharset(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/tasks",
		bytes.NewBufferString(`{"title":"valid"}`))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnsupportedMediaType {
		t.Fatalf("application/json; charset=utf-8 should pass; got 415")
	}
}

// TestContentTypeMiddleware_GETUnaffected confirms read endpoints
// keep working without the header — only state-changing methods
// (POST/PATCH/PUT) are gated.
func TestContentTypeMiddleware_GETUnaffected(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/status")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/status should pass without Content-Type; got %d", resp.StatusCode)
	}
}

// TestContentTypeMiddleware_BodylessPOSTAllowed pins the empty-body
// allowance: a POST with ContentLength 0 has nothing for a JSON
// decoder to misinterpret, so the gate doesn't fire.
func TestContentTypeMiddleware_BodylessPOSTAllowed(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/conversation/new", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnsupportedMediaType {
		body := make([]byte, 256)
		n, _ := resp.Body.Read(body)
		t.Fatalf("bodyless POST should not be 415, got: %s", strings.TrimSpace(string(body[:n])))
	}
}
