# DFMC Security Audit Report

**Date:** 2026-05-05  
**Auditor:** Claude Code Security Scanner (4-phase pipeline)  
**Scope:** Full codebase audit — `github.com/dontfuckmycode/dfmc`  
**Commit:** `63e865b60d0937de98990d278e740a404374f963`

---

## Executive Summary

DFMC demonstrates **strong security engineering** with multiple defense-in-depth layers across all attack surfaces. The codebase has clearly undergone prior security hardening (VULN-019 through VULN-050 series). The majority of common vulnerability classes (path traversal, SSRF, deserialization, cryptographic misuse) are properly mitigated.

**3 actionable findings** require code changes. The rest are design-level observations or informational notes.

| Severity | Count | Action Required |
|----------|-------|-----------------|
| High | 1 | Yes — script runner eval bypass in `run_command` |
| Medium | 1 | Yes — unbounded `io.ReadAll` on provider streaming error paths |
| Low | 4 | Advisory |
| Informational | 3 | No action |

---

## Findings

### HIGH-001: Script Runner Eval Flag Bypass in `run_command`

| Field | Value |
|-------|-------|
| **File** | `internal/tools/command.go:49,90,628` |
| **CWE** | CWE-78 (OS Command Injection) |
| **CVSS** | 7.3 (High) |
| **Exploitable** | Yes — via LLM prompt injection |

**Description:**

When an LLM calls `run_command` with `command: "python3"` and `args: ["-c", "malicious_code"]`, all guards pass:

1. `isBlockedShellInterpreter("python3")` → **false** (only blocks bash/sh/cmd/powershell)
2. `hasScriptRunnerWithEvalFlag(["-c", "malicious_code"])` → **false** (scans for binary names *inside* args, e.g. `["xargs", "python3", "-c", "..."]`, but NOT when the binary is the `command` itself)
3. `ensureCommandAllowed("python3", ...)` → **passes** (python3 is not in `isBlockedBinary`)

The function `hasScriptRunnerWithEvalFlag` was designed to catch the *nested* pattern (script runner invoked as an argument to another binary), but misses the *direct* pattern (script runner AS the command with its eval flag as the first arg).

**Proof of Concept:**

An attacker places a prompt injection in a code comment that causes the LLM to call:
```json
{"name": "run_command", "args": {"command": "python3", "args": ["-c", "import subprocess; subprocess.run(...)"]}}
```

This executes successfully. Same applies to `node -e`, `ruby -e`, `perl -e`, `php -r`.

**Fix:**

Add a check after line 90 in `command.go` that cross-references the `command` field against `scriptRunnerEvalFlags`:

```go
if flag, ok := scriptRunnerEvalFlags[canonicalCommandBinary(command)]; ok {
    if len(args) > 0 && args[0] == flag {
        return Result{}, fmt.Errorf(
            "run_command: %s with %s flag allows arbitrary code execution and is blocked. "+
                "If you need to run a script, write it to a file first and execute that file",
            command, flag)
    }
}
```

---

### MED-001: Unbounded `io.ReadAll` on Provider Streaming Error Paths

| Field | Value |
|-------|-------|
| **Files** | `internal/provider/anthropic.go:193`, `openai_compat.go:225`, `google.go:160` |
| **CWE** | CWE-400 (Uncontrolled Resource Consumption) |
| **CVSS** | 5.3 (Medium) |
| **Exploitable** | Only if user configures a malicious provider endpoint |

**Description:**

The non-streaming `Complete()` paths correctly use `readBoundedBody()` (capped at 32 MiB). However, the streaming `StreamComplete()` error paths (HTTP 4xx/5xx) use raw `io.ReadAll(resp.Body)` without any size limit. A malicious or compromised provider endpoint returning a 400 status with an infinite body could cause OOM.

**Affected code:**

```go
// anthropic.go:193
raw, _ := io.ReadAll(resp.Body)

// openai_compat.go:225
raw, _ := io.ReadAll(resp.Body)

// google.go:160
raw, _ := io.ReadAll(resp.Body)
```

**Fix:**

Replace all three with the existing `readBoundedBody` helper:

```go
raw, _, _ := readBoundedBody(resp.Body)
```

---

### LOW-001: Web API Unauthenticated by Default (Loopback-only)

| Field | Value |
|-------|-------|
| **File** | `ui/web/server.go:138` |
| **CWE** | CWE-306 (Missing Authentication for Critical Function) |
| **Severity** | Low |

**Description:** The web API (port 7777) defaults to `auth=none` with loopback-only binding. Any local process (including browser JS on localhost) can call the full API including `run_command`, `write_file`, etc. Mitigated by:
- Loopback-only bind prevents remote access
- Host header allowlist prevents DNS rebinding
- Content-Type enforcement blocks form-POST CSRF
- Origin validation on WebSocket

**Residual risk:** A local malicious process (malware already on the machine) could drive DFMC. This is an accepted threat model limitation.

---

### LOW-002: Error Messages Expose Internal Filesystem Paths

| Field | Value |
|-------|-------|
| **Files** | Multiple handlers in `ui/web/server_*.go` |
| **CWE** | CWE-209 (Information Exposure Through Error Message) |
| **Severity** | Low |

