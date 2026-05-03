# sc-session + sc-rate-limiting Results

## Findings

### [Medium] WebSocket connection limiter shares same IP-extraction logic as HTTP rate limiter

- **File**: `ui/web/server_ws.go:220` / `ui/web/server.go:605`
- **Description**: Both the WebSocket connection cap (`wsConnLimiter.Acquire`) and the HTTP per-IP rate limiter call the same `clientIPKey(r, s.trustedProxies)` function. If an attacker can bypass the XFF guard for WS upgrades (by establishing a direct TCP connection when the direct peer IS a trusted proxy), they could rotate IPs via XFF to defeat the per-IP WS connection limit.
- **Impact**: An attacker could open more than `wsPerIPConnCap` (8) WebSocket connections from a single machine by cycling XFF-reported IPs.
- **Evidence**:
  ```go
  // server_ws.go:220
  wsRelease, gateMsg := s.wsConnLimiter.Acquire(clientIPKey(r, s.trustedProxies))
  // server.go:605 — same clientIPKey for HTTP rate limiting
  if !limiter.Allow(clientIPKey(r, s.trustedProxies)) {
  ```
- **Mitigation**: Separate the WS connection bucket key from the HTTP bucket key — use only `RemoteAddr` for WS connection limiting, regardless of XFF. Or add a secondary check that does NOT use XFF for WS upgrades.

### [Low] WebSocket session ID uses `time.Now().UnixNano()` which lacks cryptographic entropy

- **File**: `ui/web/server_ws.go:236`
- **Description**: Session IDs generated via `fmt.Sprintf("ws-%d", time.Now().UnixNano())`. Nanosecond timestamp is predictable.
- **Impact**: Low — IDs are not used for authentication (purely internal/logger). But if ever used in a security context, would be guessable.
- **Evidence**: `ws := &wsConn{id: fmt.Sprintf("ws-%d", time.Now().UnixNano()), ...}`
- **Mitigation**: Use `crypto/rand` or `github.com/google/uuid` for session ID generation.

### [Info] `auth=none` forces loopback bind — deliberate design

- **File**: `ui/web/server.go:140`
- **Description**: When `auth=none`, server auto-binds to loopback even if `--host` specifies a wildcard. Intentional — unauthenticated servers should not be network-reachable.
- **Status**: Not a vulnerability, working as designed.

---

## No Issues Found

### Rate Limiting (HTTP)
- Token bucket algorithm (`golang.org/x/time/rate`) — cryptographically safe
- Per-IP bucketing: 30 req/s, burst 60
- XFF bypass protection: only honors XFF when direct peer is a trusted proxy (loopback only by default)
- Rightmost (most trusted) IP from XFF chain
- Background GC of stale buckets (>10 min inactivity)
- Tests in `server_ratelimit_test.go` and `server_http_test.go`

### WebSocket Security
- Global cap: 64, per-IP cap: 8 via `wsConnLimiter`
- Per-connection rate limit: 5 rps, burst 10
- Frame size limit: 64 KiB via `SetReadLimit`
- Read deadline + pong handler for half-open connection detection
- Origin header allowlist (wildcard `"*"` explicitly rejected)
- Panic recovery in writeLoop
- Context cancellation on disconnect (prevents burning provider tokens — VULN-023)

### Session Management (Web)
- Bearer token auth using constant-time comparison (`subtle.ConstantTimeCompare`)
- Token never in URLs — always `Authorization` header
- No browser-based sessions — API-key style bearer tokens only

### Provider Rate Limiting
- 429/503 → `parseRetryAfter` with exponential backoff (1s, 2s, 4s, 8s, 16s, 30s max)
- Retry-After clamped at 5 minutes maximum

### Tool Execution Limits
- `max_tool_steps=60`, `max_tool_tokens=250000` enforced per agent loop
- `meta_call_budget=64`, `meta_depth_limit=4` for meta-tool nesting
- `autonomous_resume` cumulative ceiling: `max_steps * resume_max_multiplier`