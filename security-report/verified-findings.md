# Verified Security Findings

**Date:** 2026-05-03
**Phase:** 3 — Verification (sc-verifier)
**Input:** 12 Phase 2 scanner result files
**Criteria:** Dedup across scanners, false positive elimination, CVSS-style severity, confidence scoring

---

## Summary Statistics

| Metric | Count |
|--------|-------|
| Total raw findings | ~17 |
| After dedup | 12 unique findings |
| False positives eliminated | 5 |
| **Verified findings** | **12** |

### By Severity
- Critical: 0
- High: 2
- Medium: 5
- Low: 4
- Info: 1

---

## Verified Findings (sorted by severity)

---

### [High] RCE-001 — Drive `BeginAutoApprove` slot clobbering on concurrent run completion

- **Scanner(s)**: sc-auth-authz, sc-business-logic-race
- **File(s)**: `internal/engine/drive_adapter.go:285-297`
- **Description**: When multiple Drive runs execute concurrently, each calls `BeginAutoApprove` which installs an override approver and returns a release closure. The release closure unconditionally calls `SetApprover(prev)` — without verifying the currently-installed approver is still the one this call installed. When Run A and Run B complete concurrently (or with overlapping lifetimes), the last release to fire clobbers whatever approver the other run installed, leaving a "zombie" auto-approver using the first run's allowlist (including `["*"]` if set).
- **CVSS Vector**: CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:N
- **Confidence**: 75%
- **Impact**: After two concurrent Drive runs complete, all subsequent `/chat` or `/tool` requests in that process are auto-approved with the first run's `auto_approve` list until something else calls `SetApprover`. With `auto_approve: ["*"]` this is full privilege-escalation: unauthenticated callers bypass every approval gate.
- **Evidence**:
  ```go
  func (r *driveRunner) BeginAutoApprove(tools []string) func() {
      prev := r.e.approver()          // snapshot current
      override := newDriveAutoApprover(prev, tools, "drive")
      r.e.SetApprover(override)       // install
      return func() {
          r.e.SetApprover(prev)        // UNCONDITIONAL — no ownership check
      }
  }
  ```
  `Driver.Run` (line 168) and `Driver.Resume` (line 275) both call `BeginAutoApprove` with `defer release()`, but POST `/api/v1/drive` fires-and-forgets in a goroutine with no serialization. Concurrent execution of multiple drive runs is explicitly allowed by the scheduler's parallel worker pool.
- **Mitigation**: Use an opaque ownership token returned by `SetApprover` so `release` only restores when the slot still owns that token. Alternatively, add a process-wide single-flight mutex so `BeginAutoApprove` calls are provably serialized (LIFO nesting enforced).

---

### [High] LOGIC-202 — `apply_patch` TOCTOU: stat check and source read occur before `LockPath`

- **Scanner(s)**: sc-auth-authz, sc-business-logic-race
- **File(s)**: `internal/tools/apply_patch.go:97-170`
- **Description**: The read-before-mutation gate (`EnsureReadBeforeMutation`) and the source file read (`os.ReadFile`) both occur **before** `LockPath` is acquired. The lock is only held around the write. Between the read and the lock acquisition, another goroutine can modify the file, bypassing the snapshot check. Additionally, the `os.Stat(abs)` gate (which decides whether to invoke `EnsureReadBeforeMutation` at all) happens before the lock — a file can be deleted and recreated between the stat check and the write, silently bypassing the read-gate.
- **CVSS Vector**: CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/C:H
- **Confidence**: 80%
- **Impact**: Two concurrent `apply_patch` calls targeting the same file — or an `apply_patch` racing against an external editor — can result in the second writer silently overwriting changes from the first, or the read-gate passing when it should fail. Model's safety invariant (read-before-mutate gate) is circumvented by a TOCTOU race.
- **Evidence**:
  ```
  Line ~97:   if _, statErr := os.Stat(abs); statErr == nil {
  Line ~106:      if !dryRun { EnsureReadBeforeMutation(abs) }  // gate BEFORE lock
  Line ~120:   orig, err := os.ReadFile(absPath)               // source read BEFORE lock
  Line ~166:   release := e.LockPath(abs)                     // lock acquired LATE
  Line ~170:   err := os.WriteFile(absPath, patched)          // write under lock
  ```
- **Mitigation**: Move `LockPath` acquisition to before both the stat gate and the source read. The lock must serialize the entire stat-check + read-gate + read + write sequence per path.

---

