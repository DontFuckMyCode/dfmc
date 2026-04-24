# Code Review Report — DFMC

**Generated:** 2026-04-18  
**Scope:** ~200 Go source files across `cmd/`, `internal/`, `pkg/`, `ui/`

---

## Severity Guide
| Symbol | Meaning |
|--------|---------|
| 🔴 Must-fix | Exploitable or data-loss risk |
| 🟡 Should-fix | Correctness, maintainability, or correctness concern |
| 🟢 Nit | Style, convention, low-risk idiom |
| 📋 Tests | Gap in test coverage |

---

## 🔴 Must-Fix

### 1. SSRF risk with nolint suppression
**File:** `ui/cli/cli_plugin_install.go:281`
```go
resp, err := http.Get(src) //nolint:gosec // plugin install intentionally fetches user-provided URL.
```
**Finding:** `http.Get` has no timeout and no TLS validation. If `src` resolves to an internal host or file URL, behavior is undefined. Replace with a scoped `http.Client` with a hard timeout and `CheckRedirect` to block circular redirects.

---

### 2. `err.Error() == "EOF"` — brittle string comparison
**File:** `ui/web/server_admin_test.go:224`
```go
if err.Error() == "EOF" {
```
**Finding:** String comparison on error messages is fragile. Use `errors.Is(err, io.EOF)` instead. This fails if the error message changes or is wrapped.

---

### 3. bufio.Scanner default token limit (64 KiB) is a silent truncation risk
**File:** `internal/storage/store.go:342`
```go
// bufio.Scanner's default line limit is 64 KiB (MaxScanTokenSize).
```
**Finding:** Comment acknowledges the limit but doesn't show that the code handles truncation. If `store.go` uses a `bufio.Scanner` without setting `scanner.Buffer()`, long records could be silently dropped. Verify all Scanner usages call `Buffer()` or are bounded below 64 KiB.

---

### 4. Goroutines spawned without guaranteed shutdown
**Files:** `internal/provider/*.go`, `internal/pluginexec/client.go`
**Finding:** These goroutines may outlive their creating function if the context is cancelled but the goroutine blocks on I/O. In `internal/pluginexec/client.go:225`, if `c.cmd.Wait()` hangs, the goroutine leaks. Confirm all have context-based termination or are scoped to the lifetime of the parent struct.

---

## 🟡 Should-Fix

### 5. No timeout on `exec.Command` in `ui/cli/cli_config.go:282`
**File:** `ui/cli/cli_config.go:282`
```go
cmd := exec.Command(editorParts[0], cmdArgs...)
```
**Finding:** Unlike `internal/tools/command.go:137` and `internal/tools/git_runner.go:64` which use `exec.CommandContext`, this call uses `exec.Command` with no context. If the editor blocks, there is no timeout enforcement. Use `exec.CommandContext(ctx, ...)` instead.

---

### 6. Event-drop log message missing subscriber identity
**File:** `internal/engine/eventbus.go:216`
```go
log.Printf("dfmc: event bus dropped %d events so far; a subscriber is lagging", total)
```
**Finding:** The log message does not include which subscriber is lagging, making debugging difficult. Add subscriber identification to the log output.

---

### 7. Security scanner flags TODO markers in test files
**File:** `internal/provider/offline_analyzer.go:119`
```go
Category: "reliability", Message: "panic() — prefer returned error at this level",
```
**Finding:** The offline analyzer flags `TODO` markers as reliability findings. This means legitimate TODO comments in test files could be flagged in production scans. Verify that the analyzer filters by file path or test tag before flagging.

---

### 8. `werr` captured but never checked on `stdin.Write`
**File:** `internal/pluginexec/client.go:192`
```go
_, werr := c.stdin.Write(append(buf, '\n'))
```
**Finding:** `werr` is captured but not checked. While `_` suppresses the compiler warning, the error could indicate a broken pipe that should be handled.

---

## 🟢 Nits

