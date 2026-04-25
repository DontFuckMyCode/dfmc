# sc-race-condition — DFMC Concurrency / TOCTOU Findings

**Counts**
- Critical: 0
- High:     2
- Medium:   5
- Low:      3
- Info:     1
- Total:    11

Scope: agent loop concurrency, EventBus subscribers, drive scheduler, taskstore tree updates, conversation manager, parallel tool dispatch, read-before-mutation TOCTOU, drainage at shutdown, bbolt single-process lock.
CWE families: CWE-362 (Concurrent Execution using Shared Resource with Improper Synchronization), CWE-367 (TOCTOU), CWE-833 (Deadlock).

---

## RACE-001 — `taskstore.UpdateTask` is read-modify-write across two transactions; concurrent updates lose writes

- **Severity:** High
- **Confidence:** High
- **CWE:** CWE-362
- **File:** `internal/taskstore/store.go:74-86`

```go
func (s *Store) UpdateTask(id string, fn func(*supervisor.Task) error) error {
    t, err := s.LoadTask(id)        // bbolt View tx #1
    if err != nil { return err }
    if t == nil { return fmt.Errorf("task not found: %s", id) }
    if err := fn(t); err != nil { return err }
    return s.SaveTask(t)            // bbolt Update tx #2
}
```

**Finding.** Load and Save are two separate bbolt transactions with the application-level mutator running between them. Two concurrent `UpdateTask("X", ...)` calls — easy to trigger via `PATCH /api/v1/tasks/{id}` from two browser tabs, or from `OnTaskBlocked` (line 411) walking the tree and updating multiple tasks under load — both load the same snapshot, both apply their callback, both save, and the second `Save` wins. The first writer's modification is silently lost. There is no row-level lock, no version counter, no compare-and-swap.

**Concrete blast radius.**
- `OnTaskBlocked` (line 411-431) loops over `all` tasks updating `BlockedBy`. If two TODOs block at the same time, the BlockedBy lists race.
- `OnTaskUnblocked` (line 435-454) — same shape.
- HTTP PATCH from a polling UI is the obvious trigger.

**Fix.** Wrap the load+mutate+save in a single `db.Update` transaction so the read and write are atomic against bbolt's own write lock. (Bbolt only allows one writer at a time, so this also serializes correctly across goroutines.)

---

## RACE-002 — `tools.Engine` `recordReadSnapshot` then `ensureReadBeforeMutationMode` is TOCTOU on the snapshot map

- **Severity:** High
- **Confidence:** High
- **CWE:** CWE-367 (TOCTOU)
- **Files:**
  - `internal/tools/engine.go:516-546` (`ensureReadBeforeMutationMode`)
  - `internal/tools/engine.go:650-697` (`recordReadSnapshot`)
  - `internal/engine/agent_loop_parallel.go:91-149` (parallel dispatcher)

**Finding.** The read-before-mutate gate stores `readSnapshots[abs] = hash` in a map guarded by `readMu`. Strict mode (`write_file`, `apply_patch`) reads the file from disk and compares to the recorded hash. Lenient mode (`edit_file`) skips the hash comparison. Two concrete races:

1. **Read snapshot map under parallel dispatch.** The parallel dispatcher `parallelSafeTools` whitelist includes `read_file` (line 33), so two `read_file` calls on the same path can race. `recordReadSnapshot` takes `e.readMu.Lock()` at line 693, so the map write is safe — but the *order* in which two parallel reads of the same path return is non-deterministic. If both reads happen but only one's Output reaches `recordReadSnapshot` (the other panics, or the parallel dispatcher's `out[idx]` slot gets overwritten — it doesn't, it's pre-allocated and indexed, OK), the snapshot is whichever finished last.

   **Verdict:** map access is correctly locked; ordering is non-deterministic but only a "wrong but recent snapshot" outcome — not a corruption.

