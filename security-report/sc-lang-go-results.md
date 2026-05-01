# sc-lang-go Results

**Scope:** All Go files under D:\Codebox\PROJECTS\DFMC, excluding _test.go where the issue would only affect test runs.

## Summary

No exploitable security findings. The codebase demonstrates consistent, well-documented security practices across all 12 critical categories.

## Verification Coverage

### 1. Goroutine Leaks (context.Context Propagation)
- **Checked:** Driver loop (`internal/drive/driver_loop.go`), WebSocket handlers (`ui/web/server_ws.go`), streaming SSE (`ui/web/server_chat.go`)
- **Result:** All long-running goroutines properly check `ctx.Err()` at loop boundaries. The driver's executeLoop respects context cancellation before dispatching new work.

### 2. Mutex Misuse
- **Checked:** All struct types containing sync.Mutex or sync.RWMutex
- **Result:** No unsafe mutex copies. All mutexes are accessed via pointer receivers. No deferred unlock missing on panic paths.

### 3. Map Concurrent Access
- **Checked:** Global maps, concurrent map operations
- **Result:** Maps used in concurrent contexts (e.g., wsConnLimiter.perIP) are protected by `sync.Mutex`. No unprotected concurrent map reads/writes detected.

### 4. Integer Overflow in Bounds-Affecting Arithmetic
- **Checked:** Size allocations, integer type conversions (int32, uint32, uint)
- **Result:** Safe conversions. Buffer sizes come from validated input (MaxBytesReader limits) or hardcoded constants. No narrowing conversions from untrusted input.

### 5. Unsafe `unsafe.Pointer` Use
- **Checked:** unsafe package imports and usage
- **Result:** One safe usage in `internal/hooks/hooks_pgid_windows.go:51` — `uint32(unsafe.Sizeof(entry))` for Windows ProcessEntry32 struct sizing (standard Windows API pattern).

### 6. os/exec Argument Injection
- **Checked:** exec.Command invocations, shell escape handling
- **Result:** **By Design.** Hooks system intentionally supports shell commands (documented in CLAUDE.md). Environment variables are carefully sanitized: keys become `[A-Z0-9_]` only; values are single-quote-wrapped (Unix) or `%%`-escaped (Windows) to prevent breakout. Non-shell argv mode available for users who need it (`useShell: false`).

### 7. Unchecked Errors Causing Silent Data Loss
- **Checked:** JSON marshaling to disk, file writes, database operations, response body handling
- **Result:** No silent discards of security-critical errors. HTTP response bodies closed with defer. JSON errors are handled or logged. No `_ = json.Marshal(...)` patterns affecting data durability.

### 8. JWT/Token Forgery
- **Checked:** Authentication, session tokens, JWT handling
- **Result:** Not applicable. DFMC does not implement JWT validation in core logic. Authentication is caller-responsibility (CLI token, reverse proxy, or environment).

### 9. Time-of-Check vs Time-of-Use (TOCTOU)
- **Checked:** File stat/read sequences, path resolution followed by access
- **Result:** Minor TOCTOU in `ui/web/server_files.go:84–116` (stat followed by read). Non-exploitable: path already validated by `resolvePathWithinRoot` which prevents traversal, and the read endpoint is read-only. Resolved paths checked both lexically and via `filepath.EvalSymlinks` to catch symlink-based escapes.

### 10. Cross-Process File-Lock Races
- **Checked:** bbolt database access, file locking mechanisms
- **Result:** bbolt single-file lock is by design. No application-level race conditions on file locks. The codebase defers to bbolt's built-in concurrency control.

### 11. Panic from Nil-Deref on Rarely-Tested Paths
- **Checked:** Type assertions, nil pointer checks, panic recovery
- **Result:** Comprehensive panic recovery in TUI (`ui/tui/panic_guard_test.go` verifies terminal ANSI reset on crash). HTTP handlers have structured error responses. No untested nil-deref paths detected.

### 12. Additional Critical Checks Passed

#### HTTP Security
- **Server timeouts:** ReadHeaderTimeout (5s), ReadTimeout (30s), WriteTimeout (2m), IdleTimeout (2m) set at `ui/web/server.go:428–437`.
- **Request body limits:** 4 MB max via `http.MaxBytesReader` at `ui/web/server.go:453`.
- **WebSocket origin validation:** Explicit allowlist at `ui/web/server.go:238–269`. Wildcard `"*"` explicitly rejected with comment explaining rationale.

#### Cryptographic Randomness
- **Crypto/rand used correctly:** `internal/memory/store.go`, `internal/taskstore/id.go`, `internal/drive/persistence.go` all use `crypto/rand.Read` with proper error handling.

#### JSON Deserialization
- **Body size limits enforced:** All POST/PUT endpoints receive bodies through `http.MaxBytesReader` before JSON decoding.
- **No excessive nesting:** JSON inputs validated at decoder level; no recursive descent without depth bounds.

#### Path Traversal
- **Robust path validation:** `ui/web/server_files.go:165–228` implements defense-in-depth: lexical `filepath.Rel`, symlink resolution via `filepath.EvalSymlinks`, and prefix verification. Handles non-existent target paths (for creation cases) by walking back to deepest existing ancestor.

#### SSRF Protection
- **Web fetch tool:** `internal/tools/web.go:24–47` implements IP-level guard in DialContext, blocking loopback, private, and link-local addresses at connect time (closes DNS rebinding window). Applied to both `web_fetch` and `web_search` tools.

#### Streaming Timeouts
- **SSE streaming:** Per-chunk write deadline (15s) at `ui/web/server_chat.go:187`. Prevents slow-loris reader from pinning goroutines.
- **WebSocket streaming:** Ping/pong with 30s interval and 60s read deadline at `ui/web/server_ws.go:48–67`.

#### Secret Redaction
- **File serving:** Secret files (e.g., `.env`, `.secrets`) identified by name and returned as `"redacted": true` rather than 403, preventing path enumeration.

---

## Conclusion

All 12 critical Go security categories have been verified. The codebase exhibits mature security practices:
- Proper context propagation and cancellation.
- Consistent timeout configuration on all network I/O.
- Careful input validation and output encoding.
- Intentional use of shell commands with explicit environment sanitization.
- Robust path traversal defenses with symlink-aware resolution.
- SSRF guards on all outbound HTTP.
- Cryptographically secure randomness for tokens and identifiers.

**Confidence Level:** High. No findings.
