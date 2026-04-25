# sc-auth — Authentication Findings

**Skill**: sc-auth
**Target**: DFMC (`D:\Codebox\PROJECTS\DFMC`)
**Date**: 2026-04-25
**Scope**: HTTP/SSE server (`dfmc serve`, port 7777), remote HTTP+WS server (`dfmc remote start`, port 7778), MCP stdio server, gRPC.

## Counts

| Severity | Count |
|---|---|
| Critical | 0 |
| High     | 1 |
| Medium   | 4 |
| Low      | 4 |
| Info     | 2 |
| **Total** | **11** |

## Auth-mode summary

- Two auth modes: `none` (default) and `token` (bearer). No `basic`, no `mTLS`, no OAuth/OIDC. Defined in `internal/config/config_types.go` (`Web.Auth`, `Remote.Auth`).
- Loopback default + `--auth=none` is fenced by `normalizeBindHost` (`ui/web/server.go:152-160`) — non-loopback bind silently rewrites to `127.0.0.1` when auth=none.
- `dfmc serve` and `dfmc remote start` refuse `--auth=none` on non-loopback hosts unless `--insecure` is set (`ui/cli/cli_remote.go:66-77`, `cli_remote_start.go:45-56`).
- gRPC port (`Remote.GRPCPort`, default 7778) is reserved but never started — only the HTTP+WS port is active. No TLS, no credential interceptors.
- MCP stdio server: no authentication (parent process is implicitly trusted). The DFMC bridge unconditionally exposes the full backend tool registry (`ui/cli/cli_mcp.go`).

---

## AUTH-001 (HIGH, Confidence HIGH) — Non-constant-time bearer comparison in CLI middleware

- **File**: `ui/cli/cli_remote_server.go:64-93`
- **CWE-208** (Observable Timing Discrepancy)

There are **two bearerTokenMiddleware implementations** in the codebase:

1. `ui/web/server.go:401-419` — uses `subtle.ConstantTimeCompare` (correct)
2. `ui/cli/cli_remote_server.go:64-93` — uses raw `==` comparison (incorrect)

Both `dfmc serve` and `dfmc remote start` apply the CLI version on top of the web Handler's middleware:

```go
// ui/cli/cli_remote.go:88-93
srv := web.New(eng, *host, *port)
srv.SetBearerToken(*token)
handler := srv.Handler()           // web.bearerTokenMiddleware applied here when cfg.Web.Auth=="token"
if mode == "token" {
    handler = bearerTokenMiddleware(handler, *token)  // CLI variant — NON-constant-time
}
```

`cli_remote_server.go:83`:
```go
if got := strings.TrimSpace(r.Header.Get("Authorization")); got == expected {
```

The outer wrapper (CLI variant) rejects **first**, so its timing channel is what an attacker measures. `subtle.ConstantTimeCompare` in the inner web layer is therefore unreachable in this composition. With localhost-only deployments the attack surface is small, but if `--insecure` is used or the operator fronts with a TLS proxy that exposes the server publicly, the leak is exploitable across the LAN.

**Recommendation**: replace the `==` with `subtle.ConstantTimeCompare`, or remove the redundant CLI-side wrapper entirely (the web Handler already gates by token).

---

## AUTH-002 (MEDIUM, Confidence HIGH) — `/ws` SSE accepts token via query parameter

- **File**: `ui/cli/cli_remote_server.go:87-90`
- **CWE-598** (Information Exposure Through Query Strings in GET Request)

The CLI-side bearer middleware allows the token via `?token=` query parameter when the path is `/ws`:

```go
if rawToken != "" && r.URL.Path == "/ws" && r.URL.Query().Get("token") == rawToken {
    next.ServeHTTP(w, r)
    return
}
```

The justification cited in the comment ("EventSource cannot set custom headers") is moot because the bundled workbench uses `fetch` with `Accept: text/event-stream` and Authorization header (`ui/web/static/index.html:1316-1319`), not `EventSource`. Tokens passed in query strings end up in:

- HTTP access logs (`X-Forwarded-For` proxies; reverse-proxy access logs)
- Browser history
- Referer headers on cross-origin links
- Process-list snapshots when a curl command is recorded

This path also uses non-constant-time `==` — same CWE-208 footprint as AUTH-001.

**Recommendation**: drop the query-param fallback. If a non-`fetch` EventSource client is needed, document a small token-mint dance instead.

---

## AUTH-003 (MEDIUM, Confidence HIGH) — WebSocket upgrader has wide-open `CheckOrigin`

- **File**: `ui/web/server_ws.go:29-35`
- **CWE-346** (Origin Validation Error)

```go
var wsUpgrader = websocket.Upgrader{
    ReadBufferSize:  4096,
    WriteBufferSize: 4096,
    CheckOrigin: func(r *http.Request) bool {
        return true // configured by caller via Server
    },
}
```