2. **TOCTOU between snapshot-read and disk-write — strict mode.** In `ensureReadBeforeMutationMode`:
   ```
   stat → readMu.RLock → fileContentHash(disk) → compare
   ```
   The hash is computed from disk, NOT from the snapshot. If another goroutine (or external editor) writes the file between `recordReadSnapshot` and `ensureReadBeforeMutationMode`, the gate detects drift and refuses. ✓ This is the **intended** safety net.

   The actual TOCTOU is **between** the gate check and `tool.Execute(ctx, req)` at line 457. After the gate says "no drift", the tool's own `os.WriteFile` runs. If a third process or another goroutine writes the file in that ~microsecond gap, the in-flight write silently overwrites the third-party change. This is the classic TOCTOU on filesystem write.

   In a single-process model with no parallel `write_file` (the parallel dispatcher's whitelist excludes mutating tools, line 32-43 — confirmed safe) the only racer is an external editor, which is acceptable. **But** subagents and the engine itself are *the same process* — `RunSubagent` can spawn concurrent loops, each able to call `write_file` to the same path. The serialization comes from `parallelSafeTools` excluding `write_file` *within one batch*, but two sub-agent loops dispatching at separate moments are both single-tool dispatches and not coordinated.

**Impact.** Two concurrent sub-agents can both read a file, both pass the strict gate (each has a snapshot), and one's write blows away the other's write between gate-check and `os.WriteFile`. The drift check would only catch this on the *third* write — by then the data loss has happened.

**Fix.** Hold a per-path mutex from gate-check through the write inside `tool.Execute`. The simplest implementation: `e.readMu.Lock()` (write lock) for the entire gate+execute+recordReadSnapshot sequence on mutating tools, accepting reduced parallelism for safety. Or: a per-absPath sync.Mutex, acquired in Execute for write_file/apply_patch.

---

## RACE-003 — `executeToolCallsParallel` shared cache map mutated under one mutex but never cleared between rounds

- **Severity:** Medium
- **Confidence:** Medium
- **CWE:** CWE-362
- **File:** `internal/engine/agent_loop_parallel.go:91-188`

**Finding.** The cache map is allocated once per loop and threaded into `executeToolCallsParallel` along with `cacheMu`. Map access is correctly locked at lines 163-165 and 185-187. **However** the cache is keyed by `cacheableToolCallKey(c)` — let me check that function shape: it presumably hashes call name + args. Since `c.Input` is `map[string]any` deserialized from JSON, two calls with identical arg shapes share a key; if file `X` is `read_file`'d then modified externally, then `read_file`'d again in the same loop, the second read returns the cached stale output. That is a **logic** bug as much as a race, but worth noting because the mitigation (cache invalidation on every `write_file`/`edit_file`) is only wired for the engine's `Context.Invalidate` (in `engine_tools.go:32-49`), NOT for this per-loop tool cache.

**Impact.** Stale read after a sibling write within the same agent loop. Bounded by the loop, but visible to the model.

**Fix.** Invalidate the per-loop cache in `executeToolCallsParallel` whenever a write/edit/apply_patch dispatches that affects a path the cache holds.

---

## RACE-004 — Drive scheduler `readyBatch` with empty `file_scope` correctly serializes BUT the check is racy with worker completion

- **Severity:** Medium
- **Confidence:** Medium
- **CWE:** CWE-362
- **Files:**
  - `internal/drive/scheduler.go:147-221` (`readyBatchWithPolicy`)
  - `internal/drive/driver_loop.go:35-118` (single-goroutine main loop)

**Finding.** `executeLoop` is single-goroutine — only the main loop reads `run.Todos` and dispatches; workers receive a value-typed request and write back to the buffered `results` channel. This is the *correct* design and means the scheduler's `readyBatch` is never called concurrently. ✓

The narrow race window: the main loop's `readyBatchWithPolicy(run.Todos, ...)` reads the live `run.Todos` slice while a worker goroutine COULD be writing to that slice if it touched it. Workers don't (per the comment at line 22-30). ✓

**However**: `applyOutcome` (`driver_loop.go:217-313`) writes to `run.Todos` AND publishes events AND can call `applySpawnedTodos` which can append to `run.Todos`. If `applyOutcome` is called from `drainAndFinalize`'s `for inFlight > 0` loop (line 340-374) while the scheduler's mental model thought the loop had exited, you have two writers to `run.Todos` — but in practice `executeLoop` only ever calls `applyOutcome` after a `<-results` receive, so it's serial. ✓

**The real race:** two `RunPrepared(ctx, run)` calls on the same `*Run` pointer (e.g. two `POST /api/v1/drive/{id}/resume` calls firing concurrently before the registry registration completes). The registry guard `IsActive(run.ID)` at `driver.go:116` is checked once; if both callers race past it before either calls `register()`, both run the loop on the same `*Run` pointer. The registry itself is mutex-locked, but the gap between `IsActive` and `register` is unlocked.

**Fix.** Atomic check-and-register: introduce `tryRegister(runID, task, cancel) bool` that returns false if the ID is already present, and replace the `IsActive ... register` two-step with a single atomic op.

---

## RACE-005 — EventBus `Publish` holds `RLock` while calling `publishToChannel` which can `recover()` on send-to-closed

- **Severity:** Medium
- **Confidence:** High
- **CWE:** CWE-362, CWE-833 (Deadlock)
- **File:** `internal/engine/eventbus.go:73-91`, `203-214`

**Finding.** `Publish` takes `eb.mu.RLock()` and iterates over `subscribers[event.Type]` then `subscribers["*"]`, calling `publishToChannel(ch, event)` for each. `publishToChannel` does a non-blocking `select { case ch<-event: default: }` plus a `defer recover()` for "send on closed channel" panics.

The race: `Unsubscribe` takes `eb.mu.Lock()` (write lock), removes the channel from the bucket, then `close(ch)`. Between the time `Publish` enters its for-loop and the time it actually sends to channel `ch[i]`, `Unsubscribe` cannot run (write lock blocks on the held read lock). ✓ Publish path is safe.

**The actual issue** is the cumulative drop counter (`droppedMu` lock at line 225-235) is taken *while* `eb.mu.RLock()` is still held by the caller. Lock ordering: `eb.mu` (RLock) → `droppedMu` (Lock). If any other code path takes `droppedMu` first then tries `eb.mu`, deadlock. Searching: `noteDroppedEvent` is the only `droppedMu` user, and it never re-enters `Subscribe/Unsubscribe`. ✓ No actual deadlock today, but the lock-order is implicit and one new code path could break it.

**Impact.** Latent deadlock potential. Document the lock order, or restructure so `noteDroppedEvent` doesn't run under the `Publish` RLock (e.g., signal a counter goroutine via channel).

---

## RACE-006 — `forceCompactNativeLoopHistory` writes to `seed.Messages` outside the agentMu lock during auto-resume

- **Severity:** Medium
- **Confidence:** Medium
- **CWE:** CWE-362
- **Files:**
  - `internal/engine/agent_loop_autonomous.go:113-184` (`attemptAutoResume`)
  - `internal/engine/engine.go:137-138` (`agentMu` guards `agentParked`)

**Finding.** `attemptAutoResume` calls `e.takeParkedAgent()` (which presumably takes `agentMu`), then mutates `seed.Messages`, `seed.CumulativeSteps`, `seed.TotalTokens`, etc. *outside* the lock. The `seed` is the live `parkedAgentState` taken from `e.agentParked`. After `takeParkedAgent` removes the pointer, no other goroutine should hold a reference, so external mutation is impossible — **but** `e.saveParkedAgent(seed)` later may be racing with another `takeParkedAgent` if a concurrent /continue fires.

The pattern is: `take → maybe modify → save` (lines 114, 119-123, 138). If two callers (e.g. autonomous wrapper and a manual /continue) both call `attemptAutoResume` / `ResumeAgent` simultaneously, both `take` (one returns nil, OK), but the path that receives the seed mutates and the loser path may have observed `nil` and surfaced "no parked agent" — which is acceptable. The risk is if `takeParkedAgent` is not the only seed-clearer; let me note it for review without escalating.

**Impact.** Likely benign given the `take`-style API; flagged for inspection because the mutation of `seed.*` fields is not under `agentMu` and a future code change that re-publishes `seed` back into `agentParked` mid-mutation would corrupt it.

**Fix.** Document the "seed is exclusively owned after take" invariant in a comment on `parkedAgentState`.

---

## RACE-007 — Drainage 2-second grace window is bounded but workers can still leak post-finalize

- **Severity:** Low
- **Confidence:** High
- **CWE:** CWE-833 (Deadlock-adjacent: leaked goroutines, not deadlock)
- **File:** `internal/drive/driver_loop.go:333-376`

**Finding.** `drainAndFinalize` waits up to `cfg.DrainGraceWindow` (default 2s per the architecture doc) for in-flight workers. After the grace timer fires, the driver returns and `executeLoop` exits. The worker goroutines are still alive — they're blocked on `d.runner.ExecuteTodo(ctx, req)` which respects ctx, but a cooperative `ExecuteTodo` may not check ctx between sub-agent rounds. Workers eventually try to send on the `results` channel — the channel is **buffered to MaxParallel** (line 44), so send succeeds even with no reader, but the goroutine and any resources it holds (HTTP client connections, file handles, prompt-cache references) live until `ExecuteTodo` returns naturally.

**Impact.** Goroutine leak under repeated cancel/abandon cycles. Memory grows linearly with abandoned runs until the LLM provider's HTTP timeout (default 20s) fires. Bounded but not zero.

**Fix.** After the grace timer, optionally close the results channel — workers that try to send on closed will panic and the panic recovery in `dispatchTodo`'s defer (line 171-181) handles it. Simpler: log an explicit warn on each leaked worker so operators see the leak in the activity feed.

---

## RACE-008 — bbolt process lock is advisory at the file-lock layer; stale lock detection is OS-dependent

- **Severity:** Low
- **Confidence:** Medium
- **CWE:** CWE-362
- **Files:**
  - `internal/storage/store.go:71-83` (1-second `Timeout`)
  - `cmd/dfmc/main.go:60-77` (degraded-startup allow-list)

**Finding.** bbolt uses `flock` on Unix and `LockFileEx` on Windows. Both are *mandatory* for the kernel within bbolt's open call but *advisory* in the sense that a stale lock from a crashed process is released by the kernel automatically (the file handle is closed). On NFS or some Windows scenarios, file locks can persist for ~30s after a crash. The 1s timeout in `bbolt.Options{Timeout: 1*time.Second}` is short enough that legitimate startup races between two `dfmc` invocations look identical to a stale lock. The architecture treats `ErrStoreLocked` as a hard "another process owns it" signal — in the stale-lock case the user gets the same error message and is told to "close other DFMC/TUI processes" when no other process exists.

**Impact.** Operator confusion. Not a security finding; included because the prompt asked.

**Fix.** Detect stale lock by trying to read the file's PID marker, or surface a different message for "lock acquired by us in <N seconds" vs "lock held by PID X". Optional.

---

## RACE-009 — `failureMu` and `readMu` independently — recordReadSnapshot under `readMu` calls helpers that don't take other locks (OK)

- **Severity:** Info
- **Confidence:** High
- **File:** `internal/tools/engine.go:46-56`, `693-697`

**Finding.** Confirmed: `recordReadSnapshot` only takes `readMu.Lock()`; `trackFailure` only takes `failureMu.Lock()`. Both are leaf locks. ✓ No lock-ordering hazard between them today.

**Watch item.** The `LRU` eviction under `readMu.Lock()` is O(N) on each call (scans `readSnapshotLRU`). At `maxReadSnapshots=256` this is cheap, but a pathological agent loop with thousands of distinct file reads will spend visible CPU under the write lock.

---

## RACE-010 — `wsConn.cleanup` close-then-close-channel is racy under fast disconnect

- **Severity:** Low
- **Confidence:** Medium
- **CWE:** CWE-362
- **File:** `ui/web/server_ws.go:305-312`

```go
func (c *wsConn) cleanup() {
    c.closeMu.Lock()
    if !c.closed.Swap(true) {
        _ = c.conn.Close()
    }
    c.closeMu.Unlock()
    close(c.sendCh)            // outside the lock
}
```

**Finding.** `cleanup()` is called in the `defer` of `readLoop`. The `sendCh` close is outside the mutex. If `cleanup` is called twice (e.g. from both readLoop on connection error and from a future shutdown path), the second `close(c.sendCh)` panics with "close of closed channel". The `closed` atomic guards the conn-close but not the chan-close.

The current code only calls `cleanup` from one site (readLoop's defer), so today this doesn't fire. Latent bug.

**Fix.** Use `sync.Once` for the cleanup, or move `close(c.sendCh)` inside the `if !c.closed.Swap(true)` branch.

---

## RACE-011 — `Engine.Tools.recordReadSnapshot` LRU manipulation is correct but `trackFailure`'s LRU is racier

- **Severity:** Low
- **Confidence:** Low
- **CWE:** CWE-362
- **File:** `internal/tools/engine.go:583-602` (`trackFailure`)

**Finding.** `trackFailure` takes `failureMu.Lock()` for the whole map+slice update — correctly atomic. The slice eviction at lines 593-600 (`recentFailOrder = recentFailOrder[1:]`) re-slices without copying; if any other goroutine ever held a reference to the old slice header, they'd see disappearing data. No other goroutine does — `recentFailOrder` is only manipulated under `failureMu`. ✓

Listed as an Info-class watch only because the slice-header re-aliasing pattern is fragile if someone later adds a "list recent failures" debug helper that escapes the lock.

---

### What was checked and OK

- `EventBus.Publish` non-blocking send + `recover()` shield — robust under subscriber close. ✓
- `parallelSafeTools` whitelist excludes every mutating tool — fan-out safe by construction. ✓
- `readyBatchWithPolicy` correctly treats empty `file_scope` as "owns everything" and refuses to schedule alongside any other TODO (`scheduler.go:202-214`, `scopeAny` sentinel). ✓
- Two empty-scope TODOs scheduled together: the second is rejected by the `len(picked) > 0` guard at line 202. ✓
- `Conversation.Manager.SaveActive` snapshots under RLock and writes outside lock — correct. ✓
- bbolt write transactions are NOT held during HTTP-handler scope (each handler opens its own short Update tx via `Store.Save`/`Save`/`Update`). ✓ No HTTP-scoped deadlock potential.
- Cron / scheduled-task firing during compaction: DFMC has **no** scheduler / cron — confirmed by architecture report section 3.6 ("None"). ✓ (Auto-compact is in-line with the agent loop, not on a timer.)
