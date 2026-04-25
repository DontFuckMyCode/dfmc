# sc-api-security — Results

OWASP API Security Top 10 audit of the DFMC HTTP+SSE+WebSocket surface
served by `dfmc serve` (default `127.0.0.1:7777`) and the parallel
`dfmc remote start` server (`7778/7779`). Out of scope: `dfmc mcp`
(stdio JSON-RPC, no network exposure).

Findings count: **6** (1 High, 3 Medium, 2 Low/Informational).
Top severity: **High** — cross-origin WebSocket hijack reaches
authenticated tool surface (`approval-gate-bypassed user source`)
with auth=none default.

---

## Finding: API-001

- **Title:** WebSocket `CheckOrigin` returns `true` unconditionally; cross-origin browser tabs on the host can drive tool/file/shell endpoints when `auth=none`
- **Severity:** High
- **Confidence:** 90
- **File:** `ui/web/server_ws.go:32-35` (CheckOrigin), `ui/web/server.go:188-256` (route registration), `internal/engine/engine_tools.go:116-138` (CallTool source="user" bypasses approval gate)
- **Vulnerability Type:** CWE-346 (Origin Validation Error) / CWE-1385 (WS Hijack)
- **Description:** `wsUpgrader.CheckOrigin` is hard-coded to `return true` with the comment "configured by caller via Server" — but no Server-level configuration ever overrides it. With the **default** `Web.Auth = "none"` (from `internal/config/defaults.go`) and the bind-host normalised to `127.0.0.1`, the WS endpoint at `GET /api/v1/ws` accepts upgrades from any Origin. A malicious page in any browser tab on the same host (`http://attacker.example`) can `new WebSocket("ws://127.0.0.1:7777/api/v1/ws")`, send `{"method":"tool","params":{"name":"run_command","params":{"command":"…"}}}`, and the WS handler dispatches via `c.engine.CallTool` — which hard-codes `source = "user"` (`engine_tools.go:120`). The `source != "user"` check at `engine_tools.go:225` therefore **skips the approval gate entirely** for WS-driven tools, even ones flagged destructive (`run_command`, `write_file`, `apply_patch`, `git_commit`).
- **Attack chain:**
  1. User starts `dfmc serve` (defaults: `auth=none`, host normalised to `127.0.0.1`).
  2. User opens a browser. Any tab — ad iframe, malicious page, drive-by — can `new WebSocket("ws://127.0.0.1:7777/api/v1/ws")` because `CheckOrigin=true` accepts it and there is no bearer-token middleware (auth=none).
  3. Attacker sends `{"id":1,"method":"tool","params":{"name":"run_command","params":{"command":"echo pwned > ~/Downloads/proof"}}}`.
  4. `wsConn.handleTool` calls `c.engine.CallTool` → `executeToolWithLifecycle(ctx, name, params, "user")` → approval gate skipped → tool runs with the dfmc process's privileges in the project root.
- **Impact:** Full RCE-equivalent on the host whenever `dfmc serve` is running with default config. Any malicious webpage the user visits can read/write project files, run shell commands, commit git changes, and invoke `web_fetch`/`web_search` from the user's machine.
- **Remediation:**
  1. Replace the hard-coded `CheckOrigin: true` with an allowlist that defaults to `same-origin` (compare `r.Header.Get("Origin")` host:port against the bind addr) and only relaxes when the operator opts in.
  2. Refuse WS upgrades with `auth=none` on **any** host — loopback or otherwise — unless a `--insecure` flag is set (mirror the existing `runServe` guard at `cli_remote.go:66-72`, which only fires for non-loopback binds).
  3. Consider a CSRF-style anti-forgery handshake on the WS endpoint: require the client to first GET a short-lived nonce from `/api/v1/status` (which a same-origin XHR can read but a cross-origin one cannot) and present it as the first WS message.
- **References:** https://owasp.org/API-Security/editions/2023/en/0xa2-broken-authentication/, https://christian-schneider.net/CrossSiteWebSocketHijacking.html

---

## Finding: API-002

