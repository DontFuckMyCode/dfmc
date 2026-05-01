# DFMC — Verified Findings

**Phase:** 3 (Verify)
**Date:** 2026-05-01
**Method:** Cross-referenced 41 per-skill result files against actual source code; eliminated unreachable / mitigated / duplicate findings; per-finding confidence scored.
**Prior scan:** 2026-04-30 — all findings carried forward, F1 confirmed fixed, no new surface introduced.

---

## Phase 2 Aggregate

| Category | Skills | Raw findings | After verify |
|----------|--------|--------------|--------------|
| Language scanner | sc-lang-go | 0 | 0 |
| Injection (9) | sqli, nosqli, graphql, xss, ssti, xxe, ldap, cmdi, header-injection | 0 | 0 |
| Code execution | rce, deserialization | 0 | 0 |
| Access control | auth, authz, privilege-escalation, session | 0 | 0 |
| Data exposure | secrets, data-exposure, crypto | 0 | 0 |
| Server-side | ssrf, path-traversal, file-upload, open-redirect | 0 | 0 |
| Client-side | csrf, cors, clickjacking, websocket | 0 | 0 |
| Logic & API | business-logic, race-condition, mass-assignment, api-security, rate-limiting, jwt | 0 | 0 |
| Infrastructure | iac, docker, ci-cd | 0 | 0 |
| **Total** | **41 skill files** | **0** | **0** |

---

## Prior Findings Status (2026-05-01 follow-up)

### F1 — SECRETS-001 — High → **RESOLVED** (2026-04-30)

**File:** `internal/hooks/hooks.go:247`

**Code (current, confirmed):**
```go
cmd.Env = append(security.ScrubEnv(os.Environ(), nil), hookEnv(event, payload)...)
```

