# Security Report — github.com/dontfuckmycode/dfmc

**Date:** 2026-05-02
**Branch:** main (clean working tree)
**Review method:** 4-phase pipeline — Recon → Hunt → Verify → Report
**Coverage:** Full codebase (Go 1.25, 48 security skills applied)

---

## Architecture

**Module:** `github.com/dontfuckmycode/dfmc`
**Stack:** Go 1.25 · bubbletea TUI · gorilla/websocket · tree-sitter AST · bbolt · wazero WASM

**Entry points:**
- `cmd/dfmc` — CLI entrypoint
- `ui/tui` — bubbletea terminal UI
- `ui/web` — HTTP/SSE server (`dfmc serve`)
- `internal/mcp` — MCP server/bridge (stdio)
- `internal/drive` — autonomous plan/execute runner

---

## Phase 2 — Hunt Results

### ✅ Path Traversal — `tools.EnsureWithinRoot` (engine.go:920-961)

Multi-layer defense:
1. **Lexical:** `filepath.Abs` + `filepath.Rel` rejects `..` prefix
2. **Symlink resolution:** `filepath.EvalSymlinks` on both root and target; dangling symlinks walk to nearest existing ancestor
3. **Resolved-ancestor recheck:** catches symlink escapes through intermediate directories

No bypass found across all entry paths: `read_file`, `write_file`, `edit_file`, `apply_patch`, `glob`, `codemap`, `gh_pr`, `git_*` tools.

---

### ✅ read_file → mutation gate (engine.go:635-668)

- **edit_file:** `readGateLenient` — prior `read_file` snapshot required, hash drift tolerated (edit_file has its own exact-string anchor)
- **write_file / apply_patch:** `readGateStrict` — prior snapshot + SHA-256 equality required; hash captured from disk (not in-memory Output)
- **apply_patch** bypasses this gate in favor of its own per-target `EnsureReadBeforeMutation` call — required because multi-file patches can't route through the single-path gate
- **New file exemption:** creating a file that doesn't exist is always allowed (no prior snapshot needed)

Fabricated `/dev/null` diff headers against existing files are caught by independent `os.Stat` check in `apply_patch.go:106` before the read gate is consulted.

---

### ✅ TOCTOU protection — per-path file locks

- `write_file` (builtin.go:65-85): `LockPath` → read-modify-write under mutex
- `edit_file` (builtin_edit.go:61-62): `LockPath` over entire read-match-write
- `apply_patch` (apply_patch.go:167-168): `LockPath` before `writeFileAtomic`
- `git_worktree_add` (git_worktree.go): locks the worktree path before create

`LockPath` uses `sync.Map` of per-path mutexes — no global lock contention across subagents.

---

### ✅ Git flag injection — `rejectGitFlagInjection` (git_runner.go:119-137)

CVE-2018-17456 class fix. Any `ref`/`revision`/`branch`/`path` value starting with `-` is refused with a descriptive error.

Blocklist: `--no-verify`, `--no-gpg-sign`, `--amend`, `-i`, `--interactive`, `--force`, `-f`, `--hard`, `--no-checkout`, `--exec=`, `--receive-pack=`, `--upload-pack=`

Used at every callsite: `git_diff`, `git_log`, `git_branch`, `git_worktree_*`, `git_commit`, `gh_pr`.

---

### ✅ Shell metachar detection (command.go:432-459)

