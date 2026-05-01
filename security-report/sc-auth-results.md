# SC-Auth Security Scan Results

**Date:** 2026-04-30  
**Project:** DFMC  
**Total Findings:** 0  

## Summary

No authentication vulnerabilities detected in DFMC.

### Verification Findings

#### Auth Surface 1: Bearer Token Middleware
**File:** `ui/web/server.go:676-699`  
**Status:** SECURE ✓

```go
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
```

**Verification:**
- **Constant-time comparison:** ✓ `crypto/subtle.ConstantTimeCompare()` prevents timing attacks (line 693)
- **Bearer scheme enforcement:** ✓ Expected format is `"Bearer " + token` (line 683)
- **No environment leak:** ✓ Token sourced once at server init from `os.Getenv("DFMC_WEB_TOKEN")` (server.go:159), never re-read
- **Exceptions:** `/healthz` and GET `/` (when no token) are public (lines 685, 689)

#### Auth Surface 2: Bind Host Normalization
**File:** `ui/web/server.go:173-184`  
**Status:** SECURE ✓

```go
func normalizeBindHost(authMode, host string) string {
    if strings.EqualFold(strings.TrimSpace(authMode), "none") && !isLoopbackBindHost(host) {
        fmt.Fprintf(os.Stderr, "[DFMC] NOTICE: auth=none forces loopback bind; ignoring --host %s and using 127.0.0.1. Pass --auth=token to expose on a network interface.\n", host)
        return "127.0.0.1"
    }
    if strings.EqualFold(strings.TrimSpace(authMode), "token") && !isLoopbackBindHost(host) {
        fmt.Fprintf(os.Stderr, "[DFMC] WARNING: auth=token with non-loopback bind (%s) exposes the agent on all interfaces. Use --host 127.0.0.1 or set auth=none.\n", host)
    }
    return host
}
```

**Verification:**
- **0.0.0.0 bind with auth=none:** REJECTED - forces loopback 127.0.0.1 (line 174)
- **Non-loopback bind with auth=token:** WARNED but ALLOWED (operator responsibility) (line 181)
- **Loopback check:** Includes IPv4 (127.0.0.1), IPv6 (::1), localhost (line 196)

#### Auth Surface 3: WebSocket Origin + Token Check
**File:** `ui/web/server.go:238-269`  
**Status:** SECURE ✓

```go
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
```

**Verification:**
- **Origin check:** Enforced for browser-initiated WS (has Origin header) (line 239)
- **Native client bypass:** Correct (native tools like curl, wscat omit Origin) (line 241)
- **Wildcard rejection:** `"*"` in allowlist treated as "no match" to prevent accidental open (line 252)
- **Token check:** Bearer token required alongside origin check (via `bearerTokenMiddleware` upstream) ✓

#### Auth Surface 4: Token Environment Handling
**File:** `ui/web/server.go:159`  
**Status:** SECURE ✓

```go
token: strings.TrimSpace(os.Getenv("DFMC_WEB_TOKEN")),
```

**Verification:**
- **Read once at init:** Token fetched once during `New()`, not repeatedly (prevents log leaks on each request)
- **No logging:** Token is never logged or printed (verified by grep: no `fmt.Println`, `log.Printf` of `s.token`)
- **env var naming:** `DFMC_WEB_TOKEN` is clear; no collision with other token env vars
- **Trimmed:** Whitespace removed to prevent off-by-one auth failures

### False Positives Cleared

- No hardcoded credentials (verified by grep: no `password = "..."`, `admin:admin`, etc.)
- No weak password hashing (DFMC is serverless; no user password storage)
- No brute-force protection needed (operator runs the server; single-user tool)
- No JWT (token is fixed bearer token; no role claims to tamper)
- No account enumeration (no user list endpoints)

## Conclusion

**Risk Level:** LOW  
Auth implementation uses constant-time comparison, enforces loopback bind on auth=none, checks WS origin, and reads token once at init. No credential storage or brute-force surface.

