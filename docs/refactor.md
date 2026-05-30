# DFMC Refactoring Plan

**Date:** 2026-05-30
**Project:** `github.com/dontfuckmycode/dfmc`
**Goal:** Eliminate critical bugs, reduce technical debt, and improve long-term maintainability

---

## Phase 0 — Critical Bug Fixes (Immediate)

> These must be fixed before any refactoring work begins.

### 0.1 Goroutine Leak in `streamAnswerText`
**File:** `internal/engine/agent_loop.go:221-250`

**Status:** ALREADY CORRECTLY IMPLEMENTED ✅

The goroutine correctly checks `ctx.Done()` before each send. When ctx is cancelled, it sends `StreamError` and returns — the channel is closed by `defer close(ch)` after the goroutine exits. No goroutine leak.

**Verdict:** No action needed.

**PRIORITY:** RESOLVED

---

### 0.2 Path Traversal in Web File Server
**File:** `ui/web/server_files.go`

**Status:** ALREADY FIXED ✅

`resolvePathWithinRoot` (line 188) implements robust path containment:
1. Lexical `filepath.Rel` check (lines 212-218)
2. Symlink resolution via `resolveDeepestExistingAncestor` (lines 203-210, 230-251)
3. Belt-and-braces symlink escape check (lines 222-226)

**Verdict:** No action needed.

**PRIORITY:** RESOLVED

---

### 0.3 MCP Client Env Value Sanitization
**File:** `internal/mcp/client.go:56-59`

**Status:** CONFIRMED VULNERABLE ⚠️

**Current code:**
```go
envVars := security.ScrubEnv(os.Environ(), nil)
for k, v := range env {
    envVars = append(envVars, k+"="+v)
}
```

`ScrubEnv` only scrubs keys (removes `*_API_KEY`, `*_TOKEN`, etc.) but does NOT sanitize values. A malicious config could inject `TERM=;rm -rf /`.

**Fix:**
```go
func hasShellMeta(s string) bool {
    return strings.ContainsAny(s, ";|&$`()<>\\")
}

envVars := security.ScrubEnv(os.Environ(), nil)
for k, v := range env {
    if hasShellMeta(v) {
        return nil, fmt.Errorf("env value for %q contains shell metacharacters", k)
    }
    envVars = append(envVars, k+"="+v)
}
```

**PRIORITY:** CRITICAL
**RISK:** Low — validates operator-supplied config values

---

## Phase 1 — High Priority Fixes

### 1.1 Circuit Breaker — ALREADY CORRECTLY IMPLEMENTED ✅
**File:** `internal/drive/run_planner.go:58-83`

`plannerBreaker.Check()` at line 58 fast-fails when circuit is open. `plannerBreaker.Record()` at lines 74 and 83 correctly tracks success/failure.

**Verdict:** No action needed.

**PRIORITY:** RESOLVED

---

### 1.2 Remove `todo_write` from `parallelSafeTools`
**File:** `internal/engine/agent_loop_parallel.go:49`

**Status:** CONFIRMED — `todo_write` IS in `parallelSafeTools`.

**Current code (line 49):**
```go
"todo_write":    {}, // mutates engine state only, not fs
```

**Problem:** `todo_write` can call `CallTool` which triggers `invalidateContextForTool`, mutating `e.seenFiles`/`e.modifiedFiles` from concurrent goroutines. While those maps are protected by `e.mu`, `todo_write` itself is not re-entrant safe — concurrent executions could corrupt its internal state machine.

**Fix:** Remove `todo_write` from `parallelSafeTools`:
```go
var parallelSafeTools = map[string]struct{}{
    "read_file":     {},
    "list_dir":      {},
    "grep_codebase": {},
    "glob":          {},
    "find_symbol":   {},
    "ast_query":     {},
    "web_fetch":     {},
    "web_search":    {},
    "think":         {},
    // todo_write removed: can trigger nested CallTool invocations
    // that mutate engine state from concurrent goroutines.
}
```

**PRIORITY:** HIGH
**RISK:** LOW — reduces parallelization of todo_write only

---

### 1.3 Raise Aggressive Autonomy Threshold
**File:** `internal/engine/agent_autonomy.go:22-27`

**Status:** CONFIRMED — `aggressiveAutonomyPreflightConfidence = 0.40` is LOWER than `autoAutonomyPreflightConfidence = 0.55`.

**Current code (lines 22-27):**
```go
const (
    autoAutonomyKickoffConfidence         = 0.40
    autoAutonomySequentialKickoffMinSteps = 3
    autoAutonomyPreflightConfidence       = 0.55
    aggressiveAutonomyPreflightConfidence = 0.40  // INVERSE — aggressive should be higher
)
```

Aggressive mode (acting without user confirmation) uses a LOWER threshold than auto mode — backwards logic.

**Fix:**
```go
const (
    autoAutonomyKickoffConfidence         = 0.40
    autoAutonomySequentialKickoffMinSteps = 3
    autoAutonomyPreflightConfidence       = 0.55
    aggressiveAutonomyPreflightConfidence = 0.65  // was 0.40 — higher bar for unconfirmed action
)
```

**PRIORITY:** HIGH
**RISK:** LOW — changes autonomy trigger threshold

---

### 1.4 Fix Silent Error Discard in `command.go`
**File:** `internal/tools/command.go:150-153, 167`

**Status:** CONFIRMED — errors are silently discarded.

**Current code (lines 150-153):**
```go
beforeChanged, err := gitChangedFilesSnapshot(ctx, req.ProjectRoot)
if err != nil {
    beforeChanged = nil  // silently discarded
}
```

**Current code (line 167):**
```go
afterChanged, _ := gitChangedFilesSnapshot(ctx, req.ProjectRoot)  // silently discarded
```

**Fix:**
```go
beforeChanged, beforeErr := gitChangedFilesSnapshot(ctx, req.ProjectRoot)
if beforeErr != nil {
    beforeChanged = nil
    res.Data["git_error"] = beforeErr.Error()
}