**Description:** HTTP error responses include raw `err.Error()` strings that may contain full filesystem paths (e.g., `/home/user/.dfmc/data.db: permission denied`). Mitigated by authentication requirement for non-loopback access.

**Recommendation:** Wrap errors sent to HTTP clients to strip internal paths when `auth=token` is active.

---

### LOW-003: `gh` Runner Flag Injection Check Weaker Than Git Runner

| Field | Value |
|-------|-------|
| **File** | `internal/tools/gh_runner.go:60-64` |
| **CWE** | CWE-88 (Argument Injection) |
| **Severity** | Low |

**Description:** The `gh` (GitHub CLI) flag injection check only blocks single-dash args but allows double-dash flags (`--exec=...`) through. The `gh` CLI has a narrower attack surface than `git` (no `--upload-pack=CMD` class), and the subcommand allowlist (`pr`, `issue`, `run`, `repo`, `api`) limits exposure.

---

### LOW-004: Remote Server Token in WebSocket URL Query Parameter

| Field | Value |
|-------|-------|
| **File** | `ui/cli/cli_remote_server.go:88` |
| **CWE** | CWE-598 (Use of GET Request Method With Sensitive Query Strings) |
| **Severity** | Low |

**Description:** The remote server accepts `?token=` on the `/ws` endpoint because EventSource cannot set custom headers. The token appears in access logs. This is a known SSE limitation, documented in code.

---

### INFO-001: `math/rand` for Retry Jitter (Not a Vulnerability)

Intentional and documented. Retry jitter does not require cryptographic randomness. All security-sensitive random generation correctly uses `crypto/rand`.

### INFO-002: Real API Keys in Local `.env` (Gitignored)

The `.env` file contains real API keys (ZAI, MiniMax, Kimi). It is correctly listed in `.gitignore` and never committed. Standard local development practice.

### INFO-003: Dependency Inventory — No Known Vulnerabilities

All 14 direct dependencies are from reputable sources at current versions. No known CVEs. The highest-risk dependency (`tetratelabs/wazero` for WASM plugin execution) is from a funded organization with active maintenance.

---

## Security Controls Already in Place

The codebase has comprehensive, well-engineered security controls:

| Control | Implementation | Assessment |
|---------|---------------|------------|
| Path Traversal | `EnsureWithinRoot` + symlink resolution | Excellent |
| SSRF | `safeTransport` — DNS+connect-level IP checks on every hop | Excellent |
| Shell Injection | No shell invocation, metacharacter detection, binary blocklist | Good (see HIGH-001) |
| Git Flag Injection | `rejectGitFlagInjection` on all user-supplied values | Excellent |
| Secret Redaction | Multi-layer: EventBus → web API → CLI → file serving | Excellent |
| Auth | Bearer token with `subtle.ConstantTimeCompare`, loopback-only fallback | Good |
| Rate Limiting | Per-IP limiter + WebSocket connection caps | Good |
| CSRF | Content-Type enforcement + Origin validation | Good |
| DNS Rebinding | Host header allowlist | Good |
| Subprocess Env | `security.ScrubEnv` strips all `*_API_KEY`, `*_TOKEN`, etc. | Excellent |
| File Permissions | bbolt 0o600, config 0o600, project-config writability check | Good |
| Panic Recovery | `executeToolWithPanicGuard` on every tool execution | Good |
| Body Size Limits | 32 MiB provider cap, 16 MiB MCP frame, 1 MiB config, bounded command output | Good (see MED-001) |
| Tool Approval | `executeToolWithLifecycle` funnel + source taxonomy + subagent allowlist | Good |
| Read-before-Mutate | Hash-verified snapshots for write/patch, anchor-verified for edit | Excellent |

---

## Threat Model Assessment

| Threat | Likelihood | Impact | Mitigation |
|--------|-----------|--------|-----------|
| LLM prompt injection → tool execution | Medium | High | Approval gate (opt-in), binary blocklist, shell blocking |
| Malicious provider endpoint → DoS | Low | Medium | `readBoundedBody` (partial — see MED-001) |
| Local process → API abuse | Low | High | Loopback bind, host allowlist, content-type enforcement |
| Supply chain (dependency) | Very Low | High | All deps reputable, current versions, go.sum verification |
| Config file tampering | Low | Medium | Permission checks, placeholder rejection |

---

## Remediation Priority

1. **Immediate** — Fix HIGH-001 (script runner eval bypass). Single function addition.
2. **Short-term** — Fix MED-001 (bounded reads on streaming error paths). Three one-line changes.
3. **Advisory** — Consider making `RequireApproval: ["*"]` the default for agent-initiated tool calls, or at minimum default-require approval for `run_command` from `SourceAgent`.

---

## Conclusion

DFMC's security posture is **above average** for a CLI tool of this complexity. The prior security audit (VULN-019 through VULN-050 series) addressed the major attack surface comprehensively. The two actionable findings (HIGH-001, MED-001) are straightforward to fix and represent gaps in otherwise well-designed defense layers.
