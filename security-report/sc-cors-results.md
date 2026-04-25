# sc-cors — Cross-Origin Resource Sharing Results

**Target:** DFMC `dfmc serve` / `dfmc remote start` HTTP+SSE+WS surface
**Skill:** sc-cors
**Counts:** Critical: 0 | High: 1 | Medium: 3 | Low: 1 | Info: 2 | Total: 7

---

## Summary

DFMC sets **no CORS headers anywhere** in `ui/web/`. Verified by
`grep "Access-Control"` across the entire repo — zero matches.
There is no `Access-Control-Allow-Origin`, no `Access-Control-Allow-
Credentials`, no `Access-Control-Allow-Methods`, no
`Access-Control-Allow-Headers`, no `Access-Control-Max-Age`, and no
explicit OPTIONS handler.

This means:

- **Browser SOP applies in default form.** Cross-origin `fetch` /
  `XHR` calls from another origin can be **sent** (and POSTs with
  side effects fire) but the response body is **not readable** in
  JavaScript.
- **Simple GETs leak via side channels** (timing, cache, image load,
  `<script>` tag CSP-permitting), but the workbench CSP
  `default-src 'self'` blocks third-party page from including
  DFMC URLs as scripts/styles.
- **Preflighted requests fail closed.** Anything that triggers a
  preflight (`Content-Type: application/json` POSTs, custom headers,
  PATCH/DELETE methods) requires an OPTIONS that succeeds with
  CORS headers — there is no OPTIONS handler, so the stdlib mux
  returns 405. The browser fails the preflight and aborts the
  cross-origin call. **This is incidentally CORS-safe**, but
  brittle: any future "fix" that adds a permissive OPTIONS handler
  (`mux.HandleFunc("OPTIONS /api/v1/...", ...)`) without a strict
  Origin allowlist would unlock every state-changing endpoint to
  any origin.
- **Simple POSTs (form-encoded, text/plain) bypass preflight.**
  These are still sent. `handleToolExec` does not enforce
  `Content-Type: application/json`; the JSON decoder tolerates
  `text/plain` request bodies. So a `<form
  enctype="text/plain">` cross-origin POST to
  `/api/v1/tools/run_command` will be delivered.
- The dependency on bearer-token-in-header (not cookies) is the
  load-bearing mitigation: even if the response could be read,
  the attacker doesn't have the token. With `auth=none` (default,
  loopback-only) there is no token barrier at all.

---

## Findings

### CORS-001 — No CORS middleware; no `Access-Control-*` headers on any response
- **Severity:** Info
- **Confidence:** High
- **CWE:** CWE-942 (Permissive Cross-Domain Policy) — informational
  inverse: there is no policy at all; SOP defaults govern.
- **File:** `ui/web/server.go:122-129` (only headers set are CSP,
  nosniff, frame-deny); confirmed by repo-wide grep
  `Access-Control` → 0 matches.
- **Detail:** No CORS layer is installed. The only header
  middleware is `securityHeaders` which sets CSP / X-Content-Type-
  Options / X-Frame-Options.
- **Impact:** Browser SOP is the only barrier. Cross-origin POSTs
  with `Content-Type: application/json` are blocked-by-preflight-
  failure (no OPTIONS handler). Cross-origin GETs and
  text/plain/form-encoded POSTs are sent and execute server-side.
  Response bodies are not readable in cross-origin JS.
- **Note:** This is the *intended* design per the architecture
  report: "Same-origin only by browser default; the embedded
  workbench is itself served from `/`."
- **Remediation:** Add an explicit allowlist
  (`Access-Control-Allow-Origin: http://127.0.0.1:7777,
  http://localhost:7777` reflected per-request from a small set;
  `Allow-Credentials: false`; no wildcard) so future maintainers
  understand the policy is "loopback only" rather than "unset".

### CORS-002 — No OPTIONS handler; preflight fails 405 — relies on stdlib mux behaviour
- **Severity:** Medium
- **Confidence:** High
- **CWE:** CWE-693 (Protection Mechanism Failure)
- **File:** `ui/web/server.go:188-257` — every route registered
  with explicit verbs (`GET /`, `POST /api/v1/tools/{name}`, etc.).
  No `OPTIONS *` handler.
- **Detail:** Go 1.22+ method-routed mux returns
  `405 Method Not Allowed` for unmatched verbs. Preflight is
  therefore rejected, which **incidentally** blocks credentialed
  cross-origin JSON POSTs. The brittleness is that a developer
  adding "OPTIONS support so curl preflight works" might add
  `Access-Control-Allow-Origin: *` without auditing the rest —
  silently unlocking every endpoint.
- **Impact:** Today: preflight fails closed (good). Tomorrow:
  one accidentally-liberal commit unlocks everything.
- **Remediation:** Add an explicit OPTIONS middleware that
  validates Origin against a loopback allowlist and returns
  the minimal CORS headers; pin behaviour with a test.

### CORS-003 — `EventSource` (SSE) is not subject to preflight; cross-origin can subscribe
- **Severity:** High
- **Confidence:** High
- **CWE:** CWE-942 (Overly Permissive Cross-Origin Policy)
- **File:** `ui/web/server_chat.go:116-168` (`/ws` SSE handler);
  registered at `server.go:254`.
- **Detail:** `EventSource` issues a plain `GET` with
  `Accept: text/event-stream` and **does not preflight**. Without
  an Origin allowlist, a page on any origin can
  `new EventSource('http://127.0.0.1:7777/ws')` and receive every
  event the engine publishes (tool results, agent loop transitions,
  intent decisions, provider request/response, drive events). With
  `auth=none` (default) there is no bearer-token barrier; with
  `auth=token`, the attacker still cannot read the token to set
  the header, **but the SSE handler does not consult the auth
  middleware for streaming the body once subscribed** — actually
  it does, since `bearerTokenMiddleware` wraps `mux` (server.go:280)
  before any handler — verified. So `auth=token` blocks this. With
  `auth=none` it's wide open.