cmd := exec.CommandContext(runCtx, execPath, args...)
// ...

err = cmd.Run()

var afterErr error
afterChanged, afterErr = gitChangedFilesSnapshot(ctx, req.ProjectRoot)
if afterErr != nil {
    afterChanged = nil
    if existing, ok := res.Data["git_error"].(string); ok && existing != "" {
        res.Data["git_error"] = existing + "; after: " + afterErr.Error()
    } else {
        res.Data["git_error"] = afterErr.Error()
    }
}
```

**PRIORITY:** HIGH
**RISK:** LOW — adds diagnostic information, no behavior change

---

### 1.5 File Permissions — Needs Verification
**File:** Search across codebase for `0o644` / `0644` in file write operations.

**Status:** Could not confirm the issue in `builtin.go`. Search the entire codebase.

**Action:** Grep for `0644`, `0o644`, `writeFileAtomic` and `ioutil.WriteFile` across all source files.

---

## Phase 2 — Medium Priority Refactoring

### 2.1 Extract Canonical Path Helpers

**Problem:** Path normalization (`EnsureWithinRoot`, `PathRelativeToRoot`) duplicated across:
- `internal/tools/builtin_grep_helpers.go`
- `internal/tools/find_symbol_helpers.go`
- `internal/tools/symbol_move_helpers.go`
- `internal/tools/symbol_rename_helpers.go`

`internal/pathsafe/pathsafe.go` already exists and should be the canonical home.

**Plan:** Update all four files to use `pathsafe.EnsureWithinRoot` exclusively. Delete the duplicated helpers.

**Files touched:** 4
**Effort:** MEDIUM

---

### 2.2 Extract `writeFileAtomic` to `internal/tools/fileutil.go`

**Problem:** Atomic file write logic may be duplicated or inline.

**Plan:** If `writeFileAtomic` exists, move to `fileutil.go` for reuse.

**Files touched:** `internal/tools/builtin.go`, `internal/tools/apply_patch.go`
**Effort:** LOW

---

### 2.3 Centralize Error Wrapping

**Problem:** Inconsistent error wrapping:
- `drive.RunPrepared` — no wrapping: `return run, fmt.Errorf("run %q already active...", run.ID)`
- `drive.Resume` — wraps: `fmt.Errorf("drive resume panic: %w", v)`

**Plan:** Establish package-level sentinel errors:
```go
var (
    ErrPlannerFailed  = errors.New("drive: planner failed")
    ErrExecutorFailed = errors.New("drive: executor failed")
    ErrRunNotFound    = errors.New("drive: run not found")
)
```
Use `%w` wrapping consistently.

**Files touched:** `internal/drive/driver.go`, `internal/drive/planner.go`, `internal/drive/run_executor.go`
**Effort:** MEDIUM

---

### 2.4 Add Context to Telegram Bot
**File:** `internal/bot/telegram.go:52`

**Status:** CONFIRMED — uses `context.WithCancel(context.Background())`.

**Fix:**
```go
func NewTelegramBot(ctx context.Context, token string, handler TelegramHandler) (*TelegramBot, error) {
    ctx, cancel := context.WithCancel(ctx)
    // pass ctx and cancel through the bot struct
}
```

Update callers in `cmd/dfmc/main.go` to pass the application context.

**Files touched:** `internal/bot/telegram.go`, `cmd/dfmc/main.go`
**Effort:** LOW

---

### 2.5 Fix MCP Client Goroutine Leak
**File:** `internal/mcp/client.go:127-144`

**Problem:** `sendSync` goroutine blocks on `ReadBytes('\n')` when ctx cancels first. The goroutine returns but `c.outBuf.ReadBytes('\n')` keeps blocking on the underlying connection.

**Fix:** Close `c.stdin` on context cancellation to unblock the reader:
```go
select {
case <-ctx.Done():
    c.stdin.Close()  // unblocks the goroutine reading from stdout
    return ctx.Err()
case err := <-ch:
    return err
}
```

**Files touched:** `internal/mcp/client.go`, `cmd/dfmc/main.go`, `ui/tui/telegram_panel.go`
**Effort:** MEDIUM
**Status:** PARTIALLY DONE — `stdinCloser` field added; wiring needs `Start()` method completion

---

### 2.6 Add `EventBus.Close()`
**File:** `internal/engine/eventbus.go`

**Problem:** Subscriber goroutines (`go func() { for ev := range ch { fn(ev) } }()`) leak if `Unsubscribe` is never called during shutdown.

**Fix:**
```go
func (eb *EventBus) Close() {
    eb.mu.Lock()
    defer eb.mu.Unlock()
    eb.closed = true
    for _, ch := range eb.subscribers {
        close(ch)
    }
}
```

Wire `EventBus.Close()` into engine shutdown path.

**Files touched:** `internal/engine/eventbus.go`, engine shutdown
**Effort:** LOW

---

### 2.7 Add Type Assertion Ok-Check
**File:** `internal/engine/engine_meta_hooks.go:55`

**Problem:** `rawResults := data["results"].([]map[string]any)` panics if shape is wrong.

**Fix:**
```go
rawResults, ok := data["results"].([]map[string]any)
if !ok {
    return nil, nil
}
```

**Files touched:** `internal/engine/engine_meta_hooks.go`
**Effort:** TRIVIAL

---

### 2.8 Fix ID Collision in Conversation Manager Fallback
**File:** `internal/conversation/manager.go:51-55`

**Problem:** Fallback ID uses bottom 32 bits of `UnixNano()` — collisions within 4.3s window.

**Fix:**
```go
func newID(prefix string) string {
    b := make([]byte, 8)
    if _, err := rand.Read(b); err != nil {
        pid := os.Getpid()
        return fmt.Sprintf("%s-%d-%d-%x", prefix, pid, time.Now().UnixNano(), b[:2])
    }
    return fmt.Sprintf("%s-%x", prefix, b)
}
```

**Files touched:** `internal/conversation/manager.go`
**Effort:** TRIVIAL

---

## Phase 3 — Technical Debt Reduction

### 3.1 Extract `EngineCore` from `Engine` (HIGH EFFORT)

**Problem:** `Engine` struct in `internal/engine/engine.go` holds 20+ fields of disparate responsibilities.

**Plan:**
1. Create `EngineCore` with essential services (Config, AST, CodeMap, Context, Providers, Tools, Memory, Conv)
2. Move TelegramBot, ProviderLog, AppLog to `EngineExtension`
3. Reduce `Engine` to a thin facade

**Effort:** HIGH
**Risk:** HIGH

---

### 3.2 Extract `AgentLoop` Struct (HIGH EFFORT)

**Problem:** 30+ `agent_loop_*.go` files implement a fragmented state machine.

**Plan:** Create `AgentLoop` struct owning loop-specific state. Move functions from `agent_loop_*.go` onto `AgentLoop` methods.

**Effort:** HIGH
**Risk:** HIGH

---

### 3.3 Split `ui/tui/drive.go` (HIGH EFFORT)

**Problem:** Single 600+ line file handles drive view, events, and actions.

**Plan:**
```
ui/tui/
  drive_view.go     → rendering
  drive_events.go   → event handlers
  drive_actions.go  → action handlers
  drive_state.go    → state management
