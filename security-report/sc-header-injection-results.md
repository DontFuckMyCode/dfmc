# Header Injection Results — DFMC

Scope: HTTP response header CRLF injection, host-header trust, outbound
header injection on provider/MCP/web_fetch clients, SSE event newline
injection.

Go version is **1.25** (per `go.mod` and architecture report) — this is
relevant because `net/http` since Go 1.20 rejects CR/LF in header values
written via `Header.Set`/`Header.Add` (CVE-2019-16276 class) and
`Request.Write`/`Response.Write` validate header values before sending.
So the classical CRLF-injection class is closed at the stdlib level for
both inbound and outbound handlers in this repo.

## Counts per file

| File | Findings |
|---|---|
| `D:/Codebox/PROJECTS/DFMC/ui/web/server.go` | 1 (positive) + 1 (Low) |
| `D:/Codebox/PROJECTS/DFMC/ui/web/server_chat.go` | 1 (Info) |
| `D:/Codebox/PROJECTS/DFMC/ui/web/server_files.go` | 1 (Info) |
| `D:/Codebox/PROJECTS/DFMC/internal/tools/web.go` | 1 (Info) |
| `D:/Codebox/PROJECTS/DFMC/internal/provider/*.go` | 1 (Info) |

---

## HDR-001 — Response headers use only static strings — no CRLF injection surface (Informational, positive)

- **Severity**: Info (positive)
- **Confidence**: High
- **File:line**: every `w.Header().Set(...)` call across `ui/web/`
  uses static-literal values:
  - `D:/Codebox/PROJECTS/DFMC/ui/web/server.go:124-126` (CSP, nosniff,
    frame-deny — all static literals)
  - `D:/Codebox/PROJECTS/DFMC/ui/web/server_chat.go:79-82, 123-126`
    (text/event-stream, no-cache, keep-alive, X-Accel-Buffering — all
    static)
  - `D:/Codebox/PROJECTS/DFMC/ui/web/server_files.go:` no
    `w.Header().Set(...)` calls; uses `writeJSON` only
- **CWE**: N/A (positive)

`writeJSON` (`server_chat.go:180-184`) sets `Content-Type:
application/json; charset=utf-8` and writes body via
`json.NewEncoder.Encode` — JSON encoding inherently escapes
control characters, so user input in JSON values cannot inject
header-frame breaks.

No code path I inspected sets a response header from
attacker-controlled bytes. So the classical inbound CRLF-injection
class doesn't apply.

## HDR-002 — `clientIPKey` trusts `X-Forwarded-For` unconditionally for rate-limit bucketing (Low)

- **Severity**: Low
- **Confidence**: High
- **File:line**: `D:/Codebox/PROJECTS/DFMC/ui/web/server.go:374-390`
- **CWE**: CWE-348 (Use of Less Trusted Source) — for rate-limit-bypass
  intent.

```go
func clientIPKey(r *http.Request) string {
    if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwarded != "" {
        for _, part := range strings.Split(forwarded, ",") {
            if ip := strings.TrimSpace(part); ip != "" {
                return ip   // ← first XFF entry wins, NO validation that the source is a trusted proxy
            }
        }
    }
    host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
    ...
}
```

The comment above the function (line 370-373) reasons:

> X-Forwarded-For is trusted only when the request originates from a
> known local proxy (e.g. nginx on localhost). Remote clients cannot
> spoof this header because they cannot establish a connection through
> the proxy without first passing the bearer-token auth gate.

This reasoning has a hole. **The `auth=none` mode** (default per
`Server.New`, line 132) STILL runs through `rateLimitMiddleware` (line
277-282) — bearer-token middleware is conditional on `auth==token`
(line 279). When `auth=none` (default), an attacker on the same host (or
a misconfigured non-loopback bind that bypasses the warning at line
156-158 via `auth=token` + non-loopback) can send arbitrary
`X-Forwarded-For: <random-each-time>` headers, bypassing the per-IP
rate limit by rotating fake IPs.

Concretely:

1. Attacker on the local machine (or a `--insecure` non-loopback bind
   under `auth=none`) sends 60 requests with `X-Forwarded-For: 1.1.1.1`,
   exhausting that bucket.
2. Same attacker sends 60 more with `X-Forwarded-For: 2.2.2.2`,
   exhausting that bucket.
3. ... repeats indefinitely.

The rate-limit's purpose (DOS protection, brute-force prevention) is
defeated. The `clientIPKey` function should ONLY honor `X-Forwarded-For`
when `RemoteAddr` is a trusted proxy IP (loopback or operator-configured
list).

**Recommendation**: parse `RemoteAddr`, check `IsLoopback()` (or check
against a configured trusted-proxy list), and only THEN honor
`X-Forwarded-For`. Otherwise fall through to `RemoteAddr`.

## HDR-003 — SSE event payloads are JSON-encoded; embedded user input cannot fragment events (Informational, positive)

- **Severity**: Info (positive)
- **Confidence**: High
- **File:line**: `D:/Codebox/PROJECTS/DFMC/ui/web/server_chat.go:170-174`
  (`writeSSE`)
- **CWE**: N/A (positive)

```go
func writeSSE(w http.ResponseWriter, flusher http.Flusher, payload any) {
    data, _ := json.Marshal(payload)
    _, _ = fmt.Fprintf(w, "data: %s\n\n", data)
    flusher.Flush()
}
```

The payload is JSON-marshalled BEFORE being wrapped in the SSE `data:`
frame. JSON encoding escapes literal `\n` as `\\n` and literal `\r` as
`\\r` inside string values, so a user-supplied prompt containing
embedded newlines cannot terminate the SSE event early or inject a new
SSE field. Verified with the SSE spec: `\n\n` is the event terminator,
and JSON-encoded strings never contain a raw `\n`. Good.