- **Title:** `engine.CallTool` hard-codes `source="user"` regardless of caller — HTTP/WS callers bypass the approval gate that protects against agent-initiated misuse
- **Severity:** Medium (becomes High when chained with API-001)
- **Confidence:** 95
- **File:** `internal/engine/engine_tools.go:116-138` (CallTool), `:218-244` (executeToolWithLifecycle approval check), `ui/web/server_tools_skills.go:151-173` (handleToolExec), `ui/web/server_ws.go:244-266` (WS handleTool), `ui/web/approver.go:1-67` (web approver)
- **Vulnerability Type:** CWE-863 (Incorrect Authorization)
- **Description:** The web layer routes `POST /api/v1/tools/{name}` and the WS `tool` method through `s.engine.CallTool`, which in turn calls `executeToolWithLifecycle(ctx, name, params, "user")`. The "user" source is treated as a trusted human at the keyboard, and the approval gate at `engine_tools.go:225` (`if source != "user" && e.requiresApproval(name)`) is **skipped**. The `webApprover` deny-by-default policy in `ui/web/approver.go` is therefore unreachable for these endpoints — it only fires for agent-initiated tool calls inside `Engine.Ask`, not for direct HTTP tool invocations. A network client (or, per API-001, a cross-origin browser tab) can invoke any registered tool — including `run_command`, `write_file`, `apply_patch`, `git_commit` — with no operator confirmation, even when `Config.Tools.RequireApproval` lists them.
- **Impact:** Operators who configured `tools.require_approval = ["run_command", "write_file"]` reasonably expect those tools to gate on every invocation surface. The current shape silently exempts the HTTP/WS endpoints, which are the highest-risk surfaces (network-reachable). Bearer-token auth limits *who* can reach the endpoint but does not constrain *which* tools an authenticated client can run.
- **Remediation:** Either (a) propagate the call source to `CallTool` (`CallTool(ctx, name, params, "user"|"web"|"ws")`) and reserve `"user"` for stdin-driven CLI/TUI; the web/ws sources still respect `requiresApproval`, gated by the `webApprover`'s `DFMC_APPROVE` env var; or (b) document and enforce that the `webApprover` is the *only* surface for HTTP/WS tool calls and remove the source-shortcut entirely for non-stdio entry points. Tests `internal/engine/approver_test.go:150` already pin the `"user"` shortcut behaviour, so the fix has a clear regression target.
- **References:** OWASP API5:2023 (BFLA), https://owasp.org/API-Security/editions/2023/en/0xa5-broken-function-level-authorization/

---

## Finding: API-003

