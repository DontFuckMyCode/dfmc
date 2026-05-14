package web

// server_middleware.go — request-pipeline middleware and the supporting
// rate-limiter / proxy-trust / token-auth helpers. Each middleware is a
// drop-in http.Handler wrapper composed by Server.Handler() in server.go;
// the helpers (clientIPKey, isTrustedProxy, matchAllowlist, …) are also
// reused by individual handlers in the server_*.go siblings.
//
// Wiring order, outermost first:
//   bearerTokenMiddleware (when auth=token)
//   rateLimitMiddleware
//   securityHeaders
//   hostAllowlistMiddleware
//   limitRequestBodySize
//   contentTypeEnforcementMiddleware
//
// Each layer is responsible for ONE policy decision. Layered ordering
// matters: body-size cap sits inside auth so a 100MB unauthenticated POST
// never gets read into memory; content-type runs innermost so 415s only
// fire after auth is satisfied.

import (
	"crypto/subtle"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// maxRequestBodyBytes caps the size of a single POST/PUT/PATCH body.
// 4 MiB is generous for any chat message or workspace patch the CLI
// would ever send (typical is < 100 KB); the cap exists so a
// malicious or buggy client can't exhaust memory streaming endless
// JSON into a single Decode call. Overflow surfaces as 413 from the
// stdlib's http.MaxBytesReader automatically.
const maxRequestBodyBytes int64 = 4 * 1024 * 1024

func limitRequestBodySize(h http.Handler, max int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			switch r.Method {
			case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
				r.Body = http.MaxBytesReader(w, r.Body, max)
			}
		}
		h.ServeHTTP(w, r)
	})
}

// matchAllowlist reports whether value matches any allowlist entry.
// "*" anywhere in the list is the explicit wildcard escape hatch.
// Port is stripped from both value and entry so "127.0.0.1:PORT"
// matches allowlist entry "127.0.0.1" — critical for ephemeral port
// httptest servers where the actual port is random.
func matchAllowlist(value string, list []string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return false
	}
	value = stripPort(value)
	for _, entry := range list {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if entry == "*" {
			return true
		}
		entry = stripPort(entry)
		if entry == value {
			return true
		}
	}
	return false
}

// contentTypeEnforcementMiddleware rejects non-JSON Content-Types on
// state-changing requests (POST/PATCH/PUT) before the body is decoded.
// Prevents a CORS-simple `<form enctype="text/plain">` POST from reaching
// the JSON decoder at all (Go's json.NewDecoder is content-type-blind).
// Returns 415 Unsupported Media Type.
func contentTypeEnforcementMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodPatch, http.MethodPut:
		default:
			next.ServeHTTP(w, r)
			return
		}
		// Empty body (ContentLength 0 or -1 for chunked) has nothing to
		// decode — allow it so bodyless POSTs (e.g. /conversation/undo)
		// are not rejected.
		if r.ContentLength <= 0 {
			next.ServeHTTP(w, r)
			return
		}
		ct := strings.TrimSpace(strings.ToLower(r.Header.Get("Content-Type")))
		// VULN-050: block non-JSON content types on body-bearing requests.
		// Bodyless POSTs (ContentLength <= 0) always pass — the handler
		// doesn't read a body anyway, so enforcing JSON there is pointless.
		// Empty Content-Type on a body-bearing request is passed through
		// to the decoder (a decode error will surface as 400 from the
		// handler, not a 415 from us — this avoids rejecting valid empty
		// CT POSTs to endpoints like /conversation/undo that don't read
		// the body).
		if r.ContentLength <= 0 || (ct == "") {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(ct, "application/json") {
			next.ServeHTTP(w, r)
			return
		}
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]any{
			"error": "Content-Type must be application/json; received " + r.Header.Get("Content-Type"),
		})
	})
}

// perIPLimiter provides a basic per-IP rate limiter using a token-bucket
// algorithm. Each client IP gets its own bucket. Buckets for IPs not seen
// in over 10 minutes are garbage-collected periodically.
type perIPLimiter struct {
	mu       sync.Mutex
	buckets  map[string]*rate.Limiter
	lastSeen map[string]time.Time
	rate     rate.Limit
	burst    int
	// stop signals the gc goroutine to exit. Closed by Stop() at most
	// once (stopOnce). The previous implementation pinned the gc
	// goroutine for the life of the process — fine in production
	// where there's exactly one Server, but tests that construct N
	// throwaway Servers via httptest.NewServer leaked N gc
	// goroutines.
	stop     chan struct{}
	stopOnce sync.Once
}

func newPerIPLimiter(r rate.Limit, burst int) *perIPLimiter {
	l := &perIPLimiter{
		buckets:  make(map[string]*rate.Limiter),
		lastSeen: make(map[string]time.Time),
		rate:     r,
		burst:    burst,
		stop:     make(chan struct{}),
	}
	go l.gc() // background cleanup of stale entries
	return l
}

