# sc-csrf — Cross-Site Request Forgery Results

**Target:** DFMC `dfmc serve` / `dfmc remote start` HTTP+SSE+WS surface
**Skill:** sc-csrf
**Counts:** Critical: 0 | High: 2 | Medium: 3 | Low: 2 | Info: 1 | Total: 8

---

## Summary

DFMC does **not** implement any CSRF protection of its own. There is **no
CSRF token, no double-submit cookie, no custom-header check, no Origin/
Referer allowlist, and no preflight requirement** anywhere in
`ui/web/`. Verified by grep across `ui/web/**/*.go` — the only `Origin`
hits are inside the WS upgrader (`CheckOrigin: return true`) and an
unrelated `Origin` field on a task struct.

The intended mitigation is layered:

1. The bind host is forced to loopback when `auth=none`
   (`server.go:152-160`, `normalizeBindHost`), so a remote browser can
   only reach the listener at all when paired with `auth=token` or
   `--insecure`.
2. Authentication is bearer-token-in-header (not cookie). A foreign
   origin's `<form>` POST or `fetch` cannot read the token out of
   `localStorage`/`Authorization` storage, so credentialed cross-origin
   forgery against a token-mode server is blocked **as long as no XSS
   exists in the workbench**.
3. CSP `script-src 'self'` and `X-Frame-Options: DENY` reduce
   workbench-XSS-driven theft, but neither of these is a CSRF control.

The residual risks are concentrated on three vectors:
**(a)** browser-driven access to a localhost `auth=none` server from a
malicious page running in the same browser
(`http://evil.com` → `http://127.0.0.1:7777`);
**(b)** **DNS rebinding** — same browser, attacker-controlled hostname
that flips its A record from a public IP to 127.0.0.1, bypassing the
loopback-bind mitigation while still letting the attacker control the
JS that fires the requests;
**(c)** the `/ws` SSE stream and `/api/v1/ws` WebSocket, which a
remote page can subscribe to from any origin (CheckOrigin returns
true; SSE has no preflight at all).

---

## Findings

### CSRF-001 — No CSRF token / Origin check on any state-changing endpoint
- **Severity:** High
- **Confidence:** High
- **CWE:** CWE-352 (Cross-Site Request Forgery)
- **File:** `ui/web/server.go:188-257` (route registration); confirmed
  by absence across all `server_*.go` siblings.
- **Detail:** Every POST/PATCH/DELETE handler — including
  `POST /api/v1/tools/{name}` (arbitrary tool dispatch),
  `POST /api/v1/workspace/apply` (unified-diff write),
  `POST /api/v1/drive` (autonomous run kickoff),
  `POST /api/v1/conversation/load`, `PATCH /api/v1/tasks/{id}`,
  `DELETE /api/v1/drive/{id}` — accepts a request without ever
  checking a CSRF token, custom anti-CSRF header, Origin, or Referer.
  `handleToolExec` (`server_tools_skills.go:151-173`) decodes JSON
  and calls `engine.CallTool` directly.
- **Impact, default config (`auth=none`, loopback bind):** A page on
  any origin loaded by the user's browser can `fetch('http://127.0.0.1:7777/api/v1/tools/run_command', {method:'POST', body: '{"params":{"command":"calc"}}', headers:{'Content-Type':'application/json'}})`
  and the request will be sent — SOP doesn't block sending, only
  reading. With `source` defaulting to non-`"user"` the approval gate
  will fire (deny-by-default web approver), but the model-driven path
  through `/api/v1/chat` and `/api/v1/drive` does not consult the
  approver per CLAUDE.md (`CallTool` from web with
  `source != "user"` does), so net impact varies per endpoint.
  `POST /api/v1/workspace/apply` does **not** route through
  `CallTool`/Approver — it directly applies a diff to the project
  root (`server_workspace.go:54-96`); a CSRF here lets evil.com
  rewrite source files of any DFMC user with `dfmc serve` running.
