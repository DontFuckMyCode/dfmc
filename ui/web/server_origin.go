// Origin / host / bind-host helpers + browser-hardening response middleware.
// Sibling to server.go which keeps Server construction, lifecycle, and route
// wiring; the request-pipeline middleware (rate limiter, bearer auth, etc.)
// lives in server_middleware.go.

package web

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// securityHeaders adds browser-enforced security boundaries to every
// response. The embedded workbench is self-contained, so we lock down
// CSP to 'self' only and set standard hardening headers.
func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; connect-src 'self' ws: wss:; img-src 'self' data:; font-src 'self'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		h.ServeHTTP(w, r)
	})
}

func normalizeBindHost(authMode, host string) string {
	if strings.EqualFold(strings.TrimSpace(authMode), "none") && !isLoopbackBindHost(host) {
		fmt.Fprintf(os.Stderr, "[DFMC] NOTICE: auth=none forces loopback bind; ignoring --host %s and using 127.0.0.1. Pass --auth=token to expose on a network interface.\n", host)
		return "127.0.0.1"
	}
	// VULN-049: when auth=none and already on a loopback bind, stay quiet.
	// The "any process on this machine" notice was redundant — loopback is
	// already the safe default and the user explicitly chose it.
	if strings.EqualFold(strings.TrimSpace(authMode), "token") && !isLoopbackBindHost(host) {
		fmt.Fprintf(os.Stderr, "[DFMC] WARNING: auth=token with non-loopback bind (%s) exposes the agent on all interfaces. Use --host 127.0.0.1 or set auth=none.\n", host)
	}
	return host
}

// isLoopbackBindHost reports whether a host value binds only to the local
// machine. Empty string is treated as non-loopback because Go binds that
// to every interface.
func isLoopbackBindHost(host string) bool {
	h := strings.TrimSpace(host)
	if strings.HasPrefix(h, "[") && strings.HasSuffix(h, "]") {
		h = h[1 : len(h)-1]
	}
	h = strings.ToLower(h)
	switch h {
	case "127.0.0.1", "localhost", "::1":
		return true
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// checkWebSocketOrigin validates the Origin header against the per-Server
// allowlist. Cross-origin browser tabs are rejected so a malicious site
// can't drive the WS connection on the user's behalf. Native WS clients
// (no Origin header) are always accepted.
func (s *Server) checkWebSocketOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		// Native client (curl, wscat, IDE plugin) — no Origin header,
		// accept unconditionally.
		return true
	}
	originHost := origin
	if h := parseURLHost(origin); h != "" {
		originHost = h
	}
	// Strip port once, before the loop — stripPort is idempotent.
	originHost = stripPort(originHost)
	for _, allowed := range s.allowedOrigins {
		if allowed == "*" {
			// "*" in the allowlist is not a valid entry — it would
			// accept any origin, defeating the purpose of the check.
			// Treat it as "no match" so operators who accidentally set
			// allowed_origins: ["*"] are not silently open.
			continue
		}
		allowedHost := allowed
		if h := parseURLHost(allowed); h != "" {
			allowedHost = h
		}
		allowedHost = stripPort(allowedHost)
		if originHost == allowedHost {
			return true
		}
	}
	return false
}

// parseURLHost returns the scheme://host:port from a URL string, stripping
// the path. Returns the parsed scheme://host:port on success or "" on failure.
func parseURLHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// stripPort removes any :port suffix from hostOrHostPort, handling IPv6
// and scheme://host:port forms. Without scheme awareness, LastIndex(":")
// on "http://127.0.0.1" would land on the scheme separator and return
// just "http"; that broke origin matching for any allowlist entry that
// omits the port.
func stripPort(hostOrHostPort string) string {
	if hostOrHostPort == "" {
		return hostOrHostPort
	}
	// Skip past scheme:// when looking for the port boundary.
	prefixLen := 0
	if i := strings.Index(hostOrHostPort, "://"); i >= 0 {
		prefixLen = i + 3
	}
	rest := hostOrHostPort[prefixLen:]
	// IPv6: [::1]:8080 — keep brackets, strip trailing :port.
	if strings.HasPrefix(rest, "[") {
		if end := strings.LastIndex(rest, "]"); end > 0 {
			return hostOrHostPort[:prefixLen+end+1]
		}
	}
	// host:port or host — only strip when the suffix is digits-only,
	// otherwise a hostname like "service:dev" would be truncated.
	if idx := strings.LastIndex(rest, ":"); idx > 0 {
		port := rest[idx+1:]
		allDigits := port != ""
		for _, c := range port {
			if c < '0' || c > '9' {
				allDigits = false
				break
			}
		}
		if allDigits {
			return hostOrHostPort[:prefixLen+idx]
		}
	}
	return hostOrHostPort
}

// resolveAllowedOrigins fills in defaults when the config list is
// empty. Defaults cover the workbench's own URL plus the localhost
// alias on the same port.
func resolveAllowedOrigins(configured []string, host string, port int) []string {
	if len(configured) > 0 {
		return normalizeAllowlist(configured)
	}
	bindHost := strings.TrimSpace(host)
	if bindHost == "" || bindHost == "0.0.0.0" || bindHost == "::" {
		bindHost = "127.0.0.1"
	}
	return []string{
		fmt.Sprintf("http://%s:%d", bindHost, port),
		fmt.Sprintf("http://localhost:%d", port),
		fmt.Sprintf("http://127.0.0.1:%d", port),
	}
}

// normalizeAllowlist trims whitespace and drops empties. Lowercase
// origins/hosts so case-mismatched browser submissions don't break
// the comparison.
func normalizeAllowlist(in []string) []string {
	out := make([]string, 0, len(in))
	for _, entry := range in {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if entry == "" {
			continue
		}
		out = append(out, entry)
	}
	return out
}

// hostAllowlistMiddleware rejects requests whose Host header doesn't
// match the allowlist. Defined here so it can use matchAllowlist from
// server.go without duplication.
// Returns 421 Misdirected Request (RFC 7540 §9.1.2).
func hostAllowlistMiddleware(next http.Handler, allowed []string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := strings.TrimSpace(r.Host)
		if host == "" {
			next.ServeHTTP(w, r)
			return
		}
		if matchAllowlist(host, allowed) {
			next.ServeHTTP(w, r)
			return
		}
		writeJSON(w, http.StatusMisdirectedRequest, map[string]any{
			"error": fmt.Sprintf("host %q is not allowed; configure web.allowed_hosts to permit additional values", host),
		})
	})
}
