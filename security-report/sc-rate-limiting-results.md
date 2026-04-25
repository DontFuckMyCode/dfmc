# sc-rate-limiting — Rate Limiting & Application-Level DoS Results

**Target:** DFMC HTTP+WS surface (`dfmc serve`, default `127.0.0.1:7777`),
`/api/v1/chat` SSE, `/api/v1/ws` JSON-RPC, `/api/v1/drive*`,
`/api/v1/tasks*` pagination, LLM provider router, Drive runner.
**Skill:** sc-rate-limiting
**Counts:** Critical: 0 | High: 2 | Medium: 3 | Low: 2 | Info: 2 | Total: 9

---

## Summary

DFMC has solid baseline rate limiting on the HTTP surface — every
request goes through a per-IP token-bucket limiter (30 rps, burst
60) at `ui/web/server.go:277` and a 4 MiB body cap via
`MaxBytesReader` at `server.go:314-326`. RE2 regex engine
(Go stdlib) is ReDoS-immune by construction — every
`regexp.MustCompile` call across the 32 files using regex is safe.

The residual exposure is concentrated in two places:

1. **WebSocket post-upgrade has zero rate limiting.** The HTTP
   limiter fires once on the `Upgrade` request, but after that
   the JSON-RPC `readLoop` at `server_ws.go:108-122` is a tight
   `for { ReadMessage(); handleMessage() }` with no per-message
   budget, no `SetReadLimit`, no read deadline, and no idle
   reaper. A single connection can spam unlimited
   `chat`/`ask`/`tool` calls — each spawns a real LLM request and
   chews provider quota / spawns Drive runs.
2. **Pagination has no `limit` cap.** `/api/v1/tasks?limit=N`
   accepts any positive integer (`server_task.go:63-66`); on a
   large bbolt task store this trivially exhausts memory and
   response size.

Defense-in-depth via `agent.max_tool_steps`,
`max_tool_tokens`, `parallel_batch_size`, `meta_call_budget`
(per-turn caps in `internal/config/defaults.go`) and Drive's
`MaxParallel` / `MaxFailedTodos` / `MaxWallTime` (per-run caps in
`internal/drive/driver.go`) limit blast radius **once a single
request is in flight** but do not bound how many concurrent
requests a remote client can launch.

`golang.org/x/time/rate` is used in exactly one place
(`ui/web/server.go:31,277,338,347,353-355`) for the per-IP HTTP
limiter. It is NOT applied to internal LLM calls — the provider
router's only throttle is a *response*-side `ThrottledError`
parser (`internal/provider/throttle.go:71-83`) that handles
upstream 429s. There is no client-side rate limit on outbound
provider calls beyond the agent loop's `max_tool_tokens` per
turn, which means a flood on `/api/v1/chat` directly translates
to a flood on the configured LLM API key.

---

## Findings

### Finding RATE-001: WebSocket Has No Per-Connection Or Per-Message Rate Limit

- **Severity:** High
- **Confidence:** 95
- **File:** `ui/web/server_ws.go:108-122`
- **CWE:** CWE-770 (Allocation of Resources Without Limits or
  Throttling) / CWE-799 (Improper Control of Interaction
  Frequency)

**Evidence:**

```go
// ui/web/server_ws.go:108-122
func (c *wsConn) readLoop() {
    defer c.cleanup()
    for {
        _, raw, err := c.conn.ReadMessage()  // no SetReadLimit, no SetReadDeadline
        if err != nil { return }
        var msg wsMessage
        if err := json.Unmarshal(raw, &msg); err != nil {
            c.sendError(0, -32700, "parse error")
            continue
        }
        c.handleMessage(msg)  // dispatches chat/ask/tool/drive.start
    }
}
```

The HTTP per-IP rate limiter at `server.go:277` only protects
the upgrade handshake. After upgrade, the read loop is unbounded
and dispatches `chat`, `ask`, `tool`, `drive.start` synchronously
to `c.engine.Ask(ctx, ...)` (`server_ws.go:212-217`). Each of
those invokes the configured provider with the user's API key.

