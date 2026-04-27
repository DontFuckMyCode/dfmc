// Server-side plumbing for `dfmc serve` and `dfmc remote start`:
// graceful-shutdown helper, loopback-bind detection, bearer-token
// middleware, and the JSON response writer. Extracted from
// cli_remote.go — none of these reach the client-side runRemote
// dispatcher.

package cli

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"
)

func serveWithContext(ctx context.Context, server *http.Server) error {
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		return nil
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

// isLoopbackBindHost reports whether a `dfmc serve --host` value binds
// only to the local machine. Loopback binds are safe to leave without
// auth — nothing off-box can connect. Anything else (0.0.0.0, "", a
// LAN/public IP) reaches further than the user's own machine and is
// treated as network-exposed for the auth guard in runServe.
//
// Empty host is treated as non-loopback because Go's net package binds
// that to all interfaces, exactly like "0.0.0.0".
func isLoopbackBindHost(host string) bool {
	h := strings.TrimSpace(host)
	// Strip optional brackets around IPv6 literals like "[::1]".
	if strings.HasPrefix(h, "[") && strings.HasSuffix(h, "]") {
		h = h[1 : len(h)-1]
	}
	h = strings.ToLower(h)
	switch h {
	case "127.0.0.1", "localhost", "::1":
		return true
	}
	// A parseable IP covers oddities like "127.0.0.2" or "::ffff:127.0.0.1".
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

func bearerTokenMiddleware(next http.Handler, token string) http.Handler {
	rawToken := strings.TrimSpace(token)
	expected := "Bearer " + rawToken
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			writeRemoteJSON(w, http.StatusOK, map[string]any{"status": "ok"})
			return
		}
		// The workbench HTML at GET / is the entry shell — it contains no
		// secrets and the operator needs to load it to enter their token in
		// the browser. Gating it would create a chicken-and-egg lockout.
		// Every API path under /api/v1/ and /ws still requires the token.
		if r.Method == http.MethodGet && r.URL.Path == "/" {
			next.ServeHTTP(w, r)
			return
		}
		// Accept the token via Authorization header everywhere. A query-
		// param fallback is allowed ONLY for /ws because EventSource
		// cannot set custom headers.
		if got := strings.TrimSpace(r.Header.Get("Authorization")); rawToken != "" && subtle.ConstantTimeCompare([]byte(got), []byte(expected)) == 1 {
			next.ServeHTTP(w, r)
			return
		}
		if rawToken != "" && r.URL.Path == "/ws" && subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("token")), []byte(rawToken)) == 1 {
			next.ServeHTTP(w, r)
			return
		}
		writeRemoteJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
	})
}

func writeRemoteJSON(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(payload)
}