- **Title:** `auth=none` is the default; new operators get an unauthenticated network-local API exposing tool/shell/filesystem
- **Severity:** Medium
- **Confidence:** 80
- **File:** `internal/config/defaults.go` (Web.Auth default), `ui/web/server.go:131-150` (New), `ui/cli/cli_remote.go:40-77` (runServe guard fires only when non-loopback)
- **Vulnerability Type:** CWE-1188 (Initialization of a Resource with an Insecure Default)
- **Description:** `Web.Auth` defaults to `"none"`. The bind-host normalisation at `server.go:152-160` rewrites a non-loopback host back to `127.0.0.1` in that case (and `runServe` refuses non-loopback unless `--insecure`), so the network-from-LAN risk is bounded. **However**, on the loopback interface the API is wide open to any local process or any cross-origin browser tab (per API-001). The CLI emits no warning about this default, and the workbench HTML at `/` does not surface "you are unauthenticated" to the user. A token-mode default would make this surface fail-closed.
- **Impact:** The default posture trusts every process running as the same user — including untrusted code in browser sandboxes, dev-server hot-reload pages, and any local container/VM that can reach `127.0.0.1` of the host (Docker Desktop's `host.docker.internal`, WSL2 in mirrored mode, etc.).
- **Remediation:** Flip the default to `auth=token` and auto-generate a token at startup (printed to stderr like `git-credential` does), or print a stark warning on every `dfmc serve` start that explains "any local process can drive this server". Document the threat model in the workbench HTML banner.
- **References:** OWASP API8:2023 (Security Misconfiguration)

---

## Finding: API-004

- **Title:** `bearerTokenMiddleware` allows `GET /` with empty token even when `auth=token` is configured
- **Severity:** Medium
- **Confidence:** 75
- **File:** `ui/web/server.go:401-419` (bearerTokenMiddleware), `:409-412` (the empty-token shortcut)
- **Vulnerability Type:** CWE-287 (Improper Authentication)
- **Description:** The middleware contains an explicit branch:
  ```go
  if r.Method == http.MethodGet && r.URL.Path == "/" && rawToken == "" {
      next.ServeHTTP(w, r)
      return
  }
  ```
  This serves the workbench HTML to any unauthenticated `GET /` when the configured token is empty. The comment claims this is "questionable; relies on `runServe` refusing empty token at startup" — and indeed `cli_remote.go:62-65` does refuse `auth=token` with empty token. But `web.New(...)` plus `srv.SetBearerToken(token)` can be invoked by any other surface (tests, a future remote starter, an embedded host), and the empty-token shortcut would silently disable auth for `/` if the token were ever forgotten or wiped via `SetBearerToken("")`. The check should be `rawToken != ""` only.
- **Impact:** Defense-in-depth gap. Today benign because `runServe` rejects empty-token startup, but the middleware shouldn't depend on a sibling caller to enforce its precondition. Any code path that constructs the `Server` directly (tests, embedded usage, or future surfaces like `dfmc remote start` if its token check ever drifts) loses auth on `/`.
- **Remediation:** Remove the empty-token-shortcut entirely. If the token is empty when the middleware runs, treat that as a misconfiguration and 503 (or, better, fail at construction time — refuse to wrap the handler in `bearerTokenMiddleware` with an empty token). The `/healthz` exemption is sufficient on its own for liveness probing.
- **References:** OWASP API2:2023 (Broken Authentication)

---

## Finding: API-005

- **Title:** Verbose error messages echo internal details — `err.Error()` returned directly to API clients in 70+ handlers
- **Severity:** Low
- **Confidence:** 70
- **File:** Across `ui/web/server_*.go` — 72 occurrences in 10 files. Examples: `server_files.go:36,68,74,96` (filesystem error strings echoed), `server_drive.go:79,104,167` (bbolt path leaks), `server_workspace.go:31,75,225-229` (git stderr echoed), `server_chat.go:51` (provider error message)
- **Vulnerability Type:** CWE-209 (Generation of Error Message Containing Sensitive Information)
- **Description:** Every web handler follows the pattern `writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})`. For local-only loopback this is acceptable, but the same handlers run on `dfmc remote start` which can be exposed cross-network. Surfaced strings include git stderr (`server_workspace.go:113,229`), bbolt paths, internal file system absolute paths from `EvalSymlinks`, provider HTTP error bodies, and Go formatting verbs. An attacker probing the API gains a clear map of the server's filesystem layout and internal pipeline shape.
- **Impact:** Information disclosure. Aids attacker reconnaissance — e.g. provider error messages can confirm valid API key prefixes; `EvalSymlinks` errors disclose existence of files outside project root; git stderr can leak repo paths. No direct exploit.
- **Remediation:** Centralise error responses through a helper that sanitises (or generic-ifies) error strings before serialising. Keep full details in the engine event bus (already sanitised callers can subscribe) and return short canonical messages over HTTP. The Go stdlib pattern `errors.Is`/`errors.As` lets you map known sentinels (`os.ErrNotExist`, `storage.ErrStoreLocked`) to user-friendly strings while collapsing everything else to `"internal error"`.
- **References:** OWASP API8:2023 (Security Misconfiguration)

---

## Finding: API-006

- **Title:** `POST /api/v1/drive` and tool/skill exec accept any registered tool name without per-route allowlist
- **Severity:** Low (informational)
- **Confidence:** 60
- **File:** `ui/web/server_tools_skills.go:151-173` (handleToolExec), `ui/web/server_drive.go:55-121` (handleDriveStart with `AutoApprove []string`)
- **Vulnerability Type:** CWE-862 (Missing Authorization) — informational
- **Description:** `POST /api/v1/tools/{name}` routes to `engine.CallTool(name, params)` for any registered tool — meta tools, destructive tools, and search tools share one endpoint. There is no per-tool route policy, no read-only-tool subset, and no rate-limit-per-tool-class. Similarly `DriveStartRequest.AutoApprove []string` lets the HTTP caller pre-approve a list of tools the drive will use without any operator gate. This is intentional (per architecture.md the surface is "single-user local API") but worth pinning explicitly: anyone reaching the API has access to every registered tool.
- **Impact:** Architectural — not directly exploitable on its own. Combined with API-001/002 it widens the blast radius of an unauthenticated WS hijack to "every tool the binary ships with" instead of "a curated read-only subset".
- **Remediation:** Consider a `--read-only` serve mode that binds a tool allowlist (e.g. `read_file`, `grep_codebase`, `find_symbol`, `codemap`, `web_fetch`) and rejects everything else at the route layer. Also worth restricting `DriveStartRequest.AutoApprove` to the same allowlist when the server runs in read-only mode.
- **References:** OWASP API5:2023 (BFLA)

---

## Cross-references / corroborated areas

- BOLA (API1): conversation/drive/task IDs are not user-scoped because the architecture is single-process / single-user (bbolt holds an exclusive process lock). All identifiers are global to the running binary; `/api/v1/conversation/{id}` and friends authenticate via the bearer token (when `auth=token`) and authorise implicitly. No multi-tenant footgun present today, but the contract is "anyone with the bearer token sees everything".
- Rate limiting (API4): per-IP token bucket at 30 r/s burst 60 in `server.go:277-367` is present; deferred to `sc-rate-limiting` for the exhaustive review.
- Mass assignment (API6): `handleTaskUpdate` (`server_task.go:160-218`) uses an explicit field-by-field switch on the patch map instead of structural unmarshalling — good. Drive request and Task create are flat structs with no field stripping; deferred to `sc-mass-assignment`.
- Injection (API8): tool layer is hardened (path containment, git flag rejection, blocklist on `run_command`); deferred to `sc-cmdi`/`sc-sqli`.
- SSRF (API10): outbound HTTP via `web_fetch` already wrapped in `safeTransport`; deferred to `sc-ssrf`.
- CSRF: no token, but bearer-in-header (not cookie) blocks classic form-based CSRF. The WS gap (API-001) is the residual cross-origin risk.

## Special focus answers (from prompt)

- **Listening interface:** `cmd/dfmc/main.go` does not bind anything itself. `runServe` (`ui/cli/cli_remote.go:40-111`) defaults to `eng.Config.Web.Host` (default `127.0.0.1` per `internal/config/defaults.go`), and `web.normalizeBindHost` (`server.go:152-160`) **forces 127.0.0.1** when `auth=none`. Verified: cannot listen on 0.0.0.0 unauthenticated without `--insecure`.
- **Browser reachability of `/`:** Yes, any browser tab on the host can `GET http://127.0.0.1:7777/` and (because there is no auth in the default config) receive the workbench HTML. The CSP on the workbench (`server.go:124`: `default-src 'self'; script-src 'self'; style-src 'self'`) prevents an attacker from injecting external scripts into that HTML, but does **not** prevent a *different* origin's page from opening a same-origin-target `WebSocket` (CSP doesn't gate cross-origin WS — see API-001).
- **CSRF surface:** Bearer token in `Authorization` header (not cookie) rules out classic form-CSRF. The non-cookie auth model is the right call. The remaining gap is the WS endpoint with `CheckOrigin: true` plus the default `auth=none` (API-001 / API-003).

---

**Discovery + Verification complete.** Six findings recorded; the API-001 + API-002 chain is the standout — default config + browser tab on host = unauthenticated approval-gate-skipping tool execution. Recommend addressing those two together.