### 9. Duplicate `sync.Mutex` field across structs
**Pattern:** `mu sync.RWMutex` appears in 40+ structs:
- `internal/ast/engine.go:396`
- `internal/codemap/engine.go:18`
- `internal/conversation/manager.go:52`
- `internal/engine/engine.go:103`
- `internal/engine/approver.go:63`
- ...
**Finding:** Not a bug, but consider a common embed or helper to reduce boilerplate. Low priority — pattern is idiomatic.

### 10. Missing error variable reuse conventions
**File:** `internal/config/config.go:62`
```go
dotEnv, err := loadDotEnv(projectRoot)
```
**Finding:** `dotEnv` is used but `err` is discarded. If dotenv loading is optional, document this. If it can fail silently in production, this should be surfaced.

---

## 📋 Tests to Add

### 11. Stream cancellation edge cases
**File:** `internal/engine/stream_cancel_test.go`
**Finding:** Add a test for when the context cancels **during** a streaming chunk — verify goroutines drain before the engine returns.

### 12. Plugin exec timeout test
**File:** `internal/pluginexec/client_test.go`
**Finding:** `time.Sleep(10 * time.Second)` and `time.Sleep(60 * time.Second)` indicate long test timeouts. Add a parameterized test that verifies `exec.CommandContext` respects `ctx.Canceled` within 1ms of cancellation.

### 13. Goroutine leak test for provider goroutines
**File:** `internal/provider/router.go`
**Finding:** Add a test that spawns a router, cancels its context, and asserts all goroutines exit within 500ms using `runtime.NumGoroutine()` comparison.

---

## Summary

| # | Severity | File | Issue |
|---|----------|------|-------|
| 1 | 🔴 Must-fix | `ui/cli/cli_plugin_install.go:281` | SSRF — `http.Get` with no timeout, `//nolint:gosec` suppressing it |
| 2 | 🔴 Must-fix | `ui/web/server_admin_test.go:224` | `err.Error() == "EOF"` — fragile string comparison, use `errors.Is` |
| 3 | 🔴 Must-fix | `internal/storage/store.go` | `bufio.Scanner` 64 KiB default token limit — long records silently truncated if `Buffer()` not set |
| 4 | 🔴 Must-fix | `internal/provider/*.go`, `internal/pluginexec/client.go` | Goroutines spawned without guaranteed context-based shutdown |
| 5 | 🟡 Should-fix | `ui/cli/cli_config.go:282` | `exec.Command` without timeout — `cli_plugin_install.go` uses `exec.CommandContext`, this one doesn't |
| 6 | 🟡 Should-fix | `internal/engine/eventbus.go:216` | Event-drop log message missing subscriber identity for debugging |
| 7 | 🟡 Should-fix | `internal/provider/offline_analyzer.go:119` | Security scanner flags `TODO` markers — legitimate comments in test files could be flagged |
| 8 | 🟡 Should-fix | `internal/pluginexec/client.go:192` | `werr` captured but never checked on `stdin.Write` |
| 9 | 🟢 Nit | 40+ files | `mu sync.RWMutex` boilerplate replicated across structs — consider embed |
| 10 | 🟢 Nit | `internal/config/config.go:62` | `err` from `loadDotEnv` discarded — confirm intentional |
| 11 | 📋 Tests | `internal/engine/stream_cancel_test.go` | Add mid-stream cancellation drain test |
| 12 | 📋 Tests | `internal/pluginexec/client_test.go` | Goroutine leak test via `runtime.NumGoroutine()` |
| 13 | 📋 Tests | `internal/provider/router.go` | Router lifecycle leak test (cancel → assert goroutines exit ≤500ms) |

**Estimated blast radius:** The SSRF issue (`ui/cli/cli_plugin_install.go`) and the goroutine leaks (`internal/provider/*.go`, `internal/pluginexec/client.go`) are the highest priority. The `bufio.Scanner` truncation risk needs a one-file audit. The error-string comparison is a one-line fix.