- **Impact, `auth=token`:** Same SOP-allows-send rules, but the
  attacker page cannot read the bearer token; preflight is required
  whenever the request has `Content-Type: application/json` (forces
  OPTIONS, which the server does not handle → fails closed). However,
  see CORS-002 for the missing OPTIONS handler — current behavior
  is the request is rejected, which is incidentally CSRF-safe but
  brittle.
- **Remediation:** Add an Origin/Referer allowlist
  (`http://127.0.0.1:7777`, `http://localhost:7777`) on every
  state-changing handler, OR require a custom header
  (`X-DFMC-Csrf: 1`) that simple-form/HTML-element submissions
  cannot set without preflight.

### CSRF-002 — DNS-rebinding bypass of loopback-only bind
- **Severity:** High
- **Confidence:** Medium
- **CWE:** CWE-350 / CWE-352 (DNS-rebinding-enabled CSRF)
- **File:** `ui/web/server.go:131-160` (bind normalization);
  no `Host` header validation in any handler.
- **Detail:** Bind to 127.0.0.1 protects against direct cross-network
  reach but not against a same-browser DNS-rebinding attack.
  `evil.example.com` resolves first to a public IP (passes initial
  page load and any CORS preflight from that origin), then re-binds
  to 127.0.0.1; the browser sees the page origin as
  `http://evil.example.com` but the TCP connection goes to the local
  DFMC. The server accepts arbitrary `Host:` headers
  (`r.Host` is never validated against an allowlist anywhere in
  `ui/web/`). Combined with CSRF-001, evil.example.com can drive
  every DFMC POST endpoint.
- **Impact:** Full RCE-equivalent (`run_command` via
  `POST /api/v1/tools/run_command` with `source="user"` skips
  approval per `engine_tools.go:225` and CLAUDE.md trust-boundary
  notes; the attacker chooses the source value in the JSON body if
  the handler forwards it).
- **Remediation:** Validate `r.Host` against an allowlist of
  `{127.0.0.1:<port>, localhost:<port>}`. Reject on mismatch with
  421 Misdirected Request.

### CSRF-003 — `/ws` SSE event stream subscribable cross-origin
- **Severity:** High
- **Confidence:** High
- **CWE:** CWE-200 / CWE-942 (Cross-origin information leak via SSE
  / overly-permissive cross-domain policy)
- **File:** `ui/web/server_chat.go:116-168` (handleWebSocket — the
  SSE handler at `GET /ws`).
- **Detail:** `EventSource` does **not** issue a CORS preflight and
  is not blocked by SOP for sending. With `auth=none` (default), a
  page on any origin can `new EventSource('http://127.0.0.1:7777/ws')`
  and receive every event the EventBus publishes — including
  `tool:result` payloads (truncated to 32 KiB but still rich),
  `agent:loop:*`, `provider:request/response`, `intent:decision`,
  `drive:*`. These events leak source content the agent has read,
  shell command outputs, prompts, model names, and (through tool
  result truncation) potentially API responses with sensitive data.
- **Impact:** Cross-origin reconnaissance and continuous data
  exfiltration. The EventSource stays open across reloads of the
  attacker page.
- **Remediation:** Origin allowlist on `/ws`; reject empty/non-
  loopback Origin headers when bind is loopback-only.

### CSRF-004 — `POST /api/v1/workspace/apply` with `source: "latest"` allows file writes via CSRF
- **Severity:** High
- **Confidence:** High
- **CWE:** CWE-352 (CSRF)
- **File:** `ui/web/server_workspace.go:54-96`.
- **Detail:** This handler bypasses `engine.CallTool` and the approval
  gate entirely. It applies a unified-diff patch to the project root
  through `applyUnifiedDiffWeb`. With `auth=none` and loopback-only
  bind, a malicious page in the user's browser can POST a crafted
  diff and silently overwrite project files. Same reasoning as
  CSRF-001 but more concrete because there is **no second gate**.
- **Remediation:** Either (a) route through `CallTool` so the
  approver fires, or (b) require an Origin allowlist + custom header.

