# sc-lang-go: Go-Specific Security Findings

DFMC (`github.com/dontfuckmycode/dfmc`, Go 1.25). Phase 2 deep scan focused
on concurrency hazards, context propagation, error handling, net/http
hardening, TLS, file-path handling, and bbolt usage. All file:line refs
verified against the working tree at scan time.

## Summary by severity

| Severity | Count |
|---|---|
| Critical | 0 |
| High     | 4 |
| Medium   | 8 |
| Low      | 6 |
| Info     | 3 |

Headline findings:

- **LANG-GO-001 (High)** — HTTP/WS tool-exec endpoints invoke `engine.CallTool`
  with `source="user"`, which is the explicit short-circuit that skips the
  approval gate in `executeToolWithLifecycle`. Anyone reaching the
  authenticated tool endpoint can run `run_command`, `write_file`,
  `apply_patch`, `git_commit`, etc. without ever triggering the operator's
  approval prompt.
- **LANG-GO-002 (High)** — `websocket.Upgrader.CheckOrigin` returns `true`
  unconditionally; combined with LANG-GO-001 the WS endpoint is a JSON-RPC
  surface for arbitrary tool calls.
- **LANG-GO-003 (High)** — `clientIPKey` blindly trusts `X-Forwarded-For`
  with no proxy-origin check, so the per-IP rate limiter is trivially
  bypassed by any remote client.
- **LANG-GO-004 (High)** — MCP client spawns servers with
  `exec.Command` (no context), inherits the entire parent environment
  (API keys included), no working-dir hardening, and `sendSync` leaks a
  goroutine on every cancelled call.

There are NO findings for `unsafe.Pointer` use, `tls.InsecureSkipVerify`,
or `math/rand`-for-tokens — those categories audited clean.

---

## LANG-GO-001 — HTTP and WebSocket tool dispatch bypasses approval gate

- **Severity**: High
- **Confidence**: 95
- **CWE**: CWE-862 (Missing Authorization)

### Where

- `internal/engine/engine_tools.go:120` — `CallTool` calls
  `executeToolWithLifecycle(..., "user")`.
- `internal/engine/engine_tools.go:225` — guard:
  `if source != "user" && e.requiresApproval(name) { ... }`.
- `ui/web/server_tools_skills.go:167` — `handleToolExec` calls
  `s.engine.CallTool(r.Context(), name, req.Params)`.
- `ui/web/server_ws.go:260` — `(c *wsConn).handleTool` calls
  `c.engine.CallTool(ctx, req.Name, req.Params)`.

### Description

Both the HTTP `POST /api/v1/tools/{name}` route and the WebSocket
`tool` JSON-RPC method funnel through `engine.CallTool`, which
unconditionally tags the source as `"user"`. The approval gate in
`executeToolWithLifecycle` is explicitly skipped for that source — the
comment at `engine_tools.go:152-154` calls this out as intended for
typed-by-the-human-at-the-keyboard CLI/TUI use. The web layer is not
"the human at the keyboard" but it inherits the same trust label.

Once an attacker passes the bearer-token check (or the server is run
in `auth=none` mode against the loopback bind), they can invoke any
registered tool — `run_command`, `write_file`, `apply_patch`,
`git_commit`, `web_fetch` — without the operator ever seeing an
approval prompt, regardless of how `tools.require_approval` is
configured. The operator's mental model ("I configured approvals;
nothing destructive runs without my consent") is wrong for the web
surface.

### Impact

Authenticated remote code execution scoped to whatever
`run_command`'s block list misses, plus arbitrary file write, patch
apply, git operations, and outbound HTTP. The block list in
`internal/tools/command.go` blocks shutdown/sudo/rm/format — but `go
run`, `python -e`-via-script-file, package-manager scripts, and any
user-installed binary are all reachable. The approval gate is the
operator's last line of defense and it is structurally bypassed for
every web client.

### Suggested fix

Either:

1. Tag HTTP/WS-initiated calls as a third source (`"web"` /
   `"remote"`) and treat them like agent calls for approval purposes;
   the existing `webApprover` already exists for exactly this — wire
   it back into the path. OR
2. Make `CallTool` accept a `source` parameter and have the web
   handlers pass `"web"` rather than relying on the implicit `"user"`.

The CLAUDE.md and architecture map both describe approvals as
mandatory for non-direct-typed surfaces; the code should match that
contract.