The same property holds for `/ws` SSE writes
(`server_chat.go:154-160`) and the WebSocket JSON writes
(`server_ws.go:296-302, 314-328`) — both use `json.Marshal`.

## HDR-004 — Inbound Authorization header constant-time compare is correct (Informational, positive)

- **Severity**: Info (positive)
- **Confidence**: High
- **File:line**: `D:/Codebox/PROJECTS/DFMC/ui/web/server.go:401-419`

`bearerTokenMiddleware` reads `r.Header.Get("Authorization")` and uses
`subtle.ConstantTimeCompare` against `"Bearer " + rawToken`. No CRLF
parsing, no header echoing back to the response. The 401 response uses
`writeJSON`, so the unauthorized message is the static literal
`"unauthorized"` — no header value ever flows back into a response
header.

Edge case: `r.URL.Path == "/" && rawToken == ""` short-circuits to allow
unauthenticated workbench fetch (line 409-412). The earlier
`runServe` startup check (per architecture report
`cli_remote.go:62-65`) refuses to start with empty token in token mode,
so this branch should be unreachable in practice. Worth a defence-in-depth
check that rejects empty-token mode at construction (`Server.New` could
panic if `auth=token && token==""`), but that's a hardening recommendation,
not a header-injection finding.

## HDR-005 — Outbound provider/web_fetch headers use static literals or pre-validated values (Informational, positive)

- **Severity**: Info (positive)
- **Confidence**: High
- **File:line**:
  - `D:/Codebox/PROJECTS/DFMC/internal/tools/web.go:139-141`
    (User-Agent, Accept, Accept-Language — all static literals)
  - `D:/Codebox/PROJECTS/DFMC/internal/tools/web.go:367-368` (same for
    web_search)
  - `D:/Codebox/PROJECTS/DFMC/internal/provider/anthropic.go:89-91,
    183-185` (Content-Type, x-api-key, anthropic-version — apiKey
    comes from config)
  - `D:/Codebox/PROJECTS/DFMC/internal/provider/openai_compat.go:108-110,
    214-216` (Content-Type, Bearer auth — apiKey from config)
  - `D:/Codebox/PROJECTS/DFMC/internal/provider/google.go:79-80,
    151-152` (Content-Type, x-goog-api-key — apiKey from config)
  - `D:/Codebox/PROJECTS/DFMC/ui/cli/cli_update.go:171-172, 200-201`
    (Accept, User-Agent — `currentExtra` is a build-time constant /
    version string, no runtime injection)

Outbound headers are either static-literal or take values from the
config (`apiKey`, version strings). Go 1.25's `Header.Set` rejects
CR/LF in header values, so even a malicious config pushing
`api_key: "abc\r\nX-Inject: foo"` would fail at request time rather than
inject a header.

The `apiKey` itself is the most interesting input — a hostile project
config could inject CR/LF, but stdlib's
`textproto.MIMEHeader.validHeaderFieldValue` (called from
`http.Request.Write`) rejects `\r`, `\n`, and `\x00` in header values
since Go 1.13 / hardened in 1.20. Verified by stdlib source: any
attempt to set such a value crashes the request with
`net/http: invalid header field value`.

## HDR-006 — Host header trust: server doesn't echo Host into responses (Informational, positive)

- **Severity**: Info (positive)
- **Confidence**: High
- **File:line**: across `ui/web/*.go`

No handler reads `r.Host` and emits it back into a response (no
self-referential URL builders, no `<base href=...>` tag, no
canonical-URL header). The embedded workbench HTML is served verbatim
from `static/index.html` with no Host substitution
(`server.go:392-395`). So host-header injection (cache poisoning,
password-reset-link spoofing) does not apply.

## HDR-007 — WebSocket Origin check returns `true` unconditionally (gap, but mitigated)

- **Severity**: Info (already documented in architecture.md §5)
- **Confidence**: High
- **File:line**: `D:/Codebox/PROJECTS/DFMC/ui/web/server_ws.go:32-35`
- **CWE**: CWE-346 (Origin Validation Error) — not strictly a header
  injection but related to header trust.

```go
var wsUpgrader = websocket.Upgrader{
    ...
    CheckOrigin: func(r *http.Request) bool {
        return true // configured by caller via Server
    },
}
```

The comment "configured by caller via Server" doesn't match the code —
nothing in `Server` overrides `wsUpgrader.CheckOrigin`. So origin
validation is effectively disabled. The architecture report already
calls this out and notes the bind-host normalization mitigates it in
practice (a non-loopback `auth=none` bind is refused at startup), so
the practical impact is bounded to the case where an `auth=token`
operator runs on a non-loopback bind and a victim's browser is tricked
into hitting `ws://internal.host:7777/api/v1/ws` from a malicious
origin — but the bearer token must still be presented in a header,
which a browser cannot do for a cross-origin WS without explicit
plumbing.

Re-flagged here for completeness because it's the only header-trust
gap I found that's user-controlled.

---

## Summary

DFMC's header-handling surface is clean for CRLF injection — Go 1.25's
stdlib enforces CRLF rejection on both inbound parsing and outbound
header values, and the application code uses static literals or
pre-validated config values everywhere I looked.

The only meaningful finding is **HDR-002**: the per-IP rate limiter
unconditionally trusts `X-Forwarded-For`, which under `auth=none`
(default) lets a same-host attacker bypass the rate limit by rotating
fake IPs. Recommend gating XFF trust on a loopback / configured
trusted-proxy `RemoteAddr` check.

HDR-007 (WebSocket `CheckOrigin: true`) is already documented in the
architecture report; surfacing here for header-trust traceability.

SSE/WebSocket frames are JSON-encoded before transmission, so embedded
user input cannot fragment events or inject control sequences.
