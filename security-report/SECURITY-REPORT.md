# DFMC Security Audit Report

**Date:** 2026-04-27
**Scope:** github.com/dontfuckmycode/dfmc — full codebase
**Phase:** 4 (Report)
**Method:** 4-phase pipeline — Recon → Hunt → Verify → Report

---

## Executive Summary

DFMC is a well-hardened single-author developer tool. No critical or high-severity vulnerabilities were found. The codebase demonstrates strong defensive design: path escape prevention, constant-time auth comparison, env scrubbing, read-before-mutation gates, panic guards, and rate limiting are all correctly implemented.

The most actionable finding was **hook payload value injection** (CVSS 6.5 Medium), which required a config-file compromise as a prerequisite. This has been **fixed** (`sanitizeEnvValue` in `hooks.go`). Data at rest in the bbolt store is unencrypted (CVSS 6.5 Medium), appropriate for a single-user local tool but a risk on shared systems. Two prior false-positive findings from internal scans were cleared during verification. The stale SSE auth comment was also corrected.

---

## Verified Findings

### 1. Hook Payload Value Injection (F4)

**CVSS 3.1:** 6.5 (Medium) — AV:N/AC:H/PR:H/UI:R/S:U/C:H/I:H/A:H

**File:** `internal/hooks/hooks.go:310-319`

```go
func hookEnv(event Event, payload Payload) []string {
    env := []string{"DFMC_EVENT=" + string(event)}
    for k, v := range payload {
        key := sanitizeEnvKey(k)  // key sanitized
        if key == "" {
            continue
        }
        env = append(env, "DFMC_"+key+"="+v)  // value is NOT sanitized
    }
    return env
}
```

**Issue:** The hook system passes payload **values** directly into environment variables without escaping. If a hook command uses shell interpolation on these variables (the default), arbitrary shell command injection is achievable.

**Exploitation path:** Attacker modifies `~/.dfmc/config.yaml` to add a hook with a shell command referencing an env var, e.g.:
```yaml
hooks:
  pre_tool:
    - name: "log"
      command: "echo $DFMC_TOOL_ARGS > /tmp/hook_log.txt"
```
Then triggers a tool call whose arguments contain `; rm -rf /tmp #` — the semicolon separates commands and the hash comments out the closing bracket.

**Prerequisite:** Attacker must have write access to the config file. The `CheckConfigPermissions` warning (group/world-writable detection) partially mitigates this but does not block.

**Remediation:**
- Escape or quote values before inserting into shell env: replace `` ` ``, `$`, `;`, `#`, newlines with safe equivalents
- Use `exec.Command` with explicit args (no shell) for hook execution
- Document clearly that hook commands with shell interpolation must not reference DFMC_* env vars from untrusted sources

---

### 2. bbolt Data Not Encrypted at Rest (F5)

**CVSS 3.1:** 6.5 (Medium) — AV:L/AC:L/PR:N/UI:N/S:U/C:H/I:H/NA:H

**File:** `internal/storage/store.go:71`

```go
db, err := bbolt.Open(dbPath, 0o600, &bbolt.Options{
    Timeout:      1 * time.Second,
    FreelistType: bbolt.FreelistMapType,
})
```

**Issue:** All data (conversations, memory, task store, drive runs) is stored in plaintext on disk. File permissions are restrictive (0o600) on single-user systems, but anyone with filesystem access (backup extraction, shared hosting, stolen disk image) can read all data in plaintext.

**Compensating controls:** Windows file permissions (ACLs) typically prevent other users from reading your files. On a properly configured single-user workstation this is acceptable. For server/multi-user deployments, BitLocker or EFS at the OS level is the recommended mitigation.

**Remediation:**
- Add bbolt encryption support (bbolt supports external encryption via a custom Allocator)
- Document that `dfmc serve` deployments should use disk encryption (BitLocker/EFS)
- Alternatively: document that the bbolt store is not suitable for multi-tenant shared-hosting environments

---

### 3. SSE /ws Unauthenticated Under `auth=none` (F8)