---

## LANG-GO-002 — WebSocket upgrader accepts every Origin

- **Severity**: High
- **Confidence**: 80
- **CWE**: CWE-942 (Permissive Cross-domain Policy with Untrusted Domains)

### Where

- `ui/web/server_ws.go:29-35`:
  ```go
  var wsUpgrader = websocket.Upgrader{
      ReadBufferSize:  4096,
      WriteBufferSize: 4096,
      CheckOrigin: func(r *http.Request) bool {
          return true // configured by caller via Server
      },
  }
  ```

### Description

`gorilla/websocket` calls `CheckOrigin` to defend against
cross-origin WebSocket hijacking; the documented default rejects
mismatched Origin headers. DFMC overrides it to always return `true`.
The comment "configured by caller via Server" is aspirational — no
caller actually overrides it. The architecture map at
`security-report/architecture.md:417` correctly flags this as the
"lone CORS-relevant gap."

### Impact

When the server runs with `auth=none` AND a non-loopback bind, a
malicious page in any browser can open a WS connection to the DFMC
server and drive `tool` JSON-RPC calls (see LANG-GO-001), as long as
the user's browser can reach the server. The bind-host normalization
in `server.go:152-160` rewrites `auth=none` non-loopback to 127.0.0.1
so this combo is *generally* unreachable in practice, but:

- Nothing prevents an operator from running `auth=token` on a
  non-loopback bind (the warning at `server.go:157` is informational
  only). With `auth=token`, the WS endpoint is still bearer-gated,
  but if the token rides in a query string in any future iteration,
  or in a cookie added later, a CSRF-style WS-hijack becomes
  exploitable.
- Even with auth=token and Authorization-header-only tokens, every
  non-trivial extension (operator running a local proxy that forwards
  the token, dev who pastes a curl command with `--header` into a
  browser-driven tool) re-opens the surface.

Confidence is 80 not 95 because the immediate exploit path requires
either auth=none (refused by `runServe` without `--insecure`) or a
future change to how tokens are carried.

### Suggested fix

Validate the Origin header against the server's expected host(s).
At minimum, reject Origins whose host doesn't match the listener's
addr; let the caller register an allow-list via a
`Server.SetAllowedOrigins([]string)` setter. `subtle.ConstantTimeCompare`
is overkill — a plain `==` is fine for origin matching since origins
are public.

---

## LANG-GO-003 — Per-IP rate limit is trivially bypassed via X-Forwarded-For

- **Severity**: High
- **Confidence**: 95
- **CWE**: CWE-770 (Allocation of Resources Without Limits or Throttling),
  CWE-348 (Use of Less Trusted Source)

### Where

- `ui/web/server.go:374-390` — `clientIPKey`:
  ```go
  if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwarded != "" {
      for _, part := range strings.Split(forwarded, ",") {
          if ip := strings.TrimSpace(part); ip != "" {
              return ip
          }
      }
  }
  ```

### Description

The comment at `server.go:370-373` argues that "remote clients cannot
spoof this header because they cannot establish a connection through
the proxy without first passing the bearer-token auth gate." That
argument is wrong:

1. The rate-limit middleware sits BEFORE the bearer-token middleware
   in the handler chain (`server.go:277-282` — `rateLimitMiddleware`
   is wrapped first, then `bearerTokenMiddleware`, so requests hit
   the rate limiter before auth). Unauthenticated requests are
   rate-limited.
2. There is no proxy-origin check. ANY client — authenticated or
   not, on any network path — can send `X-Forwarded-For: 1.1.1.1`
   and shift their bucket every request, defeating the rate limiter
   entirely.

A brute-force attacker against the bearer token (constant-time
compare prevents timing attacks but not online guessing) can rotate
synthetic XFF values to keep their throttle bucket fresh.

### Impact

Rate limit bypass on every endpoint, including the unauthenticated
`/healthz` and `GET /` HTML serve. Enables online password/token
guessing at unbounded RPS, and resource-exhaustion against expensive
endpoints like `POST /api/v1/analyze` or `GET /api/v1/scan`.

### Suggested fix

Only honor `X-Forwarded-For` when the immediate `r.RemoteAddr` is in
a configured trusted-proxy list (loopback by default; environment-
configurable for ops who front DFMC with nginx/caddy). Otherwise use
the connection peer IP. Document the policy in the config.