func (l *perIPLimiter) get(ip string) *rate.Limiter {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[ip]
	if !ok {
		b = rate.NewLimiter(l.rate, l.burst)
		l.buckets[ip] = b
	}
	l.lastSeen[ip] = time.Now()
	return b
}

func (l *perIPLimiter) Allow(ip string) bool {
	return l.get(ip).Allow()
}

// Stop signals the gc goroutine to exit. Idempotent (safe to call
// multiple times) and nil-safe. The companion call site is
// Server.Close — operators stopping a serve session, and tests that
// pair httptest.NewServer with t.Cleanup(srv.Close).
func (l *perIPLimiter) Stop() {
	if l == nil {
		return
	}
	l.stopOnce.Do(func() { close(l.stop) })
}

// gc periodically removes IPs with no activity in 10 minutes.
// Exits promptly when Stop() closes l.stop so a torn-down Server
// doesn't leak its background timer for the rest of the process.
func (l *perIPLimiter) gc() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			l.mu.Lock()
			cutoff := time.Now().Add(-10 * time.Minute)
			for ip, last := range l.lastSeen {
				if last.Before(cutoff) {
					delete(l.buckets, ip)
					delete(l.lastSeen, ip)
				}
			}
			l.mu.Unlock()
		case <-l.stop:
			return
		}
	}
}

func rateLimitMiddleware(s *Server, limiter *perIPLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !limiter.Allow(clientIPKey(r, s.trustedProxies)) {
				writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "rate limit exceeded"})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// clientIPKey extracts the client IP for rate-limit bucketing.
// X-Forwarded-For is trusted only when the direct remote address belongs to a
// known local proxy (loopback by default). Remote clients cannot spoof this
// header because they cannot establish a connection through the proxy without
// first passing the bearer-token auth gate. When XFF is honored, the rightmost
// (last) IP is used — that is the most recent proxy hop.
//
// VULN-010 fix: previously XFF was honored unconditionally, and the leftmost
// (first) entry was returned. An attacker could rotate XFF random-each-time
// to reset their bucket every request and bypass the per-IP rate limit entirely.
// clientIPKey standalone function.
func clientIPKey(r *http.Request, trustedProxies []string) string {
	if r == nil {
		return ""
	}
	remoteHost := func() string {
		host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
		if err == nil && host != "" {
			return host
		}
		return strings.TrimSpace(r.RemoteAddr)
	}()

	// VULN-010: only honor XFF when the direct peer is a trusted proxy.
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwarded != "" {
		if isTrustedProxy(remoteHost, trustedProxies) {
			// Use rightmost (last) IP — it was added by the rightmost
			// (most trusted) proxy hop.
			parts := strings.Split(forwarded, ",")
			for i := len(parts) - 1; i >= 0; i-- {
				if ip := strings.TrimSpace(parts[i]); ip != "" {
					return ip
				}
			}
		}
	}
	return remoteHost
}

// isTrustedProxy reports whether the given remote host is in the
// configured trusted-proxy list. Nil or empty list means no proxies
// are trusted (XFF will be ignored).
func isTrustedProxy(host string, trusted []string) bool {
	if len(trusted) == 0 {
		return false
	}
	host = strings.TrimSpace(strings.ToLower(host))
	for _, t := range trusted {
		t = strings.TrimSpace(strings.ToLower(t))
		if t == host || isTrustedProxyAddr(host) {
			return true
		}
		// Support CIDR notation (e.g. "127.0.0.0/8") for future use.
		if strings.Contains(t, "/") {
			if _, cidr, err := net.ParseCIDR(t); err == nil && cidr.Contains(net.ParseIP(host)) {
				return true
			}
		}
	}
	return false
}

// isTrustedProxyAddr is a simple predicate for testing trusted proxy
// detection. Exported so the ratelimit test can call it directly without
// going through clientIPKey (which needs a full Server).
func isTrustedProxyAddr(host string) bool {
	if host == "" {
		return false
	}
	host = strings.TrimSpace(strings.ToLower(host))
	switch host {
	case "127.0.0.1", "localhost", "::1", "[::1]":
		return true
	}
	return false
}

// bearerTokenMiddleware validates bearer tokens using constant-time
// comparison to prevent timing side-channels. This middleware is only
// registered when auth=token; with auth=none the /ws SSE stream has no
// auth check. When active, callers must present the bearer token in the
// Authorization header so secrets never ride in URLs.
func bearerTokenMiddleware(next http.Handler, token string) http.Handler {
	rawToken := strings.TrimSpace(token)
	expected := "Bearer " + rawToken
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/" && rawToken == "" {
			next.ServeHTTP(w, r)
			return
		}
		if got := strings.TrimSpace(r.Header.Get("Authorization")); rawToken != "" && subtle.ConstantTimeCompare([]byte(got), []byte(expected)) == 1 {
			next.ServeHTTP(w, r)
			return
		}
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
	})
}
