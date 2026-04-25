# sc-websocket — WebSocket Security Results

**Target:** DFMC `/api/v1/ws` (Gorilla WS upgrade) +
`/ws` (SSE event stream — included since both are upgrade-class
endpoints) + `dfmc remote start` (same WS surface on 7779)
**Skill:** sc-websocket
**Counts:** Critical: 1 | High: 3 | Medium: 4 | Low: 2 | Info: 1 | Total: 11

---

## Summary

The WebSocket upgrader at `ui/web/server_ws.go:29-35` is the
single largest residual exposure in DFMC's network surface. It
disables origin validation
(`CheckOrigin: func(r *http.Request) bool { return true }`) and
exposes an RPC surface — `chat`, `ask`, `tool`, `drive.start`,
`drive.stop`, `drive.status`, `events.subscribe` — that
transitively reaches every backend tool the LLM has access to.

The bearer-token middleware does protect WS upgrades when
`auth=token` (the middleware wraps `mux` before WS routes are
hit, verified at `server.go:279-281`), so the cross-origin
hijack reduces to "page on any origin can call the WS *if* it
has the bearer token". With `auth=none` (default, loopback bind)
there is no token to gate the upgrade, and CSRF-style cross-
origin connect from a malicious page in the same browser
succeeds.

The other practical concerns:

- **No SetReadLimit / no max message size** — a malicious peer can
  buffer arbitrary message sizes; verified by grep
  `SetReadLimit|SetReadDeadline|SetPingHandler` → 0 hits in
  `ui/web/`.
- **No ping/pong heartbeat configured server-side**; idle
  connections rely solely on stdlib server `IdleTimeout` (2 min)
  + `WriteTimeout` (cleared for streams). Half-open connections
  pile up.
- **No per-IP connection cap** for WS upgrades; the per-IP rate
  limiter (`server.go:357-367`) governs HTTP requests, but a
  successful upgrade is one request and then long-lived.
- **gRPC remote (port 7778) is not actually started**
  (`cli_remote_start.go:74` "grpc": "not_started" /
  ":78` "gRPC port (reserved)"). Only the WS+HTTP listener on
  7779 (default `ws-port`) runs. Same WS handler, same
  `CheckOrigin: true`.

---

## Findings

### WS-001 — `CheckOrigin: return true` permits cross-origin WebSocket hijack
- **Severity:** Critical (when `auth=none`); High (when `auth=token`)
- **Confidence:** High
- **CWE:** CWE-1385 (Origin Validation Error) / CWE-346 (Origin
  Validation Bypass)
- **File:** `ui/web/server_ws.go:29-35`.
- **Detail:**
  ```go
  var wsUpgrader = websocket.Upgrader{
      ReadBufferSize:  4096,
      WriteBufferSize: 4096,
      CheckOrigin: func(r *http.Request) bool {
          return true // configured by caller via Server
      },
  }
  ```
  Gorilla's default `CheckOrigin` validates same-origin; this
  override returns true unconditionally. Effect: any web page
  in the user's browser can `new WebSocket('ws://127.0.0.1:7777/api/v1/ws')`
  from any origin and hold a long-lived bidirectional channel.
- **Impact, `auth=none` (default):** The attacker page can call
  `chat`, `ask`, `tool`, `drive.start`. The `tool` method routes
  through `engine.CallTool` which fires the approval gate (web
  approver, deny-by-default), so direct tool invocation is gated
  — but `chat`/`ask` drive the agent loop which produces tool
  calls **without** the user's interactive approval (the agent
  loop's tool calls run with `source != "user"` → approver fires;
  deny-by-default means they fail closed unless `DFMC_APPROVE=yes`
  is set). When `DFMC_APPROVE=yes`, full RCE (run_command),
  arbitrary file write, drive runs.
- **Impact, `auth=token`:** The attacker page would need the
  bearer token to upgrade. Tokens travel in
  `Authorization: Bearer …`, which **WebSocket cannot set from JS
  via the standard WebSocket constructor** — there is no
  `headers` parameter. A cross-origin attacker therefore cannot
  authenticate the upgrade. (The exception: if the operator ever
  exposes the token via a same-origin endpoint readable by the
  attacker, e.g. the workbench HTML embedded a `window.TOKEN = "..."`
  global — verified absent in `index.html`.)
- **Impact, DNS-rebinding:** Combined with the absent Host-header
  check (see WS-005), the rebinding scenario lets the page's
  origin match `evil.example.com:7777` and the WS connect
  succeeds **regardless of bearer token presence**, since the
  attacker controls the page and can include any header — this
  needs `fetch`-based WS bootstrap not standard WebSocket.
- **Remediation:** Replace `CheckOrigin` with a strict allowlist
  of `http://127.0.0.1:7777`, `http://localhost:7777`,
  `http://127.0.0.1:7779` (and remote variants when applicable).