The comment says "configured by caller via Server" but no caller actually overrides it. With `auth=none` on loopback, **any** local browser process (e.g. a malicious page in the same browser session, a localhost-bound dev server with rebinding tricks) can upgrade `/api/v1/ws` and call `chat`/`tool` JSON-RPC methods. The same applies to **DNS-rebinding**: a remote site that rebinds `evil.com` → `127.0.0.1` after a TLS load can issue WebSocket upgrades from the user's browser.

When `auth=token`, the CLI bearer middleware fences `/api/v1/ws` (the upgrade is gated), but the SSE endpoint at `/ws` accepts query-param token (AUTH-002) which is reachable cross-origin through `EventSource`/img/iframe/`fetch+credentials:include` chains. CSP `default-src 'self'` blocks iframe/img loads from third-party origins to the workbench, but does NOT prevent a malicious page from issuing `fetch("http://127.0.0.1:7777/ws?token=stolen")` if it has the token, and from issuing the upgrade if `auth=none`.

**Recommendation**: validate `Origin` against an allow-list including `http://127.0.0.1:<port>` and `http://localhost:<port>`; reject everything else. This is fully covered by the [CORS / cross-origin assessment skill](https://skills/cors-cross-origin-misconfiguration).

---

## AUTH-004 (MEDIUM, Confidence MEDIUM) — `auth=none` defaults give zero-friction tool execution to any local process

- **File**: `ui/web/server.go:131-150` + `internal/engine/engine_tools.go:218-244`
- **CWE-306** (Missing Authentication for Critical Function)

Default config (`Web.Auth = "none"`) plus loopback bind plus `executeToolWithLifecycle`'s `source != "user"` gate (engine_tools.go:225) means:

- Any local process can `POST /api/v1/tools/run_command` (no auth) and trigger arbitrary subprocess execution because `engine.CallTool` always passes `source="user"` (engine_tools.go:120) — the approval gate is **explicitly skipped for user-tagged calls**.
- The `webApprover` (`ui/web/approver.go`) is the only gate, and it never fires for user-source calls.

Threats this exposes on a multi-user host or in shared dev environments:
- Other unprivileged users on the box reach 127.0.0.1:7777 (UNIX firewalls do not filter loopback by user).
- Container neighbors sharing the host network namespace.
- Browser-driven DNS rebinding (already noted in AUTH-003) allows third-party pages to reach loopback once they bypass `Origin` checks.

**Recommendation**: ship `Web.Auth = "token"` with an auto-generated token written to `~/.dfmc/web.token` mode 0600 as the default. Print the URL with `#token=...` on `dfmc serve` startup so the user opens the workbench with credentials baked in. The current `auth=none` default trades far too much security for first-run UX.

---

## AUTH-005 (MEDIUM, Confidence HIGH) — `GET /` workbench HTML served unauthenticated even when token is set

- **File**: `ui/web/server.go:409-411`, `ui/cli/cli_remote_server.go:76-79`
- **CWE-862** (Missing Authorization)

Both bearer middlewares deliberately bypass the token check for `GET /`:

```go
// server.go:409
if r.Method == http.MethodGet && r.URL.Path == "/" && rawToken == "" {
    next.ServeHTTP(w, r)
    return
}
// cli_remote_server.go:76 (no rawToken==""” gate — always allows)
if r.Method == http.MethodGet && r.URL.Path == "/" {
    next.ServeHTTP(w, r)
    return
}
```

Note the **divergence** between the two: the web middleware only un-gates `/` when `rawToken == ""` (i.e. never in token mode); the CLI middleware un-gates it unconditionally. Since `dfmc serve` composes the CLI middleware **on top of** the web middleware, the outer (CLI) check fires first and lets the workbench HTML through to anyone who can reach the port.

The HTML body itself contains no secrets, so the disclosure is not catastrophic. But (a) the inconsistency is a maintenance trap, and (b) an unauthenticated XSS vector inside the workbench would be reachable without the token. CSP `script-src 'self'` (server.go:124) is the mitigation, but the embedded HTML inlines significant amounts of JS and any future change that loosens CSP would expose this surface.

**Recommendation**: pick one behavior. If the workbench needs to be reachable to enter a token, make the auth flow explicit (`/auth/login` page) rather than relying on a silent bypass.

---

## AUTH-006 (LOW, Confidence HIGH) — No token rotation, no expiry, no revocation

- **File**: `ui/web/server.go:181-186`, `ui/web/static/index.html:703-747`
- **CWE-613** (Insufficient Session Expiration)

The bearer token is set once via env (`DFMC_WEB_TOKEN` / `DFMC_REMOTE_TOKEN`) or `--token` flag and never rotated. No refresh endpoint exists. There is no way to invalidate a leaked token short of restarting the process with a new token. Token storage is `localStorage` in the workbench (XSS-readable). See SESS-001 in the session report.

**Recommendation**: add a `POST /api/v1/auth/rotate` admin endpoint that mints a new token and invalidates the old. Document operator workflow.

---

## AUTH-007 (LOW, Confidence HIGH) — Healthz exempt — confirmation-only, but disclosure path

- **File**: `ui/web/server.go:405-407`, `ui/cli/cli_remote_server.go:68-71`
- **CWE-204** (Observable Response Discrepancy)

`/healthz` returns 200 with `{"status":"ok"}` regardless of auth. This is conventional, but the lack of any rate-limiting on healthz (it's served at the same handler depth as everything else; the per-IP limiter applies but not at a more aggressive rate) means an attacker can use it to probe whether `dfmc serve` is running on a given port — and combined with `bind=127.0.0.1` defaults, confirms which dev users on a shared host run DFMC. Low severity given the local-only threat model.

---

## AUTH-008 (LOW, Confidence HIGH) — `dfmc remote start` does not consult `web.New`'s loopback normalization

- **File**: `ui/cli/cli_remote_start.go:58-66`
- **CWE-665** (Improper Initialization)

`runServe` and `remoteStart` both call `web.New(eng, *host, *port)`. `web.New` normalizes the bind host via `normalizeBindHost` based on **`eng.Config.Web.Auth`** (server.go:132-136), not on the runtime `--auth` flag. So a configuration where `cfg.Web.Auth == "token"` and the operator runs `dfmc remote start --auth=none --host=0.0.0.0 --insecure` would **not** normalize because the config says token. The CLI itself fences this case at `cli_remote_start.go:45-51` (refuses without `--insecure`), but the layered normalization is config-driven not flag-driven, which is brittle if the config layout changes.

**Recommendation**: thread the runtime auth-mode through `web.New` rather than reading it back off `eng.Config`.

---

## AUTH-009 (LOW, Confidence MEDIUM) — `--insecure` accepted without explicit confirmation prompt

- **File**: `ui/cli/cli_remote.go:66-77`
- **CWE-1295** (Lack of Acknowledgement)

`--insecure` only requires a flag; no interactive `Are you sure? [y/N]` prompt and no env-var double-confirm. Combined with the readable shell-history footprint of `--insecure`, an operator could paste a tutorial command and silently expose tools to the LAN. The runtime warning (cli_remote.go:73-77) is to stderr only.

**Recommendation**: require `DFMC_ALLOW_INSECURE=1` env var alongside `--insecure`, or add a 5-second countdown banner.

---

## AUTH-010 (INFO, Confidence HIGH) — gRPC port reserved but not implemented; no TLS surface anywhere

- **File**: `ui/cli/cli_remote_start.go:22, 73-83`
- **CWE-319** (Cleartext Transmission of Sensitive Information) — informational only

Both `dfmc serve` and `dfmc remote start` are HTTP-only. The architecture report notes "Operators must front with a TLS proxy if exposing remotely." There is no in-process TLS; no `--cert`/`--key` flags. With the default loopback bind and refusal-without-`--insecure` guard, this is not a finding for the in-tree code, but **any documentation that recommends `--insecure --host 0.0.0.0` for remote access ships bearer tokens in cleartext over the LAN.**

---

## AUTH-011 (INFO, Confidence HIGH) — MCP server: no authentication, full tool registry exposed

- **File**: `ui/cli/cli_mcp.go:71-141`, `internal/mcp/server.go`
- **CWE-306** (Missing Authentication for Critical Function) — informational

The MCP server listens on stdin/stdout. The IDE host that launches DFMC owns the trust boundary. **The server unconditionally exposes the full backend tool registry** (read_file, write_file, run_command, apply_patch, edit_file, plus 6 synthetic Drive tools). If the IDE host is compromised, all tools are reachable.

The only gate within DFMC is `executeToolWithLifecycle`'s approval gate — which the MCP bridge bypasses for `source="user"` (engine_tools.go:120 → CallTool always uses source="user"). This means a compromised MCP host gets the same authority as the operator typing `/tool` in the TUI.

The MCP **client** path (`internal/mcp/client.go`) is more interesting: tool descriptions and outputs from third-party MCP servers feed into the agent loop unfiltered. This is a separate concern (see PRIV-005).

---

## Cross-references

- Constant-time comparison on the **inner** layer (server.go:413) is correct; the **outer** (cli_remote_server.go:83) is not. AUTH-001.
- CSRF/CORS interactions covered in `sc-csrf-results.md` (separate skill out-of-scope here).
- Session/token storage detailed in `sc-session-results.md` (SESS-001..N).
