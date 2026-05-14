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
		{"127.0.0.1", true},             // loopback
		{"::1", true},                   // loopback v6
		{"10.0.0.1", true},              // private
		{"172.16.0.1", true},            // private
		{"192.168.1.1", true},           // private
		{"169.254.169.254", true},       // link-local (cloud metadata)
		{"fe80::1", true},               // link-local v6
		{"0.0.0.0", true},               // unspecified
		{"224.0.0.1", true},             // multicast
		{"8.8.8.8", false},              // public
		{"1.1.1.1", false},              // public
		{"2606:4700:4700::1111", false}, // public v6 (Cloudflare)
		{"100.64.0.1", true},            // CGNAT 100.64/10 — blocked since the Phase-2 SSRF widening
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

// TestSSRFGuard_PinsResolvedIPAcrossInnerDial regresses a DNS-rebinding
// TOCTOU: pre-fix the guard validated via its own LookupIPAddr but then
// handed the original hostname to inner.DialContext, which did a fresh
// resolution. A malicious DNS server returning a public IP first and a
// private IP second bypassed every check we made. The fix dials the
// pre-validated IP directly so inner doesn't re-resolve.
//
// We exercise this at the wrapDialWithSSRFGuard level (rather than
// through a full http.Client) so we don't need a live DNS server to
// observe the addr inner receives. The captureInner function records
// the addr it sees; we assert it's the IP form, not the original
// hostname. Pre-fix this test would see "example.com:443" — the
// hostname inner re-resolved itself.
func TestSSRFGuard_PinsResolvedIPAcrossInnerDial(t *testing.T) {
	captured := ""
	captureInner := func(ctx context.Context, network, addr string) (net.Conn, error) {
		captured = addr
		// Return a synthetic error so we don't actually open a socket;
		// the addr capture is what we care about.
		return nil, errors.New("intercepted: not dialing")
	}
	guarded := wrapDialWithSSRFGuard(captureInner)
	// example.com is a real public hostname; this resolves at test time.
	// If the resolver fails (no DNS in the sandbox), the test is skipped
	// rather than failing — we're testing the guard's plumbing, not DNS.
	_, err := guarded(context.Background(), "tcp", "example.com:443")
	if err == nil {
		t.Fatalf("expected intercepted-not-dialing error from captureInner")
	}
	if !strings.Contains(err.Error(), "intercepted") {
		// DNS resolution itself may have failed. Skip rather than
		// false-alarm.
		if strings.Contains(err.Error(), "no such host") || strings.Contains(err.Error(), "lookup") {
			t.Skipf("DNS unavailable in test env: %v", err)
		}
		t.Fatalf("unexpected error from guarded dial: %v", err)
	}
	// Captured addr must be <ip>:443, not example.com:443. ParseIP
	// returns non-nil only for IPv4/IPv6 literals, so we use it as a
	// strong signal that DNS happened in the guard, not in inner.
	host, _, splitErr := net.SplitHostPort(captured)
	if splitErr != nil {
		t.Fatalf("captured addr %q is not host:port: %v", captured, splitErr)
	}
	if net.ParseIP(host) == nil {
		t.Fatalf("guard handed inner a hostname (%q) — TOCTOU window still open", captured)
	}
}

// TestSSRFGuard_IPLiteralPassthroughUnchanged confirms that when the
// caller-supplied addr is already an IP literal (no DNS to resolve),
// the guard hands inner the SAME addr it received — we don't fabricate
// a different one. This pins that the fix above doesn't accidentally
// rewrite IP-direct dials.
func TestSSRFGuard_IPLiteralPassthroughUnchanged(t *testing.T) {
	captured := ""
	captureInner := func(ctx context.Context, network, addr string) (net.Conn, error) {
		captured = addr
		return nil, errors.New("intercepted")
	}
	guarded := wrapDialWithSSRFGuard(captureInner)
	const want = "1.1.1.1:443" // public, won't be blocked
	_, err := guarded(context.Background(), "tcp", want)
	if err == nil {
		t.Fatalf("expected intercepted error")
	}
	if captured != want {
		t.Fatalf("guard rewrote IP-literal addr: got %q, want %q", captured, want)
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

// TestIsBlockedDialTarget_ExtendedRanges pins the post-Phase-2-#8
// rejection set. Beyond the stdlib's IsPrivate / IsLoopback /
// IsLinkLocal* / IsMulticast / IsUnspecified, the guard now also
// rejects CGNAT (RFC6598), IETF protocol assignments (RFC6890),
// benchmark (RFC2544), and the limited broadcast address — each of
// which is reachable on corporate networks but never legitimate
// for a tool-driven outbound HTTP call.
func TestIsBlockedDialTarget_ExtendedRanges(t *testing.T) {
	cases := []struct {
		label   string
		ip      string
		blocked bool
	}{
		// Stdlib baseline still works
		{"loopback IPv4", "127.0.0.1", true},
		{"loopback IPv6", "::1", true},
		{"RFC1918 10/8", "10.0.0.5", true},
		{"RFC1918 192.168/16", "192.168.1.1", true},
		{"RFC1918 172.16/12", "172.20.0.5", true},
		{"link-local IPv4", "169.254.169.254", true},
		{"link-local IPv6", "fe80::1", true},
		{"unspecified IPv4", "0.0.0.0", true},
		{"unspecified IPv6", "::", true},
		{"multicast", "224.0.0.1", true},
		// Extended ranges added in Phase 2 #8
		{"CGNAT low boundary", "100.64.0.1", true},
		{"CGNAT mid", "100.100.50.50", true},
		{"CGNAT high boundary", "100.127.255.254", true},
		{"benchmark RFC2544 low", "198.18.0.1", true},
		{"benchmark RFC2544 high", "198.19.255.254", true},
		{"IETF protocol assignment", "192.0.0.1", true},
		{"limited broadcast", "255.255.255.255", true},
		// Sanity: legitimate public addresses still allowed
		{"public Cloudflare DNS", "1.1.1.1", false},
		{"public Google DNS", "8.8.8.8", false},
		{"public IPv6", "2606:4700:4700::1111", false},
		// CGNAT-adjacent that should NOT trip the range check
		{"just below CGNAT", "100.63.255.254", false},
		{"just above CGNAT", "100.128.0.1", false},
		// Benchmark-adjacent
		{"just below benchmark", "198.17.255.254", false},
		{"just above benchmark", "198.20.0.1", false},
		// Just outside IETF protocol assignment 192.0.0/24
		{"just above IETF block", "192.0.1.1", false},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			ip := net.ParseIP(tc.ip)
			if ip == nil {
				t.Fatalf("invalid test IP %q", tc.ip)
			}
			got := IsBlockedDialTarget(ip)
			if got != tc.blocked {
				t.Fatalf("IsBlockedDialTarget(%s) = %v, want %v", tc.ip, got, tc.blocked)
			}
		})
	}
}
