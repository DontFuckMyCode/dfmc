# sc-websocket + sc-cors + sc-header-injection Results

## Findings

### [Medium] WebSocket upgrade without bearer token auth on auth=token deployments

- **File**: `ui/web/server_ws.go:205-229`
- **Description**: When `auth=token` is set, the HTTP-level `bearerTokenMiddleware` is active, but the WebSocket upgrade path (`handleWebSocketUpgrade`) does not independently validate the bearer token. The Origin allowlist provides protection against cross-site WebSocket hijacking, but a legitimate bearer-token holder who connects via WebSocket does not have their token verified per-message.
- **Impact**: If an attacker obtains a valid bearer token, they could connect to the WebSocket endpoint without additional auth; however, the Origin allowlist prevents cross-origin browser-based abuse. Native WS clients (no Origin) bypass origin checks entirely.
- **Evidence**:
  ```go
  // Handler() applies bearerTokenMiddleware when auth=token (server.go:410-412)
  // But handleWebSocketUpgrade (server_ws.go:205) has no independent bearer check before upgrade
  ```
- **Mitigation**: Add bearer token check inside `handleWebSocketUpgrade` before calling `upgrader.Upgrade()`, or require token presentation via first WebSocket message or Sec-WebSocket-Protocol header.

### [Low] ETag header reflects internal version number without sanitization

- **File**: `ui/web/server_task.go:166`
- **Description**: ETag is set from `task.Version` (integer) using `fmt.Sprintf("\"%d\"", task.Version)`. While safe, no validation ensures the integer cannot contain control characters.
- **Impact**: Minimal — the integer is quoted and bounded, but pattern could be risky if extended to user-controlled fields.
- **Evidence**: `w.Header().Set("ETag", fmt.Sprintf(`"%d"`, task.Version))`
- **Mitigation**: Ensure any future ETag or header value construction validates no CR/LF characters.

### [Info] No CORS preflight handler (OPTIONS) on REST endpoints

- **File**: `ui/web/server.go:320-383`
- **Description**: No `Access-Control-Allow-*` headers are emitted. CORS is not applicable to this API surface (no browser-based cross-origin calls expected). Deliberate design choice.
- **Impact**: None — workbench is self-contained (`default-src 'self'`).
- **Mitigation**: No action needed.

---

## No Issues Found

### WebSocket Origin Validation
- `checkWebSocketOrigin` (`server.go:238-269`) properly validates Origin header against allowlist
- Wildcard `"*"` in allowlist is **explicitly rejected**
- Native clients (no Origin header) are accepted unconditionally
- IPv6 and scheme-aware parsing implemented correctly

### WebSocket Connection Limits
- Global cap: 64, per-IP cap: 8
- Connection acquisition happens **before** upgrade, preventing goroutine leakage from flood attacks (VULN-021)
- Per-IP and global slots released in `cleanup()` after goroutines exit

### WebSocket Message Safety
- `wsReadLimit` = 64 KiB per frame (prevents buffer exhaustion)
- `wsReadDeadline` = 60s per message (half-open connection detector)
- `wsPingInterval` = 30s with pong handler sliding deadline
- Per-connection rate limiter: 5 rps with burst of 10
- Write deadline: 5s per write
- Panic recovery in `writeLoop`

### HTTP Header Injection
- All outbound headers are static strings — no user-controlled values reflected without sanitization
- Error responses use structured JSON via `writeJSON`
- Content-Type enforcement middleware blocks non-JSON content types before body decode