---

## LANG-GO-004 — MCP client subprocess hardening gaps

- **Severity**: High
- **Confidence**: 85
- **CWE**: CWE-200 (Information Exposure), CWE-404 (Improper Resource Shutdown),
  CWE-668 (Exposure of Resource to Wrong Sphere)

### Where

- `internal/mcp/client.go:36` — `cmd := exec.Command(command, args...)` —
  not `CommandContext`.
- `internal/mcp/client.go:38-43` — env merge:
  ```go
  envVars := make([]string, len(os.Environ()))
  copy(envVars, os.Environ())
  for k, v := range env { envVars = append(envVars, k+"="+v) }
  cmd.Env = envVars
  ```
- `internal/mcp/client.go:198-222` — `sendSync`:
  ```go
  go func() {
      raw, err := c.outBuf.ReadBytes('\n')
      ...
      ch <- nil
  }()
  select {
  case <-ctx.Done(): return ctx.Err()
  case err := <-ch: return err
  }
  ```

### Description

Three independent issues collide on the same component:

1. **Process not bound to context.** `exec.Command` is used instead of
   `exec.CommandContext`, so the child MCP server outlives any cancelled
   parent context. `Stop()` does kill explicitly, but a Go-routine timeout
   on the parent that doesn't reach `Stop()` (e.g. the engine crashes,
   panics through the recover, or shuts down via `os.Exit`) leaves
   orphan processes.
2. **Full parent env inherited.** `os.Environ()` is copied wholesale into
   the child. DFMC reads `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`,
   `DEEPSEEK_API_KEY`, `KIMI_API_KEY`, `ZAI_API_KEY`,
   `ALIBABA_API_KEY`, `MINIMAX_API_KEY`, `GOOGLE_AI_API_KEY`,
   `DFMC_WEB_TOKEN`, `DFMC_REMOTE_TOKEN` from the environment. Every
   user-configured external MCP server gets all of them, regardless of
   which provider it actually needs. A hostile or compromised MCP server
   exfiltrates the entire LLM/auth credential set on first connect.
3. **Goroutine leak on cancel.** When the caller's ctx fires, `sendSync`
   returns, but the inner goroutine is still parked on `c.outBuf.ReadBytes('\n')`.
   `ch` is buffered cap 1, so the send is non-blocking — but the read
   only returns when the child writes a line or closes stdout. With the
   process still alive (issue 1), the goroutine never returns. Every
   cancelled call permanently leaks one goroutine and one buffered
   channel; long-running DFMC processes that connect to slow MCP
   servers accumulate them indefinitely.
4. **Working dir not set.** `cmd.Dir` is unset, so the child inherits
   DFMC's CWD. A malicious MCP server can read project-relative paths
   the operator didn't intend to expose.

### Impact

