package web

import (
	"net/http/httptest"
	"testing"
)

// TestClientIPKey_IgnoresXFFFromRemotePeer pins VULN-010: a remote
// client (non-loopback peer) cannot spoof its bucket key by sending
// an `X-Forwarded-For` header. Without this, anyone could rotate the
// XFF value per request and defeat the rate limit.
func TestClientIPKey_IgnoresXFFFromRemotePeer(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/v1/status", nil)
	req.RemoteAddr = "203.0.113.5:54321" // arbitrary remote IP
	req.Header.Set("X-Forwarded-For", "10.10.10.10, 192.0.2.99")

	got := clientIPKey(req, []string{"127.0.0.1", "localhost", "::1"})
	if got != "203.0.113.5" {
		t.Fatalf("remote peer must NOT be allowed to set XFF — expected key=%q (the peer IP), got %q", "203.0.113.5", got)
	}
}

// TestClientIPKey_TrustsXFFFromLoopback confirms the legitimate
// reverse-proxy use case still works: an XFF header from a loopback
// peer (the local proxy) is honoured, and the RIGHTMOST entry wins
// (closest to our proxy, not the attacker-claimed leftmost).
func TestClientIPKey_TrustsXFFFromLoopback(t *testing.T) {
	cases := []struct {
		name       string
		remoteAddr string
		xff        string
		want       string
	}{
		{
			name:       "loopback peer with single XFF entry",
			remoteAddr: "127.0.0.1:54321",
			xff:        "203.0.113.5",
			want:       "203.0.113.5",
		},
		{
			name:       "loopback peer with chained XFF — rightmost wins",
			remoteAddr: "127.0.0.1:54321",
			xff:        "10.0.0.1, 192.168.1.1, 203.0.113.99",
			want:       "203.0.113.99",
		},
		{
			name:       "ipv6 loopback peer",
			remoteAddr: "[::1]:54321",
			xff:        "203.0.113.5",
			want:       "203.0.113.5",
		},
		{
			name:       "trailing whitespace tolerated",
			remoteAddr: "127.0.0.1:54321",
			xff:        "  203.0.113.5  ",
			want:       "203.0.113.5",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/v1/status", nil)
			req.RemoteAddr = c.remoteAddr
			req.Header.Set("X-Forwarded-For", c.xff)
			got := clientIPKey(req, []string{"127.0.0.1", "localhost", "::1"})
			if got != c.want {
				t.Errorf("clientIPKey = %q, want %q", got, c.want)
			}
		})
	}
}

// TestClientIPKey_NoXFFFallsBackToPeer covers the no-proxy case —
// the most common shape for `dfmc serve` running locally.
func TestClientIPKey_NoXFFFallsBackToPeer(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/v1/status", nil)
	req.RemoteAddr = "192.0.2.42:54321"
	got := clientIPKey(req, []string{"127.0.0.1", "localhost", "::1"})
	if got != "192.0.2.42" {
		t.Fatalf("clientIPKey should fall back to peer IP, got %q", got)
	}
}

// TestIsTrustedProxyAddr unit-checks the proxy-allowlist predicate.
func TestIsTrustedProxyAddr(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"127.0.0.1", true},
		{"::1", true},
		{"localhost", true},
		{"LOCALHOST", true},
		{"[::1]", true},
		{"10.0.0.1", false},
		{"192.168.1.1", false},
		{"203.0.113.5", false},
		{"", false},
	}
	for _, c := range cases {
		got := isTrustedProxyAddr(c.host)
		if got != c.want {
			t.Errorf("isTrustedProxyAddr(%q) = %v, want %v", c.host, got, c.want)
		}
	}
}
