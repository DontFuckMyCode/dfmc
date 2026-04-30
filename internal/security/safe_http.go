package security

// safe_http.go — SSRF-guarded http.Client factory shared between
// the LLM provider router (internal/provider) and the CLI surfaces
// that fetch models.dev catalogs / GitHub release metadata
// (internal/config, ui/cli/cli_update). Closes VULN-057 / VULN-058.
//
// The provider package keeps its own copy of the dialer wrap
// because it has different timeouts and a Transport tuned for
// streaming. This file is for the simpler "fetch a small JSON
// blob over HTTPS once" callers that don't justify their own
// transport tuning.
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

// wrapDialWithSSRFGuard mirrors the provider package's guard but
// lives here so the CLI / config callers can reuse it without
// taking a dependency on internal/provider.
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

func isBlockedDialTarget(ip net.IP) bool {
	if ip == nil {
		return true
	}
	switch {
	case ip.IsLoopback():
		return true
	case ip.IsPrivate():
		return true
	case ip.IsLinkLocalUnicast():
		return true
	case ip.IsLinkLocalMulticast():
		return true
	case ip.IsUnspecified():
		return true
	case ip.IsMulticast():
		return true
	}
	return false
}