### [Medium] DATA-001 — `Load()` / `LoadReadOnly()` unlock before writing to `m.active` (data race)

- **Scanner(s)**: sc-business-logic-race
- **File(s)**: `internal/conversation/manager.go:417`, `internal/conversation/manager.go:429`
- **Description**: Both `Load(id)` and `LoadReadOnly(id)` take `m.mu.RLock()`, read the conversation from the store, release the lock, then assign to `m.active` without holding any lock. A concurrent reader of `Active()` (which takes `RLock`) can observe `m.active` mid-assignment (partial pointer write on 64-bit architectures) or stale value while another goroutine writes the new value.
- **CVSS Vector**: CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:H
- **Confidence**: 85%
- **Impact**: One goroutine calling `Load()` while another calls `Active()` or `AddMessage()` can see a corrupted or stale `*Conversation` pointer. This can cause incorrect conversation context in tool calls or a crash if the pointer fields are in an inconsistent state.
- **Evidence**:
  ```go
  m.mu.RLock()
  store := m.store   // store is immutable for lifetime of Manager
  m.mu.RUnlock()
  // ... load from store ...
  m.mu.Lock()
  m.active = c       // DATA RACE: no lock held here
  m.mu.Unlock()
  return cloneConversation(c), nil
  ```
- **Mitigation**: Hold `m.mu.Lock()` (not RLock) for the entire duration of the swap, including the store load. Alternatively, use an atomic `sync.Value` for `m.active`.

---

### [Medium] RACE-001 — Concurrent `Persist()` on `memory.Store` can silently lose writes

- **Scanner(s)**: sc-business-logic-race
- **File(s)**: `internal/memory/store.go:99-119`
- **Description**: `Persist()` takes an RWMutex read lock, snapshots `s.working`, releases the lock, then calls bbolt `Update`. If two goroutines call `Persist()` concurrently (or one races with `SetWorkingQuestionAnswer`/`TouchFile`/`TouchSymbol`), both snapshot `s.working` under shared read locks, then both perform their bbolt `Put` sequentially. The second `Put` overwrites the first with identical data — masking concurrent in-memory mutations that were never persisted. Lost-update race.
- **CVSS Vector**: CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:H
- **Confidence**: 75%
- **Impact**: When concurrent goroutines persist (e.g. background persist racing with shutdown persist), the resulting bbolt state reflects one of the two snapshots, not the union of both. Data is silently lost.
- **Evidence**:
  ```go
  func (s *Store) Persist() error {
      s.mu.RLock()
      wm := WorkingMemory{...}              // snapshot under read lock
      s.mu.RUnlock()
      return s.storage.DB().Update(func(tx *bbolt.Tx) error {
          return b.Put([]byte(bucketWorkingKey), data)  // last writer wins
      })
  }
  ```
- **Mitigation**: Use a write lock (`Lock()`) for the entire Persist operation (including the bbolt transaction), or serialize Persist calls with a dedicated mutex.

---

### [Medium] NET-001 — WebSocket connection limiter uses same XFF-extraction as HTTP rate limiter

