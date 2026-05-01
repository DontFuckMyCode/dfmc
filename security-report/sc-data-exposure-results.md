# SC-DATA-EXPOSURE Results

**Scanned:** D:\Codebox\PROJECTS\DFMC  
**Date:** 2026-04-30

## Summary
- **Critical Issues:** 0
- **High Issues:** 0
- **Medium Issues:** 1
- **Low Issues:** 1
- **Total Findings:** 2

---

## Findings

### DATA-001 — Medium — BBolt Database Unencrypted at Rest

**File:** `internal/storage/store.go:82`

**Code:**
```go
db, err := bbolt.Open(dbPath, 0o600, &bbolt.Options{
	Timeout:      1 * time.Second,
	FreelistType: bbolt.FreelistMapType,
})
```

**Issue:**
DFMC uses `go.etcd.io/bbolt` for persistent storage of conversations, memory tiers (episodic/semantic), and configuration cache. The database file is opened with no encryption layer — data is stored in plaintext at rest.

**Contents at Risk:**
- Conversation history (JSONL lines with LLM reasoning, tool calls, results)
- Episodic memory vectors / semantic cache
- AST cache
- Plugin metadata
- Configuration snapshots

File permissions are correctly set to `0o600` (owner-read/write only), so local multi-user attacks are mitigated. However, if the host is compromised, the database is readable without additional key material.

**Verification:**
- `bbolt.Open(dbPath, 0o600, ...)` confirmed at line 82
- No encryption parameter passed to bbolt options
- BBolt is not an encrypted database; it provides B+tree key-value storage only
- No wrapper encryption layer (e.g., via `crypto/aes`) observed in the codebase

**Remediation:**
This is a design trade-off. Encryption options:

1. **Application-level:** Wrap bbolt in a crypto layer (e.g., `secretbox` or AES-256-GCM) at write/read boundaries
   - Pros: Protects data at rest; key material can be derived from OS keychain
   - Cons: Performance overhead; key management complexity
   
2. **Filesystem-level:** Use encrypted filesystem (LUKS, BitLocker, FileVault)
   - Pros: Transparent to app; OS-managed keys
   - Cons: Requires end-user setup; not portable

3. **Accept plaintext:** Document as a known limitation for local-only deployments
   - Current approach; reasonable for developer tools where host is trusted

**Status:** Already documented in verification spec as a known Medium-risk item. No immediate action required if this is acceptable for the deployment model.

---

### DATA-002 — Low — Conversation JSONL Files Unencrypted

**File:** `.dfmc/artifacts/`

**Issue:**
Conversation history is stored as JSONL in artifacts directory alongside bbolt database. Same encryption-at-rest concerns as DATA-001.

**Verification:**
- Directory is `.dfmc/` which is git-ignored and local-only
- File permissions depend on parent directory mode (`0o755` at line 73 in `store.go`)
- No encryption applied to serialized conversation data

**Remediation:**
Same options as DATA-001. If encryption is added to bbolt, apply the same to artifact files.

---

## Verification Summary

### What Passed
- ✅ **BBolt File Permissions:** Database opened with `0o600` (owner-read/write only)
- ✅ **Data Directory Permissions:** Created with `0o755` (standard user-readable); reasonable for local dev
- ✅ **web_fetch Size Limits:** Capped at 1 MB (`internal/tools/web.go:126-131`)
- ✅ **web_fetch SSRF Guard:** IP-level protection blocks private/loopback/link-local addresses
- ✅ **Config Dumps Redaction:** `dfmc config list/get --raw` requires explicit flag; defaults to `***REDACTED***` for sensitive fields
- ✅ **dfmc doctor Output:** Only surfaces aggregate metrics, not raw secrets (checked `cli_doctor.go`)
- ✅ **EventBus Payload Redaction:** Covered in SC-SECRETS; all event payloads redacted before broadcast
- ✅ **SSE/WS Event Safety:** WebSocket subscribers receive redacted payloads only

### What Failed
- ⚠️ **BBolt Unencrypted:** No encryption layer for persistent storage (MEDIUM — documented limitation)
- ⚠️ **Artifact Files Unencrypted:** Conversation JSONL in `.dfmc/artifacts/` plaintext (LOW — same as bbolt)

---

## Recommendations

**Short-term:**
- Document encryption-at-rest status in README (acceptable for local-dev; not for cloud deployments)
- If cloud support is planned, implement application-layer encryption before release

**Long-term:**
- Consider filesystem-level encryption guidance for enterprise deployments
- Evaluate performance of wrapping bbolt reads/writes with `crypto/aes`

---

## Out of Scope (Already Verified)
- **Telegram MCP Integration:** Message content forwarding is caller responsibility (MCP spec); verified in code that secrets are redacted in EventBus before any tool execution
- **Custom Crypto:** None found; no home-grown ciphers or RNG (uses `crypto/rand` for IDs)
