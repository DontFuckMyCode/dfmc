# sc-header-injection Results

No issues found by sc-header-injection.

## Scope

CRLF injection into outgoing HTTP response headers, attacker-controlled
`Host`/`Origin` trust, header-based auth bypass, SSE event-line
injection, and outbound header injection on the LLM provider /
`web_fetch` clients.

## Inbound header trust

| Header | Trusted? | Code |
|---|---|---|
| `Authorization` | Compared constant-time against expected `Bearer <token>`; not echoed in any response | `ui/web/server.go:673` |
| `Host` | Allowlist match required (`hostAllowlistMiddleware`); 421 on mismatch | `ui/web/server_origin.go:48-66` |
| `Origin` | Allowlist match required for WS upgrades; native clients (no Origin) accepted; `*` in allowlist explicitly rejected as a misconfig | `ui/web/server.go:238-269` |
| `X-Forwarded-For` | Honored only when direct peer is a trusted proxy (loopback by default); rightmost IP wins; never echoed | `ui/web/server.go:585-634` |
| `Content-Type` | Required `application/json` on body-bearing POST/PATCH/PUT | `ui/web/server.go:469-505` |
| Anything else | Not read by any DFMC handler |

## Response-header writes audited

Grep across `ui/web/` for `w.Header().Set` and `w.Header().Add`:

- All values are **literal constants** — `Content-Type`,
  `Cache-Control`, `Connection`, `X-Accel-Buffering`,
  `Content-Security-Policy`, `X-Content-Type-Options`,
  `X-Frame-Options`. No user-controlled string flows into a
  response header.
- `Content-Type: text/html; charset=utf-8` (workbench HTML),
  `Content-Type: text/event-stream` (SSE), `Content-Type:
  application/json; charset=utf-8` (JSON responses) — all fixed.

Go's `net/http` rejects header values containing CR/LF at write
time (`textproto.MIMEHeader` validation), so even if a user value
*did* somehow flow into a `Header().Set` call, the runtime would
panic or strip rather than emit. Combined with the literal-only
audit above, CRLF response-splitting is structurally impossible.

## SSE / WS event injection

`writeSSE` (`ui/web/server_chat.go:171-183`) builds each frame as
`"data: %s\n\n"` where the payload is `json.Marshal(payload)`. The
JSON encoder escapes embedded newlines (`\n` → `\\n` in the JSON
output), so a `delta` field containing CR/LF cannot terminate the
SSE event early or inject a fake `event:` line.

WS messages go through `conn.WriteJSON` (gorilla), which JSON-encodes
the payload. Same defense: newlines and control characters are
escaped, so an LLM tool result that smuggles a fake JSON-RPC frame
into its output cannot break out of the response envelope.

## Outbound header injection (provider/web_fetch clients)

`internal/provider/*.go` builds outbound requests with
`req.Header.Set("Authorization", "Bearer "+apiKey)` and similar.
The Go stdlib `http.Header.Set` validates header values; control
characters in the API key (which would be operator-controlled, not
attacker-controlled) would cause `Set` to silently drop or panic.
Reviewed: no path takes an LLM-controlled string and feeds it as a
header name/value to an outbound client. `web_fetch` builds a
plain `GET`/`POST` with a single fixed `User-Agent` and the URL
that was tool-validated upstream.

## Header-based auth bypass

The bearer middleware reads `r.Header.Get("Authorization")` and
compares it with `subtle.ConstantTimeCompare` (`ui/web/server.go:673`).
There is no:

- "trusted internal header" (`X-Forwarded-User`, `X-Auth-User`,
  `X-Internal-Caller`, etc.) that bypasses auth.
- Method-override header (`X-HTTP-Method-Override`).
- Debug header that disables auth (`X-Debug-Auth`).

Grep for any of those names: zero matches in `ui/web/`.

## Verifications

1. Grep confirmed every `Header().Set` second argument is a string
   literal in `ui/web/`. No format strings, no concatenation with a
   user-controlled value.
2. Reviewed `writeJSON` (`ui/web/server_chat.go:203-209`) — encodes
   via `json.NewEncoder(w).Encode(payload)`, no header
   manipulation per call beyond the literal `Content-Type` and
   `WriteHeader(code)`.
3. Reviewed `writeSSE`/`writeSSEWithDeadline`
   (`ui/web/server_chat.go:171-197`) — payload is JSON-encoded
   before formatting into the SSE envelope.
4. Reviewed `ui/web/server_files.go` — file paths flow into
   response *bodies* (under JSON encoding), never into headers.

## Why this is "no issues, not just N/A"

Every header value sent by DFMC is a literal constant; every
header value read by DFMC is either ignored, allowlisted, or
constant-time-compared. CRLF injection is structurally blocked by
the stdlib's header validation plus the absence of user-controlled
header writes. The SSE envelope's payload field is JSON-encoded
before splicing, so newline-based event-line injection is
impossible. Outbound clients compose headers from operator-controlled
config, never from attacker-controlled input.
