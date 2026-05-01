# sc-race-condition — DFMC TOCTOU and Concurrency Findings (Phase 2)

**Scope:** read-before-mutation gate vs. mutation, bbolt store transactions, rate-limiter GC, WS broadcast subscriber lifecycle, agent-loop ctx guard in parallel dispatch, file ops without `O_EXCL`, supervisor task retry idempotency.

**Counts**
- High:    3
- Medium:  3
- Low:     2
- Info:    3
- Total:   11

CWE families: CWE-362 (concurrent execution / TOCTOU), CWE-367 (TOCTOU on filesystem), CWE-667 (improper locking), CWE-668 (exposure via lifecycle gap), CWE-770 (resource exhaustion), CWE-833 (deadlock).

---

## RACE-201 — `apply_patch` reads source BEFORE locking; concurrent writers win the race

- **Severity:** High
- **Confidence:** High
- **CWE:** CWE-367 (TOCTOU file open vs use), CWE-362
- **File:** `internal/tools/apply_patch.go:106-170`

**Race.** Per-file dispatch order:

```
os.Stat(abs)                                    // 106
EnsureReadBeforeMutation(abs)                   // 110 — reads file, hashes, compares to snapshot
original, _ := os.ReadFile(abs)                 // 142 — reads bytes used as patch base
updated, _, _, _, _ := applyHunks(original, …)  // 149 — produces final bytes
release := t.engine.LockPath(abs)               // 167 — LOCK ACQUIRED
defer release()                                 // 168 — releases at FUNCTION exit, not iteration
writeFileAtomic(abs, updated, 0o644)            // 169
```

