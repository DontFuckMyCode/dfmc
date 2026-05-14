package security

// safe_http.go — SSRF-guarded http.Client factory used by both
// the LLM provider router (internal/provider, via the exported
// WrapDialWithSSRFGuard / EndpointIsLoopback helpers) and the
// CLI surfaces that fetch models.dev catalogs / GitHub release
// metadata (internal/config, ui/cli/cli_update). Closes
// VULN-057 / VULN-058 plus the follow-up that found the provider
// transport had no guard at all (the previous version of this
// comment claimed the provider "kept its own copy" — that was
// historical fiction; provider now reuses these helpers).
//
// The guard refuses connections to private / loopback / link-
// local / multicast / unspecified addresses by default. If the
// configured `endpoint` URL itself points at a loopback host
// (operator running a local mirror) the guard relaxes — that's
// the explicit-configuration opt-in.

import (
	"context"
	"fmt"
	"net"
	"net/http"
	neturl "net/url"
	"strings"
	"time"
)

// NewSafeHTTPClient returns an http.Client that refuses to dial
// private / loopback / link-local addresses unless `endpoint` is
// itself a loopback URL (operator opt-in). `timeout` bounds the
// entire request via Client.Timeout — appropriate for short JSON
// fetches; not for streaming bodies.
//
// Intended for callers like models.dev catalog refresh, GitHub
// release metadata for `dfmc update`, and any other one-shot HTTPS
// GET where SSRF would be the dominant risk class. Provider-side
// clients (anthropic / openai / google) have their own factory in
// internal/provider/http_client.go that handles streaming.
func NewSafeHTTPClient(timeout time.Duration, endpoint string) *http.Client {
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	dialer := &net.Dialer{
		Timeout:   15 * time.Second,
		KeepAlive: 15 * time.Second,
	}
	dialContext := dialer.DialContext
	if !endpointIsLoopback(endpoint) {
		dialContext = wrapDialWithSSRFGuard(dialer.DialContext)
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           dialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          5,
			IdleConnTimeout:       30 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: time.Second,
		},
	}
}

// EndpointIsLoopback reports whether `raw` (a URL string) points at a
// loopback host (localhost, 127.0.0.1, ::1). Callers building their own
// http.Client use this to decide whether to engage WrapDialWithSSRFGuard:
// a loopback endpoint is the explicit operator opt-in for local mirrors
// (Ollama, on-prem proxies) where SSRF "guarding" against the very host
// you're talking to is wrong.
func EndpointIsLoopback(raw string) bool {
	return endpointIsLoopback(raw)
}

func endpointIsLoopback(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	u, err := neturl.Parse(raw)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// WrapDialWithSSRFGuard wraps an inner DialContext with the SSRF
// guard. Exported so callers building their own http.Client (e.g.
// internal/provider, which needs its own Transport tuning for
// streaming response bodies) can install the same defense without
// duplicating the rebinding-safe resolution logic.
func WrapDialWithSSRFGuard(inner func(ctx context.Context, network, addr string) (net.Conn, error)) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return wrapDialWithSSRFGuard(inner)
}

// wrapDialWithSSRFGuard is the internal implementation. The exported
// version above is just a thin pass-through; we keep the unexported
// name in use across this file's test suite to minimise churn.
//
// DNS-rebinding TOCTOU defense: the previous version did its own
// LookupIPAddr to validate, then handed the original hostname to
// inner.DialContext, which performed a SECOND DNS lookup. A malicious
// DNS server controlling a TTL=0 record could return a public IP for
// the validation lookup and a private IP (cloud-metadata, internal
// service) for the dial-time lookup, bypassing every check we made.
// Both lookups MUST observe the same answer for the guard to bind.
//
// Fix: after validating all resolved IPs, dial the first one DIRECTLY
// — pass `<ip>:<port>` to inner so it skips DNS entirely. TLS SNI is
// driven by http.Request.Host (set by http.Transport from the URL
// host, NOT from the dial addr), so HTTPS continues to validate the
// certificate against the hostname even though we connect to a
// pinned IP. ips[0] is good-enough — if it happens to be unreachable
// the connection fails the same way it would have under inner's
// own resolver pick.
func wrapDialWithSSRFGuard(inner func(ctx context.Context, network, addr string) (net.Conn, error)) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return inner(ctx, network, addr)
		}
		if ip := net.ParseIP(host); ip != nil {
			if isBlockedDialTarget(ip) {
				return nil, &net.AddrError{Err: "SSRF guard: refusing dial to private/loopback/link-local address", Addr: addr}
			}
			// addr is already an IP, no DNS to TOCTOU.
			return inner(ctx, network, addr)
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("SSRF guard: %s resolves to no addresses", host)
		}
		for _, ip := range ips {
			if isBlockedDialTarget(ip.IP) {
				return nil, &net.AddrError{Err: "SSRF guard: refusing dial to host that resolves to private/loopback/link-local IP", Addr: addr}
			}
		}
		// Pin the dial to the validated first IP so a rebinding DNS
		// server can't swap answers between our check and inner's
		// second lookup.
		return inner(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}
}

// IsBlockedDialTarget is the canonical SSRF rejection set used by
// every outbound HTTP dial in DFMC (provider router, web_fetch /
// web_search, models.dev / GitHub release fetchers). Beyond the
// stdlib's IsLoopback / IsPrivate / IsLinkLocal* / IsMulticast /
// IsUnspecified it also rejects:
//
//   - CGNAT 100.64.0.0/10 (RFC6598) — common on corporate networks,
//     not flagged by IsPrivate so an internal DFMC could otherwise
//     reach CGNAT-addressed services.
//   - IETF protocol assignments 192.0.0.0/24 (RFC6890) — reserved
//     and never a valid public target.
//   - Benchmark 198.18.0.0/15 (RFC2544) — sometimes wired to
//     internal services for performance testing.
//   - Limited broadcast 255.255.255.255.
//
// IPv6 ULA (fc00::/7) is already covered by IsPrivate.
func IsBlockedDialTarget(ip net.IP) bool {
	return isBlockedDialTarget(ip)
}

func isBlockedDialTarget(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() ||
		ip.IsInterfaceLocalMulticast() {
		return true
	}
	if ip4 := ip.To4(); ip4 != nil {
		// CGNAT 100.64.0.0/10
		if ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
			return true
		}
		// IETF protocol assignments 192.0.0.0/24
		if ip4[0] == 192 && ip4[1] == 0 && ip4[2] == 0 {
			return true
		}
		// Benchmark 198.18.0.0/15
		if ip4[0] == 198 && (ip4[1] == 18 || ip4[1] == 19) {
			return true
		}
		// Limited broadcast 255.255.255.255 (IsMulticast covers 224/4
		// but not the all-ones address).
		if ip4[0] == 255 && ip4[1] == 255 && ip4[2] == 255 && ip4[3] == 255 {
			return true
		}
	}
	return false
}
