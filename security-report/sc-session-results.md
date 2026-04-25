# sc-session — Session Management Findings

**Skill**: sc-session
**Target**: DFMC (`D:\Codebox\PROJECTS\DFMC`)
**Date**: 2026-04-25
**Scope**: session concept on the web/remote servers, token storage, lifecycle, rotation, expiry, revocation, client-side persistence.

## Counts

| Severity | Count |
|---|---|
| Critical | 0 |
| High     | 1 |
| Medium   | 4 |
| Low      | 3 |
| Info     | 2 |
| **Total** | **10** |

## Architecture summary

DFMC has **no session concept**. Authentication is a single static bearer token, sourced once at process start from `DFMC_WEB_TOKEN` / `DFMC_REMOTE_TOKEN` / `--token`. There are:
- No login endpoints (no `/auth/login`, no `/auth/logout`)
- No session IDs, no session cookies, no server-side session store
- No token rotation, expiry, revocation, or refresh
- No multi-user separation; the same token authenticates every caller

The "session" in the broader sense is **the lifetime of the `dfmc serve` process**. Restarting the process is the only way to invalidate a leaked token (and only if a different token is supplied at restart).

---

## SESS-001 (HIGH, Confidence HIGH) — Token persisted in browser `localStorage` (XSS-readable, no isolation)

- **File**: `ui/web/static/index.html:703-747`
- **CWE-922** (Insecure Storage of Sensitive Information), **CWE-1004** (Sensitive Cookie Without HttpOnly Flag) — n/a since no cookie used; substantive risk parallels CWE-1004

The workbench HTML stores the bearer token in `window.localStorage["dfmcWebToken"]`:

```javascript
function persist(value) {
    try {
        if (value) window.localStorage.setItem(STORAGE_KEY, value);
        else window.localStorage.removeItem(STORAGE_KEY);
    } catch (_) {}
}
```

`localStorage` is:
- Readable by any JS executing on the same origin (`http://127.0.0.1:7777`).
- Survives tab close, browser restart, and process restart of the server.
- NOT scoped per-tab; a malicious extension or a future XSS in the workbench reads it instantly.

DFMC's CSP `default-src 'self'; script-src 'self'` (server.go:124) prevents inline-script injection in theory, but:
- The workbench inlines significant amounts of JS in the HTML response — any future relaxation of CSP, or a `<script src="/static/...">` injection point, exposes localStorage.
- Browser extensions with full-host permission read localStorage regardless of CSP.
- DevTools Console exposes `localStorage` to any user with physical access (or screen-sharing in the wrong moment).

**Recommendation**: prefer in-memory storage with a session-cookie fallback (HttpOnly + SameSite=Strict + Secure-when-HTTPS). The token can be re-prompted on workbench reload — the small UX cost is justified given the static-token threat model.

---

## SESS-002 (MEDIUM, Confidence HIGH) — Token can be planted via URL hash, then auto-stored

- **File**: `ui/web/static/index.html:707-735`
- **CWE-598** (Information Exposure Through Query Strings in GET Request)

The workbench reads `#token=...` from the URL hash on load and writes it to localStorage:

```javascript
const fromHash = readFromHash();
if (fromHash) {
    token = fromHash.trim();
    persist(token);
    clearHashToken();
}
```

Combined with the wide-open `WebSocket CheckOrigin` (AUTH-003), an attacker can:
1. Lure operator to `http://attacker.com/`
2. Attacker page navigates to `http://127.0.0.1:7777/#token=ATTACKER_PLANTED` (DNS rebinding or top-level navigation)
3. Workbench JS auto-stores the planted token to operator's localStorage
4. Operator subsequently loads the workbench legitimately, but `localStorage` already has the wrong token — operator types real token into prompt, workbench overwrites the planted one. No real harm here.

The MORE interesting flow is the reverse: an attacker who reads the token (XSS, extension) then plants it into a URL the operator can't notice (browser history, tab restoration). Once `dfmcWebToken` is set, it's the active credential.

**Recommendation**: drop the hash-token bootstrap. Require explicit prompt entry. If hash-token is kept for a use case (CLI launches browser with `#token=`), at minimum bind it to a single fetch and require user confirmation before persisting.

---

## SESS-003 (MEDIUM, Confidence HIGH) — No token expiry / no rotation / no revocation

- **File**: `ui/web/server.go:181-186` (SetBearerToken — no expiry tracking)
- **CWE-613** (Insufficient Session Expiration)

