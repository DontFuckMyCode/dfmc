# DFMC — Verified Findings

**Phase:** 3 (Verify)
**Date:** 2026-04-30
**Method:** Cross-referenced 41 per-skill result files against actual source code; eliminated unreachable / mitigated / duplicate findings; per-finding confidence scored.

---

## Phase 2 Aggregate

| Category | Skills | Raw findings | After verify |
|----------|--------|--------------|--------------|
| Language scanner | sc-lang-go | 0 | 0 |
| Injection (9) | sqli, nosqli, graphql, xss, ssti, xxe, ldap, cmdi, header-injection | 0 | 0 |
| Code execution | rce, deserialization | 0 | 0 |
| Access control | auth, authz, privilege-escalation, session | 0 | 0 |
| Data exposure | secrets, data-exposure, crypto | 4 | 3 (1 fixed in this scan) |
| Server-side | ssrf, path-traversal, file-upload, open-redirect | 0 | 0 |
| Client-side | csrf, cors, clickjacking, websocket | 0 | 0 |
| Logic & API | business-logic, race-condition, mass-assignment, api-security, rate-limiting, jwt | 0 | 0 |
| Infrastructure | iac, docker, ci-cd | 0 | 0 |
| **Total** | **41 skill files** | **4** | **3** |

---

## Verified Findings

### F1 — SECRETS-001 — Critical → **FIXED IN THIS SCAN**

**Status:** Resolved at audit time. Recorded for traceability.

**File:** `internal/hooks/hooks.go:238` (pre-fix)

**Pre-fix code:**
```go
cmd.Env = append(os.Environ(), hookEnv(event, payload)...)
```

**Post-fix code:**
```go
cmd.Env = append(security.ScrubEnv(os.Environ(), nil), hookEnv(event, payload)...)
```

