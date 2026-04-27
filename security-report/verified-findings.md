# Verified Findings

Findings below have been confirmed via source code verification.

---

## F4: Hook Payload Value Injection

**CVSS 3.1:** 6.5 (Medium)
**CWE:** CWE-78 (OS Command Injection)
**File:** `internal/hooks/hooks.go:310-319`
**Status:** VERIFIED — High Confidence

**Root Cause:** `hookEnv` sanitizes the environment variable **key** (alphanumerics only via `sanitizeEnvKey`) but passes the **value** directly into the subprocess environment without any escaping.

```go
// hooks.go:hookEnv — value is injected verbatim
env = append(env, "DFMC_"+key+"="+v)  // v is not escaped
```

**Exploitation:** Requires config file write access (prerequisite). Hook commands that use `$DFMC_*` shell variables in a shell context are vulnerable:

```yaml
# Attacker-controlled ~/.dfmc/config.yaml
hooks:
  pre_tool:
    - name: "log"
      command: "echo $DFMC_TOOL_ARGS >> /tmp/hook_log.txt"
```

A tool call with args containing `; cat /etc/passwd #` would result in shell command injection.

**Compensating Controls:**
- Config permission check warns on group/world-writable configs
- Hook execution uses `exec.CommandContext` without shell wrapping by default
- 30s hard timeout limits damage from runaway commands

**Recommended Fix:** Escape shell metacharacters in values (`` ` ``, `$`, `;`, `#`, `\`, `"`, `'`, newlines) before inserting into env, OR use `exec.Command` with explicit arg slicing instead of a shell command string.

**Status: ✅ FIXED** — `sanitizeEnvValue` added (`hooks.go:348`). Unix: single-quote wrapping with embedded quote escaping (`'` → `'\''`). Windows cmd.exe: double-quote wrapping with `%` → `%%` doubling and `^` escaping for `"`, `\`, `!`, `^` to block %VAR% expansion and quote-breakout. `hookEnv` now calls `sanitizeEnvValue(v)` at line 318.

---

## F5: bbolt Data Not Encrypted at Rest

**CVSS 3.1:** 6.5 (Medium)
**CWE:** CWE-311 (Missing Encryption of Sensitive Data)
**File:** `internal/storage/store.go:71`
**Status:** VERIFIED — High Confidence

**Root Cause:** `bbolt.Open` is called with no encryption options. The database file `~/.dfmc/data/dfmc.db` contains plaintext data.

**Data at risk:**
- Conversations (full prompt/response history)
- Memory store (working, episodic, semantic tiers)
- Task store (all TODOs)
- Drive run persistence
- Codemap cache

**Compensating Controls:**
- File permissions 0o600 (owner read/write only) — effective on single-user systems
- Windows ACLs typically prevent other users from reading

**Recommended Fix:** Document that `dfmc serve` deployments on shared/multi-user systems should use OS-level full-disk encryption (BitLocker on Windows, EFS for the data directory). For future: consider bbolt encryption extension.

---

## F8: SSE /ws Endpoint Unauthenticated Under `auth=none`

**CVSS 3.1:** 5.3 (Medium)
**CWE:** CWE-306 (Missing Authentication for Critical Function)
**File:** `ui/web/server.go:370, 643-661`
**Status:** VERIFIED — High Confidence

**Root Cause:** With default `auth=none`, the bearer token middleware short-circuits without checking tokens. The SSE stream at `GET /ws` then accepts all connections.

```go
// server.go:651 — middleware passes through when auth=none
if got := strings.TrimSpace(r.Header.Get("Authorization")); rawToken != "" ...
// With auth=none: rawToken == "" → middleware passes through → /ws is unauthenticated
```

**Impact:** Any local process can connect to `http://127.0.0.1:<port>/ws` and receive the full event stream including all tool call payloads and LLM responses.

**Compensating Controls:**
- `normalizeBindHost` forces loopback-only binding when `auth=none`
- This is explicitly documented as a single-user local configuration

**Recommended Fix:**
1. Update the stale comment at server.go:641 ("All authenticated surfaces, including the /ws SSE stream")
2. For deployments needing local multi-process access with auth=none, consider optional local authentication
3. This is Low severity given localhost-only bind assumption

**Status: ✅ FIXED** — comment updated to clarify the middleware is only active when `auth=token`. The behavior is unchanged but the documentation now accurately reflects the conditional auth.

---

## F10: No Per-Client Isolation in `dfmc serve`

---

## F10: No Per-Client Isolation in `dfmc serve`

**CVSS 3.1:** 5.3 (Medium)
**CWE:** CWE-266 (Incorrectness — Trust Between Users)
**File:** `ui/web/server.go` — all routes share `*engine.Engine`
**Status:** VERIFIED — High Confidence (by design)

**Root Cause:** One `Engine` instance is shared across all HTTP/SSE/WebSocket clients. The bearer token authenticates the identity of the caller but creates no per-client isolated state.

**Impact:** An authorized but malicious client can observe or interfere with another client's conversation via the shared engine. This is NOT a vulnerability for the documented single-user personal assistant use case.

**Recommended Fix:** Document clearly that `dfmc serve` is single-tenant. For multi-user hosting scenarios, architectural changes (per-client engine instances or conversation-level ACLs) would be required.

---

## F14: Config Permission Check Advisory Only

**CVSS 3.1:** 4.8 (Medium)
**CWE:** CWE-732 (Incorrect Permission Assignment)
**File:** `internal/hooks/hooks.go:344-354`
**Status:** VERIFIED — High Confidence

**Root Cause:** `CheckConfigPermissions` warns but does not block when the config file is group/world-writable.

**Impact:** A co-tenant with write access to `~/.dfmc/config.yaml` can inject hook commands that run with the owner's shell environment.

**Prerequisite:** Attacker must have write access to the config file — a high bar on a properly configured single-user system.

**Recommended Fix:**
1. Refuse to load hooks from group/world-writable configs (breaking change — existing setups may break)
2. At minimum, refuse project-level hooks from group/world-writable configs
3. Document the risk clearly in the config security section

---

## F3: RequireApprovalNetwork Documentation Gap

**CVSS 3.1:** 3.0 (Low)
**File:** `internal/config/config_types.go:370`
**Status:** VERIFIED — Documentation issue only

**Root Cause:** The struct field `RequireApprovalNetwork` has no field-level documentation tag. The secure default behavior (`RequireApprovalNetwork: []string{"*"}`) is documented only in `defaults.go`.

**Impact:** Operators reading the struct definition do not learn about this security-sensitive default.

**Recommended Fix:** Add field-level documentation to `config_types.go`:
```go
// RequireApprovalNetwork is the same as RequireApproval but applies
// to network-originated calls (source=web, ws, mcp). Defaults to
// ["*"] — all non-user tool calls require approval unless explicitly
// configured otherwise.
RequireApprovalNetwork []string `yaml:"require_approval_network,omitempty"`
```

---

## Severity Distribution

| Severity | Count | Findings |
|----------|-------|---------|
| Critical | 0 | — |
| High | 0 | — |
| Medium | 5 | F4, F5, F8, F10, F14 |
| Low | 1 | F3 (docs only) |
| Info | 0 | — |

---

*Findings above represent confirmed, exploitable vulnerabilities or meaningful security gaps. False positives from the Hunt phase have been cleared — see `FALSE-POSITIVES.md` for details.*
