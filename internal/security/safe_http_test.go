package security

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestNewSafeHTTPClient_RefusesPrivateIP pins VULN-057/058: a
// hostile config pointing the client at a private IP must fail
// with a clear "SSRF guard" error before any data leaves the host.
// We test against AWS cloud-metadata canonical IP (169.254.169.254)
// — the iconic SSRF target.
func TestNewSafeHTTPClient_RefusesPrivateIP(t *testing.T) {
	cases := []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://10.0.0.1/internal",
		"http://192.168.1.1/admin",
		"http://172.16.0.1/api",
		"http://[::1]/local",
		"http://127.0.0.1:9999/local",
	}
	for _, target := range cases {
		t.Run(target, func(t *testing.T) {
			// Public-host endpoint (so the guard is active).
			c := NewSafeHTTPClient(2*time.Second, "https://example.com/")
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

// TestNewSafeHTTPClient_AllowsLoopbackWhenEndpointIsLoopback
// confirms the explicit-opt-in path: when the configured `endpoint`
// is itself loopback, the guard relaxes (Ollama / on-prem mirror /
// local dev). httptest.NewServer binds to 127.0.0.1 so this is the
// shape every other test in the repo expects.
func TestNewSafeHTTPClient_AllowsLoopbackWhenEndpointIsLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := NewSafeHTTPClient(2*time.Second, srv.URL)
	resp, err := c.Get(srv.URL + "/probe")
	if err != nil {
		t.Fatalf("loopback override should allow dial, got %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}
}

// TestEndpointIsLoopback covers the parser separately so its
// behaviour is pinned independently of the dialer wiring.
func TestEndpointIsLoopback(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"http://127.0.0.1:7777", true},
		{"http://localhost", true},
		{"http://localhost:11434", true},
		{"http://[::1]:8080", true},
		{"https://api.openai.com", false},
		{"http://169.254.169.254", false},
		{"http://10.0.0.1", false},
		{"", false},
		{"not-a-url", false},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			if got := endpointIsLoopback(c.in); got != c.want {
				t.Errorf("endpointIsLoopback(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

// TestIsBlockedDialTarget pins the IP-classification table so
// future tweaks can't silently re-allow a class of address.
func TestIsBlockedDialTarget(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"127.0.0.1", true},                 // loopback
		{"::1", true},                       // loopback v6
		{"10.0.0.1", true},                  // private
		{"172.16.0.1", true},                // private
		{"192.168.1.1", true},               // private
		{"169.254.169.254", true},           // link-local (cloud metadata)
		{"fe80::1", true},                   // link-local v6
		{"0.0.0.0", true},                   // unspecified
		{"224.0.0.1", true},                 // multicast
		{"8.8.8.8", false},                  // public
		{"1.1.1.1", false},                  // public
		{"2606:4700:4700::1111", false},     // public v6 (Cloudflare)
		{"100.64.0.1", false},               // CGNAT — borderline; passes today
	}
	for _, c := range cases {
		t.Run(c.ip, func(t *testing.T) {
			ip := net.ParseIP(c.ip)
			if ip == nil {
				t.Fatalf("bad test fixture: %s", c.ip)
			}
			if got := isBlockedDialTarget(ip); got != c.want {
				t.Errorf("isBlockedDialTarget(%s) = %v, want %v", c.ip, got, c.want)
			}
		})
	}
}

// TestNewSafeHTTPClient_AddrErrorWrap pins the error type the
// guard emits — callers may differentiate with errors.As if they
// want to surface "SSRF rejection" specifically.
func TestNewSafeHTTPClient_AddrErrorWrap(t *testing.T) {
	c := NewSafeHTTPClient(2*time.Second, "https://example.com/")
	_, err := c.Get("http://127.0.0.1:9/blocked")
	if err == nil {
		t.Fatalf("expected error")
	}
	var addrErr *net.AddrError
	if !errors.As(err, &addrErr) {
		t.Fatalf("expected net.AddrError wrapping; got %T: %v", err, err)
	}
}