### WS-002 — No `SetReadLimit` — single message can exhaust memory
- **Severity:** High
- **Confidence:** High
- **CWE:** CWE-770 (Allocation of Resources Without Limits)
- **File:** `ui/web/server_ws.go:108-122` (`readLoop`).
- **Detail:** `c.conn.ReadMessage()` is called without
  `c.conn.SetReadLimit(...)`. Gorilla's default is unlimited.
  A peer can send a single multi-GiB frame; the entire payload
  is buffered before `ReadMessage` returns, OOMing the server.
  The 4 MiB HTTP body cap (`server.go:314-326`) does not apply
  to WS frames after upgrade.
- **Remediation:** `c.conn.SetReadLimit(4 * 1024 * 1024)` (or
  smaller — JSON-RPC payloads should be much smaller) right
  after the upgrade succeeds.

### WS-003 — No ping/pong heartbeat; half-open WS leaks connection slots
- **Severity:** Medium
- **Confidence:** High
- **CWE:** CWE-400 (Resource Exhaustion)
- **File:** `ui/web/server_ws.go:92-106` (no
  `SetPingHandler`/`SetPongHandler`/timer).
- **Detail:** The server never sends WS pings and does not
  install a pong-deadline reader. The stdlib `IdleTimeout: 2m`
  on the underlying connection is bypassed once the connection
  is hijacked by the WS upgrader (Gorilla takes ownership). A
  malicious or buggy client that opens connections then stops
  reading/writing will hold them indefinitely (until the OS
  TCP keepalive eventually times out, often 2+ hours).
- **Impact:** Slow-resource exhaustion on shared hosts; pre-
  fork attacker can exhaust file descriptors.
- **Remediation:** `SetReadDeadline(now + 60s)` on every
  `ReadMessage`; install a `SetPongHandler` that extends the
  deadline; have `writeLoop` send a ping every 30s.

### WS-004 — No per-IP connection cap on WS upgrades
- **Severity:** Medium
- **Confidence:** High
- **CWE:** CWE-770
- **File:** `ui/web/server.go:357-367` (rate limiter is per-
  HTTP-request); WS is one request that becomes a long-lived
  session.
- **Detail:** The per-IP rate limiter governs the upgrade
  request (1 req of 30/sec quota), but not the resulting
  long-lived session. An attacker can open up to 30/sec
  upgrades without being rate-limited; with no concurrent-
  connection cap, they accumulate.
- **Remediation:** Track active WS sessions per IP; refuse
  upgrade above N (e.g. 10) per IP.

### WS-005 — No `Host:` header validation on WS upgrade
- **Severity:** High
- **Confidence:** High
- **CWE:** CWE-350 (DNS-rebinding-class)
- **File:** `ui/web/server_ws.go:92-106` —
  `wsUpgrader.Upgrade(w, r, nil)` does not inspect `r.Host`.
- **Detail:** Combined with the loopback-only bind, the only
  way for a non-localhost origin to reach this endpoint is
  DNS rebinding. With no Host check, a rebinding attacker
  whose hostname now resolves to 127.0.0.1 succeeds. With
  WS-001 (any-origin), the upgrade is unconditional once
  network reach is achieved.
- **Remediation:** Validate `r.Host` against
  `{127.0.0.1:<port>, localhost:<port>}`; reject otherwise.

### WS-006 — Inbound message validation is minimal
- **Severity:** Medium
- **Confidence:** High
- **CWE:** CWE-20 (Improper Input Validation)
- **File:** `ui/web/server_ws.go:108-154` (handlers).
- **Detail:** `readLoop` does:
  ```go
  _, raw, err := c.conn.ReadMessage()
  if err != nil { return }
  var msg wsMessage
  if err := json.Unmarshal(raw, &msg); err != nil { ... }
  c.handleMessage(msg)
  ```
  - No frame-type check (`messageType` is discarded — binary
    frames are happily JSON-unmarshalled and may panic or
    produce odd errors).
  - No message-rate limit (an attacker can spam
    `chat`/`ask`/`tool` calls).
  - `msg.Method` is matched against a switch but `msg.Params`
    is forwarded as raw JSON to handler-specific
    `json.Unmarshal` with no shape validation.
  - `tool` handler (`server_ws.go:244-266`) accepts any tool
    name and any params map — same shape as the HTTP
    `handleToolExec`.
- **Remediation:** Per-connection method rate limit; reject
  binary frames; require non-zero message ID for non-event
  methods.

### WS-007 — Bearer token rides in URL when WS clients can't set Authorization header
- **Severity:** Medium
- **Confidence:** Medium
- **CWE:** CWE-598 (Information Exposure Through Query Strings)
- **File:** `ui/web/server.go:401-419`
  (`bearerTokenMiddleware` looks only at the `Authorization`
   header).