**Impact:**
- Authenticated WS client (any page in the same browser when
  `auth=none`, or any holder of the bearer token when
  `auth=token`) can drive unbounded LLM-call volume → exhausts
  Anthropic/OpenAI quota and bills the operator.
- Unbounded `drive.start` over WS would queue unbounded Drive
  runs, but the current implementation stubs that to
  `"use POST /api/v1/drive"` (`server_ws.go:268-271`), so this
  vector is mitigated **by accident**, not by design.
- No `conn.SetReadLimit` means a client can send a single 100 MB
  JSON-RPC frame and force the server to allocate it.

**Remediation:**
1. Add `c.conn.SetReadLimit(64*1024)` (or similar) right after
   upgrade in `handleWebSocketUpgrade` so a single frame can't
   exhaust memory.
2. Add a per-connection `rate.Limiter` (e.g. 5 rps, burst 10) on
   `c.handleMessage` — drop or `sendError(-32029, "too many
   requests")` when exceeded.
3. Add `SetReadDeadline` plus a ping/pong handler so idle or
   dead connections are reaped (currently they live until TCP
   keepalive expires, default 2h on most kernels).

---

### Finding RATE-002: `/api/v1/tasks` Pagination Limit Has No Upper Bound

- **Severity:** High
- **Confidence:** 95
- **File:** `ui/web/server_task.go:63-66`
- **CWE:** CWE-770 (Allocation of Resources Without Limits)

**Evidence:**

```go
// ui/web/server_task.go:63-66
if lim := r.URL.Query().Get("limit"); lim != "" {
    if n, err := strconv.Atoi(lim); err == nil && n > 0 {
        opts.Limit = n
    }
}
```

Any positive integer is accepted. `taskstore.ListTasks` then
walks the bbolt bucket up to `Limit` records and serializes the
result to JSON in one allocation. There is no upper cap, and no
default cap is applied when `limit` is omitted (the check is
`!= ""`, so a missing query string leaves `opts.Limit = 0` which
in `taskstore.go` semantics means "no limit").

**Impact:** A single `GET /api/v1/tasks?limit=999999999` (or
no `limit` at all) on a populated task store triggers an
unbounded read + JSON encode pass → server-process OOM. Through
the per-IP limiter the attacker still has 30 such requests/sec.

**Remediation:**
```go
const taskListMax = 500
const taskListDefault = 100
if opts.Limit <= 0 || opts.Limit > taskListMax {
    if opts.Limit > taskListMax {
        opts.Limit = taskListMax
    } else {
        opts.Limit = taskListDefault
    }
}
```

The same pattern (capped page size with safe default) belongs on
every list-style endpoint. The `/api/v1/conversations` and
`/api/v1/conversation/branches` handlers should be audited for
the same gap (out of scope for this skill but flagged below as
RATE-007 informational).

---

### Finding RATE-003: No Concurrency Cap On Concurrent Drive Runs Per Client

- **Severity:** Medium
- **Confidence:** 80
- **File:** `ui/web/server_drive.go:55-121`
- **CWE:** CWE-770

**Evidence:** `handleDriveStart` returns immediately after
persisting the planning stub and launches the driver via
`s.engine.StartBackgroundTask("web.drive.run", ...)`
(`server_drive.go:110-112`). There is no check that an
identifier (IP, user, or session) already has N runs in flight.
Each Drive run then creates planner LLM calls, scheduler
goroutines, and per-TODO subagent invocations.

The per-IP HTTP limiter (30 rps) caps how fast new runs can be
**requested** but not how many can be **in flight** — and the
runs themselves bypass the rate limiter once started because the
driver runs entirely server-side.

**Impact:** A client can launch ~1800 Drive runs/min until the
operator's LLM API key hits provider-side 429s. Per-run caps
(`MaxParallel`=3, `MaxWallTime`, `MaxFailedTodos`) limit each
run's blast radius, but multiplied by run count the total cost
is unbounded.