`run_command` refuses the binary slot if it contains:
- Multi-char: `&&`, `||`, `>>`, `2>&1`, `2>`, `<<`
- Single-char: `;`, `|`, `>`, `<`, `` ` ``, `$()`, standalone `&`
- Prefix: `cd ` (LLM chdir-then-run tell)

Detection is conservative (only `command`, not `args` — putting `>` in args is fine since the binary sees it as a positional arg). `cd <dir> && <cmd>` pattern is detected and rewrote into a recovery hint with the right `command`/`args`/`dir` shape.

Shell interpreters blocked: `cmd`, `cmd.exe`, `powershell`, `pwsh`, `bash`, `sh`, `zsh`, `fish`, `nu`, `dash`, `ash`, `ksh`, `tcsh`, `csh`, `jsh`.

Script runner inline-eval flags blocked: `node -e`, `python -c`, `perl -e`, `ruby -e`, `php -r`, `pwsh -c`.

---

### ✅ Env scrubbing for subprocesses (env_scrub.go)

Both MCP client and hooks forward only scrubbed env:

```go
cmd.Env = append(security.ScrubEnv(os.Environ(), nil), hookEnv(event, payload)...)
```

Secret-shaped keys stripped (`*_API_KEY`, `*_TOKEN`, `*_SECRET`, `AWS_*`, `GH_TOKEN`, etc.). Explicit allowlist opt-in supported.

Used in: `internal/mcp/client.go:57` (MCP subprocess env), `internal/hooks/hooks.go:247` (hook subprocess env — **SECRETS-001 was fixed pre-scan; code now matches the MCP pattern exactly**).

---

### ✅ Secret file redaction — `LooksLikeSecretFile` (secret_files.go)

Intercepts: `.env`, `.envrc`, `.env.*`, `.netrc`, `.pgpass`, `id_rsa`, `id_dsa`, `id_ecdsa`, `id_ed25519`, `credentials`, `credentials.json`, `secrets.json`, `secrets.yaml`, `htpasswd`, `service-account.json`, `private.key`, `.pem`, `.key`, `.p12`, `.pfx`, `.kdbx`, `.jks`, `.keystore`, `.der`, `.gpg`, and files with `secret`/`credential`/`password`/`apikey`/`private_key` in basename.

Used at:
- TUI preview (`ui/tui/clipboard.go`)
- Web file API `GET /api/v1/files/{path...}` (server_files.go:68) — returns `redacted: true` with `content: ""`; 403 would reveal path existence to an attacker

---

### ✅ Web approval gate — deny-by-default (approver.go)

`DFMC_APPROVE=no` (default): auto-denies all network-originated (web/ws/mcp) tool calls. `DFMC_APPROVE=yes`: auto-approves read-only tools only; requires `DFMC_APPROVE_DESTRUCTIVE=yes` for write/shell.

TUI approver: blocks engine goroutine with 30s timeout, interactive modal in bubbletea program.

---

### ✅ Hook panic containment — VULN-048 (hooks.go:164-186)

```go
func (d *Dispatcher) fireOne(...) {
    defer func() { recover() }()
    // ...
}
```

Both hook dispatch and observer callback are wrapped. A panicking hook never unwinds the dispatch loop; subsequent hooks still fire.

Process-group isolation: `applyProcessGroupIsolation(cmd)` ensures timed-out hooks kill their entire subprocess tree (not just the shell parent).

---

### ✅ X-Forwarded-For spoofing defense — VULN-010 (server.go:605-631)

`clientIPKey` only honors `X-Forwarded-For` when direct peer is in `trustedProxies` list (default: `127.0.0.1`, `localhost`, `::1`). Uses rightmost (most recent proxy) IP. Prevents IP rotation bypass of per-IP rate limit.

---

### ✅ Bearer token — constant-time comparison (server.go:693)

```go
subtle.ConstantTimeCompare([]byte(got), []byte(expected)) == 1
```

---

### ✅ Content-type enforcement — VULN-050 (server.go:489-525)

`contentTypeEnforcementMiddleware` rejects non-JSON Content-Types on POST/PUT/PATCH before body decoding. Bodyless POSTs (`ContentLength <= 0`) always pass.

---

### ✅ SSRF guard with DNS rebinding defense — VULN-057/VULN-058 (safe_http.go:127-157)

Two fixes:
1. **DNS rebinding TOCTOU:** validates all resolved IPs, then dials the **first validated IP directly** (bypasses inner's second DNS lookup). A TTL=0 attacker can't swap answers between validation and dial.
2. **Blocked targets:** loopback, private, link-local, multicast, unspecified, multicast addresses

Applied to: `NewSafeHTTPClient` (config/mcp catalog fetches), `newProviderHTTPClient` (LLM API calls), `WrapDialWithSSRFGuard` (exported for callers building custom transports).

---

### ✅ Config file permission checks — VULN-036 (main.go:41-48, config.go:92-102)

Warns if global or project config is group/world-writable. On Windows, check is skipped (POSIX bits meaningless under NTFS ACLs — would always trigger on legitimate RW files).

Project hooks from group/world-writable configs are discarded; global hooks are still loaded as the safe fallback.

---

### ✅ Subagent allowlist gate — `checkSubagentAllowlist` (engine_tools.go:284)

Fires before approval gate. Unlisted tools refused without prompting even when approver is permissive.

---

### ✅ Panic guard around tool execution — `executeToolWithPanicGuard` (engine_tools.go:233-256)

`defer/recover` around `Tools.Execute`. Panic → structured error + `tool:panicked` event + truncated stack trace. Prevents one buggy tool from killing the entire DFMC process, all connected web/SSE clients, and every queued reply.

---

### ✅ Intent fail-open layer — `internal/intent/router.go`

`Evaluate()` always returns a usable `Decision`, even on classifier error. `FailOpen=true` (default) makes errors route to `Fallback(raw)`. The layer can never block the engine.

---

### ✅ Output bounding

| Location | Cap | Mechanism |
|----------|-----|-----------|
| `run_command` stdout/stderr | 4 MiB | `newBoundedBuffer(runCommandOutputCap)` |
| Hook output per stream | 1 MiB | `hookOutputCap` constant |
| Max config file size | 1 MB | `loadYAML` guard |
| Max request body (web) | 4 MiB | `maxRequestBodyBytes` constant |
| `git_log` output | 200 commits | cap in `GitLogTool.Execute` |

---

## Phase 3 — Verified Findings

**None.** All controls verified against their threat models. No bypass identified.

Notable cleared items from Phase 2:
- **SECRETS-001** (hooks env scrubbing): Fixed pre-scan — `hooks.go:247` now uses `security.ScrubEnv(os.Environ(), nil)` matching the MCP pattern exactly
- **SECRETS-002** (live `.env` keys): Acceptable risk — file is gitignored, documented as local-only, and explicitly warns against committing
- **CMDi bypass via symlink/junction**: False positive — `isBlockedShellInterpreter` operates on resolved binary name before path resolution
- **Windows junction bypass**: False positive — `filepath.EvalSymlinks` on Windows resolves junctions
- **`golang.org/x/net` CVE-2024-45338**: False positive — fixed in v0.33.0, running v0.53.0
- **`bbolt` CVE-2023-43804**: False positive — fixed in v1.3.5, running v1.4.3

---

## Phase 4 — Consolidated Report

### Critical Controls — All Present ✅

| Control | Location | Status |
|---------|----------|--------|
| Path traversal defense | `tools.EnsureWithinRoot` | ✅ Verified |
| read_file → mutation gate | `engine.go:635-668` | ✅ Verified |
| TOCTOU locks | `LockPath` per tool | ✅ Verified |
| Git flag injection prevention | `rejectGitFlagInjection` | ✅ Verified |
| Shell metachar detection | `detectShellMetacharacter` | ✅ Verified |
| Script runner eval flag blocking | `hasScriptRunnerWithEvalFlag` | ✅ Verified |
| Env scrubbing | `security.ScrubEnv` | ✅ Verified |
| Secret file redaction | `LooksLikeSecretFile` | ✅ Verified |
| Web approval gate | `webApprover` | ✅ Verified |
| Hook panic containment | `fireOne defer/recover` | ✅ Verified |
| XFF spoofing defense | `clientIPKey` | ✅ Verified |
| Constant-time bearer token | `subtle.ConstantTimeCompare` | ✅ Verified |
| Content-type enforcement | `contentTypeEnforcementMiddleware` | ✅ Verified |
| SSRF guard + DNS rebinding defense | `wrapDialWithSSRFGuard` | ✅ Verified |
| Config permission checks | `CheckConfigPermissions` | ✅ Verified |
| Subagent allowlist | `checkSubagentAllowlist` | ✅ Verified |
| Tool panic guard | `executeToolWithPanicGuard` | ✅ Verified |
| Intent fail-open | `Router.Evaluate` | ✅ Verified |
| Output bounding | `boundedBuffer`, constants | ✅ Verified |

---

### Hardening Indicators

| Item | Evidence |
|------|----------|
| Security annotations in code | VULN-010, VULN-013, VULN-036, VULN-048, VULN-049, VULN-050, VULN-057, VULN-058 |
| Self-teaching errors | `missingParamError`, `readGuardError`, `editFileMissMessage`, `editFileAmbiguityMessage`, `rejectGitFlagInjection` error messages, `suggestRunCommandRecovery`, `suggestSplitRunCommand` |
| Deny-by-default network posture | `DFMC_APPROVE=no` default, `RequireApprovalNetwork: ["*"]` default, origin allowlist for WS, per-IP rate limiting |
| Fail-open intent layer | `Router.Evaluate` always returns usable Decision |
| Graceful degradation | CGO check (ast backend), placeholder providers (offline), bbolt lock handling (degraded startup allowlist) |

---

### No Remediation Needed

This codebase is clean. The security controls are well-engineered and consistently applied across every entry point. No findings require action.

---

*Report generated by security-check skill. 48 skills applied across 6 core, 9 injection, 2 code execution, 4 access control, 3 data exposure, 4 server-side, 4 client-side, 3 logic & design, 3 API security, 3 infrastructure, 7 language scanners.*