- **Detail:** Browsers' built-in `WebSocket` constructor cannot
  set custom headers on the upgrade. Practical clients
  (`wscat`, IDE-host code) commonly fall back to passing the
  token in the query string, e.g.
  `ws://127.0.0.1:7777/api/v1/ws?token=…`. The current middleware
  rejects such requests because the Authorization header is
  empty — but if a future commit relaxes that to also accept
  `?token=…` (a common pattern), the token will:
  - leak into web server access logs,
  - leak into shell history if curl/wscat is used,
  - leak into HTTP referrer headers if the URL is ever rendered
    in a browser tab title.
  Today the code is OK (header-only); flagging as guidance.
- **Remediation:** If WS-from-browser is ever supported, use
  the Sec-WebSocket-Protocol subprotocol channel for the
  token (`new WebSocket(url, ['bearer.<token>'])`) which is
  not logged in standard access logs and not visible in
  history.

### WS-008 — `events.subscribe` over WS does not actually filter; trivially leaks all events
- **Severity:** Low
- **Confidence:** High
- **CWE:** CWE-200
- **File:** `ui/web/server_ws.go:281-293`.
- **Detail:** `handleEventsSubscribe` only echoes
  `{subscribed: req.Type}` and never registers a real
  subscription. The full event stream is delivered only through
  the `chat` (stream=true) path, but if a future change wires
  `events.subscribe` to `EventBus.SubscribeFunc("*",...)`
  without sanitization, the same data leak as CSRF-003 (SSE
  subscribe-from-anywhere) will land on WS without the SSE's
  one-shot bearer middleware caveats.
- **Remediation:** When wiring real subscriptions, scope to
  the `req.Type` and apply the same Origin/Host gates as the
  upgrade.

### WS-009 — `dfmc remote start` reuses the same WS upgrader on a network-reachable port
- **Severity:** High
- **Confidence:** High
- **CWE:** CWE-1385 + CWE-668 (Exposure of Resource to Wrong
  Sphere)
- **File:** `ui/cli/cli_remote_start.go:58-66`; verified that
  `web.New()` is reused, so `wsUpgrader` is shared.
- **Detail:** `dfmc remote start` is meant to expose the
  engine to an authenticated client (default port 7779).
  `--auth=token` is required if `--host` is non-loopback
  (refuses otherwise unless `--insecure`). With `auth=token`,
  WS-001 reduces to "any cross-origin page in any browser
  cannot supply the token" → safe. With `--insecure --auth=none
  --host 0.0.0.0`, **anyone on the network can WS-upgrade
  with no origin check, no Host check, no auth** and call
  `tool` / `chat` / `drive.start`.
- **Impact:** Full engine takeover from the network when the
  insecure flag is used.
- **Remediation:** Even under `--insecure`, retain the strict
  `CheckOrigin` allowlist (or an explicit "I really mean it"
  flag like `--allow-any-origin`).

### WS-010 — `wsConn.cleanup` race + double-close on `sendCh`
- **Severity:** Low
- **Confidence:** Medium
- **CWE:** CWE-672 (Operation on a Resource after Expiration or
  Release) / CWE-362
- **File:** `ui/web/server_ws.go:295-312`.
- **Detail:** `cleanup()` calls `close(c.sendCh)` after
  setting `c.closed = true`. `sendWS` and `send` write to
  `sendCh` — `send` (line 78) does so before checking
  `c.closed.Load()` again, with a 5s timeout. If `cleanup`
  closes the channel between `c.closed.Load()` returning
  false and the `sendCh <- …` line, the goroutine panics
  ("send on closed channel"). Not security-critical — DoS
  via crash of the per-connection goroutine, recovered by the
  process — but worth flagging.
- **Remediation:** Use a done-channel pattern instead of
  closing the buffered send channel; guard sends with a
  select on done.

### WS-011 — gRPC port advertised but not actually started (informational)
- **Severity:** Info
- **Confidence:** High
- **CWE:** N/A
- **File:** `ui/cli/cli_remote_start.go:73-83` (`"grpc":
  "not_started"`, `"gRPC port (reserved)"`).
- **Detail:** Architecture report and CLI both reference port
  7778 as gRPC. No gRPC server is registered; only the WS+HTTP
  listener on 7779 (`ws-port`). The 7778 port is not bound.
  Recorded so reviewers don't chase a phantom listener.

---

## Notable Finding

**WS-001 + WS-005 together** are the load-bearing residual risk.
`CheckOrigin: return true` is a single line that makes the WS
upgrade origin-agnostic; combined with no Host-header validation,
DNS rebinding turns "loopback-only bind" into "any internet
origin reaches this WS in the user's browser." With `auth=none`
(the default), the cross-origin attacker page can immediately
call `tool` via WS — the deny-by-default web approver is the
only thing standing between them and `run_command`. The
mitigation Phase 1 noted ("loopback-only bind") is **defeated by
DNS rebinding**, which the code does not defend against
anywhere; the documented mitigation does not cover the residual
risk on shared/multi-user hosts (where another user's browser
can connect to your loopback) or any browser-DNS-rebinding
scenario.