**Remediation:** Track active Drive runs in `Server`
(or `engine`) keyed by `clientIPKey(r)`; reject new starts above
e.g. 3 concurrent runs/IP with 429. A global cap (e.g. 10
concurrent runs total per process) would also be reasonable —
the bbolt store and event bus aren't designed for thousands of
parallel runs.

---

### Finding RATE-004: SSE `/api/v1/chat` Has No Per-Stream Token Or Wall-Clock Cap

- **Severity:** Medium
- **Confidence:** 75
- **File:** `ui/web/server_chat.go:60-114`
- **CWE:** CWE-770

**Evidence:** `handleChat` calls `clearStreamingWriteDeadline`
(`server_chat.go:66`) which removes the 2-minute write deadline,
then forwards every `provider.StreamDelta` to the client until
the model emits `StreamDone` or the agent loop runs out of
budget. The `StreamAsk` path uses the engine's main-Ask
infrastructure, which IS bounded by `agent.max_tool_steps` and
`agent.max_tool_tokens`, but those caps are configured per
process — not per request.

**Impact:** A long-running stream that the client never closes
holds a goroutine, an SSE response body, and a chunk of the
provider's outbound stream open. Since the stdlib write
deadline is cleared, even a stalled client (slow-loris read)
keeps the connection live.

**Remediation:**
1. Set a hard wall-clock ceiling on `r.Context()` derived from
   `agent.max_stream_seconds` (new config knob).
2. Track active SSE connections per IP and cap (e.g. 4
   simultaneous streams).
3. Consider re-applying a generous write deadline (e.g. 10
   minutes) instead of clearing it entirely — a real user turn
   should never need that long, and slow-loris readers cannot
   then pin connections indefinitely.

---

### Finding RATE-005: Provider Router Has Response-Side Backoff But No Outbound Rate Cap

- **Severity:** Medium
- **Confidence:** 70
- **File:** `internal/provider/throttle.go:1-103`,
  `internal/provider/router.go`
- **CWE:** CWE-770

**Evidence:** The provider layer reacts to upstream 429/503 via
`newThrottledErrorFromResponse` and exponential backoff
(`backoffForAttempt` clamps at 30 s, `clampRetryAfter` clamps
hint at 5 m). There is no client-side rate limit on outbound
calls — the router will dispatch as fast as the agent loop
calls `Providers.Complete`.

The agent loop's `parallel_batch_size: 4` (default in
`internal/config/defaults.go`) caps in-batch tool fan-out, but
multiple concurrent user-level `Ask` calls (parallel SSE
clients, parallel Drive runs, MCP host sessions) each spawn
their own batch budget.

**Impact:** Defense-in-depth gap. Once a flood passes the HTTP
gate (or originates from inside via Drive / MCP), upstream
provider quota burns until the upstream itself returns 429.
This is acceptable as a baseline but worth flagging because the
existing knobs (`max_tool_steps`, `max_tool_tokens`) are
*per-turn* and do not bound aggregate usage.

**Remediation:** Add an optional `agent.global_rate_limit_rps`
config knob backed by a `rate.Limiter` shared across all
providers in the router. Off by default (preserve current
behavior); operators with a strict provider budget can opt in.

---

### Finding RATE-006: No Auth-Endpoint Brute-Force Protection (Bearer Token)

- **Severity:** Low
- **Confidence:** 80
- **File:** `ui/web/server.go:401-419`
- **CWE:** CWE-307 (Improper Restriction of Excessive
  Authentication Attempts)

**Evidence:** `bearerTokenMiddleware` performs a constant-time
compare against the configured token and returns 401 on
mismatch. It does NOT increment a per-IP failure counter or
delay subsequent attempts; the only protection is the global
30 rps per-IP limiter, which gives an attacker 30 token guesses
per second.

For a 32-char hex token (128 bits of entropy) this is utterly
infeasible to brute force, so the practical risk is Low. Worth
flagging because the 30 rps limit is not specific to the auth
path, and an operator who configures a weak custom token (e.g.
8 chars) gets dramatically less protection than they'd expect.

