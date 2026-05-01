# sc-csrf Results

No issues found by sc-csrf.

## Why CSRF tokens are not required here

CSRF attacks require the victim's browser to be coaxed into sending
*ambient* credentials (typically cookies) on a cross-origin request.
DFMC's web surface does not use ambient credentials:

- **No cookies, no `Set-Cookie`, no `SameSite` attribute** — grep
  across `ui/web/` confirms zero issuance (`sc-session-results.md`).
- **Authentication is bearer-token only** (`Authorization: Bearer
  <DFMC_WEB_TOKEN>`, `ui/web/server.go:661-679`). Browsers do NOT
  attach `Authorization` headers to cross-origin requests
  automatically — the page's JavaScript would have to set it
  explicitly, at which point the browser's CORS preflight gates the
  request.
- **No CORS headers are sent** (`sc-cors-results.md`), so a
  cross-origin browser tab cannot read responses or send simple POSTs
  with custom JSON content-types.

This is the same posture as Anthropic's own MCP/CLI surfaces and is
considered the canonical "bearer-token APIs are CSRF-immune" stance
by OWASP.

## Defenses against the residual `<form>` vector

The classical CSRF leak vector for bearer-less APIs is a cross-origin
HTML `<form enctype="text/plain">` POST that sneaks JSON-shaped bytes
past the CORS preflight (since `text/plain` is a "simple" request). DFMC
defangs this with `contentTypeEnforcementMiddleware`
(`ui/web/server.go:469-505`):

```
if strings.HasPrefix(ct, "application/json") {
    next.ServeHTTP(w, r); return
}
writeJSON(w, http.StatusUnsupportedMediaType, ...)
```

Any body-bearing POST/PATCH/PUT with a non-`application/json`
content-type is rejected with 415 *before* the body reaches the JSON
decoder. This blocks the `text/plain` vector entirely.

Verified pinning: `ui/web/server_content_type_test.go`.

## Verifications

1. **No `Set-Cookie` issuance.** Grep across `ui/web/` for
   `Set-Cookie`, `http.Cookie`, `SameSite`: zero matches.
2. **No CSRF-token middleware** required because no cookies; grep for
   `csrf`, `XSRF`, `_token`: zero matches.
3. **GETs are read-only.** Reviewed every `GET` route in
   `setupRoutes` (`ui/web/server.go:300-363`) — none mutates state.
   Drive control mutations are `POST`/`DELETE`. Task mutations are
   `POST/PATCH/DELETE`. Workspace apply is `POST`. Conversation
   load/save/undo are `POST`. So the cross-origin `<img src=...>` /
   `<link>` / GET-based CSRF vector has no mutation target.
4. **WebSocket origin allowlist** (`checkWebSocketOrigin` in
   `ui/web/server.go:238-269`) rejects cross-origin browser-tab WS
   upgrades; native clients (no `Origin` header) are accepted
   intentionally. `*` in the allowlist is explicitly treated as "no
   match" so an operator typo can't open the door.
5. **Host header allowlist** (`hostAllowlistMiddleware`,
   `ui/web/server_origin.go:48-66`) returns 421 on a mismatch — blocks
   DNS-rebinding which is a sibling-attack to CSRF.

## Why this is "no issues, not just N/A"

The threat model deliberately replaces a CSRF token system with the
combination of (loopback bind + bearer header + no cookies +
content-type enforcement + host/origin allowlists). Each of those
controls is implemented and pinned by tests. The only way CSRF
findings could become applicable is if a future change introduced
session cookies — at which point the existing host/origin allowlists
would still reduce blast radius, but a SameSite attribute and either
double-submit or origin-bound CSRF token would need to be added.
