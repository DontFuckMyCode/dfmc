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
func wrapDialWithSSRFGuard(inner func(ctx context.Context, network, addr string) (net.Conn, error)) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			return inner(ctx, network, addr)
		}
		if ip := net.ParseIP(host); ip != nil {
			if isBlockedDialTarget(ip) {
				return nil, &net.AddrError{Err: "SSRF guard: refusing dial to private/loopback/link-local address", Addr: addr}
			}
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
		return inner(ctx, network, addr)
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