**Remediation (if desired):** Bucket failed auth attempts
separately at e.g. 1 rps after 3 misses; log to event bus.

---

### Finding RATE-007: Other List Endpoints Likely Share The Pagination Gap

- **Severity:** Low
- **Confidence:** 60
- **File:** Multiple — `/api/v1/conversations`,
  `/api/v1/conversation/branches`, `/api/v1/drive`,
  `/api/v1/memory`, `/api/v1/files`
- **CWE:** CWE-770

**Evidence:** RATE-002 documents the pagination gap on
`/api/v1/tasks`. The same skill scope (rate limiting) flags
that the other list-style endpoints in `server_*.go` should be
audited for analogous unbounded reads — `handleDriveList` at
`server_drive.go:126-145` returns every persisted run with no
limit at all (`runs, err := store.List()`).

A bbolt store with 10 000 Drive runs would serialize all of
them in one JSON array per request. Marked Low because Drive
run counts grow slowly in practice; flagged for completeness so
a future reviewer doesn't miss the pattern.

**Remediation:** Add `?limit=` + `?offset=` with a hard cap to
every list endpoint, defaulting to e.g. 200.

---

### Finding RATE-INFO-008: HTTP Surface Defenses Confirmed Working

- **Severity:** Info
- **File:** `ui/web/server.go:122-129, 263-326`

**Evidence (positive):**
- `securityHeaders` middleware applied globally
  (`server.go:275`).
- `rateLimitMiddleware` applied globally before bearer-token
  auth (`server.go:277-278`) → token validation itself is
  rate-limited.
- `limitRequestBodySize` caps every POST/PUT/PATCH/DELETE body
  at 4 MiB (`server.go:316-326`), and `MaxHeaderBytes: 1 << 20`
  caps headers at 1 MiB (`server.go:290`).
- `ReadHeaderTimeout: 5s`, `ReadTimeout: 30s`,
  `WriteTimeout: 2m`, `IdleTimeout: 2m` mitigate slow-loris on
  the HTTP layer (`server.go:286-291`).
- Bind host normalized to `127.0.0.1` when `auth=none`
  (`server.go:152-160`).

These defenses are appropriate for the local-first deployment
model and are NOT findings — recorded so a future reviewer
doesn't propose adding what already exists.

---

### Finding RATE-INFO-009: ReDoS Surface Is Empty (Go RE2 Engine)

- **Severity:** Info
- **File:** All 32 files using `regexp.MustCompile` /
  `regexp.Compile`

**Evidence:** Go's stdlib `regexp` package uses RE2 semantics
(linear-time guarantee, no backtracking, no backreferences, no
lookaround). The engine cannot exhibit catastrophic
backtracking by construction, so user-supplied patterns to
`grep_codebase` (`internal/tools/builtin_grep.go`) and
`find_symbol` cannot trigger ReDoS. Operator-controlled
patterns elsewhere are equally safe.

This is recorded so the skill's ReDoS check has a positive
attestation rather than appearing skipped.

---

## Out of Scope (handled by sibling skills)

- WebSocket origin validation, hijack, and CSRF — already
  documented in `sc-websocket-results.md` (referenced from this
  report's RATE-001 remediation).
- Body-size limit on file-upload endpoints — covered by
  `sc-file-upload-results.md`.
- Provider-side rate-limit response handling correctness —
  covered by `sc-business-logic-results.md` if behavioral, or
  here under RATE-005 if structural.

---

## References
- https://cwe.mitre.org/data/definitions/770.html (Allocation
  Without Limits)
- https://cwe.mitre.org/data/definitions/799.html (Improper
  Control of Interaction Frequency)
- https://github.com/google/re2/wiki/Syntax (RE2 ReDoS
  immunity, used by Go's `regexp`)
- https://pkg.go.dev/golang.org/x/time/rate (token-bucket
  primitive used at `server.go:331-355`)