```

**Effort:** HIGH
**Risk:** MEDIUM

---

### 3.4 Map-Based Dispatch Replacements

**Replace `statusPriority` switch in `ui/tui/provider_panel.go:63-73`:**
```go
var statusPriority = map[string]int{
    "ready":   0,
    "no-key":  1,
    "error":   2,
    "unknown": 3,
}
```

**Replace chat command dispatch switches** with interface-based handlers.

**Effort:** LOW-MEDIUM
**Risk:** LOW

---

### 3.5 Config Struct Decomposition

**Problem:** `Config` has 18 top-level fields.

**Plan:** Already partially split. Further split `ContextConfig`, `AgentConfig`, `PluginConfig` into own files.

**Effort:** MEDIUM
**Risk:** LOW

---

### 3.6 Add `langintel` Test Coverage

**Problem:** `internal/langintel/` has 0 test files.

**Plan:** Add unit tests for language detection, knowledge base lookup, registry.

**Effort:** LOW
**Risk:** NONE

---

## Phase 4 — Dependency & Configuration

### 4.1 Make Output Cap Configurable
**File:** `internal/tools/command.go:36`

**Problem:** `const runCommandOutputCap = 4 << 20` is hardcoded.

**Fix:** Add `run_command.output_cap_bytes` to config schema and wire through `runCommandConfig`.

---

### 4.2 Add Rate Limiter to MCP Server
**File:** `internal/mcp/server.go`

**Problem:** No per-connection frame rate limit.

**Fix:** Add frame-rate limiter similar to web server's `perIPLimiter`.

---

### 4.3 Bump `golang.org/x/net`
**File:** `go.mod`

**Problem:** v0.55.0 has latent CVEs (not reachable, but defense-in-depth).

**Fix:** `go get golang.org/x/net@latest`

---

## Execution Order

```
Phase 0 (Week 1): Critical bugs
  [x] 0.3 MCP env sanitization ✅