Two windows are racy: (a) between line 110 (`EnsureReadBeforeMutation`'s hash read) and line 142 (`os.ReadFile original`) — a concurrent writer can mutate the file; the hunks are then computed against bytes that match neither the snapshot nor what's on disk; (b) between line 142 and line 167 — a concurrent `edit_file` (which DOES lock first) can apply a clean edit, then `apply_patch` overwrites those bytes with hunks based on pre-edit `original`. The atomic rename in `writeFileAtomic` does not save us — atomicity protects against half-written files, not against base-snapshot drift.

`defer release()` placement is also wrong for multi-file patches: the release runs at function exit, so locks accumulate across all files. With N targets, the lock window holds N locks simultaneously, which is wasted serialisation but also opens a deadlock against any future tool that locks two files in opposite order.

**Fix.** Acquire the per-file lock BEFORE the `EnsureReadBeforeMutation` and `os.ReadFile` calls; release at the end of each loop iteration (extract a per-file helper and `defer release()` inside it).

---

## RACE-202 — `symbol_rename` / `symbol_move` mutate without `LockPath` and use non-atomic `os.WriteFile`

- **Severity:** High
- **Confidence:** High
- **CWE:** CWE-362, CWE-367
- **Files:**
  - `internal/tools/symbol_rename.go:171-216`
  - `internal/tools/symbol_move.go:220-289`

**Race.** Both tools execute:

```
EnsureReadBeforeMutation(fpath)
data, _ := os.ReadFile(fpath)
// in-memory line edit
os.WriteFile(fpath, …, 0644)        // ← non-atomic, no LockPath
```

Three problems compound:
1. **No `LockPath`.** A concurrent `edit_file`/`apply_patch` (which DOES lock) can complete between `os.ReadFile` and `os.WriteFile`; the symbol pass overwrites it.
2. **Non-atomic write.** Bare `os.WriteFile` is `O_TRUNC | O_WRONLY` — a crash mid-write truncates the target. `internal/tools/builtin_edit.go` and `apply_patch.go` both use `writeFileAtomic` (temp + `os.Rename`) for exactly this reason.
3. **Cross-file ordering.** `symbol_rename` walks all referencing files in map iteration order (Go map iteration is randomised) — if it deadlocks against another tool that takes locks in a different order, results will be non-deterministic. (Currently no deadlock because there are no locks.)

The engine itself flags this in a comment: `internal/tools/engine.go:83-87` says "the window between `EnsureReadBeforeMutation` and `os.WriteFile` is a TOCTOU race" — that's the exact pattern these two tools use.

**Fix.** Replace each `os.WriteFile` site with:
```go
release := t.engine.LockPath(fpath); defer release()
// (or hoist read+gate INSIDE the lock window)
if err := writeFileAtomic(fpath, data, 0644); err != nil { return err }
```

---

## RACE-203 — Read-before-mutation `readGateLenient` for `edit_file` is exploitable via concurrent fabrication+race

- **Severity:** Medium
- **Confidence:** Medium
- **CWE:** CWE-362
- **Files:** `internal/tools/engine.go:484-548`, `internal/tools/builtin_edit.go:60-103`

**Race.** Lenient mode requires only a prior `read_file` snapshot key for the path; hash equality is NOT checked. The reasoning (in the comment) is sound for normal use: `edit_file`'s exact-string anchor catches unsafe edits because the `old_string` must match disk verbatim.

The race window: an attacker (or buggy parallel agent) does (1) `read_file path` to register the snapshot key, (2) waits for a co-tenant tool / human editor to mutate the file, (3) issues `edit_file` whose `old_string` happens to ALSO appear in the post-mutation bytes (e.g. a common Go import line, a function name). The lenient gate passes; the anchor matches the new bytes; the edit lands on a file the attacker never read. This is narrow — it requires `old_string` to be coincidentally unique in BOTH versions — but a small `old_string` like `package main\n` or `import "fmt"` would work.

`builtin_edit.go:60` correctly takes `LockPath` before `os.ReadFile`, so within one process the race is closed for direct concurrency. The remaining window is between user/external editor writes and the next `edit_file`, which the lenient gate explicitly tolerates.

**Fix.** Either accept this as documented behaviour (matches the comment's "drift tolerance" rationale) or tighten to `readGateStrict` and rely on the actionable error message to teach the model to re-read on drift. The current design choice is defensible — flagged as Medium because the scenario requires a small-anchor edit which `edit_file` already discourages via its ambiguity check.

---

## RACE-204 — Rate limiter GC: leak fixed, but `lastSeen` is unbounded between sweeps

- **Severity:** Medium
- **Confidence:** High
- **CWE:** CWE-770
- **File:** `ui/web/server.go:507-560`

**Race.** The GC ticker runs every 10 minutes and removes `lastSeen[ip]` older than 10 minutes. Between sweeps, a malicious or misconfigured client iterating the IP space (e.g. via cooperating proxies that legitimately set XFF) can balloon both maps to arbitrary size — there is no upper bound and no eviction outside the timer. Recent commit `6fb5fcc fix: ... rate-limiter GC` mentions a fix; verifying:

The fix appears to be the GC loop deletion path itself (delete from BOTH `buckets` and `lastSeen` — earlier versions only deleted one). That is correct now. ✓

The remaining attack: between sweep ticks, RAM grows linearly with unique IPs seen. Trusted-proxy gating limits this to spoofers behind a real proxy (uncommon in DFMC's loopback model), so impact is small. A hard cap on map size with random/oldest eviction would close it.

**Fix.** Add a `maxBuckets` cap (e.g. 10k); when adding a new IP, evict the oldest if at cap. The current 10-minute window is fine for cleanup but is not a backpressure mechanism.

---

## RACE-205 — WS event broadcast: drop-on-full chan is correct; subscriber lifecycle is mutex-protected

- **Severity:** Info
- **Confidence:** High
- **Files:** `internal/engine/eventbus.go:75-251`, `ui/web/server_ws.go:336-355`

**Verdict.** `EventBus.Publish` holds an RLock during fan-out; `publishToChannel` does a non-blocking `select` with `default: noteDroppedEvent` — slow subscribers cannot block fast ones. `Unsubscribe` is idempotent (`tryRemove` returns false on second call, skips `close(ch)`) and recover-guards a buggy double-close. `SubscribeFunc` returns a `sync.Once`-guarded unsubscribe. ✓

One subtle correctness check passed: in WS streaming chat (`server_ws.go:336-353`), `eventsCh` has buffer 64; the bus publish path has `select … default: drop`. If the WS writer goroutine is slow, events drop silently — but the bus reports drops via `DroppedCount()` and logs every `eventBusDropWarnEvery` (100 drops). No deadlock, no goroutine leak.

The cleanup path on disconnect is `sync.Once`-wrapped and was a previously-fixed race per commit history (VULN-022). ✓

---

## RACE-206 — Agent-loop parallel dispatch: ctx guard re-checked inside worker

- **Severity:** Info
- **Confidence:** High
- **File:** `internal/engine/agent_loop_parallel.go:91-155`

**Verdict.** Recent commit `6fb5fcc fix: ctx guard in parallel dispatch` is in place. The dispatch loop checks `ctx.Err()` before acquiring a semaphore slot AND each worker re-checks at line 144-147 inside the goroutine before calling `dispatch()`. This closes the window where a cancellation arrives between the outer check and the goroutine actually starting. The `sem` channel is bounded by `batchSize`, and `wg.Wait()` synchronises before reading `out` (memory model comment is correct). ✓

The only nit: when ctx is cancelled mid-batch, in-flight workers continue to run (`dispatch` does not pass ctx through to `executeToolWithLifecycle` for early abort) — but `executeToolWithLifecycle` itself respects ctx via `context.WithCancel` in approval and `tool.Execute` paths, so a long-running tool will see ctx.Done().

---

## RACE-207 — bbolt store transactions are correctly bounded; no parallel-write paths

- **Severity:** Info
- **Confidence:** High
- **Files:** `internal/storage/store.go:80-130`, `internal/taskstore/store.go`, `internal/drive/persistence.go`

**Verdict.** All writes use `db.Update(func(tx *bbolt.Tx) error)`; bbolt serializes writes at the file level (single-writer multi-reader). The `ErrStoreLocked` returned at process startup prevents two `dfmc` processes from racing. Inside one process, multiple goroutines concurrent-writing to different buckets is supported and safe per bbolt docs. ✓

No raw `db.View` followed by `db.Update` patterns where the read result is used to compute a write key (which would be a TOCTOU at the txn level). Spot-checked taskstore CRUD and drive persistence — all reads happen inside the same Update tx that performs the write, or are independent of the write key.

---

## RACE-208 — `os.OpenFile`/`os.Create` callers do not use `O_EXCL`

- **Severity:** Low
- **Confidence:** High
- **CWE:** CWE-367
- **Files:** every `os.Create`/`os.WriteFile`/`os.OpenFile` in `internal/tools/`, `internal/storage/`, `internal/conversation/`, `internal/drive/persistence.go`

**Finding.** No production code uses `O_EXCL` (verified by grep — no hits in non-test sources). For `writeFileAtomic` this is safe because the temp file goes through `os.CreateTemp` (random suffix → no clobber) followed by atomic rename. For `symbol_rename`/`symbol_move`/legacy `os.WriteFile` callers, an attacker who plants a symlink at the target path between `os.Stat` and `os.WriteFile` could redirect the write — DFMC's project-root containment via `EnsureWithinRoot` (which resolves symlinks) catches the obvious attack, but a TOCTOU between resolve and write remains.

Single-user trust model bounds blast radius. Not exploitable without local code execution as the same user (which already implies game-over for a developer tool).

**Fix.** Where atomic semantics matter, route through `writeFileAtomic`. New tools should never bypass it.

---

## RACE-209 — Supervisor retry: classification is fail-open and the loop is idempotency-blind

- **Severity:** Medium
- **Confidence:** Medium
- **CWE:** CWE-362
- **Files:** `internal/supervisor/policies.go:106-150` (`ClassifyFailure`), `internal/supervisor/coordinator*.go`

**Race / logic.** `ClassifyFailure` defaults to `FailureRetryable` for any error string it doesn't recognise (test confirms: `policies_test.go:98 "Fail open: unknown errors are treated as retryable"`). With `DefaultRetryPolicy.MaxAttempts=3`, an unknown-error tool that has side effects (e.g. `run_command "rm foo"` that fails after partial deletion, or `git_commit` that succeeded server-side but timed out on response) is retried up to 3 times.

There is no idempotency guard in the retry path — `ShouldRetry` only consults attempt count + class, not "did the previous attempt mutate state". For pure-read tools (`read_file`, `grep_codebase`) this is fine. For mutating tools, retrying after a transient error can:
- Re-run `git_commit` and create a duplicate commit if the first one succeeded but the network reply was lost.
- Re-run `apply_patch` on a file the previous attempt partially patched (the patch will likely fail with hunk mismatch, but in fuzzy-offset mode it could land in unexpected places).
- Re-run `web_fetch` / `web_search` POSTs — but these are GET-only.

**Fix.** Either narrow `ClassifyFailure` defaults to `FailurePermanent` for tools with `Risk: RiskWrite`/`RiskExecute`, or add a per-tool idempotency declaration (`ToolSpec.Idempotent` exists — wire it into the retry policy).

---

## RACE-210 — `BeginAutoApprove` slot restoration is not atomic with the snapshot read (cross-reference LOGIC-201)

- **Severity:** High (mirror of LOGIC-201 from a concurrency lens)
- **Confidence:** High
- **CWE:** CWE-362
- **File:** `internal/engine/drive_adapter.go:285-297`

**Race.** Same as LOGIC-201 in the business-logic report. `prev := r.e.approver()` and `r.e.SetApprover(override)` are NOT done atomically — between them, another goroutine's `SetApprover` can land. The release closure restores `prev`, which by then may be a different (or stale) approver than what was actually installed when this `BeginAutoApprove` was called. Concurrent Drive runs reproduce this.

**Fix.** Wrap snapshot+install in a mutex, or change `SetApprover` to return a typed token that `release` validates before swapping back.

---

## RACE-211 — Path-lock map grows unbounded (`sync.Map` never evicts)

- **Severity:** Low
- **Confidence:** High
- **CWE:** CWE-770
- **File:** `internal/tools/engine.go:87-102`

**Finding.** `pathLocks sync.Map` stores a `*sync.Mutex` per absolute path. There is no eviction — a long-running session that touches many files (e.g. a 10k-file repo with full codebase rewrites) accumulates 10k unfreed mutexes. Each is small (~24 bytes) but grows linearly with reachable file count. No correctness bug, just a slow leak in long-lived `dfmc serve` sessions.

**Fix.** Replace `sync.Map` with a TTL cache, or expire entries when their refcount returns to zero. For DFMC's typical CLI single-shot usage this is irrelevant; for `serve` it might matter over weeks.

---

## Verdict

Sharpest race: **RACE-201** + **RACE-202** are the same bug in two places — mutating tools that take `LockPath` AFTER reading the file (or never), defeating the whole point of `LockPath`. Concurrent fan-out (parallel sub-agents, drive workers `MaxParallel=3`, batched tool calls) reaches these paths in normal use. Fix is mechanical: hoist `LockPath` before the read; replace bare `os.WriteFile` with `writeFileAtomic`. **RACE-210** (= LOGIC-201) is the same priority from a different angle: the global approver slot is not atomically swapped, so concurrent Drive runs corrupt each other's restoration.
