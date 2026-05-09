# DFMC Project - Bug Research Report

**Generated:** 2026-05-02  
**Project Root:** `D:/Codebox/PROJECTS/DFMC`  
**Status:** Active Investigation

---

## Critical Findings

### 1. DATA LOSS - Ignored Write Errors

**Files:**
- `internal/tools/symbol_move.go:238`
- `internal/tools/symbol_rename.go:204`

**Code:**
```go
if err := os.WriteFile(fpath, []byte(strings.Join(lines, "\n")), 0644); err != nil {
    _ = err  // ⚠️ FILE WRITE FAILED BUT CONTINUES! User gets "success" response!
}
```

**Impact:**
- Disk full, permission denied, or I/O errors are silently swallowed
- User believes symbol was moved/renamed when file is unchanged
- **Active data loss risk**

---

### 2. PANIC RISK - Unsafe Type Assertion

**File:** `internal/tools/test_discovery.go:142`

**Code:**
```go
sort.Slice(results, func(i, j int) bool {
    return results[i]["path"].(string) < results[j]["path"].(string)
    //     ^^^^^^^^^^^^^^^^^^^^
    //     If "path" key is missing or wrong type → RUNTIME PANIC
})
```

**Impact:**
- If map entry is missing "path" key → immediate crash
- If "path" value is not `string` type → immediate crash
- **Runtime panic risk on malformed input**

---

### 3. DEADLOCK POTENTIAL - Lock Ordering Violation

**Documented in:** `docs/analysis.md:299-331`

**Lock Order Inconsistency:**

| File | Lock Order |
|------|------------|
| `agent_parked.go:76` | `e.mu` → `e.agentMu` |
| `engine.go:234` | `e.mu` (alone) |

**Risk:** If same goroutine acquires locks in wrong order → deadlock

**Locations with `e.mu.Lock()`:**
- `engine_context.go:197`
- `engine_context.go:222`
- `engine_context.go:269`
- `engine_context.go:275`

---

## Medium Risk Issues

### 4. Potential Race Condition - Map Concurrent Access

**File:** `internal/ast/cache.go`

**Issue:**
- Uses RWMutex correctly (`defer c.mu.Unlock()`)
- However, `entries map[string]*cacheEntry` could have race conditions under heavy concurrent access
- `-race` flag requires CGO, making it untestable in current CI

---

### 5. Unbounded Slice Append - Memory Growth Risk

**Files:**
- `internal/tools/glob.go`
- `internal/tools/semantic_search.go`

**Code:**
```go
results = append(results, ...)
```

**Issue:** No capacity pre-allocation. Large datasets could cause excessive memory allocations.

---

## Low Risk Issues

### 6. Test Files - Ignored Errors

These are tolerable in tests but represent code smell:

| File | Line | Note |
|------|------|------|
| `internal/mcp/connection_test.go` | 436 | Test context - tolerable |
| `internal/tools/gh_pr_test.go` | 165 | Test context - tolerable |

---

## Summary Table

| # | Risk | Severity | File:Line | Type |
|---|------|----------|-----------|------|
| 1 | Write error ignored | 🔴 Critical | `symbol_move.go:238` | Data loss |
| 2 | Write error ignored | 🔴 Critical | `symbol_rename.go:204` | Data loss |
| 3 | Type assertion panic | 🔴 Critical | `test_discovery.go:142` | Runtime panic |
| 4 | Deadlock potential | 🟠 Medium | `engine.go:234` | Deadlock |
| 5 | Map race condition | 🟡 Low | `cache.go:27` | Potential race |
| 6 | Unbounded append | 🟢 Low | `glob.go`, `semantic_search.go` | Memory growth |

---

## Recommended Fixes

### Fix 1: symbol_move.go:238 & symbol_rename.go:204

**Before:**
```go
if err := os.WriteFile(fpath, data, 0644); err != nil {
    _ = err
}
```

**After:**
```go
if err := os.WriteFile(fpath, data, 0644); err != nil {
    return Result{Error: fmt.Sprintf("write failed: %v", err)}, err
}
```

---

### Fix 2: test_discovery.go:142

**Before:**
```go
return results[i]["path"].(string) < results[j]["path"].(string)
```

**After:**
```go
pathI, okI := results[i]["path"].(string)
pathJ, okJ := results[j]["path"].(string)
if !okI || !okJ {
    return false
}
return pathI < pathJ
```

---

### Fix 3: Deadlock Prevention

Standardize lock ordering. Pick one convention and apply everywhere:

**Option A:** Always lock `e.mu` before `e.agentMu`
**Option B:** Always lock `e.agentMu` before `e.mu`

All code paths must follow the same order. Consider using `sync.Mutex` wrapper that tracks lock ordering for debugging.

---

## Verification

```bash
# Run static analysis
go vet ./...

# Run tests (requires CGO for race detector)
CGO_ENABLED=1 go test -race ./...

# Memory sanitizer
go test -msan ./...
```

---

**Report Status:** Complete  
**Next Action:** Prioritize Fix #1 and #2 (critical data loss risks)