**Issue (from prior scan):** Hooks dispatcher forwarded the full parent environment — including `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `DEEPSEEK_API_KEY`, `GITHUB_TOKEN`, `AWS_*`, `DFMC_WEB_TOKEN`, etc. — to every user-configured hook subprocess.

**Verification path (2026-05-01 follow-up):**
1. Read `internal/hooks/hooks.go:247` — `security.ScrubEnv(os.Environ(), nil)` confirmed present
2. Read `internal/mcp/client.go:57` — MCP path also uses `ScrubEnv` (confirmed in prior scan)
3. Read `internal/security/env_scrub.go` — deny-list covers `*_API_KEY`, `*_TOKEN`, `*_SECRET`, `*_PASSWORD`, `AWS_ACCESS_KEY_ID`, `GH_TOKEN`, `NPM_TOKEN`, etc.
4. Confirmed no changes to these files in commits `8bd65a1`, `49cfd36`, `e0c3f7c`, `be5afa2`, `6c5ea8f`

**CVSS 3.1:** 8.1 (High) → RESOLVED — AV:L/AC:H/PR:L/UI:R/S:C/C:H/I:H/A:N

**Confidence:** 100 — verified by reading both call sites.

---

### F2 — DATA-001 — Medium — Persistent data unencrypted at rest

**File:** `internal/storage/store.go:82`

**Status:** Unchanged — accepted as design tradeoff.

**Code:**
```go
db, err := bbolt.Open(dbPath, 0o600, &bbolt.Options{
    Timeout:      1 * time.Second,
    FreelistType: bbolt.FreelistMapType,
})
```

**Threat model:**
- Local same-user attacker: blocked by OS process boundary.
- Local other-user on shared host: blocked by `0o600` permissions on Unix; NTFS ACLs on Windows.
- In-scope: backup / disk image leak, cloud-synced `.dfmc/` directory.

**Compensating controls:**
- File permissions `0o600` (explicitly set).
- Project-scoped storage (`.dfmc/dfmc.db`) — never in shared `/tmp` or system dirs.
- `.dfmc/` gitignored.
- Documented as intentional in `CLAUDE.md`.

**CVSS 3.1:** 5.5 (Medium) — AV:L/AC:L/PR:L/UI:N/S:U/C:H/I:N/A:N

**Recommendation:** Document that `.dfmc/` should not live on cloud-synced paths without disk-level encryption.

**Confidence:** 100.

---

### F3 — SECRETS-002 — Medium — Developer environment hygiene

**File:** `.env:8,11,29` (local working copy; not in git)

**Status:** Unchanged — operator action required.

**Verification path (2026-05-01):**
1. Confirmed `.env` in `.gitignore:26` — not tracked.
2. `.env.example` exists with placeholder pattern.
3. Key format validation: Z.AI (`82ebd5d747cc...`), MiniMax (`sk-cp-VV4Dy...`), Kimi (`sk-kimi-PEPkGt...`) match vendor patterns.

**Recommendation to operator:**
1. Rotate the three keys at provider dashboards.
2. Replace with `<placeholder>` in `.env`; export real keys via shell only at runtime.
3. Never `git add -f .env`.

**CVSS 3.1:** 6.2 (Medium) — AV:L/AC:L/PR:H/UI:N/S:U/C:H/I:H/A:N

**Confidence:** 100.

---

## Cleared Patterns (would-be findings, mitigation verified)

These are categories where automated scanners might raise alarms; verified to be properly mitigated and not findings:

| Category | Mitigation | Evidence |
|----------|------------|----------|
| Command injection (run_command) | argv-only `exec.Command`, blocked-binary list, shell-metachar guard, timeout cap 120s | `internal/tools/command.go:137`, `ensureCommandAllowed` at line 280 |
| Git flag injection (CVE-2018-17456 class) | `rejectGitFlagInjection` on every ref/path arg | `internal/tools/git_runner.go:128-136`, applied in git.go & patch_validation.go |
| Hook env-var value injection | `sanitizeEnvValue` (Unix single-quote wrap; Windows double-quote + `%%`) | `internal/hooks/hooks.go:357-398` |
| Path traversal | `EnsureWithinRoot` + `filepath.EvalSymlinks` on root and target; ancestor-walk for non-existent paths | `internal/tools/engine.go:920-960`, tested in `ensure_within_root_test.go` |
| TOCTOU symlink swap | `resolveExistingAncestor` walk before EvalSymlinks; tested in `ensure_within_root_test.go` | same |
| WS origin spoofing | Allowlist; `*` explicitly rejected with stderr warning | `ui/web/server.go:238-268` |
| WS DoS (slow-loris, oversize, conn flood) | 64KiB read limit; 60s read deadline + pong; 5s write deadline; 64 global / 8 per-IP cap; 5rps + burst 10 per-conn rate limit; sync.Once cleanup | `ui/web/server_ws.go:41-67`, `233-256`, `483-528` |
| SSE slow-loris | `writeSSEWithDeadline` 15s per-chunk; handlers bail on deadline expiry | `ui/web/server_chat.go:183-203` |
| CORS overpermissive | No `Access-Control-Allow-*` headers ever set; localhost-bind by default | grep verified zero matches |
| CSRF | Bearer-token-in-header auth; no cookies; `Set-Cookie` zero matches | `ui/web/server.go:681-699` |
| Clickjacking | `X-Frame-Options: DENY` + CSP `default-src 'self'` on every response | `ui/web/server.go:128-135`, `406` |
| Mass assignment | All web handlers decode into typed structs (no `map[string]any` → struct merge); 4MiB body cap | `ui/web/server.go:50-123`, `446` |
| Token timing attack | `crypto/subtle.ConstantTimeCompare` for bearer token | `ui/web/server.go:693` |
| 0.0.0.0 bind without auth | `normalizeBindHost` forces loopback when `auth: none` | `ui/web/server.go:173` |
| Unsafe deserialization | Only JSON / YAML-into-typed-struct; no `gob`, no `eval`-style decoders; 8MiB scan buffer cap on conversation JSONL | grep verified |
| SSRF | `internal/security/safe_http.go:isBlockedDialTarget()` blocks 10/8, 172.16/12, 192.168/16, 127/8, 169.254/16, fc00::/7, multicast, unspecified; DNS-rebinding TOCTOU fixed via pinned-IP dial | applied via wrapped `DialContext` |
| File upload | No multipart parser anywhere — surface absent | grep verified zero `multipart` references |
| Open redirect | No `http.Redirect` calls; no `Location:` header writes | grep verified zero matches |
| Tool approval bypass | All tool exec funnels `engine.executeToolWithLifecycle`; `SourceUser` allow-list documented; MCP-Drive deliberate bypass tagged in CLAUDE.md | `internal/engine/engine_tools.go:290-331` |
| Memory ID collision | crypto/rand 6-byte suffix on bbolt entry IDs | `internal/memory/store.go:142-146` |
| Conversation save race vs Storage close | `saveWg sync.WaitGroup` drained in `Close()` before bbolt close | `internal/conversation/manager.go:327-368` |
| Patch validation timeout | `120 * time.Second` literal (was previously nanoseconds bug) | `internal/tools/patch_validation.go:190` |

---

## Changes Since Prior Scan

| Commit | Description | Security surface changes |
|--------|-------------|--------------------------|
| `8bd65a1` | refactor: code quality improvements | None |
| `49cfd36` | docs: security scan results update | None |
| `e0c3f7c` | refactor: keyboard shortcut improvements | None |
| `be5afa2` | refactor: add patchViewState struct | None |
| `6c5ea8f` | test: explicit setState in non-Init fixtures | None |

**Net change:** 0 new vulnerabilities introduced.

---

## Confidence Distribution

| Confidence | Count | Notes |
|-----------|-------|-------|
| 100 | 3 | F1 (resolved), F2, F3 |
| 75-99 | 0 | — |
| 50-74 | 0 | — |
| <50 | 0 | — |

All verified findings cite exact file:line + verified guard absence. No speculative entries.

---

*Report generated by security-check skill — 4-phase pipeline (Recon → Hunt → Verify → Report) — DFMC at 2026-05-01.*
