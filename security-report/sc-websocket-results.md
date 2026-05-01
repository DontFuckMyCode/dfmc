# sc-websocket Results

No issues found by sc-websocket.

## Surfaces audited

- `GET /api/v1/ws` — bidirectional JSON-RPC 2.0 over `gorilla/websocket`
  (`ui/web/server_ws.go`).
- `GET /ws` — SSE event stream (`ui/web/server_chat.go:117-169`)
  — strictly speaking not WebSocket, but included because it's the
  parallel upgrade-class endpoint that subscribes to the EventBus.
- `dfmc remote start` — same WS surface, port 7779.

## Hardening present (every relevant CWE addressed)

| Control | Constant / Code | Closes |
|---|---|---|
| Origin allowlist on upgrade | `s.checkWebSocketOrigin` plumbed into `wsUpgraderFor` (`server_ws.go:144-150`); `*` wildcard explicitly rejected (`server.go:251-258`) | Cross-Site WebSocket Hijacking (CSWSH) |
| Native-client passthrough | Empty `Origin` accepted (`server.go:240-244`) for `wscat` / IDE plugins | Operational, not a bypass — browsers always send Origin |
| Bearer-token gate at upgrade | `bearerTokenMiddleware` wraps the mux, including `/api/v1/ws` (`server.go:390-393`) | Unauthenticated-user upgrade |
| Per-frame read limit | `wsReadLimit = 64 KiB`, `conn.SetReadLimit` (`server_ws.go:48, 233`) | OOM via 100 MB frames |
| Read deadline + ping/pong | `wsReadDeadline = 60s`, `wsPingInterval = 30s`, `SetPongHandler` slides deadline (`server_ws.go:53-58, 253-256, 470-490`) | Half-open / dead-peer goroutine leaks |
| Per-connection inbound rate | `wsRPS = 5`, `wsBurst = 10` via `rate.Limiter` (`server_ws.go:66-67, 245, 270-272`) | Provider-quota burn from one greedy conn |
| Global + per-IP connection cap | `wsGlobalConnCap = 64`, `wsPerIPConnCap = 8` via `wsConnLimiter.Acquire` (`server_ws.go:41-42, 99-134, 210-217`) | Goroutine + buffer exhaustion |
| Cancel-on-disconnect | `connCtx`/`connCancel` parented at upgrade, derived for each per-message handler; cleanup cancels (`server_ws.go:163-165, 238-247, 488, 497-513`) | Continuing to bill provider after client closed (VULN-023) |
| Once-only cleanup | `closeOnce` (`server_ws.go:172-173, 497-513`) | Double-close panic (VULN-022) |
| Write-loop panic recovery | `defer recover()` in `writeLoop` (`server_ws.go:463-469`) | Goroutine death → readLoop hang |
| Write deadline per send | `wsSendTimeout = 5s`, `SetWriteDeadline` before `WriteJSON`/`WriteMessage` (`server_ws.go:203, 483, 528`) | Slow-loris reader pinning writer |
| JSON-RPC envelope validation | Method required, empty rejected (`server_ws.go:289-291`); unknown method → -32601 | Spec-shaped abuse |
| Tool calls go through lifecycle gate | `engine.CallToolFromSource(ctx, name, params, engine.SourceWS)` (`server_ws.go:422`) | Web→tool privilege escalation (`sc-privilege-escalation-results.md`) |

## Verifications

1. **CSWSH:** `Origin` set to `https://evil.example` returns 403 from
   gorilla because `CheckOrigin` returns false. Pinned by
   `ui/web/server_ws_hardening_test.go` and `server_origin_test.go`.
2. **Frame-size:** `conn.SetReadLimit(wsReadLimit)` runs before the
   first `ReadMessage` (`server_ws.go:233`). A 65 KiB+ frame errors
   the next read and the connection cleans up via `cleanup()`.
3. **Deadline:** `wsReadDeadline = 60s` is applied immediately
   post-upgrade (`server_ws.go:253`); the writer's `pingTicker` keeps
   the peer alive on a 30s cadence, so a healthy peer never trips the
   deadline.
4. **Per-conn rate:** `c.limiter.Wait(c.connCtx)` blocks the read
   loop; once the conn closes, `connCancel` releases the wait and
   the goroutine returns — no deadlock on close.
5. **Conn cap release:** `wsConnLimiter.Acquire` returns a release
   closure; `cleanup()` calls it last, after `connCancel` and
   `conn.Close`, so a counter underflow is impossible.
6. **Cancel-on-disconnect:** all three handlers (`handleChat`,
   `handleAsk`, `handleTool`) take `ctx context.Context` from
   `c.connCtx`; verified each passes it to `engine.Ask` /
   `engine.CallToolFromSource`, so a closed connection cancels
   in-flight provider calls.
7. **No JSON-RPC ID injection:** `wsResponse.ID` is `int64`; an
   attacker cannot smuggle an arbitrary JSON shape into the response
   ID slot.

## Adjacent observations (no findings)

- `handleEventsSubscribe` and `handleEventsUnsubscribe`
  (`server_ws.go:442-453`) are stubs that just echo the request
  type. Real subscription routing lives in `handleChat`'s streaming
  path. The stubs are inert.
- `handleDriveStart`/`Stop`/`Status` over WS are intentionally
  redirected ("use POST /api/v1/drive ...") — `server_ws.go:430-440`.
  No state mutation through WS for drive runs.

## Why this is "no issues, not just N/A"

Every WebSocket failure mode catalogued by OWASP and the CWE
WebSocket subset (CWE-1023, CWE-1287, CWE-770, CWE-664) maps to a
control above. Pinning lives in
`ui/web/server_ws_hardening_test.go`, `server_origin_test.go`, and
`server_sse_slowloris_test.go`. The VULN-019..023 batch was closed
with the current shape and tests run on every build.
