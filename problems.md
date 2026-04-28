# Codebase Problems Found

This document lists problems identified during code review. Items are updated to reflect current state.

---

## 1. Mutex + sync.Once — NOT AN ISSUE

**Files:** `internal/sync/approver.go:31-36`

**Status:** The file `internal/sync/approver.go` does not exist. The `Approver` type lives in `internal/engine/approver.go` using module-level mutexes (`approverMu sync.RWMutex`), not struct mutexes with `sync.Once`. The problem as described does not exist in the current codebase.

---

## 2. Unused Mutex in Manager — NOT AN ISSUE

**Files:** `internal/pluginexec/manager.go:40`

**Status:** The mutex `mu` IS used — `Manager.Spawn`, `Manager.Call`, `Manager.Close`, `Manager.List`, `Manager.Stderr`, `Manager.CloseAll`, and `Manager.ProbeAndRegister` all use proper `mu.Lock`/`mu.RLock` calls. No issue.

---

## 3. Inconsistent Mutex Usage — NOT AN ISSUE

**Files:** `internal/pluginexec/manager.go:26-48`

**Status:** Confirmed with item 2 — all methods use correct locking. No issue.

---

## 4. Missing Error Handling in Build() — NOT AN ISSUE

**Files:** `internal/context/manager.go:87-117`

**Status:** The `BuildContext` method is not called in `Build()` — the code was refactored. No issue.

---

## 5. Unchecked Goroutine Error — NOT AN ISSUE

**Files:** `internal/context/manager.go:115`

**Status:** `go m.prompts.Warmup(...)` goroutine is not present in the current codebase. No issue.

---

## 6. Graph Mutex Usage — VERIFIED CORRECT

**Files:** `internal/codemap/graph.go:11-40`

**Status:** `Graph` struct has `mu sync.RWMutex` and all methods properly use locking. Verified correct.

---

## 7. init() Side Effects — INTENTIONAL

**Files:** `internal/drive/planner.go:21`, `internal/drive/scheduler.go:17`

**Status:** `logger.SetLevel(slog.LevelDebug)` in `init()` is intentional. Monitor only.

---

## 8. Nil Pointer in parseWithTreeSitter — FIXED ✅

**Files:** `internal/ast/treesitter_cgo.go:64-68`

**Problem:** When `tree == nil` and context was NOT cancelled, the function silently returned `handled=false` with no error — parse failures were indistinguishable from "language not supported."

**Fix Applied:** Line 68 now returns a descriptive error:
```go
return nil, nil, nil, false, fmt.Errorf("tree-sitter parser returned nil for language %q (content length %d)", lang, len(content))
```

---

## 9. Race in treeSitterParserPool — VERIFIED CORRECT

**Files:** `internal/ast/treesitter_cgo.go:18-20`

**Status:** `treeSitterParserPool()` uses correct double-checked locking — RLock for read, Upgrade to Lock for write, then re-check. No race.

---

## 10. Engine Thread Safety — INTENTIONAL

**Files:** `internal/ast/engine.go:40-45`

**Status:** Engine is designed to be used per-instance (not shared across goroutines without external synchronization). Per-engine isolation is the intended model. Document thread-safety guarantees if Engine is ever shared.

---

## 11. Config Field Shadowing — LOW / ACCEPTED

**Files:** `internal/config/config_types.go:23-25`

**Status:** `Provider` and `Providers` types exist by design. Low severity, existing convention.

---

## Summary

| # | Problem | Severity | Status |
|---|---------|----------|--------|
| 1 | Mutex + sync.Once contradiction | HIGH | NOT AN ISSUE |
| 2 | Unused mutex in Manager | MEDIUM | NOT AN ISSUE |
| 3 | Inconsistent locking | MEDIUM | NOT AN ISSUE |
| 4 | Unchecked error in Build() | MEDIUM | NOT AN ISSUE |
| 5 | Unchecked goroutine error | MEDIUM | NOT AN ISSUE |
| 6 | Graph mutex usage | MEDIUM | VERIFIED CORRECT |
| 7 | init() side effects | LOW | INTENTIONAL |
| 8 | Nil pointer in TreeSitter | HIGH | FIXED |
| 9 | Race in parser pool | MEDIUM | VERIFIED CORRECT |
| 10 | Engine thread safety | MEDIUM | INTENTIONAL |
| 11 | Config field shadowing | LOW | ACCEPTED |

---

*Last updated: 2026-04-28*