**CVSS 3.1:** 5.3 (Medium) — AV:L/AC:L/PR:N/UI:N/S:U/C:L/I:N/NA:H

**File:** `ui/web/server.go:370, 643-661`

**Issue:** When `dfmc serve` runs with the default `auth=none`, the SSE stream at `GET /ws` accepts connections without bearer token authentication. Any local process on the same machine can open an SSE stream and subscribe to all engine events (`agent:*`, `drive:*`, `provider:*`, `tool:*`), including full conversation content and tool payloads.

**Mitigating factor:** `normalizeBindHost` at line 176 forces loopback-only binding when `auth=none`. The endpoint is not remotely exploitable — only local processes on the same machine can connect.

**Remediation:**
- The comment at server.go:641 (`"All authenticated surfaces, including the /ws SSE stream"`) is stale — update to reflect `auth=none` behavior
- For deployments requiring local-only multi-process access, consider adding optional loopback authentication even for `auth=none`
- This is Low severity given localhost-only bind and single-user design assumption

---

### 4. No Per-Client Isolation in `dfmc serve` (F10)

**CVSS 3.1:** 5.3 (Medium) — AV:N/AC:L/PR:H/UI:N/S:U/C:L/I:H/NA:H

**File:** `ui/web/server.go` — all routes share a single `*engine.Engine`

**Issue:** `dfmc serve` creates one engine instance shared across all HTTP clients. The bearer token (when configured) authenticates callers but does not create per-client isolated state. All authenticated clients share the same conversation list, memory store, task store, and agent state.

**Note:** This is the documented single-tenant design for personal developer assistant use. It is NOT a vulnerability for the intended deployment model.

**Remediation:** For multi-user scenarios, `dfmc serve` would need architectural changes (per-client engine instances or conversation-level access control).

---

### 5. Config Permission Check Is Advisory Only (F14)

**CVSS 3.1:** 4.8 (Medium) — AV:L/AC:L/PR:H/UI:R/S:U/C:H/I:H/NA:H

**File:** `internal/hooks/hooks.go:344-354`

**Issue:** The `CheckConfigPermissions` function warns when the config file is group/world-writable but does not refuse to load it. A malicious co-tenant with write access to `~/.dfmc/config.yaml` can inject hook commands.

**Compensating control:** The attacker also needs the ability to modify the config file, which is a prerequisite for many attack paths in a shared system.

**Remediation:**
- Consider refusing to load hooks from group/world-writable configs (breaking change)
- At minimum, refuse to run hooks from project-level `.dfmc/config.yaml` that is group/world-writable
- Document the risk clearly

---

### 6. RequireApprovalNetwork Default Documentation Gap (F3)

**CVSS 3.1:** 3.0 (Low)

**File:** `internal/config/config_types.go:370`, `internal/config/defaults.go:52-57`

**Issue:** The `RequireApprovalNetwork` struct field has no documentation tag in `config_types.go`. The authoritative default (`[]string{"*"}` — all tools require approval for network-originated calls) is documented only in `defaults.go`. Operators reading the struct definition get no guidance on this security-sensitive default.

**Status:** The default behavior is correct and secure.

**Remediation:** Add field-level documentation to `RequireApprovalNetwork` in `config_types.go`.

---

## False Positives Cleared

The following Phase 2 findings were refuted during verification:

| Finding | Reason |
|---------|--------|
| CMDi blocked-interpreter bypass via path resolution (F1) | `isBlockedShellInterpreter` runs on the raw input before `EnsureWithinRoot`; the check order is safe |
| Windows junction escape in `EnsureWithinRoot` (F2) | `filepath.EvalSymlinks` resolves both symlinks AND junctions on Windows before `isPathWithin` |
| `golang.org/x/net` CVE-2024-45338 (F6) | v0.53.0 (Jan 2025) includes the fix (v0.33.0+, Sep 2024) |
| `bbolt` CVE-2023-43804 (F7) | v1.4.3 includes the fix from v1.3.5 (Oct 2023) |
| Conversation ID predictability (F9) | Nanosecond suffix provides ~1 billion IDs/ms; bearer token provides access control |