Credential exposure to user-configured external MCP servers (high
severity given DFMC's mission profile is "user adds MCP server X to
config"); resource leaks under MCP server unreliability.

### Suggested fix

- Use `exec.CommandContext(ctx, command, args...)` and let `Stop()`
  cancel a context owned by the client.
- Build a minimal env from a config-defined allow-list; never inherit
  `*_API_KEY` keys unless the user's MCP server config explicitly
  declares which ones it needs.
- In `sendSync`, hold a read deadline on the underlying file (close
  stdin/stdout when ctx fires so the parked read returns with an
  error). Add a defer that drains `ch` to drop the leaked send.
- Set `cmd.Dir` to a config-specified working directory, defaulting
  to a sandbox dir not containing project secrets.

---

## LANG-GO-005 — wsConn.send may panic on send-to-closed-channel

- **Severity**: Medium
- **Confidence**: 75
- **CWE**: CWE-362 (Concurrent Execution Using Shared Resource with
  Improper Synchronization)

### Where

- `ui/web/server_ws.go:73-85` — `send`:
  ```go
  if c.closed.Load() { return ... }
  select {
  case c.sendCh <- ...:
  ...
  }
  ```
- `ui/web/server_ws.go:305-312` — `cleanup`:
  ```go
  c.closeMu.Lock()
  if !c.closed.Swap(true) { _ = c.conn.Close() }
  c.closeMu.Unlock()
  close(c.sendCh)
  ```

### Description

`send()` checks `c.closed.Load()` then sends on `c.sendCh` outside any
mutex. `cleanup()` flips `closed` true under `closeMu`, but then
closes `c.sendCh` AFTER releasing the lock. Two interleavings panic:

- Goroutine A: `send` reads `closed == false`, enters the select,
  evaluates `c.sendCh <-`.
- Goroutine B: `cleanup` runs to completion, closes `c.sendCh`.
- Goroutine A: send completes against the now-closed channel — runtime
  panic "send on closed channel".

The window is small but real, especially on disconnect storms.
Confidence 75 because real-world reproducibility depends on scheduler
luck, but the race is unambiguous on inspection.

`sendWS` (line 322) does the right thing: takes `closeMu`, rechecks
`closed`, then writes. `send` should mirror that pattern.

### Suggested fix

Hold `closeMu` (or `closeMu.RLock()` if upgraded to RWMutex) for the
duration of the send-attempt, OR remove the `close(c.sendCh)` from
`cleanup` and rely on the writeLoop drain after a sentinel.

---

## LANG-GO-006 — handleWebSocketUpgrade leaks goroutines on aggressive disconnect

- **Severity**: Medium
- **Confidence**: 60
- **CWE**: CWE-404 (Improper Resource Shutdown), CWE-770

### Where

- `ui/web/server_ws.go:92-106` — `handleWebSocketUpgrade` starts
  `writeLoop` and `readLoop`; never tracks them.
- `ui/web/server_ws.go:124-153` — `handleMessage` uses
  `ctx := context.Background()` — request context is discarded.

### Description

`handleMessage` discards the request ctx. `handleChat` then runs
`s.engine.Ask(ctx, req.Message)` with `ctx = context.Background()`,
so a client that sends a chat message and disconnects leaves the
agent loop running to completion (potentially hundreds of seconds
of provider tokens) on the dead connection. Same for `handleAsk` and
`handleTool`. The `done` channel pattern in `handleChat` exits the
streaming loop when the goroutine completes, but the goroutine itself
keeps doing work on the engine.

Combined with no upper bound on concurrent WS connections (each
upgrade just starts two goroutines), a misbehaving or malicious
client can fan out engine work and pay nothing for it.

Confidence 60 because impact depends on the engine's own
cancellation hygiene; tools that respect their ctx will halt, but
LLM provider calls already started will continue until response.

### Suggested fix

Use `r.Context()` (or derive from it) when constructing the per-
connection ctx. Cancel that ctx on `cleanup()`. Add a max-concurrent-
WS-connections bound (`server.go` already has rate limit; add a
gauge).

---

## LANG-GO-007 — handleChat streaming SSE drops events under load

- **Severity**: Low
- **Confidence**: 90
- **CWE**: CWE-665 (Improper Initialization)

### Where

- `ui/web/server_ws.go:174-193` — streaming chat:
  ```go
  eventsCh := make(chan engine.Event, 64)
  unsubscribe := c.engine.EventBus.SubscribeFunc("*", func(ev engine.Event) {
      select {
      case eventsCh <- ev:
      default:
      }
  })
  ```

### Description

The "*" subscription with a 64-deep buffer and `default:` drop will
silently drop bursts (the EventBus internally drops at 1024 — see
`internal/engine/eventbus.go:43` — but this *additional* second-tier
buffer is much smaller, so SSE clients see fewer events than even
the bus's internal counter would report). The DroppedCount surfaced
in `Engine.Status()` does not account for these per-subscriber drops.

This is operational-quality, not security, but worth flagging because
the architecture's CLAUDE.md notes that `EventBus` drops are
visible — these aren't.

### Suggested fix

Either match the bus's 1024 buffer here, or wire a counter back into
the engine status so the operator sees both layers.

---

## LANG-GO-008 — applyUnifiedDiffWeb: filepath.HasPrefix-style containment is unsafe

- **Severity**: Medium
- **Confidence**: 85
- **CWE**: CWE-22 (Path Traversal)

### Where

- `ui/web/server_workspace.go:251`:
  ```go
  if !strings.HasPrefix(absPath, root) {
      return fmt.Errorf("apply: path %s resolves outside project root (denied)", relPath)
  }
  ```

### Description

`strings.HasPrefix` is the wrong primitive for path containment.
Given `root="/proj"`, `absPath="/proj-evil/foo"` evaluates true and
slips through. The correct check is `filepath.Rel(root, absPath)`
followed by a `..`-prefix test (the rest of the codebase uses this:
see `ui/web/server_files.go:171-180` and
`internal/tools/engine.go`'s `EnsureWithinRoot`).

This is a defense-in-depth check after `git apply --check` already
validated the patch, and `git` itself rejects most escapes. The
tighter check in `resolvePathWithinRoot` exists precisely because
neither layer alone is sufficient. Apply a tightened check here too.

### Impact

A handcrafted patch with a path that begins with the root prefix but
lives in a sibling directory with the same prefix could escape. Real
exploitability is bounded by what `git apply` itself accepts, which
is non-trivial — confidence reflects that the bug is real but the
exploit chain is narrow.

### Suggested fix

Replace with the same `filepath.Rel` + `..`-prefix check used in
`server_files.go`. Better: extract `containsPath(root, p)` into a
shared helper used by every web/workspace path check.

---

## LANG-GO-009 — applyUnifiedDiffWeb uses Background() ctx, not request ctx

- **Severity**: Low
- **Confidence**: 95
- **CWE**: CWE-400 (Uncontrolled Resource Consumption)

### Where

- `ui/web/server_workspace.go:215`:
  ```go
  ctx, cancel := context.WithTimeout(context.Background(), applyTimeout)
  ```
- Same pattern at line 234.

### Description

A 60-second `git apply` is invoked under a context that ignores
`r.Context()`. If the HTTP client cancels (browser tab closed,
client times out), the apply keeps running. With WriteTimeout=2m on
the server, the handler will eventually be torn down but the child
process keeps the file lock until the 60s timeout fires.

### Suggested fix

Derive from `r.Context()`:
```go
ctx, cancel := context.WithTimeout(r.Context(), applyTimeout)
```

---

## LANG-GO-010 — gitWorkingDiffWeb runs git without context

- **Severity**: Low
- **Confidence**: 95

### Where

- `ui/web/server_workspace.go:108-110`:
  ```go
  cmd := exec.Command("git", "diff", "--")
  cmd.Dir = root
  out, err := cmd.Output()
  ```

### Description

`exec.Command` instead of `exec.CommandContext`. The handler's
HTTP request ctx is not threaded through, so client cancellation
doesn't propagate to the child process. The diff command is normally
fast, but a corrupted repo can push it into long pack-walks.

Same pattern in `ui/tui/patch_parse.go:220, 309` and
`ui/tui/filesystem.go:27` — TUI side, not security-critical, but
worth a sweep.

### Suggested fix

Use `exec.CommandContext(r.Context(), ...)` in the web handlers.

---

## LANG-GO-011 — taskstore.UpdateTask is read-modify-write across two transactions

- **Severity**: Medium
- **Confidence**: 90
- **CWE**: CWE-362 (Race Condition), CWE-367 (TOCTOU)

### Where

- `internal/taskstore/store.go:74-86`:
  ```go
  func (s *Store) UpdateTask(id string, fn func(*supervisor.Task) error) error {
      t, err := s.LoadTask(id)        // read tx
      if err != nil { return err }
      ...
      if err := fn(t); err != nil { return err }
      return s.SaveTask(t)            // separate write tx
  }
  ```

### Description

`LoadTask` runs a `db.View`, returns, then `SaveTask` runs a separate
`db.Update`. Two concurrent `UpdateTask(id, ...)` calls both read the
same baseline, both apply their mutator on copies, both write — the
second writer silently overwrites the first.

The HTTP `PATCH /api/v1/tasks/{id}` and the MCP task tools both use
this path. Concurrent task updates from the web UI + a Drive run that
mutates the same task can drop state changes.

### Impact

Lost updates on concurrent task mutations — visible to the user as
"my edit didn't take" or "drive run cleared my comment." Bbolt's
single-writer-tx model would have made the atomic version trivial.

### Suggested fix

Wrap the read-modify-write inside one `db.Update` so the
deserialization, mutation, and re-encode all happen under the same
write tx:

```go
return s.db.Update(func(tx *bbolt.Tx) error {
    b := tx.Bucket([]byte(taskBucket))
    v := b.Get([]byte(id))
    var t supervisor.Task
    if err := json.Unmarshal(v, &t); err != nil { return err }
    if err := fn(&t); err != nil { return err }
    data, _ := json.Marshal(&t)
    return b.Put([]byte(id), data)
})
```

---

## LANG-GO-012 — bearerTokenMiddleware allows empty-token serve

- **Severity**: Medium
- **Confidence**: 70
- **CWE**: CWE-287 (Improper Authentication)

### Where

- `ui/web/server.go:401-419`:
  ```go
  if got := strings.TrimSpace(r.Header.Get("Authorization"));
     rawToken != "" && subtle.ConstantTimeCompare([]byte(got), []byte(expected)) == 1 {
      next.ServeHTTP(w, r)
      return
  }
  writeJSON(w, http.StatusUnauthorized, ...)
  ```

### Description

The auth check requires `rawToken != ""`. If `auth=token` is set but
`DFMC_WEB_TOKEN`/`--token` is empty, the constant-time compare is
short-circuited and ALL requests get 401 — except the `/healthz`
exemption (line 405) and the `GET /` HTML exemption when
`rawToken == ""` (line 409, which routes through `next.ServeHTTP`
without any auth).

`runServe` does refuse empty token at startup (architecture map
`server.go:62-65`) but `Server.SetBearerToken("")` can be called
post-construction (`server.go:181-186`), which silently re-opens
unauthenticated `GET /` access. Programmatic re-opening of the
workbench HTML is the visible footgun.

### Impact

Any caller that creates a `Server` without immediately calling
`SetBearerToken` to a non-empty value, then composes
`bearerTokenMiddleware(handler, "")`, exposes the embedded HTML
workbench unauthenticated. The HTML is shipped from `static/index.html`
and the CSP locks scripts to `'self'` — but the page loads
`/api/v1/*` endpoints with whatever credentials the browser carries,
which may be none. Confidence 70 because the immediate
exploit path requires misconfiguration; the structural concern is
that empty-token mode silently degrades open.

### Suggested fix

Reject empty token at the middleware level too:
```go
if rawToken == "" {
    writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "server token not configured"})
    return
}
```
Move the workbench-HTML allowance inside the token check (only allow
when the request has a valid token, full stop). Or track a separate
boolean `authConfigured` so empty tokens can never look like "auth
disabled" by accident.

---

## LANG-GO-013 — Hooks Fire on goroutine without per-fire panic guard

- **Severity**: Low
- **Confidence**: 60

### Where

- `internal/hooks/hooks.go` (Fire dispatch) — see hooks_pgid_*.go for
  per-OS process group cleanup.

### Description

The Hooks system dispatches user-configured shell commands. The
exec'd command is properly contextualized via `CommandContext` and
process-group-clean. However `Fire` is called from
`executeToolWithLifecycle` synchronously on the engine goroutine
(see `engine_tools.go:253-264` for pre_tool, 271-298 for post_tool).
A panic in the hooks dispatch path would propagate up through the
tool lifecycle and only be caught by the per-tool panic guard at
`executeToolWithPanicGuard` — but that guard sits AROUND the actual
Execute call, not around the surrounding hook fires. A panic in the
pre_tool fire happens before Execute and is therefore NOT caught.

Hooks themselves are first-party Go code, so panic risk is low.
Confidence reflects that this is a hardening gap, not an active bug.

### Suggested fix

Wrap pre/post hook fires in the lifecycle method with their own
deferred recover — failing-best-effort is the documented contract,
hooks should NEVER take down the engine.

---

## LANG-GO-014 — listFiles ignores walk errors silently

- **Severity**: Info
- **Confidence**: 90

### Where

- `ui/web/server_files.go:107-131`:
  ```go
  err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
      if err != nil { return nil }   // ← swallows
      ...
  })
  ```

### Description

Walk errors per-entry are silently dropped; the user sees a partial
file list with no indication that traversal failed somewhere. Not a
security issue, but a clarity one — a permission-denied subtree
becomes invisible.

### Suggested fix

Either propagate the first non-permission error, or surface a
"partial: true" flag in the response when any per-entry error
occurred.

---

## LANG-GO-015 — handleToolExec doesn't validate Content-Type

- **Severity**: Info
- **Confidence**: 80

### Where

- `ui/web/server_tools_skills.go:151-173`.

### Description

`json.NewDecoder(r.Body).Decode(&req)` accepts any body that parses as
JSON, regardless of Content-Type. `text/plain` JSON would be accepted
silently. Architecture map flags this under "what's NOT present."

Combined with LANG-GO-002 (no Origin check) and LANG-GO-001 (no
approval), a `<form action="…/api/v1/tools/run_command" method=POST
enctype="text/plain">` from a malicious origin could submit JSON-
shaped form bodies to drive tool execution if same-site cookies were
ever introduced. Not exploitable today but worth tightening.

### Suggested fix

Add a Content-Type allow-list (`application/json` only) before
parsing.

---

## LANG-GO-016 — Server.New silently rebinds host to 127.0.0.1

- **Severity**: Info
- **Confidence**: 100

### Where

- `ui/web/server.go:152-160` — `normalizeBindHost`.

### Description

When `auth=none` and the configured bind host is non-loopback, the
function silently returns `127.0.0.1` and the server binds there
instead. The operator who passed `--host 0.0.0.0` may not realize
the rewrite happened until a remote client gets connection-refused.

Not a security flaw — it's the safe direction — but the silent
rewrite is a usability gap. There's a print to stderr for the
`auth=token` non-loopback case at line 157 but not the rewrite case.

### Suggested fix

Print the rewrite to stderr when it happens, mirroring the warning
that already exists for `auth=token` non-loopback.

---

## LANG-GO-017 — wsConn writeLoop has no panic guard

- **Severity**: Low
- **Confidence**: 70

### Where

- `ui/web/server_ws.go:295-303`:
  ```go
  func (c *wsConn) writeLoop() {
      for msg := range c.sendCh {
          if msg.Type == "send" {
              var v any
              _ = json.Unmarshal(msg.Params, &v)
              _ = c.conn.WriteJSON(v)
          }
      }
  }
  ```

### Description

`json.Unmarshal` and `WriteJSON` are unlikely to panic on malformed
input but a closed `c.conn` between iterations triggers a `WriteJSON`
error that's discarded. Combined with LANG-GO-005's send-on-closed
race, an uncaught panic here takes the whole DFMC process down.

### Suggested fix

Wrap the body in a `defer recover()` and return on first write error
so the goroutine exits cleanly.

---

## LANG-GO-018 — JSON unmarshal errors silently discarded in WS handlers

- **Severity**: Low
- **Confidence**: 90

### Where

- `ui/web/server_ws.go:166, 230, 254, 286`:
  ```go
  if params != nil { _ = json.Unmarshal(params, &req) }
  ```

### Description

Every WS-handler path discards the unmarshal error and proceeds with
zero-valued req. Means a malformed `params` blob silently turns into
"empty message", emits a generic "message is required" error, and
the client has no way to distinguish "I sent garbage" from "I forgot
the field." Operational, not security.

### Suggested fix

Surface `parse error` via `c.sendError(id, -32700, ...)` when
unmarshal fails on a non-empty params buffer.

---

## LANG-GO-019 — Tool engine's `_ = stdoutW` comment-as-control-flow at MCP client start

- **Severity**: Info
- **Confidence**: 50

### Where

- `internal/mcp/client.go:92-93`:
  ```go
  // stdoutW ownership transferred to the child process; only close on error paths above
  _ = stdoutW
  ```

### Description

`stdoutW` (the write end of the pipe owned by the parent) is never
closed in the success path. After the child process exits, the read
loop on `stdoutR` won't see EOF until the parent's reference is also
closed — but the parent never does. Low impact in practice because
`Stop()` kills and waits, but a server that exits cleanly via stdin
EOF leaves the parent reading stdoutR forever.

Confidence 50 because the success path appears to depend on `Stop()`
to clean up; if `Stop()` is always called, this is fine. If the
process exits naturally and `Stop()` isn't called, the reader hangs.

### Suggested fix

`stdoutW.Close()` after `cmd.Start()` succeeds — the child has
inherited its own copy of the file descriptor.

---

## LANG-GO-020 — handleWebSocketUpgrade response on Upgrade error writes JSON after upgrade attempt

- **Severity**: Info
- **Confidence**: 60

### Where

- `ui/web/server_ws.go:92-97`:
  ```go
  conn, err := wsUpgrader.Upgrade(w, r, nil)
  if err != nil {
      writeJSON(w, http.StatusBadRequest, map[string]any{"error": "websocket upgrade failed"})
      return
  }
  ```

### Description

`websocket.Upgrade` may have already written a response (101 Switching
Protocols, or a 4xx with its own body) before returning the error;
following up with `writeJSON` is at best a no-op and at worst a
"superfluous response.WriteHeader call" log spam line.

### Suggested fix

Drop the `writeJSON` on Upgrade failure — gorilla/websocket already
wrote the appropriate response. Or check `errors.Is(err,
websocket.ErrBadHandshake)` and log instead.

---

## LANG-GO-021 — clientIPKey returns first XFF entry, not last (untrusted)

- **Severity**: Info
- **Confidence**: 95

### Where

- `ui/web/server.go:378-384`.

### Description

When XFF is honored (which itself is the bug in LANG-GO-003), the
function returns the FIRST entry. The right-most entry is the one
added by the immediate proxy and is the safer one to trust; the
left-most is fully attacker-controlled. Even if LANG-GO-003 is
fixed to require a trusted-proxy gate, the entry-selection logic
is wrong.

### Suggested fix

Combined with LANG-GO-003's fix: when honoring XFF, return the
right-most entry (last hop before the proxy), not the left-most.

---

## Categories audited clean

- **`tls.Config{InsecureSkipVerify: true}`** — only appears in
  `internal/security/astscan_go.go` as a scanner pattern and in
  `internal/langintel/go_kb.go` as a doc string. NO production
  call sites.
- **`unsafe.Pointer` for memory unsafety** — only legit
  `unsafe.Sizeof` in `internal/hooks/hooks_pgid_windows.go:51` for a
  Windows syscall struct size. No reflect-on-private-fields, no
  arbitrary-pointer arithmetic.
- **`math/rand` for IDs/tokens** — IDs use `crypto/rand` consistently
  (`internal/taskstore/id.go:4`, `internal/drive/persistence.go:19`).
- **`http.DefaultClient`** — only in test code (`*_test.go` files).
  Production HTTP clients are constructed with explicit timeouts:
  `internal/provider/http_client.go:49`,
  `internal/tools/web.go:51`, `internal/config/config_models_dev.go:89`,
  CLI remote/update clients all with `Timeout` set.
- **`sync.Mutex` discipline** — `internal/conversation/manager.go:56-57`,
  `internal/supervisor/coordinator.go:132`,
  `internal/engine/eventbus.go:19,32`, `taskstore`, `drive` all
  consistently mutex-guard their maps.
- **Goroutine recover guards** — Drive worker
  (`internal/drive/driver_loop.go:171-182`) recovers; EventBus
  `SubscribeFunc` recovers per event; tool execute panic guard
  recovers. Gap: hook fire path (LANG-GO-013).
- **HTTP server timeouts** — `ui/web/server.go:286-291` sets
  ReadHeader/Read/Write/Idle/MaxHeaderBytes correctly. Streaming
  endpoints clear write deadline explicitly via
  `clearStreamingWriteDeadline`.
- **Path-traversal in tool engine** — `EnsureWithinRoot`
  (`internal/tools/engine.go:788`) and `resolvePathWithinRoot`
  (`ui/web/server_files.go:143`) are correctly symlink-safe with
  `filepath.Rel` containment checks. Note that
  `applyUnifiedDiffWeb` does NOT use these helpers — see LANG-GO-008.
- **bbolt long transactions** — `taskstore.ListTasks` and
  `listAll` hold a `db.View` for a single ForEach, no nested HTTP
  work. SaveTask is a single Put. The only concurrency issue is
  LANG-GO-011's read-modify-write across two transactions.

---

## Notes on confidence and exploitability

- LANG-GO-001 (approval bypass) is structurally unambiguous; the
  question is intent. CLAUDE.md repeatedly states `executeToolWithLifecycle`
  is the "single mandated entry" with approval — but the architecture
  map at `architecture.md:518-524` explicitly notes "even an
  authenticated web client invoking POST /api/v1/tools/run_command
  with source='user' skips the approval gate." This was either an
  intended carve-out or a known gap. Treating as a finding because
  the operator-facing config (`tools.require_approval`) makes no
  distinction between "agent" and "web user" sources, so the
  bypass is invisible from the config surface.
- LANG-GO-005 (send-on-closed) is the highest-confidence concurrency
  finding here; the other concurrency notes (UpdateTask, hooks panic)
  are real but lower-impact.
- The HTTP rate-limit XFF bypass (LANG-GO-003) is the easiest to
  weaponize end-to-end — a curl one-liner with rotating XFF defeats
  the only resource throttle in the entire web layer.