- **Impact:** Continuous cross-origin exfiltration of every event
  (file content snippets in tool results, command outputs,
  conversation prompts).
- **Remediation:** Origin allowlist on the `/ws` SSE handler, or
  documented "auth=none is loopback-only" guarantee re-enforced by
  refusing to accept connections whose `r.Host` is non-loopback
  (defense-in-depth against rebinding).

### CORS-004 — DNS-rebinding turns "loopback bind" into "any origin"
- **Severity:** High
- **Confidence:** Medium
- **CWE:** CWE-350 (Reliance on Reverse DNS / DNS-rebinding)
- **File:** `ui/web/server.go:131-160` (bind normalization); no
  `Host:` header validation anywhere.
- **Detail:** The architecture's CORS-policy is "same-origin only
  by browser default; loopback-bind keeps the listener
  unreachable to non-localhost browsers." DNS rebinding sidesteps
  both: the attacker controls the hostname, points it at 127.0.0.1
  after the page loads, and now the **same-origin** check passes
  — the page's origin is `http://attacker.example.com` and the
  request goes to `http://attacker.example.com` which now resolves
  to 127.0.0.1. SOP allows the read; CORS headers (none set)
  don't matter; the server has no Host allowlist to refuse the
  attacker-controlled hostname.
- **Impact:** Combined with no CORS check + no Host check, every
  endpoint becomes cross-origin-reachable from any internet origin
  in the user's browser.
- **Remediation:** Validate `r.Host` against
  `{127.0.0.1:<port>, localhost:<port>}` allowlist; reject with
  421 Misdirected Request otherwise. This is the canonical DNS-
  rebinding mitigation (e.g. used by Tor Browser, Geth, etc.).

### CORS-005 — Bearer-token-in-header model reduces cross-origin risk where auth=token
- **Severity:** Info
- **Confidence:** High
- **CWE:** N/A (positive finding)
- **File:** `ui/web/server.go:401-419` (`bearerTokenMiddleware`).
- **Detail:** Tokens travel in `Authorization: Bearer …`, never in
  cookies. Cross-origin pages cannot read the token without an XSS
  in the workbench (CSP `script-src 'self'` blocks the obvious
  channels). Net: with `auth=token`, cross-origin CSRF/CORS
  exposure is bounded to what the attacker can do without the
  token (basically: send blind requests that fail 401). This is
  the load-bearing safety property — it does not generalize to
  `auth=none`.
- **Note:** `runServe` and `runRemote` both refuse to start with
  `auth=none` on a non-loopback host without `--insecure`
  (`cli_remote.go:66-77`, `cli_remote_start.go:45-50`), which
  closes the obvious foot-gun.

### CORS-006 — `Content-Type` not enforced on JSON POSTs — simple form POSTs bypass preflight
- **Severity:** Medium
- **Confidence:** High
- **CWE:** CWE-352 / CWE-942
- **File:** `ui/web/server_tools_skills.go:151-173`
  (`handleToolExec`); same shape across other handlers.
- **Detail:** `json.NewDecoder(r.Body).Decode(&req)` accepts any
  request body, regardless of Content-Type. A cross-origin
  `<form enctype="text/plain">` POST whose single input is named
  `{"params":{"command":"calc"},"x":"` (with a closing `"}` from
  another input) is sent without preflight (text/plain is a
  CORS-simple type). The server happily decodes it. **This makes
  CORS-002's preflight-as-incidental-CSRF-defense leaky** — the
  preflight only kicks in for application/json content type.
- **Impact:** Cross-origin write CSRF without preflight even
  with the current zero-CORS configuration. Combines with
  CSRF-001 / CSRF-004 to make those exploitable from any origin
  page (no rebinding required) when `auth=none`.
- **Remediation:** Reject any POST/PATCH whose
  `Content-Type` is not `application/json` (or is missing) with
  415; this turns the SOP-simple-POST loophole back into a
  preflight-required call.

### CORS-007 — Auth bypass for `GET /` allows the workbench HTML to be returned cross-origin
- **Severity:** Medium
- **Confidence:** Medium
- **CWE:** CWE-942 (sub-case)
- **File:** `ui/web/server.go:409-411`.
- **Detail:** When `auth=token` but the configured token is empty,
  `bearerTokenMiddleware` lets `GET /` through unauthenticated.
  Cross-origin SOP still blocks reads of the HTML (no CORS
  headers), but with rebinding (CORS-004) the same-origin check
  passes and the attacker's page can `fetch('/').then(r=>r.text())`
  and inspect the workbench HTML for fingerprinting (template
  version, embedded URLs).
- **Note:** `runServe` refuses to start in token-mode with empty
  token (`cli_remote.go:62-65`), so this branch is theoretical
  unless the operator constructs `web.New` directly.
- **Remediation:** Defensive: include `GET /` in the auth gate
  unconditionally; keep `/healthz` exempt only.

---

## Notable Finding

**CORS-006** is the unexpected one. The default-CORS-zero design
relies on the fact that JSON POSTs require a CORS preflight, which
fails 405 because there's no OPTIONS handler. But the JSON decoder
does not enforce `Content-Type: application/json` — a
**`<form enctype="text/plain">`** cross-origin POST is a simple
request, skips preflight, and the body is decoded as JSON regardless.
This means cross-origin write attacks against `POST
/api/v1/tools/{name}` and `POST /api/v1/workspace/apply` go through
unmodified when `auth=none`, and only fail 401 when `auth=token`. The
"preflight is the de-facto CSRF defense" assumption in the
architecture report does not hold.