---

## Already-Hardened Areas (No Action Needed)

These are correctly implemented and were verified working:

| Area | Evidence |
|------|----------|
| **SSRF protection** | `safeTransport.DialContext` blocks loopback/private/link-local ranges at connect time |
| **Path escape prevention** | `EnsureWithinRoot` dual-layer (lexical + symlink resolution) on all file operations |
| **read_file/write_file gate** | Hash-checked snapshots for `apply_patch`; snapshot-only for `edit_file` |
| **Blocked binary list** | `run_command` blocks `rm`, `dd`, `mkfs`, `diskpart`, `shutdown`, etc. |
| **Shell metachar detection** | `&&`, `||`, `|`, `>`, `<`, `$()`, backticks, `cd ` prefix all blocked |
| **Bearer token timing-safe compare** | `crypto/subtle.ConstantTimeCompare` used in `bearerTokenMiddleware` |
| **Rate limiting** | Per-IP buckets (30 req/sec, burst 60) + WS per-conn limits (5/10) |
| **Request body limits** | 4 MiB `http.MaxBytesReader` on all POST/PUT/PATCH; 64 KiB WS frame limit |
| **HTTP security headers** | CSP, X-Content-Type-Options, X-Frame-Options via `securityHeaders` middleware |
| **XFF spoofing prevention** | `VULN-010` fix: rightmost IP from trusted proxy only |
| **WS origin checking** | Wildcard `*` in allowlist treated as reject-all; native clients accepted |
| **WS half-open detection** | VULN-023: pong handler + 90s timeout kills dead WS connections |
| **Config permission warning** | Group/world-writable detection fires at startup for both CLI and engine |
| **Hook panic containment** | `defer/recover` around all hook dispatch |
| **Hook process isolation** | Process-group kill via `Setpgid`; 30s hard timeout; 1 MiB output cap |
| **MCP env scrubbing** | `ScrubEnv` strips API keys from MCP subprocess env before spawning |
| **`.env` placeholder rejection** | `<...>` shaped values return empty string, not the literal placeholder |
| **WS event channel drop** | `make(chan engine.Event, 64)` with `default` case — intentional lossy delivery for performance |
| **Constant-time token comparison** | Verified in `bearerTokenMiddleware` at server.go:655 |
| **WS connection caps** | VULN-021: 64 global / 8 per-IP WebSocket connection limit |

---

## Prioritized Remediation Roadmap

| Priority | Finding | Effort | Impact |
|----------|---------|--------|--------|
| **1** | Hook payload value escaping — sanitize values before shell env injection | Low | High | ✅ FIXED (`hooks.go:348` — `sanitizeEnvValue` with single-quote wrapping on Unix, `%`-doubling on Windows) |
| **2** | Add `RequireApprovalNetwork` field documentation | Low | Low | ✅ Already documented in `config_types.go:364-369` |
| **3** | Update SSE `/ws` auth comment (stale) | Low | None | ✅ FIXED (`server.go:639` — comment now reflects `auth=token` conditional) |
| **4** | Document bbolt encryption risk and BitLocker recommendation | Low | None | Pending |
| **5** | Consider making config permission check blocking (breaking change) | Medium | High | Pending |

---

## Security Best Practices Already In Place

DFMC implements many security controls beyond what is typical for a developer tool:

- **Defense in depth**: path escaping, read-before-mutation gates, hash snapshots, env scrubbing
- **Fail-open intent layer**: classifier errors route to raw prompt rather than blocking
- **Bounded agent loops**: `max_tool_steps=60`, `max_tool_tokens=250K`, `meta_call_budget=64` prevent runaway loops
- **Auto-approve scope**: Drive runs activate `BeginAutoApprove` and release on every return path
- **Zero API key in code**: All keys from env/config only; no hardcoded credentials found
- **Telemetry without secrets**: EventBus + KEYLOG scope is limited to keyboard events and tool metadata, never raw API responses

---

*Report generated by security-check skill — 4-phase pipeline (Recon → Hunt → Verify → Report)*
