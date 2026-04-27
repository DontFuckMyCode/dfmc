// Origin allowlist resolution helpers for the web server.
// WebSocket origin checking and Host header allowlisting live in
// server.go; this file holds only the allowlist normalisation and
// resolution utilities that are shared across server.go and the
// deferred server_origin.go placement.

package web

import (
	"fmt"
	"net/http"
	"strings"
)

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