**Issue:** Hooks dispatcher forwarded the full parent environment — including `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `DEEPSEEK_API_KEY`, `GITHUB_TOKEN`, `AWS_*`, `DFMC_WEB_TOKEN`, etc. — to every user-configured hook subprocess. The MCP client (`internal/mcp/client.go:57`) had been hardened to call `security.ScrubEnv(os.Environ(), nil)` per the design intent; the symmetric hook path was the design's other identified leak surface (per env_scrub.go:5-10 doc comment), but had not been wired.

A hook script that runs `printenv > /tmp/log`, posts env to a webhook for debugging, or uses `curl -d @-` for telemetry would silently exfiltrate the full provider credential set.

**Verification path used to confirm:**
1. Read `internal/hooks/hooks.go:238` — confirmed `os.Environ()` forwarded raw
2. Read `internal/mcp/client.go:57` — confirmed MCP path used `ScrubEnv`
3. Read `internal/security/env_scrub.go:5-10` — doc comment explicitly names *both* paths as users of `ScrubEnv`
4. Verified `ScrubEnv` is allow-by-default with deny-list of secret-shaped suffixes (`*_API_KEY`, `*_TOKEN`, `*_SECRET`, `*_PASSWORD`, `AWS_ACCESS_KEY_ID`, etc.) — keeps `HOME`, `PATH`, `USER` etc. so existing hooks don't break.

**Fix applied:** Added `internal/security` import, switched env build to scrubbed copy. `go build ./...` clean, `go test ./internal/hooks/...` pass.

**CVSS 3.1:** 8.1 (High) → resolved — AV:L/AC:H/PR:L/UI:R/S:C/C:H/I:H/A:N

> Note on severity: original sub-agent classified Critical. Re-scoped to High because the prerequisite is "user has configured *any* hook" — local trust model puts a soft floor on severity. The fix is trivial enough that the practical urgency was high regardless.

**Confidence:** 100 — verified by reading both call sites + the design-intent comment.

---

### F2 — DATA-001 — Medium — Persistent data unencrypted at rest

**File:** `internal/storage/store.go:82`

**Code:**
```go
db, err := bbolt.Open(dbPath, 0o600, &bbolt.Options{
    Timeout:      1 * time.Second,
    FreelistType: bbolt.FreelistMapType,
})
```

**Issue:** DFMC stores conversations, memory tiers, AST cache, and config snapshots in `bbolt` plaintext. File permissions are `0o600` (owner read/write), and bbolt's single-OS-file lock prevents concurrent processes from opening it. Encryption is **not** layered on top.

**Threat model:**
- Local same-user attacker: blocked by the OS process boundary. Same threat covers reading `~/.bash_history`, `~/.aws/credentials`, etc. — out of DFMC's TCB.
- Local other-user attacker on shared host: blocked by `0o600` permissions on Unix. On Windows, `os.Stat`'s simulated `0666` has been a perennial false-positive trap; ACLs are the actual gate.
- Backup / disk image leak: data exposed. Conversations may include LLM reasoning over private code, tool outputs, and (transiently, in inputs) secret-shaped content the user pasted.

**Verification path:**
1. Confirmed `bbolt.Open(dbPath, 0o600, ...)` in store.go:82.
2. Grepped for any AES/secretbox/age wrapper — none found in storage layer.
3. Reviewed `internal/security/redact.go` — covers events emitted on the EventBus but does **not** redact data going into bbolt (by design — full conversation fidelity required for replay/branching).

**Compensating controls:**
- File permissions `0o600` (mode set explicitly).
- Project-scoped storage path (`.dfmc/dfmc.db`) — never in shared `/tmp` or system dirs.
- Documented as intentional in `CLAUDE.md` (single-user developer tool).

**CVSS 3.1:** 5.5 (Medium) — AV:L/AC:L/PR:L/UI:N/S:U/C:H/I:N/A:N

**Recommendation:**
- Document explicitly in user-facing docs that `.dfmc/` should not live on synced/cloud-backed paths without disk-level encryption.
- Out-of-scope for code change unless there's product appetite for an `--encrypt` flag with OS-keyring-derived key. The flag's UX cost (key recovery, lost-laptop scenario, multi-machine sync) is non-trivial.

**Confidence:** 100.

---

### F3 — SECRETS-002 — Medium → **DEVELOPMENT ENVIRONMENT ONLY**

**File:** `.env:8,11,29` (local working copy; not in git)

**Issue:** Live API keys for Z.AI, MiniMax, Kimi providers exist in the developer's local `.env`. Format-validated against vendor patterns by the sub-agent.

**Verification path:**
1. Confirmed `.env` is in `.gitignore:26` — file is **not** tracked in version control.
2. Verified format: `ZAI_API_KEY=82ebd5d747cc...`, `MINIMAX_API_KEY=sk-cp-VV4Dy...`, `KIMI_API_KEY=sk-kimi-PEPkGt...` match expected vendor key shapes.
3. `.env.example` exists with placeholder pattern (`<placeholder>`) — the proper pattern operators should follow.

**Why this is below F1 in priority:** Local file under user's direct control. The risk is *backup leak* / *accidental upload* / *machine compromise* — same risk class as `~/.aws/credentials`. Out of DFMC's TCB.

**Recommendation to the operator (this is your machine, not DFMC's bug):**
1. Rotate the three keys at provider dashboards (Z.AI, MiniMax, Kimi).
2. Replace with `<placeholder>` style values in `.env`; export real keys via shell only when running.
3. Never `git add -f .env` — the gitignore guard is good but a forced add bypasses it.

**CVSS 3.1:** 6.2 (Medium) — AV:L/AC:L/PR:H/UI:N/S:U/C:H/I:H/A:N — credential disclosure if local file leaks.

**Confidence:** 100 — file path + line numbers + format validation.

---

## Cleared Patterns (would-be findings, mitigation verified)

These are categories where automated scanners might raise alarms; verified to be properly mitigated and not findings:

| Category | Mitigation | Evidence |
|----------|------------|----------|
| Command injection (run_command) | argv-only `exec.Command`, blocked-binary list, shell-metachar guard, timeout cap 120s | `internal/tools/builtin.go`, `internal/tools/command.go:130` |
| Git flag injection (CVE-2018-17456 class) | `rejectGitFlagInjection` on every ref/path arg | `internal/tools/git_runner.go`, applied in `git.go` & `patch_validation.go` |
| Hook env-var value injection | `sanitizeEnvValue` (Unix single-quote wrap; Windows double-quote + `%%`) | `internal/hooks/hooks.go:318` |
| Path traversal | `EnsureWithinRoot` + `filepath.EvalSymlinks` on root and target; ancestor-walk for non-existent paths | `internal/tools/engine.go:843-884` |
| TOCTOU symlink swap | `resolveExistingAncestor` walk before EvalSymlinks; tested in `ensure_within_root_test.go` | same |
| WS origin spoofing | Allowlist; `*` explicitly rejected with stderr warning | `ui/web/server.go:238-268`, test in `server_origin_test.go:125-146` |
| WS DoS (slow-loris, oversize, conn flood) | 64KiB read limit; 60s read deadline + pong; 5s write deadline; 64 global / 8 per-IP cap; 5rps + burst 10 per-conn rate limit; sync.Once cleanup | `ui/web/server_ws.go:41-67`, `233-256`, `483-528` |
| SSE slow-loris | `writeSSEWithDeadline` 15s per-chunk; handlers bail on deadline expiry | `ui/web/server_chat.go:183-203` (recently wired) |
| CORS overpermissive | No `Access-Control-Allow-*` headers ever set; localhost-bind by default | grep verified zero matches |
| CSRF | Bearer-token-in-header auth; no cookies; `Set-Cookie` zero matches | `ui/web/server.go:681-699` |
| Clickjacking | `X-Frame-Options: DENY` + CSP `default-src 'self'` on every response | `ui/web/server.go:128-135`, `406` |
| Mass assignment | All web handlers decode into typed structs (no `map[string]any` → struct merge); 4MiB body cap | `ui/web/server.go:50-123`, `446` |
| Token timing attack | `crypto/subtle.ConstantTimeCompare` for bearer token | `ui/web/server.go:693` |
| 0.0.0.0 bind without auth | `normalizeBindHost` forces loopback when `auth: none` | `ui/web/server.go:173` |
| Unsafe deserialization | Only JSON / YAML-into-typed-struct; no `gob`, no `eval`-style decoders; 8MiB scan buffer cap on conversation JSONL | grep verified |
| SSRF | `internal/security/safe_http.go:isBlockedDialTarget()` blocks 10/8, 172.16/12, 192.168/16, 127/8, 169.254/16, fc00::/7, multicast, unspecified | applied via wrapped `DialContext` |
| File upload | No multipart parser anywhere — surface absent | grep verified zero `multipart` references |
| Open redirect | No `http.Redirect` calls; no `Location:` header writes | grep verified zero matches |
| Tool approval bypass | All tool exec funnels `engine.executeToolWithLifecycle`; `SourceUser` allow-list documented; MCP-Drive deliberate bypass tagged in CLAUDE.md | `internal/engine/engine_tools.go:290-331` |
| Memory ID collision | crypto/rand 6-byte suffix on bbolt entry IDs (recently fixed) | `internal/memory/store.go:142-146` |
| Conversation save race vs Storage close | `saveWg sync.WaitGroup` drained in `Close()` before bbolt close (recently fixed) | `internal/conversation/manager.go:327-368` |
| Patch validation timeout (recently fixed) | `120 * time.Second` literal; previously was `120000` interpreted as 120µs nanoseconds | `internal/tools/patch_validation.go:190` |

---

## False positives cleared

Two earlier audit reports (`report.md`, `report2.md` in the repo root) raised 15+ "findings" each. None of them survived verification:
- Cited file paths often did not contain the alleged code (e.g., `time.Sleep` in `builtin_grep.go`, `planner.go` — zero hits).
- "220 build tags" → real count 11, all deliberate platform splits.
- "tree_test.go.skip" → real but the API it tests (`Store.GetTree`, etc.) does not exist; restoring would break compile.
- "permissive CORS" → no CORS headers anywhere in the codebase.
- "yaml.Unmarshal panic in pluginexec" → no YAML in pluginexec, JSON-RPC only.

These are documented in `security-report/FALSE-POSITIVES.md` for traceability.

---

## Confidence Distribution

| Confidence | Count | Notes |
|-----------|-------|-------|
| 100 | 3 | F1 (fixed), F2, F3 |
| 75-99 | 0 | — |
| 50-74 | 0 | — |
| <50 | 0 | — |

All verified findings cite exact file:line + verified guard absence. No speculative entries.

---