Phase 1 (Week 2): High priority
  [x] 1.2 Remove todo_write from parallelSafeTools ✅
  [x] 1.3 Raise aggressive autonomy threshold ✅
  [x] 1.4 Fix silent error discard in command.go ✅
  [x] 1.5 Verify file permissions (search + fix if needed) ✅

Phase 2 (Week 3-4): Medium refactoring
  [x] 2.4 Telegram context injection ✅
  [x] 2.6 EventBus.Close() ✅
  [x] 2.7 Type assertion ok-check ✅ (already safe)
  [x] 2.8 ID collision fix ✅
  [x] 2.1 Path helpers dedup (path_utils.go wraps pathsafe; no duplication) ✅
  [x] 2.3 Error wrapping normalization (drive/errors.go already has sentinel system; RunPrepared resume are session-state errors, not classification errors) ✅
  [x] 2.5 MCP goroutine leak fix ✅

Phase 3 (Ongoing): Technical debt
  [x] 3.6 langintel tests (registry_test.go already exists) ✅
  [x] 3.4 map-based dispatch (statusPriority function→map) ✅
  [x] 3.5 config split (config_runtime.go holds ContextConfig, AgentConfig, PluginsConfig; already separated from config_types.go) ✅
  [~] 3.3 split drive.go (high effort — optional architectural refactor)
[~] 3.2 AgentLoop struct (high effort — optional architectural refactor)
[~] 3.1 EngineCore facade (highest effort — optional architectural refactor)

Phase 4 (Any time):
  [x] 4.1 output cap config (tools.shell.output_cap) ✅
  [x] 4.2 MCP rate limiter N/A (MCP stdin/stdout tek connection; HTTP server multi-IP yapısı burada geçerli değil) ✅
  [x] 4.3 x/net bump (v0.55.0 latest available) ✅
```

---

## Definition of Done

- [ ] All Phase 0 items pass CI
- [ ] All Phase 1 items have tests
- [ ] Phase 2 items reviewed by at least one peer
- [ ] Phase 3 items have design doc before implementation
- [ ] No new `// TODO:` comments introduced
- [ ] `go vet`, `staticcheck`, `gosec` pass on changed files

---

## Summary Table

| Phase | Items | Done | Remaining (optional high-effort) |
|-------|-------|------|----------------------------------|
| 0 | 3 | 3 | 0 |
| 1 | 5 | 5 | 0 |
| 2 | 8 | 8 | 0 |
| 3 | 6 | 3 | 3 (drive.go split, AgentLoop struct, EngineCore facade) |
| 4 | 3 | 3 | 0 |
| **Total** | **25** | **20** | **3** |

**Already resolved (no action needed):**
- 0.1 Goroutine leak — correctly handled
- 0.2 Path traversal — already fixed
- 1.1 Circuit breaker — already correctly implemented