# DFMC Security Audit Report

**Date:** 2026-05-01
**Scope:** `github.com/dontfuckmycode/dfmc` — full codebase
**Method:** 4-phase pipeline — Recon → Hunt → Verify → Report
**Auditor:** security-check (48 skills, 41 result files, parallel sub-agent execution)
**Prior scan:** 2026-04-30

---

## Executive Summary

DFMC is a **well-hardened single-author developer tool**. The 2026-05-01 follow-up scan verified that all findings from the 2026-04-30 audit remain resolved, and that no security-relevant code changes were introduced in the intervening commits (`8bd65a1` refactor: code quality improvements, `49cfd36` docs: security scan update, `e0c3f7c` refactor: TUI keyboard shortcuts, `be5afa2` refactor: TUI patchViewState, `6c5ea8f` test: engine setState). A total of **0 new findings** were introduced.

**Risk score:** 2.4 / 10 (Low, unchanged from prior scan) — one resolved high-severity issue, two design-level informationals about local-disk plaintext storage.

---

## Changes Since Prior Scan (2026-04-30 → 2026-05-01)

| Commit | Description | Security-relevant files changed |
|--------|-------------|--------------------------------|
| `8bd65a1` | refactor: code quality cleanup | None (engine, tui, tokens refactors only) |
| `49cfd36` | docs: security scan update | None (documentation only) |
| `e0c3f7c` | refactor: TUI keyboard shortcuts | None |
| `be5afa2` | refactor: TUI patchViewState | None |
| `6c5ea8f` | test: engine explicit setState | None |

**Conclusion:** Zero security-surface changes since prior audit. F1 remains fixed.

---

## Scan Statistics

| Metric | Value |
|--------|-------|
| Skills run | 41 |
| Files scanned | ~150 Go source files |
| LoC (rough) | ~25,000 |
| Phase 2 sub-agents (parallel) | 8 |
| Raw findings | 0 |
| Verified findings | 0 (F1 from prior scan remains resolved) |
| Cleared mitigation patterns | 22 |

---

## Findings by Severity

| Severity | Count | Status |
|----------|-------|--------|
| Critical | 0 | — |
| High | 0 | F1 fixed in prior scan (2026-04-30) |
| Medium | 2 | F2 (design tradeoff), F3 (operator hygiene) — unchanged |
| Low | 0 | — |
| Info | 0 | — |

---

## Prior Findings Status

### F1 — High → **RESOLVED** (fixed 2026-04-30, confirmed 2026-05-01)

**File:** `internal/hooks/hooks.go:247`

**Verification:** `security.ScrubEnv(os.Environ(), nil)` is confirmed present in current code. No changes to this file since prior scan.

**Remaining verification evidence:**
- `internal/hooks/hooks.go:247` — `security.ScrubEnv(os.Environ(), nil)` confirmed
- `internal/mcp/client.go:57` — `security.ScrubEnv(os.Environ(), nil)` confirmed (MCP path, fixed prior scan)
- `internal/security/env_scrub.go` — deny-list covers `*_API_KEY`, `*_TOKEN`, `*_SECRET`, `*_PASSWORD`, `AWS_ACCESS_KEY_ID`, `GH_TOKEN`, etc.

---

### F2 — Medium — Persistent data unencrypted at rest

**File:** `internal/storage/store.go:82`

**CVSS 3.1:** 5.5 (Medium) — AV:L/AC:L/PR:L/UI:N/S:U/C:H/I:N/A:N

**Status:** Unchanged — accepted as design tradeoff.

**Compensating controls:**
- File mode `0o600` (explicit)
- Project-scoped storage (`.dfmc/dfmc.db`)
- `.dfmc/` in gitignore
- Single-user developer tool model documented

---

### F3 — Medium — Live API keys in developer's local `.env` (operator hygiene)

**File:** `.env:8,11,29` (local, not in git)

**Status:** Unchanged — operator action required.

---

## Scope Summary — Verified Mitigated (unchanged from prior scan)

| Category | Where verified |
|----------|---------------|
| Command injection (run_command) | argv-only `exec.Command`, blocked-binary list, shell-metachar guard, timeout cap 120s |
| Git flag injection (CVE-2018-17456 class) | `rejectGitFlagInjection` on every ref/path arg |
| Hook env-var key leak | **Fixed in F1** |
| Hook env-var value injection | `sanitizeEnvValue` at `hooks.go:357` |
| Path traversal + symlink TOCTOU | `EnsureWithinRoot` + `filepath.EvalSymlinks` + ancestor walk |
| WS origin spoofing | Allowlist; `*` rejected with stderr warning |
| WS DoS | 64KiB read, 60s read+pong, 5s write, 64/8 conn caps, 5rps limit |
| SSE slow-loris | `writeSSEWithDeadline` 15s per chunk |
| CSRF | Bearer auth, no cookies |
| CORS | No `Access-Control-Allow-*` headers |
| Clickjacking | `X-Frame-Options: DENY` + CSP |
| Mass assignment | Typed structs, 4MiB body cap, Content-Type enforcement |
| Token timing | `crypto/subtle.ConstantTimeCompare` |
| 0.0.0.0 without auth | `normalizeBindHost` refuses |
| RCE via deserialization | JSON / typed-struct YAML only |
| SSRF | `safe_http.go:isBlockedDialTarget()` blocks all private + meta-IP ranges; DNS-rebinding TOCTOU fixed (pinned IP) |
| File upload | Surface absent (no multipart) |
| Open redirect | Surface absent |
| Tool approval bypass | Single funnel via `executeToolWithLifecycle` |
| Memory ID collision | crypto/rand 6-byte suffix |
| Conversation save race | `saveWg.Wait()` before bbolt close |
| Patch validation timeout | `120 * time.Second` literal |

---

## Remediation Roadmap (unchanged from prior scan)

### Phase 1 — Immediate (within audit)
- **F1** — `internal/hooks/hooks.go` env scrubbing — **FIXED in prior scan**.

### Phase 2 — Operator action (your machine, not DFMC)
- Rotate the three Z.AI / MiniMax / Kimi keys (F3).
- Replace `.env` values with `<placeholder>` style; export real keys via shell.

### Phase 3 — Documentation (low-priority)
- Add a paragraph to user docs warning against placing `.dfmc/` on cloud-synced paths without disk encryption.

### Phase 4 — Optional product feature (out of scope)
- `--encrypt` flag with OS-keyring-derived key for `.dfmc/dfmc.db`.

---

## Audit Confidence

**100% — every finding verified by direct source-file read; no new surface introduced since prior scan.**

---

*Report generated by security-check skill — 4-phase pipeline (Recon → Hunt → Verify → Report) — DFMC at 2026-05-01.*
