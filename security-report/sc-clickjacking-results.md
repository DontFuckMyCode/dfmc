# sc-clickjacking Results

No issues found by sc-clickjacking.

## Defenses verified

The shared `securityHeaders` middleware
(`ui/web/server.go:128-135`) sets two headers on **every** response,
including the embedded workbench HTML at `GET /`:

```
X-Frame-Options: DENY
Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self'
```

- **`X-Frame-Options: DENY`** — refuses framing in any browser,
  including IE/old Edge that don't honor CSP `frame-ancestors`.
- **CSP `default-src 'self'`** — implies `frame-ancestors 'self'` in
  CSP3 and (because there is no separate `frame-ancestors`
  directive) falls back through `default-src` for the
  `frame-ancestors` policy in CSP2-aware browsers. Combined with
  `X-Frame-Options: DENY`, the workbench cannot be framed.

The middleware sits in the chain at `ui/web/server.go:386` and runs
*after* host allowlist + body limiter but *before* the mux, so every
handler — including SSE and WS upgrade responses — emits both
headers.

## Targets considered

| Surface | Frameable risk | Defense |
|---|---|---|
| `GET /` workbench HTML | Highest — clickjacking against the only interactive UI | XFO + CSP, both set |
| `/api/v1/*` JSON responses | Browsers don't render JSON in iframes for clickjacking; even so, headers fire | XFO + CSP |
| `/ws` SSE | Streamed `text/event-stream`, not interactive — not frameable | XFO + CSP |
| `/api/v1/ws` upgrade response | Either upgrades (101) or 4xx; not frameable | XFO + CSP |

## Verifications

1. Grep for `X-Frame-Options`: only the one definition at
   `ui/web/server.go:132`. Pinned by `ui/web/server_security_test.go`.
2. Grep for `Content-Security-Policy`: only the one definition at
   `ui/web/server.go:130`. Pinned by `ui/web/server_security_test.go`.
3. Confirmed the embedded workbench (`ui/web/static/index.html`) does
   not use `target="_top"` reframing tricks or `iframe` itself.
4. No handler overrides the security headers — grep for
   `Header().Set("X-Frame-Options"` returns the single canonical
   write.

## Adjacent controls

- The workbench HTML (`ui/web/static/index.html`) is served same-origin
  with `Content-Type: text/html; charset=utf-8` (`ui/web/server.go:652`),
  so MIME-sniffing into a "renderable" frame target is also blocked
  by `X-Content-Type-Options: nosniff`.
- DENY is stricter than `SAMEORIGIN`; given the embedded workbench
  has no legitimate need to frame itself, DENY is the right choice.

## Why this is "no issues, not just N/A"

Both the legacy (`X-Frame-Options`) and modern (CSP) frame-busting
headers are set on every response, including streaming/WS surfaces.
The decision and its test pinning predate this scan.
