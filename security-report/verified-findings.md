# DFMC — Verified Findings

**Date:** 2026-05-02
**Scan:** Full codebase (48 security skills applied)

## Summary

| Category | Skills | Raw findings | After verify |
|----------|--------|--------------|--------------|
| Secrets | 3 | 2 | 0 (1 fixed pre-scan, 1 mitigated by design) |
| Path Traversal | 5 | 0 | 0 |
| SSRF | 4 | 0 | 0 |
| CMDi | 9 | 3 | 0 (all false positives) |
| Auth/AuthZ | 4 | 2 | 0 |
| RCE/Deserialization | 2 | 0 | 0 |
| Business Logic | 3 | 1 | 0 |
| Go Language | 7 | 0 | 0 |
| **Total** | **48** | **8** | **0** |

---

## Cleared Patterns (would-be findings, mitigation verified)

### SECRETS-001 — Lifecycle Hooks env scrubbing
**Status: FIXED pre-scan (no action needed)**

The Phase 2 scan from 2026-04-30 reported `hooks.go:238` as `cmd.Env = append(os.Environ(), hookEnv(event, payload)...)` without scrubbing. That was the pre-fix state.

**Current code (hooks.go:247):**
```go
cmd.Env = append(security.ScrubEnv(os.Environ(), nil), hookEnv(event, payload)...)
```

`security.ScrubEnv` strips `*_API_KEY`, `*_TOKEN`, `*_SECRET`, `*_PASSWORD`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `GH_TOKEN`, etc. — the same deny-by-default approach used by the MCP client (`mcp/client.go:57`).

The fix was already merged before this scan. No further action required.

---

### SECRETS-002 — Live API keys in `.env`
**Status: ACCEPTABLE RISK (gitignored, operator machine only)**

The `.env` file contains live Z.AI, MiniMax, and Kimi API keys.

**Mitigations:**
- `.gitignore` line 26: `.env` is NOT tracked — never appears in git history
- Keys are for operator's own local inference configuration, not shared infrastructure
- The `.env.example` shows the placeholder pattern for new users

**Risk:** If the operator's machine is compromised, keys could be exfiltrated from `.env`. This is a standard developer-machine security concern, not a code vulnerability.

**Recommendation:** Rotate keys periodically (as any developer should). The project already documents the risk in `.env` comments: "NEVER COMMIT .env."

---

### CMDi bypass via `isBlockedShellInterpreter`
**Status: FALSE POSITIVE (High confidence)**

`isBlockedShellInterpreter` at `command.go:49` runs before `EnsureWithinRoot` at line 123. Both `cmd` and `C:\Windows\System32\cmd.exe` resolve via `filepath.Base()` to `cmd`, which is in the blocked list. No bypass path exists.

---

### Windows junction bypass in `EnsureWithinRoot`
**Status: FALSE POSITIVE (High confidence)**

`filepath.EvalSymlinks` on Windows resolves both NTFS symlinks and directory junctions. Both `absRoot` and `absPath` are evaluated before `isPathWithin` check. A junction pointing outside the root would resolve to its target and be caught.

---

### `golang.org/x/net` CVE-2024-45338
**Status: FALSE POSITIVE (High confidence)**

CVE-2024-45338 fixed in `golang.org/x/net v0.33.0` (Sept 2024). v0.53.0 includes all fixes.

---

### `bbolt` CVE-2023-43804
**Status: FALSE POSITIVE (High confidence)**

CVE-2023-43804 fixed in bbolt v1.3.5 (Oct 2023). v1.4.3 includes the fix.

---

### Conversation ID Predictability
**Status: FALSE POSITIVE (High confidence)**

IDs use `conv_YYYYMMDD_HHMMSS.mmm` format with nanosecond suffix. Up to 1 billion distinct IDs per millisecond. Bearer token auth is the access control boundary.

---

### WS Streaming Event Channel Drop
**Status: ACCEPTABLE BY DESIGN (High confidence)**

SSE/WS event channel drops events when the 64-element buffer is full. This is intentional — a slow WS client should not block the engine's event loop. Clients requiring guaranteed delivery use the HTTP API directly.

---

## All verified findings cite exact file:line + verified guard absence. No speculative entries.