### CSRF-005 — `POST /api/v1/drive` triggers full autonomous agent run via CSRF
- **Severity:** Medium
- **Confidence:** High
- **CWE:** CWE-352
- **File:** `ui/web/server_drive.go:55+`, registered at
  `server.go:235`.
- **Detail:** A cross-origin POST kicks off a Drive run — planner
  call + N parallel sub-agents with full tool access. Approval gate
  fires per-tool, but the deny-by-default web approver
  (`DFMC_APPROVE` env) is per-process; if the operator set
  `DFMC_APPROVE=yes`, all gated calls execute without prompt.
- **Impact:** Resource exhaustion (LLM tokens billed to the user's
  API key), unwanted code mutations, web fetches from attacker-
  controllable URLs (the task description is the LLM seed).
- **Remediation:** Same as CSRF-001 — Origin allowlist + custom
  header.

### CSRF-006 — Health endpoint leaks server presence cross-origin
- **Severity:** Low
- **Confidence:** High
- **CWE:** CWE-200
- **File:** `ui/web/server.go:259-261`, exempt from auth at
  `server.go:405-408`.
- **Detail:** `GET /healthz` always returns 200 unauthenticated and
  has no Origin check. Any page can probe whether DFMC is running on
  the user's machine, fingerprinting the install before launching
  the rebinding/CSRF attack.
- **Remediation:** Keep `/healthz` unauthenticated (it has uptime
  use cases) but consider Origin allowlist or rate-limited probes.

### CSRF-007 — `GET /api/v1/files/{path...}` returns project content cross-origin (read-amplified CSRF)
- **Severity:** Medium
- **Confidence:** Medium
- **CWE:** CWE-200 / CWE-352
- **File:** `ui/web/server.go:230` route +
  `ui/web/server_files.go` (per architecture report).
- **Detail:** GET requests are SOP-readable only with CORS, but
  with **no `Access-Control-Allow-Origin` header set** (verified
  cross-grep), simple GETs from another origin will succeed at the
  network level — the browser blocks the read in JS but the bytes
  still hit the server, and side-channels (image-load timing,
  cache probing) can leak existence/size. With DNS rebinding the
  origin matches and reads succeed entirely.
- **Impact:** Combined with CSRF-002, full project file
  exfiltration.
- **Remediation:** Origin allowlist; for the rebinding case, a
  `Host:` header allowlist is the durable fix.

### CSRF-008 — `/api/v1/ws` WebSocket accepts any Origin (combines with CSRF)
- **Severity:** Medium
- **Confidence:** High
- **CWE:** CWE-1385 (Origin Validation Error)
- **File:** `ui/web/server_ws.go:32-35`.
- **Detail:** `CheckOrigin: return true` lets any page WebSocket-
  upgrade. Combined with `auth=none` default, a cross-origin page
  can open a WS connection and call methods — `chat`, `ask`, `tool`
  (`server_ws.go:132-153`), `drive.start`. The `tool` method routes
  through `CallTool` which fires the approver, but `chat`/`ask`
  drive a full agent loop and produce tool calls the agent decides
  on. Detailed in sc-websocket-results.md as WS-001.
- **Remediation:** Set `CheckOrigin` to validate against a loopback-
  origin allowlist; with `auth=token`, this is moot for tokens-not-
  in-cookies, but the default `auth=none` makes it exploitable.

---

## Notable Finding

**CSRF-004 is the most concrete write-side CSRF**: a malicious page in the
victim's browser can POST a unified diff to
`POST /api/v1/workspace/apply` and the handler applies it directly to
the project root, **bypassing the approval gate entirely** (it does
not route through `CallTool`). With the default `auth=none` + loopback-
only bind, only an attacker page running in the same browser as the
DFMC operator can reach it — but DNS rebinding (CSRF-002) closes that
gap from any internet origin. No CSRF token, no Origin check, no
custom-header requirement guards this endpoint.