Once set, the token is valid until the server process restarts. There is:
- No `iat`/`exp` in the token (it's a raw opaque string, not a JWT)
- No revocation list
- No `POST /api/v1/auth/rotate`
- No expiration on idle

A token leaked once is leaked forever (until manual operator restart with a new token).

**Recommendation**:
- Generate the default token as an HMAC-derived value with built-in expiry (`token = sha256(seed || timestamp)[:32]`, refuse if timestamp older than N hours).
- Add `POST /api/v1/auth/rotate` that mints a new token, invalidates old.
- Persist token-fingerprint + first-seen-timestamp; warn if a token has been used for >72h.

---

## SESS-004 (MEDIUM, Confidence HIGH) — No multi-token / per-client identity

- **File**: `ui/web/server.go:36-43` (`Server.token` is a single string)
- **CWE-287** (Improper Authentication) — coarse-grained

A single shared token authenticates every client. Two operators using `dfmc serve` together must either share the token (cannot distinguish actions in audit logs) or run separate processes (bbolt lock prevents this on the same project). There is no per-client identity carried through to:
- Conversation IDs
- Drive run records
- Task store entries
- EventBus payloads (no `actor` field)

**Recommendation**: when this matters, mint short-lived tokens with embedded actor names, log actor on every mutating call.

---

## SESS-005 (MEDIUM, Confidence MEDIUM) — `/ws` SSE accepts query-param token (logged everywhere)

- **File**: `ui/cli/cli_remote_server.go:87-90`
- **CWE-598** (Information Exposure Through Query Strings in GET Request)

Already reported as AUTH-002 from the auth-mode angle. Re-stated here from the session angle: a `?token=...` query parameter on `/ws` ends up in:
- Server access logs
- Reverse-proxy logs (when fronted by nginx/Caddy)
- Browser history
- Referer headers (when leaving the workbench)

A long-lived static token in a log file is effectively a permanent compromise — see SESS-003 (no rotation).

---

## SESS-006 (LOW, Confidence HIGH) — No `Secure` / `HttpOnly` / `SameSite` controls (no cookies used)

- **File**: n/a — no cookies set anywhere in `ui/web/`
- **CWE-1004**, **CWE-1275** — informational

DFMC does not use cookies for auth. All credentials ride in headers. This eliminates classical CSRF surface (AUTHZ side already documented) and removes cookie-flag concerns. **However**, this design forces `localStorage`-based persistence (SESS-001), which is its own XSS-amplifier. The tradeoff is real but not unambiguously better.

---

## SESS-007 (LOW, Confidence HIGH) — `prompt()` for token entry exposes credential to other browser surfaces

- **File**: `ui/web/static/index.html:758, 1321`
- **CWE-200** (Exposure of Sensitive Information)

The workbench uses `window.prompt("...")` to request the token on first 401:

```javascript
const provided = window.prompt("DFMC server requires a token (set via DFMC_WEB_TOKEN). Enter token:");
```

`window.prompt` is plaintext entry (no `type="password"` masking). Screen-sharing, shoulder surfing, or accidental clipboard captures expose the token.

**Recommendation**: replace with a proper modal containing `<input type="password" autocomplete="current-password">`.

---

## SESS-008 (LOW, Confidence MEDIUM) — No token-fingerprint logging / no "active sessions" view

- **File**: n/a — no audit surface
- **CWE-778** (Insufficient Logging)

There is no way for an operator to see which IPs have presented a token in the last N minutes, no way to see the count of distinct accepted-credential sources, no way to detect replay from an unexpected origin. The EventBus is volatile; access logs are not persisted by default.

**Recommendation**: add `GET /api/v1/auth/active` returning a list of `{client_ip, last_seen, request_count}` over a sliding window, gated on a new admin token tier.

---

## SESS-009 (INFO, Confidence HIGH) — `dfmc remote start` and `dfmc serve` use distinct env vars but identical lifecycles

- **File**: `ui/cli/cli_remote.go:32-38`
- **CWE-NONE** — informational

`DFMC_WEB_TOKEN` (for `dfmc serve`) and `DFMC_REMOTE_TOKEN` (for `dfmc remote start`) are intentionally separate so the two surfaces can run with distinct credentials side-by-side. No session concept beyond that. Documented for completeness.

---

## SESS-010 (INFO, Confidence HIGH) — Conversation/drive/task identifiers are session-equivalents but unscoped

- **File**: cross-cutting (`internal/conversation/manager.go`, `internal/drive/persistence.go`, `internal/taskstore/store.go`)
- **CWE-NONE** — informational

The closest thing DFMC has to a "session" is the *active conversation* (engine state). Switching it via `POST /api/v1/conversation/load` is unscoped (AUTHZ-007). There's no separation between "your session" and "the engine's session" — the engine has exactly one active conversation at a time, and any token-holder can pivot it.

---

## Cross-references with sc-csrf scope

- CSRF token: not present, not needed under bearer-token-in-header model **except** for `/ws` query-param fallback (SESS-005 + AUTHZ-001 + AUTH-003 cluster).
- The browser model where workbench is same-origin is the operative mitigation. Bind-host normalization keeps the origin local-only for auth=none.

## Summary table

| ID | Severity | Path | CWE |
|---|---|---|---|
| SESS-001 | HIGH | static/index.html:703-747 | 922 |
| SESS-002 | MEDIUM | static/index.html:707-735 | 598 |
| SESS-003 | MEDIUM | server.go:181-186 | 613 |
| SESS-004 | MEDIUM | server.go:36-43 | 287 |
| SESS-005 | MEDIUM | cli_remote_server.go:87-90 | 598 |
| SESS-006 | LOW | n/a (no cookies) | 1004 |
| SESS-007 | LOW | static/index.html:758, 1321 | 200 |
| SESS-008 | LOW | n/a (no audit) | 778 |
| SESS-009 | INFO | cli_remote.go:32-38 | — |
| SESS-010 | INFO | conversation/drive/task | — |