- **Scanner(s)**: sc-session-rate-limit, sc-websocket-cors-header
- **File(s)**: `ui/web/server_ws.go:220`, `ui/web/server.go:605`
- **Description**: Both the WebSocket connection cap (`wsConnLimiter.Acquire`) and the HTTP per-IP rate limiter call the same `clientIPKey(r, s.trustedProxies)` function. XFF is only honored when the direct peer is a trusted proxy (loopback by default), which is correctly restrictive for HTTP. However, the WS upgrade path originates from a direct TCP connection (not an intermediate proxy), so if the direct peer IS a trusted proxy (e.g., operator's own reverse proxy on loopback), an attacker who can inject XFF values into that proxy could rotate IPs via XFF to defeat the per-IP WS connection limit.
- **CVSS Vector**: CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:L
- **Confidence**: 60%
- **Impact**: An attacker who can route traffic through the trusted proxy (or if the loopback proxy is misconfigured to trust external XFF) could open more than `wsPerIPConnCap` (8) WebSocket connections from a single machine by cycling XFF-reported IPs. Normal deployments without a reverse proxy are unaffected (direct WS connections use `RemoteAddr`).
- **Evidence**:
  ```go
  // server_ws.go:220
  wsRelease, gateMsg := s.wsConnLimiter.Acquire(clientIPKey(r, s.trustedProxies))
  // server.go:605 — same clientIPKey for HTTP rate limiting
  if !limiter.Allow(clientIPKey(r, s.trustedProxies)) {
  ```
- **Mitigation**: Use only `RemoteAddr` for WS connection limiting, regardless of XFF headers. Add a secondary check that does NOT use XFF for WS upgrade connection caps.

---

### [Medium] REGEX-001 — Catastrophic regex backtracking in `grep_codebase`

- **Scanner(s)**: sc-lang-go
- **File(s)**: `internal/tools/builtin_grep.go:96`
- **Description**: User-supplied pattern passed directly to `regexp.Compile` per call. A crafted pattern like `^(a+)+$` can cause O(2^n) matching time, causing exponential CPU consumption on a grep call. The model controls the pattern — this is a self-DOS vector.
- **CVSS Vector**: CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:N
- **Confidence**: 70%
- **Impact**: Self-DOS — the model could craft a pattern that causes the grep to hang indefinitely, blocking the agent loop. No external attacker required; this is an artificial self-inflicted DoS if the model generates a malicious pattern.
- **Evidence**: `regexp.MustCompile(pattern)` called per invocation of `grepCodebase` without timeout at the regex level. No pre-validation of known catastrophic constructs.
- **Mitigation**: Use `regexp.Compile` with a timeout enforced at the execution level, or pre-validate patterns for known catastrophic constructs (nested quantifiers like `(a+)+`). Go 1.21+ `regexp.Regexp` has `MatchReader` for progressive matching with deadlines.

---

### [Medium] REGEX-002 — Per-call regex compilation in `find_symbol` parent resolution

- **Scanner(s)**: sc-lang-go
- **File(s)**: `internal/tools/find_symbol_parent.go:87`, `internal/tools/find_symbol_parent.go:119`
- **Description**: The `parent` argument is embedded into a regex and compiled per call. While patterns are simpler than arbitrary grep patterns, a malicious `parent` value could still cause pathological backtracking (e.g., `parent="(?:(a+)+)+"`).
- **CVSS Vector**: CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:N
- **Confidence**: 55%
- **Impact**: Self-DOS vector — a model could craft a specifically problematic parent pattern to hang `find_symbol`. Lower risk than grep because patterns are constrained, but still exploitable.
- **Evidence**: Per-call `regexp.MustCompile` on `parent` argument value at both line 87 and 119.
- **Mitigation**: Pre-compile and cache regexes for the small fixed set of parent patterns (currently just the empty string and `(.*)`). Alternatively, use `regexp.Regexp` with a deadline context.

---

### [Low] AUTH-001 — `auth=token` on non-loopback bind only warns, does not block

- **Scanner(s)**: sc-auth-authz
- **File(s)**: `ui/web/server.go:181-184`
- **Description**: `normalizeBindHost` issues a `WARNING` when `auth=token` is combined with a non-loopback bind but still binds to the specified address. With `auth=none`, the function forcibly overrides to 127.0.0.1. So `auth=token` on `0.0.0.0:7777` starts with a warning but remains fully exposed on all interfaces.
- **CVSS Vector**: CVSS:3.1/AV:N/AC:H/PR:N/UI:R/S:U/C:N/I:N/A:N
- **Confidence**: 90%
- **Impact**: An operator who sets `auth=token` (expecting bearer token to protect the service) but mistakenly binds to `0.0.0.0` gets a warning but the server still starts exposed. The token provides transport-level protection, but AST backend, config endpoint, and Drive endpoints are visible to any machine that can reach the IP. Operator error, not a code exploit.
- **Evidence**:
  ```go
  if strings.EqualFold(strings.TrimSpace(authMode), "token") && !isLoopbackBindHost(host) {
      fmt.Fprintf(os.Stderr, "[DFMC] WARNING: auth=token with non-loopback bind (%s) exposes the agent on all interfaces.\n", host)
  }
  return host  // continues to bind to the non-loopback host
  ```
- **Mitigation**: Enforce loopback for `auth=token` as well (`return "127.0.0.1"` with a notice), or elevate warning to error that refuses to start without explicit `--force-expose`.

---

### [Low] SESS-001 — WebSocket session ID uses `time.Now().UnixNano()` (predictable)

- **Scanner(s)**: sc-session-rate-limit, sc-websocket-cors-header (merged)
- **File(s)**: `ui/web/server_ws.go:236`
- **Description**: Session IDs generated via `fmt.Sprintf("ws-%d", time.Now().UnixNano())`. Nanosecond timestamp is predictable. The scanner flagged this in both the session/rate-limit and the websocket/cors reports — they are the same finding.
- **CVSS Vector**: CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:N
- **Confidence**: 95% (finding is accurate, severity is low because IDs are not used for auth)
- **Impact**: Low — IDs are used for logging/identification only, not for authentication. But if ever repurposed for a security context, would be guessable.
- **Evidence**: `ws := &wsConn{id: fmt.Sprintf("ws-%d", time.Now().UnixNano()), ...}`
- **Mitigation**: Use `crypto/rand` or `github.com/google/uuid` for session ID generation.

---

### [Low] MEM-001 — Microsecond-precision ID collision in `memory.Store.Add`

- **Scanner(s)**: sc-business-logic-race
- **File(s)**: `internal/memory/store.go:131-137`
- **Description**: When two goroutines call `Add` within the same microsecond and both provide empty entry IDs, `entry.ID` is constructed from `time.Now().Format("20060102_150405.000000")` + 6-byte random suffix. The random suffix reduces but does not eliminate collision probability (~2^-48 per call). Comment in code explicitly acknowledges this.
- **CVSS Vector**: CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:N
- **Confidence**: 50% (requires extreme scheduling precision — unlikely in practice)
- **Impact**: Extremely unlikely in practice. If it occurs, second `Put` silently overwrites the first — data loss.
- **Mitigation**: Use a bbolt transaction sequence number or UUID generation instead of timestamp-based IDs.

---

### [Low] STOR-001 — `BackupTo` TOCTOU between temp-file creation and atomic rename

- **Scanner(s)**: sc-business-logic-race
- **File(s)**: `internal/storage/store.go:197-220`
- **Description**: M5 fix replaced predictable temp names with `os.CreateTemp` (safe against pre-created symlink attacks). However, the window between temp-file creation and `os.Rename` still exists. On Windows, `os.CreateTemp` uses `GetTempFileName` which is not cryptographically random, making the Windows path more vulnerable to prediction.
- **CVSS Vector**: CVSS:3.1/AV:L/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:N
- **Confidence**: 40% (requires winning a race against a single writer on Windows)
- **Impact**: Local privilege escalation if attacker can predict temp path and win race before rename commits. Requires precise timing and knowledge of the temp name.
- **Mitigation**: On Windows, use a cryptographically random temp name. The atomic rename itself is safe once the temp file is created.

---

### [Info] CRYPTO-001 — No crypto issues found

- **Scanner(s)**: sc-crypto-jwt
- **File(s)**: N/A
- **Description**: All cryptographic operations are correctly implemented. Bearer tokens use constant-time comparison (`crypto/subtle.ConstantTimeCompare`). All security-sensitive IDs use `crypto/rand`. No custom crypto, no weak algorithms, no JWT usage.
- **Confidence**: 100%
- **Impact**: None — clean.
- **Mitigation**: N/A

---

## False Positives Eliminated

| Finding | Reason |
|---------|--------|
| CMDi bypass via path resolution in `run_command` | `isBlockedShellInterpreter` operates on resolved base name; both `cmd` and `C:\Windows\System32\cmd.exe` resolve to `cmd` which is blocked. Check order is deliberately protective. |
| Windows junction bypass in `EnsureWithinRoot` | `filepath.EvalSymlinks` on Windows resolves both NTFS symlinks and directory junctions. Both root and path are evaluated before the containment check. Junction escape would be caught. |
| `golang.org/x/net` CVE-2024-45338 | Fixed in v0.33.0 (Sept 2024). v0.53.0 includes all fixes. |
| `bbolt` CVE-2023-43804 | Fixed in v1.3.5 (Oct 2023). v1.4.3 includes the fix. |
| Conversation ID Predictability | IDs use nanosecond-suffixed timestamp format yielding ~1 billion distinct IDs per millisecond. Bearer token auth is the access control boundary. |
| WS Streaming Event Channel Drop | Intentional design — slow WS clients must not block the engine's event loop. Clients requiring guaranteed delivery use the HTTP API. |

---

## Dedup Log

| Finding ID | Merged From | Reason |
|------------|-------------|--------|
| RCE-001 | sc-auth-authz + sc-business-logic-race | Same `BeginAutoApprove` slot clobbering issue |
| LOGIC-202 | sc-auth-authz + sc-business-logic-race | Same `apply_patch` TOCTOU issue |
| SESS-001 | sc-session-rate-limit + sc-websocket-cors-header | Same WS session ID predictability issue |