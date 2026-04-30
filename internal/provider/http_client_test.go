package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestNewProviderHTTPClient_RefusesPrivateEndpoint regresses the gap
// where newProviderHTTPClient had no SSRF guard at all. Pre-fix every
// Anthropic/OpenAI/Google API client would happily dial cloud-metadata
// (169.254.169.254) or any private IP, exfiltrating the response in
// the LLM body. The previous safe_http.go doc-comment claimed provider
// "kept its own copy" of the guard — historical fiction; there was
// nothing to keep a copy of.
//
// We construct an http.Client via newProviderHTTPClient with a public
// endpoint, then issue a request to a private IP. The dial must fail
// with a "SSRF guard" error before any bytes leave the host.
func TestNewProviderHTTPClient_RefusesPrivateEndpoint(t *testing.T) {
	cases := []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://10.0.0.1/internal",
		"http://192.168.1.1/admin",
		"http://[::1]:9999/local",
	}
	for _, target := range cases {
		t.Run(target, func(t *testing.T) {
			c := newProviderHTTPClient(2*time.Second, "https://api.example.com/v1")
			req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
			_, err := c.Do(req)
			if err == nil {
				t.Fatalf("dial to %s should be refused; got nil error", target)
			}
			if !strings.Contains(err.Error(), "SSRF guard") {
				t.Fatalf("error should mention SSRF guard; got %v", err)
			}
		})
	}
}

// TestNewProviderHTTPClient_AllowsLoopbackWhenEndpointIsLoopback
// pins the operator opt-in: when the configured endpoint is itself
// loopback (Ollama, on-prem mirror, dev test server) the guard
// relaxes. Without this the local-LLM path would be unusable.
func TestNewProviderHTTPClient_AllowsLoopbackWhenEndpointIsLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := newProviderHTTPClient(2*time.Second, srv.URL)
	resp, err := c.Get(srv.URL + "/probe")
	if err != nil {
		t.Fatalf("loopback override should allow dial, got %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}
}
