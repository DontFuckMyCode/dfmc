# Code Review: DFMC Core Modules

**Date:** 2026-04-18  
**Files reviewed:**
- `internal/config/config_types.go`
- `internal/drive/driver_loop.go`
- `internal/drive/runner.go`

---

## 🔴 Must-Fix

### 1. `MCPServerConfig.Env` leaks secrets into structured logs

**File:** `internal/config/config_types.go:48`

```go
Env map[string]string `yaml:"env,omitempty"` // extra environment variables (API keys etc.)
```

The comment explicitly acknowledges API keys. Any `d.publish(...)` or `fmt.Sprintf` that touches `MCPServerConfig` fields will stringify this map. The `applyOutcome` publish at `driver_loop.go:282` does NOT include this field — but any future event or error message that interpolates the config struct will emit raw env values.

**Fix:** scrub env vars from any logging path; consider a `Secrets []string` field that tracks redacted keys by name rather than storing values.

---

### 2. `consecutiveBlocked` pointer aliasing in `applyOutcome`

**File:** `internal/drive/driver_loop.go:216`

```go
func (d *Driver) applyOutcome(run *Run, res todoOutcome, consecutiveBlocked *int) {
```

The pointer is incremented (`*consecutiveBlocked++`) and zeroed (`*consecutiveBlocked = 0`) inside `applyOutcome`. The caller in `executeLoop` reads the pointed value at the top of each iteration for the termination check. **This is correct**, but it relies on the pointer surviving the `applyOutcome` call. If `applyOutcome` were ever called without the pointer from the loop, the zeroing at line 270 would silently discard the count.

**Risk:** future callers could accidentally pass `&consecutiveBlocked` or a fresh zero value.

**Fix:** add a comment on `applyOutcome` that the pointer must reference the caller's live counter.

---

### 3. `run.Todos[idx]` index access is unchecked in `dispatchTodo`

**File:** `internal/drive/driver_loop.go:155`

```go
t := &run.Todos[idx]
```

The `picks` slice from `readyBatchWithPolicy` should guarantee valid indices, but `readyBatchWithPolicy` is not visible in this file — its correctness is an implicit contract. A bug there would cause a panic in `dispatchTodo` (not caught by the goroutine's `recover`).

**Fix:** add a bounds check before the pointer dereference:
```go
if idx < 0 || idx >= len(run.Todos) { return }
```

---

## 🟡 Should-Fix

### 4. `ContextLifecycleConfig` fields lack defaults documentation

**File:** `internal/config/config_types.go:195-199`

```go
type ContextLifecycleConfig struct {
    Enabled bool `yaml:"enabled"`
    AutoCompactThresholdRatio float64 `yaml:"auto_compact_threshold_ratio"`
    KeepRecentRounds int `yaml:"keep_recent_rounds"`
}
```

`AgentConfig.ContextLifecycle` embeds this, but unlike other `AgentConfig` fields (e.g. `MetaCallBudget: 0 → 64`), there are no inline comments indicating what zero values mean. If the intent is `Enabled: false` on zero and a sensible threshold like `0.8`, document it.

---

### 5. `ExecuteTodoRequest.AllowedTools` is copied defensively in `dispatchTodo`

**File:** `internal/drive/driver_loop.go:163`

```go
AllowedTools: append([]string(nil), t.AllowedTools...),
```

This is intentional (avoiding shared slice mutation), but `Skills` and `Labels` are also copied while `Brief`, `Title`, and `Detail` are not. This asymmetry is not documented — a future reader might add a slice field without the defensive copy.

**Fix:** add a comment grouping the copied fields.

---

### 6. `BeginAutoApprove` return type undocumented

**File:** `internal/drive/runner.go:120-124`

```go
BeginAutoApprove(tools []string) func()
```

The return is an unnamed `func()`. Callers must know it takes no arguments and returns nothing. **Fix:** add an inline comment:
```go
// BeginAutoApprove returns a release function; call it to revoke the override.
BeginAutoApprove(tools []string) func()
```

---

## 🔵 Tests to Add

| Gap | File | Evidence |
|-----|------|----------|
| `ContextLifecycleConfig` not exercised in any test | `internal/config/config_types.go` | no config tests found |
| `MCPConfig` / `MCPServerConfig` not instantiated in tests | `internal/config/config_types.go` | no `config_test.go` at all |
| `drainAndFinalize` grace-timer path not covered (inFlight > 0 branch) | `internal/drive/driver_loop.go:332-350` | panic recover tested, grace timer not |
| `ReadyBatch` behavior with mixed `TodoPending`/`TodoRunning` not isolated | `internal/drive/driver_loop.go` | only integration-level parallel tests |

---

## 📝 Nits

| # | File | Issue |
|---|------|-------|
| 1 | `runner.go:52` | `ExecuteTodoRequest.RoutingRules` comment says "rule-matched profiles" but type is `[]config.RoutingRule`. Verify this matches what `providerForTag` expects. |
| 2 | `driver_loop.go:61` | `deadline` recomputed on resume; if the original wall time already passed, the resumed deadline is identical to original start time + MaxWallTime. This is correct but worth a comment clarifying the semantics. |
| 3 | `config_types.go` | `IntentConfig.FailOpen` comment says "default `true`" but the field has no tag default. Confirm calling code initializes `FailOpen: true` in `DefaultConfig`. |

---

## Summary

| Severity | Count |
|----------|-------|
| 🔴 Must-Fix | 3 |
| 🟡 Should-Fix | 3 |
| 🔵 Tests missing | 4 |
| 📝 Nits | 3 |

**Highest-confidence risk:** #1 (secret leakage) — direct path from config to log output.

**#2 and #3** are latent correctness risks that have not yet manifested (30 tests pass), but the invariants they depend on are fragile under future modification.
