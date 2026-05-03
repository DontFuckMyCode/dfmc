# sc-business-logic + sc-race-condition Results

## Findings

### [High] Data race: `Load()` / `LoadReadOnly()` unlock before writing to `m.active`

- **File**: `internal/conversation/manager.go:417` and `internal/conversation/manager.go:429`
- **Description**: Both `Load(id)` and `LoadReadOnly(id)` take `m.mu.RLock()`, read the conversation from the store, release the lock, then assign to `m.active` without holding any lock. Textbook data race: a concurrent reader of `Active()` (which takes `RLock`) can observe `m.active` mid-assignment (partial pointer write on 64-bit architectures) or stale value while another goroutine writes the new value.
- **Impact**: One goroutine calling `Load()` while another calls `Active()` or `AddMessage()` can see a corrupted or stale `*Conversation` pointer, leading to incorrect conversation context in tool calls or a crash if the pointer fields are inconsistent.
- **Evidence**:
  ```go
  // Load — line 417
  m.mu.RLock()
  store := m.store   // store is immutable for lifetime of Manager
  m.mu.RUnlock()
  // ... load from store ...
  m.mu.Lock()
  m.active = c       // DATA RACE: no lock held here
  m.mu.Unlock()
  return cloneConversation(c), nil
  ```
  Same pattern at `LoadReadOnly` line 429.
- **Mitigation**: Hold `m.mu.Lock()` (not RLock) for the entire duration of the swap:
  ```go
  m.mu.Lock()
  c, err := m.loadFromStore(id)
  if err != nil { m.mu.Unlock(); return nil, err }
  m.active = c
  m.mu.Unlock()
  ```

---

### [High] TOCTOU in `apply_patch`: stat check and write not under same per-path lock

- **File**: `internal/tools/apply_patch.go:97-119`
- **Description**: For each file in the patch, `os.Stat(abs)` decides whether to gate through `EnsureReadBeforeMutation` (only for existing files). `LockPath(abs)` is only acquired later, just before `writeFileAtomic`. Between the stat check and lock acquisition, a concurrent goroutine (or external process) can delete the file and recreate it with different content. When `writeFileAtomic` executes, it silently writes to the new file — bypassing the read-before-mutate gate entirely.
- **Impact**: A concurrent `edit_file` or external write to the same path between the stat check and the lock could result in a patch silently overwriting a file that was modified after the model's read snapshot, without triggering the "file changed" refusal.
- **Evidence**:
  ```go
  if _, statErr := os.Stat(abs); statErr == nil {
      if !dryRun {
          if guardErr := t.engine.EnsureReadBeforeMutation(abs); guardErr != nil {
              return Result{}, fmt.Errorf(...)
          }
      }
  } // window: file can be deleted + recreated here
  release := t.engine.LockPath(abs)  // lock acquired AFTER stat gate
  defer release()
  if err := writeFileAtomic(abs, []byte(updated), 0o644); err != nil {
      // write goes to the post-recreation file
  }
  ```
- **Mitigation**: Move `LockPath` acquisition before the stat gate, so the entire stat-check + write sequence is serialized per path. Alternatively, re-stat after acquiring the lock and refuse if the file was replaced.

---

### [Medium] Concurrent `Persist()` on `memory.Store` can silently lose writes

- **File**: `internal/memory/store.go:99-119`
- **Description**: `Persist()` takes an RWMutex read lock, snapshots `s.working`, releases the lock, then calls bbolt `Update`. If two goroutines call `Persist()` concurrently (or one races with `SetWorkingQuestionAnswer`/`TouchFile`/`TouchSymbol`), both snapshot `s.working` under shared read locks, then both perform their bbolt `Put` sequentially. The second `Put` overwrites the first with identical data — masking concurrent in-memory mutations that were never persisted. Lost-update race.
- **Impact**: When concurrent goroutines persist (e.g. background persist racing with shutdown persist), the resulting bbolt state is one of the two snapshots, not the union. Data silently lost.
- **Evidence**:
  ```go
  func (s *Store) Persist() error {
      s.mu.RLock()
      wm := WorkingMemory{...}              // snapshot of live state
      s.mu.RUnlock()
      return s.storage.DB().Update(func(tx *bbolt.Tx) error {
          return b.Put([]byte(bucketWorkingKey), data)  // last writer wins
      })
  }
  ```
- **Mitigation**: Use a write lock for the entire Persist operation (including the bbolt transaction), or serialize Persist calls with a dedicated mutex / make Persist idempotent via a dirty flag.

---

### [Low] `memory.Store.Add` microsecond-precision ID collision under extreme scheduling

- **File**: `internal/memory/store.go:131-137`
- **Description**: When two goroutines call `Add` within the same microsecond and both provide empty entry IDs, `entry.ID` is constructed from `time.Now().Format("20060102_150405.000000")` + 6-byte random suffix. The random suffix reduces collision probability but does not eliminate it (~2^-48 per call). Comment in code explicitly acknowledges this.
- **Impact**: Extremely unlikely in practice (requires precise scheduling within same microsecond). If it occurs, second `Put` silently overwrites the first — data loss.
- **Mitigation**: Use a bbolt transaction sequence number or UUID generation instead of timestamp-based IDs.

---

### [Low] BackupTo: TOCTOU between temp-file creation and atomic rename

- **File**: `internal/storage/store.go:197-220`
- **Description**: M5 fix replaced predictable temp names with `os.CreateTemp` (safe against pre-created symlink attacks). However, window between temp-file creation and `os.Rename` still exists. On Windows, `os.CreateTemp` uses `GetTempFileName` which is not cryptographically random, making the Windows path more vulnerable.
- **Impact**: Local privilege escalation if attacker can predict temp path and win race before rename commits. Requires winning a race against the single writer and knowing the random temp name.
- **Mitigation**: On Windows, consider using a cryptographically random temp name or defer temp file cleanup to a best-effort cleanup path. The atomic rename itself is safe once temp file is created.

---

## Positive Observations

The codebase demonstrates strong security hygiene in several areas:

- **`readSnapshots` map**: Protected by `sync.RWMutex` (concurrent reads, exclusive writes). `pathLocks sync.Map` serializes read-gate-to-write windows per file.
- **bbolt usage**: `UpdateTaskCAS` uses optimistic concurrency control with version bumping — correctly prevents lost updates for task mutations.
- **Conversation saves**: `SaveActiveAsync` correctly clones the conversation before releasing the lock, and `Close()` waits for in-flight saves before shutting down bbolt.
- **EventBus**: Uses `sync.RWMutex` for subscribers, `atomic` for drop counters, and `recover()` per subscriber goroutine to prevent cascading failures.
- **`apply_patch` per-file locking**: `LockPath` correctly acquired per target file before `writeFileAtomic`, preventing inter-file races.
- **`engine.activeSkills`**: Set during prompt building (single-threaded pre-loop path), read during tool execution. No concurrent modification races.
- **Intent router**: Stateless between Evaluate calls — no shared state concurrency issues.
- **`hooks.Dispatcher`**: Sequential hook execution with `recover()` per hook dispatch.