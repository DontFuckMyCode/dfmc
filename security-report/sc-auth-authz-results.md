# sc-auth + sc-authz Results

## Findings

### [High] RCE-001 — Drive `BeginAutoApprove` slot leak on concurrent run completion

- **File**: `internal/engine/drive_adapter.go:285-297`
- **Description**: When multiple Drive runs execute concurrently, each calls `BeginAutoApprove` which installs an override approver and returns a release closure. The release closure restores `prev` unconditionally via `SetApprover(prev)` — **without verifying the currently-installed approver is still the one this call installed**. When Run A and Run B complete concurrently (or with overlapping lifetimes), the last release to fire clobbers whatever approver the other run installed, leaving a "zombie" wrapper that auto-approves using the first run's allowlist (including `["*"]` if set).
- **Impact**: After two concurrent Drive runs complete, all subsequent `/chat` or `/tool` requests in that process are auto-approved with the first run's `auto_approve` list until something else calls `SetApprover`. With `auto_approve: ["*"]` this is full privilege-escalation: unauthenticated callers bypass every approval gate.
- **Evidence**:
  ```go
  func (r *driveRunner) BeginAutoApprove(tools []string) func() {
      prev := r.e.approver()          // snapshot
      override := newDriveAutoApprover(prev, tools, "drive")
      r.e.SetApprover(override)       // install
      return func() {
          r.e.SetApprover(prev)        // unconditional restore — no ownership check
      }
  }
  ```
  `Driver.Run` (line 168) and `Driver.Resume` (line 275) both call `BeginAutoApprove` with `defer release()`, but they execute concurrently for different run IDs. POST `/api/v1/drive` fires-and-forgets in a goroutine with no serialization.
- **Mitigation**: Use an opaque token returned by `SetApprover` so `release` only restores when the slot still owns that token. Alternatively, add a process-wide single-flight semaphore so `BeginAutoApprove` calls are provably nested (LIFO ordering guaranteed).

### [High] LOGIC-202 — `apply_patch` read-gate and source read occur before `LockPath`

- **File**: `internal/tools/apply_patch.go:106-170`
- **Description**: The read-before-mutation gate (`EnsureReadBeforeMutation`) and the source file read (`os.ReadFile`) both happen **before** `LockPath` is acquired. The lock is only held around the write. Between the read and the lock acquisition, another goroutine can modify the file, evading the snapshot check. Classic TOCTOU race.
- **Impact**: Two concurrent `apply_patch` calls targeting the same file — or an `apply_patch` racing against an external editor — can result in the second writer overwriting changes from the first, or the read-gate passing when it should fail (if the file is replaced between the snapshot check and the write).
- **Evidence**: Sequence in `apply_patch.go`:
  1. Line ~110: `EnsureReadBeforeMutation(absPath)` called
  2. Line ~120: `orig, err := os.ReadFile(absPath)` — source bytes read
  3. Line ~166: `release := e.LockPath(absPath)` — lock acquired
  4. Line ~170: `err := os.WriteFile(absPath, patched)` — write under lock
- **Mitigation**: Move `LockPath` acquisition to before both the read-gate check and the source read. The lock serializes the entire read→write sequence and ensures no other goroutine can interleave between the snapshot check and the write.

### [Low] VULN-049 — `auth=token` on non-loopback bind only warns, does not block

- **File**: `ui/web/server.go:181-184`
- **Description**: `normalizeBindHost` issues a `WARNING` for `auth=token` on non-loopback binds but still binds to the specified address. With `auth=none`, the function forcibly overrides to 127.0.0.1. So `auth=token` on `0.0.0.0:7777` starts with a warning but remains fully exposed on all interfaces.
- **Impact**: An operator who sets `auth=token` (correctly expecting bearer token to protect the service) but mistakenly binds to `0.0.0.0` gets a warning but the server still starts exposed. The token provides transport-level protection, but AST backend, config endpoint, and Drive endpoints are exposed to any machine that can reach the IP.
- **Evidence**:
  ```go
  if strings.EqualFold(strings.TrimSpace(authMode), "token") && !isLoopbackBindHost(host) {
      fmt.Fprintf(os.Stderr, "[DFMC] WARNING: auth=token with non-loopback bind (%s) exposes the agent on all interfaces.\n", host)
  }
  return host  // continues to bind to the non-loopback host
  ```
- **Mitigation**: Enforce loopback for `auth=token` as well (`return "127.0.0.1"` with a notice), or elevate warning to error that refuses to start without explicit `--force-expose`.

---

## No Issues Found

| Control | Assessment |
|---------|------------|
| Constant-time bearer token comparison | Correct — `subtle.ConstantTimeCompare` |
| Source-based approval gate | Correct — `SourceUser`/`SourceCLI` bypass; `SourceWeb/WS/MCP` consult `RequireApprovalNetwork` |
| Subagent allowlist context propagation | Correct — rides `context.Context`, deny-by-default, per-call enforcement |
| MCP env scrub | Correct — `security.ScrubEnv` strips secret-shaped keys |
| Hook env injection | Correct — secret scrub, payload keys sanitized, values quoted |
| Config permission check | Correct — project hooks discarded when config is group/world-writable on Unix |
| Secret file redaction | Correct — `.env`, `.pem`, `id_rsa`, `credentials.json` return `redacted: true` |
| Content-Type enforcement | Correct — non-JSON rejected with 415 |
| Per-IP rate limiting | Correct — 30 req/s per IP, burst 60, GC of stale buckets |
| Host allowlist middleware | Correct — rejects 421 for non-allowlisted Host headers |
| XFF trusted-only | Correct — only loopback proxies honored; rightmost (most trusted) IP used |
| Path traversal containment | Correct — syntactic + symbolic two-layer check |
| Git flag injection guard | Correct — CVE-2018-17456 class blocked |