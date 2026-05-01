# sc-cors Results

No issues found by sc-cors.

## Posture

DFMC's HTTP surface ships **no CORS middleware**. Grep across
`ui/web/` for `Access-Control-Allow-Origin`,
`Access-Control-Allow-Credentials`, `Access-Control-Allow-Methods`,
`Access-Control-Allow-Headers`: zero matches.

This is the correct posture for the documented threat model:

- **No CORS headers ⇒ no cross-origin browser reads.** A page hosted
  at `https://evil.example` that fires a `fetch('http://127.0.0.1:7777/api/v1/files')`
  is rejected by the browser's same-origin policy before any response
  body is exposed to the attacker's JavaScript.
- **The embedded workbench is same-origin.** It is served from the
  same `127.0.0.1:7777` (or whatever host/port the server binds to)
  as the API, so it never needs CORS to reach `/api/v1/*`.
- **Programmatic clients are server-to-server** (CLI `dfmc remote`,
  scripts, MCP hosts) — they don't run in a browser, so they don't
  honor CORS anyway.

## Adjacent controls verified

| Control | Code | Why it matters here |
|---|---|---|
| WebSocket origin allowlist | `ui/web/server.go:238-269` (`checkWebSocketOrigin`) | The browser-side equivalent; rejects cross-origin WS upgrades. `*` wildcard explicitly NOT honored — a misconfig that would normally weaken CORS instead disables WS for everyone, which is the loud failure mode you want |
| Host header allowlist | `ui/web/server_origin.go:48-66` | Returns 421 on mismatch — blocks DNS-rebinding (the threat-model-aligned alternative to a strict CORS policy) |
| Content-Type enforcement | `ui/web/server.go:469-505` | Closes the `<form enctype="text/plain">` cross-origin POST vector that bypasses CORS preflight |

## Findings explicitly considered and rejected

- **"There is no `Access-Control-Allow-Origin: <safe-origin>` header even on safe origins."** Not a finding — the absence of the header is the policy; introducing the header would only weaken posture (e.g. an
  origin-reflection bug becomes possible the moment any handler emits the header).
- **"`*` in `allowed_origins` is silently dangerous."** Verified
  defended: `ui/web/server.go:144-148` prints a startup warning to
  stderr if `allowed_origins` contains `*`, and
  `ui/web/server.go:251-258` short-circuits any `*` entry as
  "no match" inside the WS origin check. Operators who copy-paste
  `["*"]` from a tutorial don't accidentally open the door.
- **"What if the operator runs DFMC behind a reverse proxy that adds
  `Access-Control-Allow-Origin: *`?"** That's a deployment concern
  outside DFMC's process. The architecture report and CLAUDE.md
  identify single-user/loopback as the supported posture; running
  behind a public CDN is explicitly out-of-scope.

## Verifications

1. Grep for any CORS-related response header write across `ui/web/`:
   zero. The only header writes are `Content-Type`,
   `Cache-Control`, `Connection`, `X-Accel-Buffering`,
   `Content-Security-Policy`, `X-Content-Type-Options`,
   `X-Frame-Options` — all benign.
2. Reviewed `securityHeaders` (`ui/web/server.go:128-135`): only
   sets CSP (`'self'`), `X-Content-Type-Options: nosniff`,
   `X-Frame-Options: DENY`. Does not emit any `Access-Control-*`.
3. Confirmed WS upgrader's `CheckOrigin` is bound to the per-Server
   allowlist rather than the gorilla default (which is open) — see
   `ui/web/server_ws.go:144-150`.

## Why this is "no issues, not just N/A"

A "no CORS headers, allowlist Origin only on the WS path, allowlist
Host on every path" posture is the right one for a same-origin
embedded workbench + server-to-server API. Each piece is implemented
and pinned by tests (`ui/web/server_origin_test.go`,
`ui/web/server_http_test.go`, `ui/web/server_ws_hardening_test.go`).
