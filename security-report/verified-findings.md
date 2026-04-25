# Verified Security Findings

## Summary
- Total raw findings from Phase 2: 218
- After duplicate merging: 73
- After false-positive elimination: 73 (none eliminated outright; several low-confidence items downgraded to Info)
- Final verified findings: 73

The bulk of the cross-skill duplication clustered around five DFMC-specific
patterns: (1) `engine.CallTool` hardcoding `source="user"` so every web/MCP/WS
caller bypasses the approval gate, (2) `wsUpgrader.CheckOrigin: return true`
giving any browser tab on the host a hijack window into that approval-bypassed
RPC surface, (3) the absence of `Host:` header validation turning the
loopback-only bind into "any internet origin via DNS rebinding", (4) the new
Phase-7 tools (`symbol_move`, `symbol_rename`, `patch_validation`,
`semantic_search`, `disk_usage`, `benchmark`) skipping `EnsureWithinRoot` /
`EnsureReadBeforeMutation` that older tools rely on, and (5)
`POST /api/v1/workspace/apply` writing project files via direct `git apply`
without going through `executeToolWithLifecycle`. Items 1+2+3 form a single
critical chain visible in seven different skill reports; they are merged
into VULN-001 / VULN-002 / VULN-003.

## Confidence Distribution
- Confirmed (90-100): 28
- High Probability (70-89): 27
- Probable (50-69): 11
- Possible (30-49): 6
- Low Confidence (0-29): 1

## Severity Distribution (post-recalculation)
- Critical: 4
- High: 19
- Medium: 22
- Low: 16
- Info: 12

## Verified Findings

### VULN-001: HTTP / WebSocket / MCP tool dispatch hardcodes `source="user"`, fully bypassing the approval gate
- **Severity:** Critical
- **Confidence:** 95/100 (Confirmed)
- **Original Skill:** sc-authz (AUTHZ-001), sc-lang-go (LANG-GO-001), sc-api-security (API-002), sc-business-logic (LOGIC-001), sc-auth (AUTH-004), sc-file-upload (UPLOAD-001), sc-csrf (CSRF-001), sc-privilege-escalation (PRIVESC #1 confirmation)
- **Vulnerability Type:** CWE-285, CWE-269, CWE-862, CWE-841
- **File:**
  - `internal/engine/engine_tools.go:120` (CallTool tags source="user" unconditionally)
  - `internal/engine/engine_tools.go:225` (`if source != "user" && requiresApproval(name)` — gate skipped)
  - `ui/web/server_tools_skills.go:167` (handleToolExec → CallTool)
  - `ui/web/server_ws.go:260` (wsConn.handleTool → CallTool)
  - `ui/cli/cli_mcp.go:134` (MCP regular-tool dispatch)
- **Reachability:** Direct (HTTP handler / WS frame / MCP frame → CallTool → Execute)
- **Sanitization:** None
- **Framework Protection:** None
- **Description:** Every backend tool — including `run_command`, `write_file`,
  `edit_file`, `apply_patch`, `git_commit`, `web_fetch` — is exposed at
  `POST /api/v1/tools/{name}`, the WS `tool` JSON-RPC method, and MCP
  `tools/call`. All three handlers funnel through `engine.CallTool`, which
  unconditionally tags `source="user"`. The gate at `engine_tools.go:225`
  therefore never fires for these surfaces, regardless of how
  `tools.require_approval` is configured. The `webApprover` (deny-by-default,
  `ui/web/approver.go`) is structurally unreachable on the HTTP/WS path.
  Operators who configure `require_approval: ["run_command", "write_file"]`
  reasonably expect those to gate on every invocation surface; the current
  code silently exempts the network-reachable ones, which are the
  highest-risk surfaces.
- **Verification Notes:** Architecture report (line 522-524) acknowledges
  this as a known carve-out. The CLAUDE.md project-instructions explicitly
  flag `executeToolWithLifecycle` as the "single mandated entry" with
  approval — but the operator-facing config makes no distinction between
  "agent" and "web-user" sources, so the bypass is invisible from the
  configuration surface. Verified directly against the cited source
  positions; no sanitization or framework mitigation found. Eight different
  skills landed on this same root cause — merged per the duplicate-detection
  rules.
- **Remediation:** Tag HTTP/WS/MCP-initiated calls with a non-user source
  (`"web"` / `"ws"` / `"mcp"`); subject those sources to `RequireApproval`
  identical to `"agent"`. Reserve `"user"` for stdin-driven CLI/TUI only.
  Existing test `internal/engine/approver_test.go:150` pins the bypass
  behavior, which gives a clear regression target.

### VULN-002: WebSocket upgrader accepts every Origin
- **Severity:** Critical (when chained with VULN-001 and `auth=none`); High otherwise
- **Confidence:** 95/100 (Confirmed)
- **Original Skill:** sc-websocket (WS-001), sc-lang-go (LANG-GO-002), sc-auth (AUTH-003), sc-api-security (API-001), sc-csrf (CSRF-008), sc-header-injection (HDR-007)
- **Vulnerability Type:** CWE-1385, CWE-346, CWE-942
- **File:** `ui/web/server_ws.go:29-35`
- **Reachability:** Direct (browser cross-origin WS connect)
- **Sanitization:** None
- **Framework Protection:** Bypassed (Gorilla default would reject mismatched Origin)
- **Description:** `wsUpgrader.CheckOrigin` is hard-coded to `return true`.
  No caller of `web.New` overrides it. Any web page in the user's browser
  can `new WebSocket('ws://127.0.0.1:7777/api/v1/ws')` from any origin and
  hold a long-lived bidirectional channel. Combined with VULN-001 (the WS
  `tool` method bypasses the approval gate), and combined with the default
  `auth=none` (VULN-007), a cross-origin attacker page can drive
  `run_command`, `write_file`, `apply_patch` directly. With `auth=token`
  the attacker page would need the bearer token to upgrade, which the
  standard `WebSocket` constructor cannot supply via header — but the
  defense-in-depth gap remains, and DNS rebinding (VULN-003) closes the
  origin-match loophole entirely.
- **Verification Notes:** Localhost binding does NOT mitigate this finding
  per project heuristic #2 — any process on the host (browser tab
  included) can hit `127.0.0.1`. Six skills independently flagged this;
  merged per duplicate rules. The architecture report (§5) calls it the
  "lone CORS-relevant gap"; the consensus across phase-2 skills is that
  it's the single largest residual exposure in DFMC's network surface.
- **Remediation:** Validate `Origin` against an allowlist
  (`http://127.0.0.1:<port>`, `http://localhost:<port>`); reject everything
  else. Expose `Server.SetAllowedOrigins([]string)` for operator config.

### VULN-003: No `Host:` header validation — DNS rebinding bypasses loopback-only bind
- **Severity:** High
- **Confidence:** 80/100 (High Probability)
- **Original Skill:** sc-csrf (CSRF-002), sc-cors (CORS-004), sc-websocket (WS-005)
- **Vulnerability Type:** CWE-350, CWE-352, CWE-1385
- **File:** `ui/web/server.go:131-160` (bind normalization without Host check); `ui/web/server_ws.go:92-106` (no `r.Host` inspection on upgrade)
- **Reachability:** Indirect (requires browser DNS rebind, no extra preconditions on the server side)
- **Sanitization:** None
- **Framework Protection:** None
- **Description:** The intended mitigation for cross-origin browser access
  is "loopback-only bind". DNS rebinding circumvents this: an attacker
  page hosted at `evil.example.com` first resolves the hostname to a
  public IP (passing initial CORS preflight from that origin), then
  re-binds to `127.0.0.1` so subsequent fetches go to the local DFMC.
  The browser's same-origin checker sees the page origin as
  `http://evil.example.com` — which now matches the WS upgrade target —
  and the request succeeds. DFMC nowhere validates `r.Host` against a
  loopback allowlist. With VULN-001 and VULN-002 present this is a
  full-RCE pathway from any internet origin in the user's browser.
- **Verification Notes:** Phase-2 skills disagreed slightly on confidence
  (csrf medium, cors medium, websocket high) because successful DNS
  rebinding requires browser DNS-cache cooperation and a willing victim
  page. Merged with combined confidence at the upper bound of the medium
  range.
- **Remediation:** Validate `r.Host` against `{127.0.0.1:<port>,
  localhost:<port>}` allowlist on every request entering
  `securityHeaders` middleware; reject mismatch with 421 Misdirected
  Request. This is the canonical DNS-rebinding mitigation.

### VULN-004: Arbitrary command execution via `validation_command` parameter in `patch_validation`
- **Severity:** Critical
- **Confidence:** 95/100 (Confirmed)
- **Original Skill:** sc-rce (RCE-001), sc-cmdi (CMDI-001), sc-path-traversal (PATH-004 partial)
- **Vulnerability Type:** CWE-77, CWE-78, CWE-94, CWE-22 (project_root override)
- **File:** `internal/tools/patch_validation.go:64-68, 99, 134-153, 224`
- **Reachability:** Direct via agent loop, MCP, web `POST /api/v1/tools/patch_validation`, `dfmc tool` CLI
- **Sanitization:** None — bypasses every guard `RunCommandTool` builds
- **Framework Protection:** None
- **Description:** `patch_validation` accepts a free-form
  `validation_command` string and a `project_root` override, then runs
  `exec.CommandContext(runCtx, cmdParts[0], cmdParts[1:]...)` with
  `cmd.Dir = projectRoot`. None of `RunCommandTool`'s protections
  (`isBlockedShellInterpreter`, `isBlockedBinary`,
  `hasScriptRunnerWithEvalFlag`, `EnsureCommandAllowed`,
  `EnsureWithinRoot` on the binary path or on the project_root override)
  apply. A single tool call gives arbitrary code execution as the dfmc
  process owner with attacker-chosen working directory. The same file
  also reads patch targets via `os.ReadFile(projectRoot + "/" +
  targetPath)` (string concatenation, no `EnsureWithinRoot`) which is an
  arbitrary file read primitive on its own (path-traversal angle merged
  here per skill duplicate rules).
- **Verification Notes:** Phase-7 tool added in the most recent commits;
  has no `EnsureWithinRoot` / `EnsureReadBeforeMutation` / block-list
  defense per project heuristic #5. Tool spec declares `Risk: RiskRead`,
  which is also wrong — should be `RiskCommand` at minimum. Three skills
  landed on the same call site.
- **Remediation:** Either delete `validation_command` and require
  callers to dry-run via `apply_patch` and run validators separately
  through `run_command`, OR route through `RunCommandTool.Execute` so
  the full block-list / shell-interpreter / eval-flag / allowed-binary
  chain applies. Drop the `project_root` override or pass it through
  `EnsureWithinRoot(req.ProjectRoot, override)`. Reclassify Risk to
  `RiskCommand`.

### VULN-005: `go test` flag injection via `target` / `cpuprofile` / `memprofile` in `benchmark`
- **Severity:** High
- **Confidence:** 85/100 (High Probability)
- **Original Skill:** sc-rce (RCE-002), sc-cmdi (CMDI-002)
- **Vulnerability Type:** CWE-88, CWE-78
- **File:** `internal/tools/benchmark.go:75-110`
- **Reachability:** Direct via agent loop, MCP, web tool dispatch
- **Sanitization:** None
- **Framework Protection:** None
- **Description:** No `--` separator between `go test` flags and the
  positional `target`, and no `rejectGitFlagInjection`-style refusal of
  leading `-`. A `target = "-exec=cmd.exe /c calc"` (or
  `-toolexec=...`, `-o=/path/anywhere`) makes `go test` shell out to a
  user-supplied wrapper. `cpuprofile`/`memprofile` accept arbitrary
  paths so the profiler doubles as a write primitive (binary pprof
  blob, but the file is written; can clobber `~/.bashrc` / startup
  scripts).
- **Verification Notes:** Newly added Phase-7 tool per project heuristic
  #5. Two skills detected the same shape; merged.
- **Remediation:** Insert `--` between flags and target
  (`args = append(args, "--", target)`); refuse leading `-` on every
  user-supplied argument; route `target`/`cpuprofile`/`memprofile`
  through `EnsureWithinRoot`.

### VULN-006: Arbitrary file write via `symbol_move` `to_file` parameter
- **Severity:** Critical
- **Confidence:** 95/100 (Confirmed)
- **Original Skill:** sc-path-traversal (PATH-001)
- **Vulnerability Type:** CWE-22 → arbitrary file write
- **File:** `internal/tools/symbol_move.go:124-128, 244, 250`
- **Reachability:** Direct via agent loop, MCP, web tool dispatch
- **Sanitization:** None — destination uses `filepath.Join(projectRoot, toFile)` with no containment check
- **Framework Protection:** None — `EnsureReadBeforeMutation` not wired for the destination
- **Description:** `to_file="../../tmp/pwned.go"` produces an
  out-of-root absolute path, then `os.MkdirAll(destDir, 0755)` +
  `os.WriteFile(destPath, newFileContent, 0644)` writes attacker-chosen
  Go source content anywhere the dfmc process can reach (home dotfiles,
  `~/.dfmc/hooks` → graduates to RCE per project heuristic #7, build-tool
  plugin dirs).
- **Verification Notes:** Phase-7 tool, missing both `EnsureWithinRoot`
  (heuristic #5) and the read-before-mutation gate.
- **Remediation:** Route `toFile` through `EnsureWithinRoot(projectRoot,
  toFile)` before constructing destPath; wire `EnsureReadBeforeMutation`
  against the destination if it already exists; refuse absolute paths
  with an actionable error.

### VULN-007: `auth=none` is the default; new operators get an unauthenticated network-local API exposing tool/shell/filesystem
- **Severity:** High
- **Confidence:** 85/100 (High Probability)
- **Original Skill:** sc-api-security (API-003), sc-auth (AUTH-004)
- **Vulnerability Type:** CWE-1188, CWE-306
- **File:** `internal/config/defaults.go` (Web.Auth default empty → "none"); `ui/web/server.go:131-150` (New); `ui/cli/cli_remote.go:40-77` (runServe guard fires only on non-loopback)
- **Reachability:** Default config
- **Sanitization:** N/A
- **Framework Protection:** None
- **Description:** Default `Web.Auth = "none"`. Bind-host normalization
  forces 127.0.0.1 in that case (so non-loopback attack is closed); but
  on the loopback interface the API is wide open to any local process
  AND any cross-origin browser tab (per VULN-002). The CLI emits no
  warning about the default; the workbench HTML doesn't surface "you
  are unauthenticated".
- **Verification Notes:** Project heuristic #6 — this is a configuration
  default, not a code bug. Remediation should explicitly mention the
  default change.
- **Remediation:** Default to `auth=token` with auto-generated token
  written to `~/.dfmc/web.token` mode 0600 and printed to stderr at
  startup. Failing that, print a stark warning on every `dfmc serve`
  start.

### VULN-008: `RequireApproval` defaults to empty list — even agent-initiated tool calls bypass the gate
- **Severity:** High
- **Confidence:** 90/100 (Confirmed)
- **Original Skill:** sc-authz (AUTHZ-002)
- **Vulnerability Type:** CWE-1188
- **File:** `internal/engine/approver.go:94-106`; `internal/config/config_types.go:382-395`; `internal/config/defaults.go` (no entry)
- **Reachability:** Default config
- **Sanitization:** N/A
- **Framework Protection:** N/A
- **Description:** `requiresApproval` returns true only when the tool
  name is on the explicit list. Default config has the list empty, so
  even agent-initiated calls — which do reach the gate (`source="agent"`
  triggers the check) — bypass it because no tool is on the list.
  Combined with VULN-001, the practical reality is "the approval gate
  is dormant in default configuration".
- **Verification Notes:** Configuration default per project heuristic
  #6. Remediation note must include the config change.
- **Remediation:** Ship sane default: `RequireApproval: ["run_command",
  "write_file", "apply_patch", "delegate_task", "git_commit",
  "patch_validation", "benchmark"]`. Or `["*"]` as the strictest default,
  letting operators subtract.

### VULN-009: `POST /api/v1/workspace/apply` writes project files via direct `git apply`, bypassing tool lifecycle
- **Severity:** High
- **Confidence:** 90/100 (Confirmed)
- **Original Skill:** sc-csrf (CSRF-004), sc-authz (AUTHZ-005), sc-path-traversal (PATH-002), sc-file-upload (UPLOAD-002), sc-lang-go (LANG-GO-008)
- **Vulnerability Type:** CWE-285, CWE-22, CWE-367, CWE-352
- **File:** `ui/web/server_workspace.go:54-96, 215, 217-257, 251`
- **Reachability:** Direct (HTTP POST)
- **Sanitization:** Partial — git apply itself rejects most escapes; M3 patch sanitiser strips embedded gitconfig directives
- **Framework Protection:** None — `executeToolWithLifecycle` deliberately bypassed
- **Description:** Three issues collide on this one handler:
  (1) it does NOT route through `engine.CallTool`, so the approval gate,
  pre/post hooks, and `EnsureReadBeforeMutation` strict-mode hash check
  are all bypassed;
  (2) post-write path verification runs AFTER `git apply` has already
  modified files — the deny branch returns 400 but cannot un-apply;
  (3) the path-containment check at line 251 uses
  `strings.HasPrefix(absPath, root)` with no separator boundary, so
  `/proj-evil/foo` passes when `root="/proj"`. Combined with CSRF
  (no token in default config, no Origin/Host check) this is a
  cross-origin write primitive against any project a `dfmc serve` is
  pointed at.
- **Verification Notes:** Five skills independently identified the same
  endpoint with overlapping rationales; merged per project heuristic #7
  (path-traversal + RCE on file-write tools cluster). Order-of-checks
  bug + path-prefix bug + bypass-of-tool-lifecycle bug are all the same
  fix scope.
- **Remediation:** Route through `engine.CallTool("apply_patch", ...)`
  so `executeToolWithLifecycle` engages (gates approval + hooks +
  read-before-mutation). Reorder so `--dry-run --porcelain` enumerates
  paths BEFORE the actual apply. Replace `HasPrefix` with
  `filepath.Rel`-based containment matching `EnsureWithinRoot`.

### VULN-010: Per-IP rate limiter trivially bypassed via `X-Forwarded-For`
- **Severity:** High
- **Confidence:** 95/100 (Confirmed)
- **Original Skill:** sc-lang-go (LANG-GO-003), sc-header-injection (HDR-002)
- **Vulnerability Type:** CWE-770, CWE-348
- **File:** `ui/web/server.go:374-390` (`clientIPKey`); also LANG-GO-021 sub-issue at `:378-384` (returns first XFF entry rather than rightmost)
- **Reachability:** Direct (any HTTP request)
- **Sanitization:** None — XFF honored unconditionally
- **Framework Protection:** None
- **Description:** The comment justifies XFF trust by claiming "remote
  clients cannot spoof because they cannot pass auth without a proxy"
  — but rate-limit middleware sits BEFORE bearer-token middleware, so
  unauthenticated requests are rate-limited (and brute-forceable) using
  the same XFF logic. Any client can rotate `X-Forwarded-For:
  random-each-time` and reset their bucket every request, defeating the
  only resource throttle in the entire web layer. Compounding bug
  (LANG-GO-021): when XFF IS honored, the function returns the
  FIRST entry rather than the rightmost (proxy-trusted) one.
- **Verification Notes:** Two skills caught it; merged. The "remote
  cannot spoof" comment in the source is wrong reasoning (rate-limit
  before auth).
- **Remediation:** Only honor XFF when `r.RemoteAddr` is in a configured
  trusted-proxy list (loopback by default). Use the rightmost entry
  rather than the leftmost when honoring it. Document policy.

### VULN-011: MCP client subprocess inherits full parent environment (including all LLM API keys + DFMC tokens)
- **Severity:** High
- **Confidence:** 85/100 (High Probability)
- **Original Skill:** sc-lang-go (LANG-GO-004), sc-cmdi (CMDI-004), sc-data-exposure (EXPOSE-005)
- **Vulnerability Type:** CWE-200, CWE-526, CWE-78, CWE-668
- **File:** `internal/mcp/client.go:35-48, 92-93, 198-222`
- **Reachability:** Engine init when any external MCP server is configured
- **Sanitization:** None
- **Framework Protection:** None
- **Description:** `cmd.Env = append(os.Environ(), env...)` copies every
  parent env var into every external MCP server subprocess: all of
  `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `DEEPSEEK_API_KEY`,
  `KIMI_API_KEY`, `ZAI_API_KEY`, `ALIBABA_API_KEY`, `MINIMAX_API_KEY`,
  `GOOGLE_AI_API_KEY`, `DFMC_WEB_TOKEN`, `DFMC_REMOTE_TOKEN`,
  `DFMC_APPROVE`. A hostile or buggy MCP server reads its own
  `os.Environ()` and exfiltrates the full credential set on first
  connect. Three secondary issues at the same call site:
  (a) `exec.Command` not `exec.CommandContext` — orphan processes on
  cancel/panic;
  (b) `sendSync` goroutine leaks one parked `ReadBytes` goroutine per
  cancelled call;
  (c) `cmd.Dir` not set — inherits DFMC's CWD and exposes project paths
  the operator didn't intend.
- **Verification Notes:** Three skills landed on the same code; the
  hardening gaps all share one fix scope.
- **Remediation:** Build a minimal env (PATH, HOME, USER, LANG, LC_ALL)
  plus per-server `env_passthrough: [LIST]` opt-in. Use
  `exec.CommandContext(ctx, ...)`. Close stdin/stdout on ctx fire so
  parked reads return. Set `cmd.Dir` to a config-specified sandbox.

### VULN-012: External MCP servers / hooks subprocesses receive every API key in env (operator-config trust)
- **Severity:** Medium (when chained with VULN-011, becomes High)
- **Confidence:** 75/100 (High Probability)
- **Original Skill:** sc-data-exposure (EXPOSE-004)
- **Vulnerability Type:** CWE-200, CWE-526
- **File:** `internal/hooks/hooks.go:197`
- **Reachability:** Direct on every hook fire
- **Sanitization:** None
- **Framework Protection:** Project-level hooks gated by `hooks.allow_project=true` (default false) — partial mitigation against malicious-clone, none against benign-but-leaky hooks
- **Description:** Hook dispatch uses `cmd.Env = append(os.Environ(),
  hookEnv(...))`. Every shell command sees every `*_API_KEY` and
  `DFMC_*_TOKEN`. A user-authored hook ("log every tool call") that
  later gets compromised leaks everything.
- **Verification Notes:** Same env-leak pattern as VULN-011 but for
  hooks rather than MCP servers; not merged because the trust boundary
  is different (operator-authored vs externally-installed).
- **Remediation:** Filter `os.Environ()` before `cmd.Env` — drop
  `*_API_KEY`, `*_TOKEN`, `*_SECRET`, `DFMC_*_TOKEN` by default; opt-in
  per-hook via `hooks.entries[].env_passthrough`.

### VULN-013: Web file API serves project secrets verbatim (`.env`, `id_rsa`, `credentials.json`)
- **Severity:** High
- **Confidence:** 95/100 (Confirmed)
- **Original Skill:** sc-data-exposure (EXPOSE-001), sc-authz (AUTHZ-006), sc-authz (AUTHZ-008)
- **Vulnerability Type:** CWE-200, CWE-552, CWE-538
- **File:** `ui/web/server_files.go:45-105, 107-131, 143-183`
- **Reachability:** Direct (HTTP GET)
- **Sanitization:** Path containment via `resolvePathWithinRoot` (correct), but no leaf-name classification
- **Framework Protection:** TUI has `looksLikeSecretFile` redactor (`ui/tui/secret_redact.go`); web does not import it
- **Description:** `GET /api/v1/files/{path...}` returns raw bytes of any
  in-root path. Project root `.env` (auto-loaded at startup, the canonical
  location for `ANTHROPIC_API_KEY` / `OPENAI_API_KEY` / etc.) reads in
  one HTTP request. Same for `id_rsa`, `*.pem`, `credentials.json`.
  Compounded by AUTHZ-008: writes via `write_file` tool also lack
  classification, so writing a malicious `.dfmc/config.yaml` (hooks
  shell command) → persistent RCE.
- **Verification Notes:** Three findings on same handler family; merged
  per duplicate rules. `looksLikeSecretFile` already exists in TUI —
  trivial to share.
- **Remediation:** Promote `looksLikeSecretFile` from `ui/tui/` to a
  shared package; `handleFileContent` substitutes `{redacted: true}`
  for any matching path. Same predicate in `EnsureWithinRoot`'s
  forbidden-leafs check on writes.

### VULN-014: `Config.Save` writes `~/.dfmc/config.yaml` (plaintext API keys) with mode 0o644
- **Severity:** High
- **Confidence:** 95/100 (Confirmed)
- **Original Skill:** sc-data-exposure (EXPOSE-002)
- **Vulnerability Type:** CWE-732, CWE-200
- **File:** `internal/config/config.go:175-187`; same pattern at `internal/config/config_models_dev.go:125`
- **Reachability:** Direct (every `Config.Save`)
- **Sanitization:** N/A
- **Framework Protection:** None
- **Description:** Provider profiles store plaintext `APIKey`. The file
  is written world-readable. On any multi-user POSIX host, every other
  local account can `cat ~/.dfmc/config.yaml` and harvest keys.
- **Verification Notes:** One-line patch (mode), high impact on
  multi-user hosts. bbolt store correctly uses 0o600 already
  (`internal/storage/store.go:71`); config.yaml is the outlier.
- **Remediation:** `os.WriteFile(path, data, 0o600)` and parent dir
  `0o700`. Optionally split secrets into `~/.dfmc/credentials.yaml`
  mirroring `~/.aws/credentials` convention.

### VULN-015: Arbitrary file modification via `symbol_rename` `file` parameter
- **Severity:** High
- **Confidence:** 90/100 (Confirmed)
- **Original Skill:** sc-path-traversal (PATH-003)
- **Vulnerability Type:** CWE-22 (read; write gated by read-before-mutation)
- **File:** `internal/tools/symbol_rename.go:124, 188, 198, 271`
- **Reachability:** Direct via agent loop / MCP / web tool dispatch
- **Sanitization:** None on the `file` parameter
- **Framework Protection:** Write path gated by read-before-mutation; read path is not gated
- **Description:** `file="../../etc/hosts"` produces an out-of-root path
  via `filepath.Join(projectRoot, file)`. The read at
  `findRenameMatches` line 271 is direct `os.ReadFile(filePath)` and
  surfaces line content in `changes[].fullLine`. Write is harder
  (read-before-mutation gate) but read alone leaks `~/.ssh/config`,
  `~/.aws/credentials`, `/etc/passwd`.
- **Verification Notes:** Phase-7 tool per heuristic #5; one-line fix.
- **Remediation:** `EnsureWithinRoot(projectRoot, file)` at line 123-124
  before the join.

### VULN-016: Arbitrary file read in `semantic_search` via `file` parameter
- **Severity:** Medium
- **Confidence:** 90/100 (Confirmed)
- **Original Skill:** sc-path-traversal (PATH-005)
- **Vulnerability Type:** CWE-22
- **File:** `internal/tools/semantic_search.go:149, 202`
- **Reachability:** Direct
- **Sanitization:** None
- **Framework Protection:** None
- **Description:** Same shape as VULN-015: `targetFiles =
  []string{filepath.Join(projectRoot, file)}` with no
  `EnsureWithinRoot`. Returned `Snippet` and `ContextLines` leak file
  content. Lower than VULN-015 because the AST pre-filter limits which
  files yield matches, but any file with content lines is leakable.
- **Remediation:** `EnsureWithinRoot` at line 149.

### VULN-017: Filesystem topology disclosure via `disk_usage` `path` parameter
- **Severity:** Medium
- **Confidence:** 85/100 (High Probability)
- **Original Skill:** sc-path-traversal (PATH-006)
- **Vulnerability Type:** CWE-22 (filesystem enumeration, no content read)
- **File:** `internal/tools/disk_usage.go:67-71`
- **Reachability:** Direct
- **Sanitization:** None
- **Framework Protection:** None
- **Description:** `path="../../../"` → `filepath.Walk(root, ...)`
  enumerates everything under that escaped root: total bytes, file
  counts, per-language breakdown, top-N largest with full paths,
  directory summaries. Recon primitive: identifies an ssh key at
  `~/.ssh/id_rsa`, a 4 GiB DB dump, project layouts on the host —
  paths and sizes, not bytes.
- **Remediation:** `EnsureWithinRoot(projectRoot, path)`.

### VULN-018: CLI `magicdoc --path <abs>` writes anywhere; web `magicdoc/update` falls back silently rather than failing
- **Severity:** Medium
- **Confidence:** 85/100 (High Probability)
- **Original Skill:** sc-path-traversal (PATH-007), sc-file-upload (UPLOAD-003), sc-file-upload (UPLOAD-004), sc-mass-assignment (MASS-005)
- **Vulnerability Type:** CWE-22, CWE-915
- **File:** `ui/cli/cli_magicdoc.go:124-132`; `ui/web/server_context.go:300-324, 386-408, 462-472`
- **Reachability:** Direct (CLI flag, HTTP POST)
- **Sanitization:** Partial — web version's `resolveMagicDocPath`
  silently falls back to default rather than refusing escape; CLI
  version passes path verbatim
- **Framework Protection:** Web has the resolver; CLI does not. Neither
  routes through the read-before-mutation gate (direct
  `os.WriteFile`)
- **Description:** Web caller can pick any in-root path
  (`path=cmd/dfmc/main.go`) and overwrite it with the rendered MAGIC_DOC
  body, with `0o644`, no read-before-mutation. CLI caller can pick any
  absolute path. Three findings on the same handler family + one
  mass-assignment finding because `Path` is a body-decoded field.
- **Remediation:** Pin web `magicdoc` writes to a fixed subtree
  (`.dfmc/magic/`); refuse other paths instead of falling back. CLI:
  port the web hardening or refuse absolute paths. Either way: route
  through the read-before-mutation gate.

### VULN-019: WebSocket has no per-connection or per-message rate limit; no `SetReadLimit`
- **Severity:** High
- **Confidence:** 95/100 (Confirmed)
- **Original Skill:** sc-rate-limiting (RATE-001), sc-websocket (WS-002)
- **Vulnerability Type:** CWE-770, CWE-799
- **File:** `ui/web/server_ws.go:108-122`
- **Reachability:** Direct (any successful upgrade)
- **Sanitization:** None — `ReadMessage` has no size cap
- **Framework Protection:** None — Gorilla default `SetReadLimit` is unset
- **Description:** After upgrade, `readLoop` is unbounded. Each message
  dispatches `chat` / `ask` / `tool` synchronously to engine.Ask, which
  invokes the configured LLM provider with the user's API key. A single
  WS connection can spam unlimited rounds and exhaust LLM quota / bill
  the operator. No `SetReadLimit` means a single 100 MB JSON-RPC frame
  is buffered into memory.
- **Remediation:** `c.conn.SetReadLimit(64*1024)` post-upgrade;
  per-connection `rate.Limiter` (e.g. 5 rps, burst 10) on
  `handleMessage`; `SetReadDeadline` + ping/pong heartbeat.

### VULN-020: WebSocket has no ping/pong heartbeat; half-open connections accumulate
- **Severity:** Medium
- **Confidence:** 80/100 (High Probability)
- **Original Skill:** sc-websocket (WS-003)
- **Vulnerability Type:** CWE-400, CWE-770
- **File:** `ui/web/server_ws.go:92-106` (no `SetPingHandler`/`SetPongHandler`/timer)
- **Reachability:** Direct
- **Sanitization:** N/A
- **Framework Protection:** Stdlib `IdleTimeout` is bypassed once Gorilla hijacks the conn
- **Description:** Slow-resource exhaustion; FD exhaustion on shared
  hosts.
- **Remediation:** `SetReadDeadline(now+60s)` per `ReadMessage`; install
  `SetPongHandler` to extend deadline; `writeLoop` sends ping every 30s.

### VULN-021: WebSocket has no per-IP connection cap on upgrades
- **Severity:** Medium
- **Confidence:** 80/100 (High Probability)
- **Original Skill:** sc-websocket (WS-004)
- **Vulnerability Type:** CWE-770
- **File:** `ui/web/server.go:357-367` (rate limit is per-HTTP-request not per-active-conn)
- **Reachability:** Direct
- **Sanitization:** N/A
- **Framework Protection:** None
- **Description:** 30 upgrades/sec × no concurrent cap → unbounded
  long-lived sessions per IP.
- **Remediation:** Track active WS sessions per IP; refuse upgrade
  above N (e.g. 10) per IP.

### VULN-022: `wsConn.send` race on closed channel; cleanup panics under disconnect storms
- **Severity:** Medium
- **Confidence:** 75/100 (High Probability)
- **Original Skill:** sc-lang-go (LANG-GO-005), sc-websocket (WS-010), sc-race-condition (RACE-010)
- **Vulnerability Type:** CWE-362, CWE-672
- **File:** `ui/web/server_ws.go:73-85, 295-312`
- **Reachability:** Disconnect storms / fast cancel
- **Sanitization:** N/A
- **Framework Protection:** N/A
- **Description:** `send` reads `closed.Load()==false`, enters select,
  evaluates send. `cleanup` flips closed, releases lock, closes
  `sendCh`. Send-on-closed panic. Three skills agreed on the race; not
  exploitable as a security boundary breach but takes the per-connection
  goroutine down (and `writeLoop` has no panic guard, LANG-GO-017,
  which means a panic could escalate). Combined sub-issue: `cleanup`
  itself is not idempotent (LANG-GO-017 / WS-010).
- **Remediation:** Hold `closeMu` for the duration of the send-attempt
  OR remove `close(c.sendCh)` and rely on writeLoop drain via sentinel.
  Wrap `cleanup` in `sync.Once`; wrap `writeLoop` body in
  `defer recover()`.

### VULN-023: WebSocket handlers use `context.Background()` — client cancellation doesn't propagate
- **Severity:** Medium
- **Confidence:** 60/100 (Probable)
- **Original Skill:** sc-lang-go (LANG-GO-006)
- **Vulnerability Type:** CWE-404, CWE-770
- **File:** `ui/web/server_ws.go:124-153`
- **Reachability:** Direct
- **Sanitization:** N/A
- **Framework Protection:** N/A
- **Description:** `handleMessage` discards the request ctx; `handleChat`
  runs `engine.Ask(ctx=Background, ...)`. Client disconnect leaves the
  agent loop consuming provider tokens on a dead connection.
- **Remediation:** Use `r.Context()` (or derive). Cancel that ctx on
  `cleanup`.

### VULN-024: `taskstore.UpdateTask` is read-modify-write across two transactions; concurrent updates lose writes
- **Severity:** High
- **Confidence:** 90/100 (Confirmed)
- **Original Skill:** sc-lang-go (LANG-GO-011), sc-race-condition (RACE-001)
- **Vulnerability Type:** CWE-362, CWE-367
- **File:** `internal/taskstore/store.go:74-86, 411-454`
- **Reachability:** Concurrent `PATCH /api/v1/tasks/{id}` calls; concurrent OnTaskBlocked / OnTaskUnblocked walks
- **Sanitization:** N/A
- **Framework Protection:** N/A
- **Description:** Load and Save are separate bbolt transactions with
  the mutator running between. Two concurrent calls both load the same
  baseline, both apply, both save → second writer silently overwrites
  the first. The HTTP PATCH and the tree-walk loops in
  OnTaskBlocked/Unblocked both use this path.
- **Remediation:** Wrap entire load+mutate+save in a single
  `db.Update(...)` transaction so bbolt's writer lock serializes
  correctly.

### VULN-025: TOCTOU between read-before-mutation gate and `os.WriteFile` under concurrent sub-agents
- **Severity:** High
- **Confidence:** 80/100 (High Probability)
- **Original Skill:** sc-race-condition (RACE-002)
- **Vulnerability Type:** CWE-367
- **File:** `internal/tools/engine.go:516-546, 650-697` + `tool.Execute(ctx, req)` at `:457`
- **Reachability:** Two concurrent subagents writing the same path
- **Sanitization:** Hash-equality check; not held during write
- **Framework Protection:** N/A
- **Description:** After the gate clears, the tool's own `os.WriteFile`
  runs. Two sub-agents passing the gate simultaneously, then writing
  in succession, lose one set of changes silently — drift only detected
  on the third write.
- **Remediation:** Per-absPath `sync.Mutex` held from gate-check
  through Execute for write_file/apply_patch.

### VULN-026: `bearerTokenMiddleware` has empty-token shortcut for `GET /` (and divergence between web and CLI middlewares)
- **Severity:** Medium
- **Confidence:** 75/100 (High Probability)
- **Original Skill:** sc-auth (AUTH-005), sc-lang-go (LANG-GO-012), sc-api-security (API-004), sc-cors (CORS-007)
- **Vulnerability Type:** CWE-287, CWE-862
- **File:** `ui/web/server.go:401-419 (esp. :409-411)`; `ui/cli/cli_remote_server.go:64-93 (esp. :76-83)`
- **Reachability:** Direct
- **Sanitization:** Partial — `runServe` refuses `auth=token` empty token at startup
- **Framework Protection:** N/A
- **Description:** Web middleware allows `GET /` without auth when token
  is empty. CLI middleware (composed on top of web) **always** allows
  `GET /` without auth — a divergence. Web caveat is "depends on
  startup sibling" rather than self-enforced. CLI variant additionally
  uses non-constant-time `==` (AUTH-001), making the timing channel
  observable on the outer wrapper before the inner constant-time check
  runs.
- **Verification Notes:** Four skills found this cluster; merged.
  AUTH-001's timing-leak sub-issue is real and on the outer wrapper.
- **Remediation:** Drop both empty-token shortcuts. Replace CLI `==`
  with `subtle.ConstantTimeCompare` OR remove the redundant CLI
  wrapper entirely (web handler already gates by token).

### VULN-027: Bearer token persisted in browser `localStorage` (XSS-readable; survives reload)
- **Severity:** High
- **Confidence:** 85/100 (High Probability)
- **Original Skill:** sc-session (SESS-001)
- **Vulnerability Type:** CWE-922, CWE-1004 (parallel)
- **File:** `ui/web/static/index.html:703-747`
- **Reachability:** Any JS on the workbench origin
- **Sanitization:** CSP `script-src 'self'` blocks inline-script injection (mitigation, not elimination)
- **Framework Protection:** Partial via CSP
- **Description:** The token survives tab close, browser restart, and
  server-process restart. A future XSS in the workbench, a malicious
  browser extension, or DevTools-shoulder-surfing reads it instantly.
- **Remediation:** Prefer in-memory storage with session-cookie fallback
  (HttpOnly + SameSite=Strict + Secure-when-HTTPS); re-prompt on
  workbench reload.

### VULN-028: `/ws` SSE accepts token via `?token=` query parameter (logged everywhere)
- **Severity:** Medium
- **Confidence:** 90/100 (Confirmed)
- **Original Skill:** sc-auth (AUTH-002), sc-session (SESS-005), sc-websocket (WS-007)
- **Vulnerability Type:** CWE-598
- **File:** `ui/cli/cli_remote_server.go:87-90`; auth comparison non-constant-time at `:83`
- **Reachability:** Direct
- **Sanitization:** None
- **Framework Protection:** N/A
- **Description:** Tokens leaked to access logs, reverse-proxy logs,
  browser history, Referer headers when leaving workbench, shell
  history when curl/wscat used. Combined with no token rotation
  (VULN-029) → permanent compromise.
- **Remediation:** Drop the query-param fallback. If browser SSE
  authentication is genuinely needed, use the
  `Sec-WebSocket-Protocol` subprotocol channel.

### VULN-029: No token rotation, no expiry, no revocation, no per-client identity
- **Severity:** Medium
- **Confidence:** 90/100 (Confirmed)
- **Original Skill:** sc-auth (AUTH-006), sc-session (SESS-003), sc-session (SESS-004)
- **Vulnerability Type:** CWE-613, CWE-287
- **File:** `ui/web/server.go:181-186`; `ui/web/server.go:36-43`
- **Reachability:** Architectural
- **Sanitization:** N/A
- **Framework Protection:** None
- **Description:** Once set, the token is valid until process restart.
  No `iat`/`exp`, no revocation list, no `/auth/rotate`. A leaked
  token is leaked forever. Single shared token authenticates every
  client; no per-client identity carried into conversation/drive/task
  records (no `actor` field on the EventBus).
- **Remediation:** Add `POST /api/v1/auth/rotate`; embed timestamp in
  default-generated tokens; mint per-client tokens with embedded actor
  names for multi-operator setups.

### VULN-030: Token can be planted via URL hash, then auto-stored
- **Severity:** Medium
- **Confidence:** 70/100 (High Probability)
- **Original Skill:** sc-session (SESS-002)
- **Vulnerability Type:** CWE-598
- **File:** `ui/web/static/index.html:707-735`
- **Reachability:** Direct (browser navigation)
- **Sanitization:** None — `clearHashToken()` runs after persist, but persistence already happened
- **Framework Protection:** None
- **Description:** Workbench reads `#token=...` from URL hash on load
  and writes to localStorage. Combined with VULN-002 (no Origin check),
  attacker page can navigate the operator's browser to
  `http://127.0.0.1:7777/#token=ATTACKER` to plant a bad token.
- **Remediation:** Drop hash-token bootstrap; require explicit prompt
  entry, or require user confirmation before persisting hash-supplied
  token.

### VULN-031: Drive/Task/Conversation IDs accessible cross-conceptual-session within auth perimeter
- **Severity:** Medium
- **Confidence:** 75/100 (High Probability)
- **Original Skill:** sc-authz (AUTHZ-003), sc-authz (AUTHZ-004), sc-authz (AUTHZ-007)
- **Vulnerability Type:** CWE-639
- **File:** `ui/web/server_drive.go:150-303`; `ui/web/server_task.go` (entire file); `ui/web/server_conversation.go`
- **Reachability:** Direct
- **Sanitization:** None — IDs are global to the running binary
- **Framework Protection:** Auth perimeter (bearer token) is the only gate
- **Description:** Any token-holder can `GET`/`PATCH`/`DELETE` any drive
  run, task, or conversation by raw ID. On multi-operator setups
  (shared `dfmc serve`, two engineers SSH'd into the same box) each
  can read/cancel/delete the other's records. Drive run records
  contain prompts that may include sensitive paths and code snippets.
- **Verification Notes:** Architectural — DFMC is "single-user by
  design"; flagged because the contract is "anyone with the bearer
  token sees everything", and the multi-operator scenario is
  realistic. Three skills agreed on the same shape.
- **Remediation:** Stamp records with originating-token fingerprint
  (`sha256(token)[:8]`); refuse cross-fingerprint reads/mutations.

### VULN-032: `PATCH /api/v1/tasks/{id}` mass-assigns state without transition validation; allows reparent cycles
- **Severity:** High
- **Confidence:** 90/100 (Confirmed)
- **Original Skill:** sc-business-logic (LOGIC-002), sc-business-logic (LOGIC-003), sc-mass-assignment (MASS-002)
- **Vulnerability Type:** CWE-915, CWE-841
- **File:** `ui/web/server_task.go:159-218`
- **Reachability:** Direct
- **Sanitization:** None — `map[string]any` decode, no allow-list
- **Framework Protection:** `ValidateTree` exists but is not called from the HTTP path
- **Description:** Caller can flip `state` arbitrarily, set `done` on
  never-started tasks (forge completion), reparent any task to any
  other (creating cycles that `ValidateTree` only detects on demand),
  clear `error`/`blocked_reason`. Drive runs that depend on task tree
  state are tricked into believing work was done that wasn't.
- **Remediation:** Replace `map[string]any` decode with explicit
  `TaskPatchRequest` struct exposing only `title`, `detail`, `summary`,
  `labels`. Reject `state` writes from HTTP entirely; add
  `POST /api/v1/tasks/{id}/transition` with state-machine validator.
  Run cycle detection on every reparent.

### VULN-033: `POST /api/v1/tasks` accepts client-controlled `id` allowing task overwrite
- **Severity:** High
- **Confidence:** 90/100 (Confirmed)
- **Original Skill:** sc-mass-assignment (MASS-001)
- **Vulnerability Type:** CWE-915, CWE-284
- **File:** `ui/web/server_task.go:25-44, 104-107`; `internal/taskstore/store.go:24-42`
- **Reachability:** Direct
- **Sanitization:** None
- **Framework Protection:** `SaveTask` does unconditional `b.Put` (silent overwrite)
- **Description:** Submitting `{"title":"x", "id":"<existing>"}`
  silently overwrites the existing task with attacker fields. Original
  history is lost. Drive TODOs persisted as tasks → guess-the-ID
  pre-create attack.
- **Remediation:** Drop `ID` from `TaskCreateRequest`; always allocate
  via `taskstore.NewTaskID()`. If explicit-ID is needed, separate
  admin endpoint that refuses on collision.

### VULN-034: `POST /api/v1/drive` accepts unbounded `MaxParallel`, `MaxTodos`, and arbitrary `auto_approve` strings
- **Severity:** Medium
- **Confidence:** 85/100 (High Probability)
- **Original Skill:** sc-business-logic (LOGIC-007), sc-mass-assignment (MASS-003)
- **Vulnerability Type:** CWE-915, CWE-770, CWE-284
- **File:** `ui/web/server_drive.go:36-48, 82-93`; `internal/drive/driver.go:165-167`
- **Reachability:** Direct
- **Sanitization:** None — caps and AutoApprove forwarded as-is
- **Framework Protection:** None
- **Description:** `MaxParallel: 1000` → 1000 worker goroutines per
  Drive run = DoS. `AutoApprove: ["*"]` flips off the approval gate
  for the duration of the run for every spawned sub-agent. Stacks
  with VULN-001 to make even gated tools approval-free.
- **Remediation:** Clamp MaxParallel ≤16, MaxTodos ≤500; validate
  AutoApprove against tool-name allow-list, reject `"*"` wildcards;
  validate Routing against `Providers.Profiles`.

### VULN-035: Sub-agent `allowed_tools` is a prompt hint, not an enforced sandbox
- **Severity:** Medium
- **Confidence:** 95/100 (Confirmed)
- **Original Skill:** sc-privilege-escalation (PRIVESC-001)
- **Vulnerability Type:** CWE-269, CWE-1357
- **File:** `internal/engine/subagent.go:31-56`; `internal/engine/subagent_profiles.go:60-138`; `internal/tools/delegate.go:73-119`; `internal/tools/builtin_specs.go:408`
- **Reachability:** Any agent loop using delegate_task / orchestrate / Drive
- **Sanitization:** N/A
- **Framework Protection:** None — field is wired only into prompt and event payloads
- **Description:** The doc string for `allowed_tools` claims it
  restricts the sub-agent ("for read-only surveys pass [...]
  — keeps it cheap and safe"). In reality the field is plumbed only as
  a prompt hint and as an event-payload telemetry field. No runtime
  gate consults it. A sub-agent spawned with
  `allowed_tools: ["read_file","grep_codebase"]` retains full tool
  authority including `run_command` and `write_file`. Operators
  reading the spec text would believe in a sandbox that doesn't exist.
- **Remediation:** Either enforce — pass `AllowedTools` through to
  `executeToolWithLifecycle` and reject any tool name not in the
  allow-list when `source=="subagent"` AND a non-empty list is set.
  OR rename to `preferred_tools` and document explicitly that this
  is a soft prompt nudge.

### VULN-036: `hooks.CheckConfigPermissions` is dead code — group/world-writable config silently grants RCE
- **Severity:** Medium
- **Confidence:** 95/100 (Confirmed)
- **Original Skill:** sc-privilege-escalation (PRIVESC-002)
- **Vulnerability Type:** CWE-732, CWE-269, CWE-1188
- **File:** `internal/hooks/hooks.go:300-314` (defined; zero call sites repo-wide)
- **Reachability:** Anyone who can write to `~/.dfmc/config.yaml` (multi-user host with permissive umask, restored backup with bad perms, sync-tool race)
- **Sanitization:** N/A — the warning that should fire never does
- **Framework Protection:** None
- **Description:** `CheckConfigPermissions` exists to warn when DFMC's
  config file is group/world-writable, which lets any party who can
  write to it inject hook commands and inherit DFMC tool authority.
  Greps repo-wide show zero call sites outside the function definition.
  No code in `cmd/dfmc/main.go`, `Engine.Init`, `dfmc doctor`, or
  `dfmc hooks` wires it in. The threat model is real (CMDI-003-class:
  shell-wrapped hooks fire on every `pre_tool`/`user_prompt_submit`
  event); the safety net is silently inactive.
- **Remediation:** Wire into `cmd/dfmc/main.go` startup
  (post-`config.Load`, stderr warning); into `dfmc doctor` as a
  dedicated row; via `EventBus` so TUI/web can surface a security
  badge. Optional `security.refuse_writable_config` strict-mode flag.

### VULN-037: SSE `/ws` event stream broadcasts raw tool params/output_preview/error — secrets exfiltrate cross-origin
- **Severity:** Medium
- **Confidence:** 80/100 (High Probability)
- **Original Skill:** sc-data-exposure (EXPOSE-003), sc-csrf (CSRF-003), sc-cors (CORS-003)
- **Vulnerability Type:** CWE-200, CWE-532, CWE-942
- **File:** `internal/engine/agent_loop_events.go:100-168`; `ui/web/server_chat.go:116-168`
- **Reachability:** Direct (any SSE subscriber within auth perimeter; cross-origin without auth when `auth=none`)
- **Sanitization:** None — payload published byte-for-byte
- **Framework Protection:** Bearer-token middleware gates when `auth=token`; with `auth=none` the SSE is wide open
- **Description:** `tool:call` payload includes raw `params`;
  `tool:result` includes `output_preview` (first 180 chars) and `error`.
  `read_file` of `.env` puts the secret into output_preview;
  `web_fetch` with `Authorization: Bearer ...` echoes the token in
  params; `run_command --token=sk-...` is reflected in `params.args`.
  Tool errors that wrap secrets in messages also leak. With `auth=none`,
  any cross-origin page can `new EventSource('/ws')` and receive
  everything (EventSource has no preflight).
- **Remediation:** Run a redactor over `tool:call.params` and
  `tool:result.output_preview/error` before publish. Reuse the secret
  regex catalog from `internal/security/scanner.go`. Origin allow-list
  on `/ws` SSE. Per-tool allow-list of which fields surface.

### VULN-038: TUI tool-output ANSI / OSC injection (terminal-control bytes pass through unfiltered)
- **Severity:** High
- **Confidence:** 85/100 (High Probability)
- **Original Skill:** sc-xss (XSS-004), sc-xss (XSS-005), sc-xss (XSS-006)
- **Vulnerability Type:** CWE-150
- **File:** `internal/engine/agent_loop_events.go:139`; `internal/engine/agent_loop.go:376-389` (compactToolPayload); `internal/tools/web.go:182-217` (htmlToText); TUI consumers in `ui/tui/engine_events_tool.go` and `ui/tui/theme/tool_chips.go`
- **Reachability:** Direct (any tool returning bytes that flow into the TUI chip)
- **Sanitization:** Partial — `compactToolPayload` truncates and strips whitespace; does NOT strip C0/C1 control bytes
- **Framework Protection:** None
- **Description:** Hostile `web_fetch` returning `\x1b[2J\x1b[H` clears
  the user's terminal; `\x1b]0;You have been pwned\x07` rewrites window
  title; OSC 8 hyperlinks (`\x1b]8;;evil\x1b\\text\x1b]8;;\x1b\\`)
  embed phishing links inside plausible "documentation" text. Modern
  emulators (kitty, iTerm2, Windows Terminal, WezTerm) honor OSC 8
  universally. LLM-emitted assistant text flows the same path
  (XSS-005). `htmlToText` in `web_fetch` doesn't filter (XSS-006).
- **Remediation:** Strip C0 (`\x00-\x1f` except `\t`,`\n`) and C1
  (`\x80-\x9f`) at the engine publish boundary
  (`agent_loop_events.go`), so TUI and web both benefit. One regex,
  applied once.

### VULN-039: Workbench `escapeHTML` does not escape quotes; SVG `title=` attribute interpolates user-controlled symbol names
- **Severity:** Medium
- **Confidence:** 60/100 (Probable)
- **Original Skill:** sc-xss (XSS-001), sc-xss (XSS-002), sc-xss (XSS-003)
- **Vulnerability Type:** CWE-79
- **File:** `ui/web/static/index.html:676-681, 894-898, 968-990, 1021-1025`
- **Reachability:** Codemap symbol with embedded `"` in name
- **Sanitization:** Partial — `&<>` escaped but not quotes
- **Framework Protection:** CSP `script-src 'self'` blocks inline event handlers; `style-src 'self'` blocks inline styles. Reduces practical exploitability significantly
- **Description:** SVG title attribute is built via string concatenation
  with `escapeHTML` which only neutralises `&<>`. Embedded `"` breaks
  out of the attribute, allowing additional attribute injection
  (`onmouseover=...` blocked by CSP today; but the unsafe pattern would
  be exploitable on any future CSP relaxation).
- **Verification Notes:** CSP makes immediate exploitation hard;
  graded Medium because the unsafe primitive `escapeHTML` is shipped
  for use elsewhere and would be exploitable in any future innerHTML
  attribute site.
- **Remediation:** Extend `escapeHTML` to replace `"` and `'`. Migrate
  innerHTML hotspot rows to `createElement` + `textContent` (consistent
  with the rest of the file).

### VULN-040: Tool panic guard returns full Go runtime stack (≤2 KiB) to the LLM-visible error
- **Severity:** Low
- **Confidence:** 60/100 (Probable)
- **Original Skill:** sc-data-exposure (EXPOSE-006)
- **Vulnerability Type:** CWE-209
- **File:** `internal/engine/engine_tools.go:174-216`
- **Reachability:** Direct on any tool panic
- **Sanitization:** Stack truncated to 2048 bytes; not stripped of paths
- **Framework Protection:** N/A
- **Description:** Stack reveals build paths, function names, goroutine
  IDs, vendored-package layout — enough to fingerprint the build and
  identify CVE-vulnerable dependencies. An attacker who can induce
  panics (malformed prompt-injected MCP reply) collects this signal.
- **Remediation:** Keep full stack in local error log + `tool:panicked`
  event; for the agent-loop-visible error, strip to `tool %s panicked:
  %v`. Toggle full visibility via `verbose`/`debug` config.

### VULN-041: Verbose error responses echo internal details — `err.Error()` returned directly to API clients in 70+ handlers
- **Severity:** Low
- **Confidence:** 70/100 (High Probability)
- **Original Skill:** sc-api-security (API-005)
- **Vulnerability Type:** CWE-209
- **File:** Across `ui/web/server_*.go` — 72 occurrences in 10 files
- **Reachability:** Any error path
- **Sanitization:** None
- **Framework Protection:** N/A
- **Description:** Git stderr, bbolt paths, EvalSymlinks errors, provider
  HTTP error bodies all flow back through `writeJSON(... err.Error())`.
  Acceptable for loopback-only; problematic for `dfmc remote start`
  exposed cross-network.
- **Remediation:** Centralize through a sanitiser that maps known
  sentinels (`os.ErrNotExist`, `storage.ErrStoreLocked`) to friendly
  strings and collapses everything else to `"internal error"`. Keep
  full details on the EventBus.

### VULN-042: `/api/v1/tasks` pagination has no upper bound on `limit`; other list endpoints unaudited
- **Severity:** High
- **Confidence:** 95/100 (Confirmed)
- **Original Skill:** sc-rate-limiting (RATE-002), sc-rate-limiting (RATE-007)
- **Vulnerability Type:** CWE-770
- **File:** `ui/web/server_task.go:63-66`; same pattern likely in `/api/v1/conversations`, `/api/v1/conversation/branches`, `/api/v1/drive`, `/api/v1/memory`, `/api/v1/files`. `handleDriveList` at `server_drive.go:126-145` confirmed unbounded.
- **Reachability:** Direct
- **Sanitization:** None — any positive integer accepted
- **Framework Protection:** None
- **Description:** `?limit=999999999` triggers unbounded read +
  JSON-encode. 30 such requests/sec via the per-IP limiter (or more
  via VULN-010's XFF bypass) → server-process OOM.
- **Remediation:** Cap every list endpoint at 200-500 with default 100.
  Audit `/api/v1/conversations`, `/api/v1/conversation/branches`,
  `/api/v1/drive`, `/api/v1/memory`, `/api/v1/files` for the same gap.

### VULN-043: No concurrency cap on Drive runs per client / globally
- **Severity:** Medium
- **Confidence:** 80/100 (High Probability)
- **Original Skill:** sc-rate-limiting (RATE-003)
- **Vulnerability Type:** CWE-770
- **File:** `ui/web/server_drive.go:55-121`
- **Reachability:** Direct
- **Sanitization:** None
- **Framework Protection:** None
- **Description:** `handleDriveStart` returns immediately and launches
  via `engine.StartBackgroundTask`. No check that an IP/user already
  has N runs in flight. Per-IP HTTP limiter caps request rate (30/s),
  not in-flight count. Per-run caps limit each run; multiplied by run
  count is unbounded.
- **Remediation:** Track active runs per IP key; reject above 3
  concurrent/IP with 429. Global cap (10 concurrent) recommended.

### VULN-044: SSE `/api/v1/chat` has no per-stream wall-clock cap; cleared write deadline = slow-loris pin
- **Severity:** Medium
- **Confidence:** 75/100 (High Probability)
- **Original Skill:** sc-rate-limiting (RATE-004)
- **Vulnerability Type:** CWE-770
- **File:** `ui/web/server_chat.go:60-114`
- **Reachability:** Direct
- **Sanitization:** Clears 2-min write deadline (`clearStreamingWriteDeadline:66`)
- **Framework Protection:** None
- **Description:** Long-running stream that the client never closes
  holds goroutine + provider stream + response body open. With
  deadline cleared, slow-loris readers pin connections indefinitely.
- **Remediation:** Hard wall-clock ceiling on `r.Context()` derived
  from `agent.max_stream_seconds`; cap concurrent SSE per IP;
  re-apply generous (10-min) write deadline rather than clearing.

### VULN-045: Provider router has no client-side outbound rate limit
- **Severity:** Medium
- **Confidence:** 70/100 (High Probability)
- **Original Skill:** sc-rate-limiting (RATE-005)
- **Vulnerability Type:** CWE-770
- **File:** `internal/provider/throttle.go:1-103`; `internal/provider/router.go`
- **Reachability:** Any agent loop
- **Sanitization:** Response-side 429/503 backoff (correct)
- **Framework Protection:** N/A — no upstream cap
- **Description:** Once a flood passes the HTTP gate (or originates
  from inside via Drive/MCP/parallel SSE), provider quota burns until
  upstream itself returns 429. Per-turn caps don't bound aggregate.
- **Remediation:** Optional `agent.global_rate_limit_rps` config knob
  backed by a `rate.Limiter` shared across routers. Off by default.

### VULN-046: `applyUnifiedDiffWeb` and `gitWorkingDiffWeb` use `context.Background()` (not request ctx)
- **Severity:** Low
- **Confidence:** 95/100 (Confirmed)
- **Original Skill:** sc-lang-go (LANG-GO-009), sc-lang-go (LANG-GO-010)
- **Vulnerability Type:** CWE-400
- **File:** `ui/web/server_workspace.go:108-110, 215, 234`
- **Reachability:** Direct
- **Sanitization:** N/A
- **Framework Protection:** N/A
- **Description:** 60-second `git apply` runs under `context.Background`.
  Client cancellation doesn't propagate; child holds file lock until
  60s timeout. Same pattern in `gitWorkingDiffWeb` and TUI siblings
  (`ui/tui/patch_parse.go:220, 309`, `ui/tui/filesystem.go:27`).
- **Remediation:** Derive from `r.Context()`:
  `ctx, cancel := context.WithTimeout(r.Context(), applyTimeout)`.

### VULN-047: SSE event drop counter mismatch — per-subscriber buffer 64, bus internal buffer 1024, drops invisible to operator
- **Severity:** Low
- **Confidence:** 90/100 (Confirmed)
- **Original Skill:** sc-lang-go (LANG-GO-007)
- **Vulnerability Type:** CWE-665
- **File:** `ui/web/server_ws.go:174-193`
- **Reachability:** Direct
- **Sanitization:** N/A
- **Framework Protection:** N/A
- **Description:** Two-tier buffering with smaller outer (64) buffer
  silently drops; `Engine.Status().DroppedCount` only sees inner-bus
  drops.
- **Remediation:** Match bus buffer (1024) here, OR wire counter back
  into engine status.

### VULN-048: Hooks `Fire` lacks per-fire panic guard around pre_tool/post_tool dispatch
- **Severity:** Low
- **Confidence:** 60/100 (Probable)
- **Original Skill:** sc-lang-go (LANG-GO-013), sc-business-logic (LOGIC-010)
- **Vulnerability Type:** CWE-755
- **File:** `internal/hooks/hooks.go` Fire dispatch; `internal/engine/engine_tools.go:253-298`; `internal/tools/engine.go:437-444` reasoning publisher
- **Reachability:** Any tool call
- **Sanitization:** N/A
- **Framework Protection:** Inner `executeToolWithPanicGuard` wraps tool body, NOT the surrounding hook fire or reasoning publisher
- **Description:** Panic in pre_tool fire / post_tool fire / reasoning
  publisher subscriber takes engine down. Hooks are first-party Go,
  panic risk low; reasoning publisher subscribers are user-installable.
- **Remediation:** Wrap pre/post hook fires and `pub(name, reason)`
  call in `defer recover()`; failing-best-effort is the documented
  contract.

### VULN-049: `Server.New` silently rebinds non-loopback host to 127.0.0.1 when `auth=none` (informational gap)
- **Severity:** Info
- **Confidence:** 100/100 (Confirmed)
- **Original Skill:** sc-lang-go (LANG-GO-016)
- **Vulnerability Type:** Operational / observability
- **File:** `ui/web/server.go:152-160`
- **Reachability:** Operator config
- **Sanitization:** N/A
- **Framework Protection:** N/A
- **Description:** Operator passes `--host 0.0.0.0` and gets
  connection-refused from remote; the silent rewrite has no stderr
  message. `auth=token` non-loopback gets a warning at line 157;
  `auth=none` rewrite case does not.
- **Remediation:** Print rewrite to stderr.

### VULN-050: `handleToolExec` doesn't validate Content-Type (text/plain JSON accepted; CORS-simple cross-origin POST bypasses preflight)
- **Severity:** Medium
- **Confidence:** 80/100 (High Probability)
- **Original Skill:** sc-lang-go (LANG-GO-015), sc-cors (CORS-006)
- **Vulnerability Type:** CWE-352, CWE-942
- **File:** `ui/web/server_tools_skills.go:151-173`; same shape across other handlers
- **Reachability:** Direct
- **Sanitization:** None
- **Framework Protection:** Default-CORS-zero relies on JSON Content-Type triggering preflight; this loophole defeats that
- **Description:** `json.NewDecoder` accepts any body type. A
  cross-origin `<form enctype="text/plain">` POST whose payload happens
  to be JSON-shaped is a CORS-simple request, skips preflight, lands
  at the JSON decoder unimpeded. With `auth=none` this is a write CSRF
  without DNS rebinding.
- **Remediation:** Reject any POST/PATCH whose `Content-Type` is not
  `application/json` with 415.

### VULN-051: WS `events.subscribe` is a stub that never registers a real subscription (latent leak path)
- **Severity:** Low
- **Confidence:** 70/100 (High Probability)
- **Original Skill:** sc-websocket (WS-008)
- **Vulnerability Type:** CWE-200 (latent)
- **File:** `ui/web/server_ws.go:281-293`
- **Reachability:** Latent
- **Sanitization:** N/A
- **Framework Protection:** N/A
- **Description:** Future change wiring `events.subscribe` to
  `EventBus.SubscribeFunc("*",...)` without sanitization repeats the
  cross-origin SSE leak (VULN-037) on the WS surface.
- **Remediation:** When wiring, scope to `req.Type` and apply same
  Origin/Host gates as the upgrade.

### VULN-052: `dfmc remote start` exposes full WS surface on a network-reachable port; relies on `auth=token` default
- **Severity:** High (when `--insecure --auth=none --host 0.0.0.0` used)
- **Confidence:** 90/100 (Confirmed)
- **Original Skill:** sc-websocket (WS-009)
- **Vulnerability Type:** CWE-1385, CWE-668
- **File:** `ui/cli/cli_remote_start.go:58-66`
- **Reachability:** Network when `--insecure` used
- **Sanitization:** N/A
- **Framework Protection:** Refuses non-loopback `auth=none` without `--insecure`
- **Description:** `web.New()` is reused, so `wsUpgrader` is shared.
  With `--insecure` flag, anyone on the network can WS-upgrade with no
  origin/Host/auth check. AUTH-009 (no `--insecure` confirmation
  prompt) compounds.
- **Remediation:** Even under `--insecure`, retain strict CheckOrigin
  allow-list (or require explicit `--allow-any-origin`). Require
  `DFMC_ALLOW_INSECURE=1` env var alongside `--insecure`.

### VULN-053: gh_runner flag-injection check is one-sided (refuses `-x`, allows `--jq=$(...)`)
- **Severity:** Medium
- **Confidence:** 70/100 (High Probability)
- **Original Skill:** sc-cmdi (CMDI-005)
- **Vulnerability Type:** CWE-88
- **File:** `internal/tools/gh_runner.go:60-65`
- **Reachability:** Direct
- **Sanitization:** Partial — only single-dash refused
- **Framework Protection:** Subcommand allow-list (`pr|issue|run|repo|api`)
- **Description:** Double-dash flags pass unchecked. `gh api --field
  @/etc/shadow` reads arbitrary files. The args[0] subcommand is
  matched against allow-list but not flag-checked.
- **Remediation:** Mirror `rejectGitFlagInjection` shape: refuse any
  argument that begins with `-` AND isn't on a per-subcommand
  allow-list of safe flags.

### VULN-054: `run_command` allow-list misses indirection bypass (`env sudo`, `nice sudo`, `nohup sudo`, `xargs sudo`)
- **Severity:** Medium
- **Confidence:** 70/100 (High Probability)
- **Original Skill:** sc-cmdi (CMDI-006)
- **Vulnerability Type:** CWE-78
- **File:** `internal/tools/command.go:296-358`
- **Reachability:** Direct
- **Sanitization:** Block-list keys on basename of the binary
- **Framework Protection:** Direct `sudo`/`bash`/`sh` blocked
- **Description:** `command="env"`, `args=["sudo","bash","-c","whoami"]`
  passes the binary check (env is allowed) and shell-interpreter check
  (bash is in args, not command). `env` then exec's `sudo bash -c
  whoami`. None of `env`, `nice`, `nohup`, `xargs`, `time`, `chroot`,
  `stdbuf`, `setsid`, `taskset` are on the block-list.
- **Remediation:** Add indirection wrappers to the binary block-list,
  OR scan args for blocked-binary names via the same canonicalization,
  not just the command slot.

### VULN-055: `git_worktree_remove` `path` arg not constrained to project root
- **Severity:** Medium
- **Confidence:** 70/100 (High Probability)
- **Original Skill:** sc-cmdi (CMDI-008)
- **Vulnerability Type:** CWE-22, CWE-78 (escalation)
- **File:** `internal/tools/git_worktree.go:189-218`
- **Reachability:** Direct
- **Sanitization:** `rejectGitFlagInjection` applied; `EnsureWithinRoot` deliberately omitted
- **Framework Protection:** None
- **Description:** Comment justifies omission ("worktrees may live
  outside project root"), but with `force=true` this becomes "remove
  ANY git worktree on disk that the dfmc process can reach".
  `git_worktree_add` correctly applies `EnsureWithinRoot` at line 120;
  `remove` should mirror or require path to be in `git worktree list`
  output.
- **Remediation:** Require `path` to be inside project root OR present
  in `git worktree list` output.

### VULN-056: Hooks shell-mode default + `args:` mode skips block-list
- **Severity:** Medium (operator-config trust boundary)
- **Confidence:** 65/100 (Probable)
- **Original Skill:** sc-cmdi (CMDI-003), sc-cmdi (CMDI-009)
- **Vulnerability Type:** CWE-77, CWE-78
- **File:** `internal/hooks/hooks.go:122-127, 246-249, 256-264, 266-298`
- **Reachability:** Hooks fire on `user_prompt_submit` / `pre_tool` / `post_tool`
- **Sanitization:** Project hooks gated by `hooks.allow_project=true` (default false)
- **Framework Protection:** Partial via project-hooks gate
- **Description:** Default `useShell=true` means a hook entry with
  `command:` only is wrapped in `sh -c` / `cmd.exe /C`. `Args:` mode
  bypasses `isBlockedBinary`/etc. Trust boundary is operator-edited
  config — but the asymmetry with `run_command` (which refuses the
  same shapes on the LLM's behalf) is worth documenting in the hook
  config schema. `DFMC_TOOL_ARGS` env var carries
  attacker-controllable JSON into the hook process — second-order
  injection sink documented in the hook spec.
- **Remediation:** Document the asymmetry in `hooks.go` and the config
  schema; add `dfmc doctor` advisory listing all configured hooks
  with their trust level.

### VULN-057: SSRF — provider router has no `safeTransport` guard; project config can point `base_url` at internal IPs
- **Severity:** Low
- **Confidence:** 80/100 (High Probability)
- **Original Skill:** sc-ssrf (SSRF-004)
- **Vulnerability Type:** CWE-918
- **File:** `internal/provider/openai_compat.go:30-39, 103, 209`; `internal/provider/anthropic.go:31-44, 84, 178`; `internal/provider/google.go:54-56, 75, 147`; `internal/provider/http_client.go:45-64`
- **Reachability:** Hostile project config (cloned repo)
- **Sanitization:** None — `Proxy: http.ProxyFromEnvironment` and stdlib `net.Dialer`
- **Framework Protection:** None
- **Description:** A cloned-repo `<project>/.dfmc/config.yaml` setting
  `providers.profiles.openai.base_url:
  http://169.254.169.254/latest/meta-data/iam/security-credentials/`
  causes the next `dfmc ask` to POST messages to the cloud-metadata
  endpoint. Differential 4xx codes / timing fingerprints internal
  services.
- **Remediation:** Gate non-loopback provider `base_url` behind
  `Hooks.AllowProject`-style opt-in, OR ship `safeTransport` SSRF
  guard on `newProviderHTTPClient` when `base_url` is non-default.

### VULN-058: SSRF — `dfmc update --host` and `dfmc config sync-models` URL override use stdlib transport without guard
- **Severity:** Low
- **Confidence:** 75/100 (High Probability)
- **Original Skill:** sc-ssrf (SSRF-005), sc-ssrf (SSRF-006)
- **Vulnerability Type:** CWE-918
- **File:** `internal/config/config_models_dev.go:77-103`; `ui/cli/cli_update.go:157-214`
- **Reachability:** Operator-supplied CLI flag
- **Sanitization:** None
- **Framework Protection:** N/A
- **Description:** Operator-trust boundary (CLI-only override), so
  practical risk is "operator types in a private URL". Same trust
  level as setting `base_url`. Listed for completeness.
- **Remediation:** Apply `safeTransport` to both clients.

### VULN-059: Drive `RunPrepared` preserves caller-supplied `Todos` slice (latent contract trap)
- **Severity:** Medium
- **Confidence:** 70/100 (High Probability)
- **Original Skill:** sc-business-logic (LOGIC-004)
- **Vulnerability Type:** CWE-915, CWE-841
- **File:** `internal/drive/driver.go:98-219` (esp. :108-127)
- **Reachability:** Latent — no current caller misuses; door wired up
- **Sanitization:** Resets Status/Reason/EndedAt only conditionally
- **Framework Protection:** None
- **Description:** Function name promises "prepared" cleanup; in reality
  `if run.Todos == nil { run.Todos = []Todo{} }` preserves a
  pre-populated slice. A future caller (or plugin) handing a pre-baked
  Todos slice bypasses the planner entirely and runs that plan with
  AutoApprove.
- **Remediation:** Unconditionally `run.Todos = []Todo{}` and
  `run.Plan = nil` on entry. Separate explicit method for callers
  that want to pre-populate.

### VULN-060: Drive `Resume` trusts persisted `Done` TODO state without integrity chain
- **Severity:** Medium
- **Confidence:** 70/100 (High Probability)
- **Original Skill:** sc-business-logic (LOGIC-005)
- **Vulnerability Type:** CWE-841
- **File:** `internal/drive/driver.go:229-283 (esp. :250-254)`
- **Reachability:** Anyone with write access to `.dfmc/` between stop and resume
- **Sanitization:** None — `result` summaries in `Brief` are free-form strings
- **Framework Protection:** Single-process bbolt lock (within process)
- **Description:** Flip `TodoBlocked` → `TodoDone` in the persisted
  JSON between `dfmc drive stop` and `dfmc drive resume <id>`. Resume
  skips those TODOs as "already done" without re-executing.
- **Remediation:** Hash each completed TODO's brief + tool-call summary
  into a chain so tampering is detected.

### VULN-061: Auto-resume cumulative ceiling has one-budget overshoot per cycle
- **Severity:** Medium
- **Confidence:** 60/100 (Probable)
- **Original Skill:** sc-business-logic (LOGIC-006)
- **Vulnerability Type:** CWE-770, CWE-841
- **File:** `internal/engine/agent_loop_autonomous.go:113-184, 47-85`; `internal/engine/agent_loop_limits.go:31-33`
- **Reachability:** Auto-resume path
- **Sanitization:** Ceiling check is per-resume, not per-step
- **Framework Protection:** None
- **Description:** With `multiplier=10`, the actual ceiling is between
  10× and 11× MaxSteps; one extra full budget can be extracted before
  the next attempt refuses. Cost-bounded environments should set
  `multiplier=1`.
- **Remediation:** Per-attempt cap = `stepCeiling - cumulative`.
  Document the overshoot.

### VULN-062: Conversation `BranchSwitch` race during in-flight `Ask` corrupts history
- **Severity:** Low
- **Confidence:** 60/100 (Probable)
- **Original Skill:** sc-business-logic (LOGIC-008)
- **Vulnerability Type:** CWE-362, CWE-841
- **File:** `internal/conversation/manager.go:146-153, 174-185`
- **Reachability:** Web client switching branches mid-ask
- **Sanitization:** Map ops are mutex-guarded; logical sequence is not
- **Framework Protection:** N/A
- **Description:** User-message lands in branch A, assistant reply
  lands in branch B. Branch A has orphaned user turn; branch B has
  assistant message with no question. Persisted forever.
- **Remediation:** Track in-flight-ask flag; refuse `BranchSwitch`
  while set, OR document as "operator footgun".

### VULN-063: `BranchCreate` allows orphan / control-char / path-style branch names
- **Severity:** Low
- **Confidence:** 60/100 (Probable)
- **Original Skill:** sc-business-logic (LOGIC-009)
- **Vulnerability Type:** CWE-20
- **File:** `internal/conversation/manager.go:155-172`
- **Reachability:** Direct
- **Sanitization:** Only `name == ""` checked
- **Framework Protection:** N/A
- **Description:** Inconsistent with `validateConvID` in
  `internal/storage/store.go:398`. Names like `"..\\..\\evil"` or
  control chars persist as JSON map keys but break path-segment UIs.
- **Remediation:** Apply `validateConvID` whitelist to branch names.

### VULN-064: EventBus `Publish` lock-order with `droppedMu` is implicit (latent deadlock potential)
- **Severity:** Info
- **Confidence:** 70/100 (High Probability — for design-fragility)
- **Original Skill:** sc-race-condition (RACE-005)
- **Vulnerability Type:** CWE-833
- **File:** `internal/engine/eventbus.go:73-91, 203-214, 225-235`
- **Reachability:** Latent
- **Sanitization:** N/A
- **Framework Protection:** Today's only `droppedMu` user (`noteDroppedEvent`) doesn't re-enter Subscribe/Unsubscribe
- **Description:** Lock-ordering `eb.mu.RLock` → `droppedMu.Lock` is
  implicit. A future `droppedMu`-first path would deadlock.
- **Remediation:** Document lock order explicitly; consider signaling
  drop counter via channel rather than under publish RLock.

### VULN-065: Drive registry `IsActive`/`register` two-step is non-atomic; concurrent `RunPrepared` on same Run pointer races
- **Severity:** Low
- **Confidence:** 60/100 (Probable)
- **Original Skill:** sc-race-condition (RACE-004)
- **Vulnerability Type:** CWE-362
- **File:** `internal/drive/driver.go:116`; `internal/drive/driver_loop.go:35-118`
- **Reachability:** Concurrent `POST /api/v1/drive/{id}/resume` calls
- **Sanitization:** Registry is mutex-locked; gap between IsActive and register is not
- **Framework Protection:** N/A
- **Description:** Both callers race past `IsActive` before either
  calls `register()`; both run executeLoop on the same `*Run` pointer.
- **Remediation:** Atomic `tryRegister(runID, task, cancel) bool`
  check-and-register.

### VULN-066: Drive drainage 2-second grace window leaks worker goroutines holding HTTP connections
- **Severity:** Low
- **Confidence:** 90/100 (Confirmed)
- **Original Skill:** sc-race-condition (RACE-007)
- **Vulnerability Type:** CWE-833 (resource leak, not deadlock)
- **File:** `internal/drive/driver_loop.go:333-376`
- **Reachability:** Repeated cancel/abandon cycles
- **Sanitization:** N/A
- **Framework Protection:** N/A
- **Description:** Workers blocked on `runner.ExecuteTodo` may not
  check ctx between sub-agent rounds. Send-on-buffered-channel
  succeeds even with no reader, but the goroutine plus its provider
  HTTP client connection live until the LLM provider's HTTP timeout
  fires (~20s). Linear memory growth with abandoned runs.
- **Remediation:** After grace timer, close the results channel;
  workers panic on send-to-closed and the per-worker recover handles.
  Log explicit warn per leaked worker.

### VULN-067: bbolt 1-second lock timeout can't distinguish stale-lock from live-lock contention
- **Severity:** Low
- **Confidence:** 60/100 (Probable)
- **Original Skill:** sc-race-condition (RACE-008)
- **Vulnerability Type:** CWE-362
- **File:** `internal/storage/store.go:71-83`; `cmd/dfmc/main.go:60-77`
- **Reachability:** Operator
- **Sanitization:** N/A
- **Framework Protection:** Degraded-startup allow-list runs without bbolt
- **Description:** On NFS or some Windows scenarios, file locks persist
  ~30s after a crash. 1-second timeout makes legitimate concurrent
  startup look identical to stale lock.
- **Remediation:** Detect stale lock by reading PID marker, or surface
  different message for "lock acquired by us in <N s>" vs "held by
  PID X".

### VULN-068: `WorkspaceApplyRequest.Source` accepts caller-supplied free-form string (today inert; future-fragile)
- **Severity:** Medium
- **Confidence:** 60/100 (Probable)
- **Original Skill:** sc-mass-assignment (MASS-004)
- **Vulnerability Type:** CWE-915
- **File:** `ui/web/server.go:90-94`; `ui/web/server_workspace.go:54-95`
- **Reachability:** Direct
- **Sanitization:** None — only `Source=="latest"` special-cased
- **Framework Protection:** N/A
- **Description:** Field is currently decorative. Future code that
  gates on `req.Source` inherits a free-form string.
- **Remediation:** Validate against closed enum
  (`"client" | "latest"`); reject unknowns with 400.

### VULN-069: `PromptRenderRequest.Vars` and `AnalyzeRequest.Path` lack bounds / validation
- **Severity:** Low
- **Confidence:** 60/100 (Probable)
- **Original Skill:** sc-mass-assignment (MASS-006), sc-mass-assignment (MASS-007)
- **Vulnerability Type:** CWE-915
- **File:** `ui/web/server.go:96-109, 58-71`
- **Reachability:** Direct
- **Sanitization:** Path goes through `EnsureWithinRoot` (verify);
  Vars unbounded
- **Framework Protection:** 4 MiB body cap
- **Description:** Vars unbounded count/size; templates rendered with
  attacker-controlled values may balloon. Path: confirm
  `EnsureWithinRoot` is called in `handleAnalyze` (`server_context.go`);
  if not, add it.
- **Remediation:** Cap Vars count and per-value length; verify and add
  EnsureWithinRoot on AnalyzeRequest.Path if missing.

### VULN-070: Token entry uses `window.prompt()` (plaintext, no masking)
- **Severity:** Low
- **Confidence:** 80/100 (High Probability)
- **Original Skill:** sc-session (SESS-007)
- **Vulnerability Type:** CWE-200
- **File:** `ui/web/static/index.html:758, 1321`
- **Reachability:** Direct
- **Sanitization:** N/A
- **Framework Protection:** N/A
- **Description:** Screen-sharing, shoulder-surfing, accidental clipboard
  captures expose the token.
- **Remediation:** Modal with `<input type="password">`.

### VULN-071: AUTH-009 `--insecure` flag has no confirmation prompt / env-var double-confirm
- **Severity:** Low
- **Confidence:** 60/100 (Probable)
- **Original Skill:** sc-auth (AUTH-009)
- **Vulnerability Type:** CWE-1295
- **File:** `ui/cli/cli_remote.go:66-77`
- **Reachability:** Operator
- **Sanitization:** Stderr warning only
- **Framework Protection:** N/A
- **Description:** Operator pasting a tutorial command silently exposes
  tools to LAN.
- **Remediation:** Require `DFMC_ALLOW_INSECURE=1` env var alongside
  `--insecure`, or 5-second countdown banner.

### VULN-072: AUTH-008 `web.New` reads auth-mode from `eng.Config.Web.Auth` rather than the runtime `--auth` flag
- **Severity:** Low
- **Confidence:** 60/100 (Probable)
- **Original Skill:** sc-auth (AUTH-008)
- **Vulnerability Type:** CWE-665
- **File:** `ui/cli/cli_remote_start.go:58-66`
- **Reachability:** Configuration drift
- **Sanitization:** CLI fences this case, but the layered normalisation is brittle
- **Framework Protection:** Partial
- **Description:** `cfg.Web.Auth=="token"` plus `--auth=none --insecure
  --host 0.0.0.0` does not normalise because config (not flag) drives
  the logic.
- **Remediation:** Thread runtime auth-mode through `web.New` instead
  of reading back off `eng.Config`.

### VULN-073: CI/CD + Docker supply-chain hardening gaps (mutable refs, no signing, no SHA pinning, root container, broken release upload, no `.dockerignore`)
- **Severity:** High (cumulative cluster — broken release pipeline is the headline)
- **Confidence:** 90/100 (Confirmed)
- **Original Skill:** sc-ci-cd (CICD-001..014), sc-docker (DOCKER-001..014)
- **Vulnerability Type:** CWE-829, CWE-494, CWE-345, CWE-250, CWE-1357, CWE-538, CWE-732
- **File:** `.github/workflows/ci.yml`, `.github/workflows/release.yml`, `Dockerfile`, missing `.dockerignore`
- **Reachability:** Build/release pipeline; container deployment
- **Sanitization:** Partial — `pull_request` (not `pull_request_target`) is correctly used
- **Framework Protection:** Partial
- **Description:** This is a cluster of supply-chain findings preserved
  as a single VULN to keep the report focused. Headline issues:
  - **CICD-002 (High):** `actions/upload-release-asset@v4` does not
    exist — release pipeline is broken AND a typo-squatting target.
  - **CICD-001 (High):** Every action pinned by mutable major-version
    tag (`@v4`, `@v5`); supply-chain risk on every CI run.
  - **CICD-003 (High):** No artifact signing (no cosign, no SLSA, no
    GPG); `dfmc update` and Homebrew installs cannot verify provenance.
  - **CICD-004 (High):** `ci.yml` has no `permissions:` block —
    `GITHUB_TOKEN` blast radius depends on per-repo settings.
  - **CICD-005 (Medium):** `release.yml` declares `contents: write` at
    workflow level, wider than per-job needs.
  - **CICD-008 (Medium):** `${{ github.ref_name }}` interpolated into
    `run:` blocks — script injection via crafted git tag name.
  - **CICD-007/012 (Medium/Low):** Bash-only normalisation runs on
    Windows runners; arm64 Linux/Windows cross-compile broken.
  - **CICD-010 (Medium):** No CodeQL / govulncheck / dependency-review.
  - **DOCKER-001 (High):** No `.dockerignore` — `COPY . .` eats
    `.env`, `.git/`, `.dfmc/`, `security-report/` on local
    `docker build`.
  - **DOCKER-002 (High):** Base images `golang:1.25-alpine` and
    `alpine:3.20` not pinned by digest.
  - **DOCKER-003 (High):** Runtime container runs as root; combined
    with DFMC's broad tool surface a tool-call-escape lands as
    container-root.
  - **DOCKER-006 (Medium):** No HEALTHCHECK; orchestrators see "Up"
    even on dead listener.
  - Other medium/low items: distroless candidate, missing EXPOSE,
    missing tini, `git`/`make` in builder image, broken `echo \n`
    config skeleton, `/etc/ssl/private 755`, missing OCI labels.
- **Verification Notes:** Two skills (sc-ci-cd and sc-docker)
  comprehensively cover the supply chain. Kept as one VULN-XXX entry
  because all are remediated by the same workflow audit; expanding
  each to a top-level VULN would dilute focus on the runtime
  application findings. The owning skills' result files retain the
  per-finding detail.
- **Remediation:** SHA-pin all actions; replace
  `actions/upload-release-asset@v4` with maintained alternative;
  add cosign keyless signing or SLSA generator; add top-level
  `permissions: contents: read` to `ci.yml`; add per-job permissions
  in `release.yml`; move `${{ github.ref_name }}` to `env:` interpolation;
  pin runner images by version; matrix-include explicit goos/goarch/ext;
  add govulncheck + CodeQL job; create `.dockerignore` covering
  `.git .github .dfmc .env .env.* .project .vscode .idea bin/ *.exe
  security-report/ shell/ assets/ Makefile CLAUDE.md README*.md *.test
  .claude/`; pin Docker base images by digest; add non-root USER;
  add HEALTHCHECK; consider distroless runtime; add EXPOSE 7777-7779;
  add tini for signal handling.

## Eliminated Findings (False Positives)

No phase-2 findings were eliminated outright as false positives. Several
items had their severity capped or were re-graded as Info (not eliminated),
per the SKILL.md severity-recalculation rules:

- **OREDIR-001/002/003** (sc-open-redirect): all positive/informational
  — DFMC has no redirect handlers and no OAuth flow. Not retained as
  VULNs (no exploitable surface). Confirmed via repo-wide grep for
  `http.Redirect` / `Set("Location"` returning zero matches.
- **HDR-001, HDR-003, HDR-004, HDR-005, HDR-006** (sc-header-injection):
  positive findings — Go 1.25 stdlib rejects CRLF in headers, response
  headers use only static strings, SSE/WS payloads are JSON-encoded,
  no `r.Host` echoing. Not retained as VULNs.
- **SSRF-001, SSRF-002, SSRF-003, SSRF-007, SSRF-008** (sc-ssrf):
  positive findings — `web_fetch` SSRF guard is solid (DNS-rebind safe
  at dial time), MCP transport is stdio-only, hooks do no HTTP. Not
  retained.
- **CLICK-001/002/003/004/005** (sc-clickjacking): all low/info —
  `X-Frame-Options: DENY` is set on every response, CSP self-only
  blocks framing. The `frame-ancestors 'none'` defense-in-depth
  recommendation is not retained as a VULN; CLICK-003 (drive
  buttons) is a future-fragility note also not retained.
- **AUTH-007** (healthz observable): low — bounded by local-only
  threat model. Not retained.
- **AUTH-010** (no in-process HTTPS): info — operators must front
  with TLS proxy; documented behaviour. Not retained.
- **AUTH-011** (MCP no auth): info — IDE host owns the trust
  boundary. Not retained.
- **AUTHZ-009** (Drive planner→executor inherits root authority):
  info — by-design "agent has same authority as operator". Not
  retained.
- **EXPOSE-007** (placeholder echo in validator error): low —
  `isLikelyPlaceholder` correctly rejects real keys today. Not
  retained as VULN; tracked in `internal/config/validator.go:60-62`
  for the future-regression risk.
- **LANG-GO-014** (listFiles silently swallows walk errors): info,
  operational quality. Not retained.
- **LANG-GO-018** (WS unmarshal errors silently discarded): low,
  operational. Not retained.
- **LANG-GO-019** (`stdoutW` ownership): info, depends on `Stop()`
  being called. Not retained.
- **LANG-GO-020** (Upgrade response after upgrade attempt): info,
  log spam only. Not retained.
- **LANG-GO-021** (XFF first-not-last entry): merged into VULN-010.
- **WS-011** (gRPC port not started): info, recorded for reviewers.
  Not retained.
- **CSRF-006** (healthz cross-origin probe): low, fingerprinting only.
  Not retained.
- **CORS-001/005** (no CORS / bearer-in-header): info, positive.
  Not retained.
- **RATE-006** (no auth brute-force protection): low — 32-char hex
  token has 128 bits of entropy, brute force infeasible. Not retained
  unless operator configures a weak custom token.
- **RATE-INFO-008/009** (HTTP defenses confirmed; ReDoS surface
  empty under RE2): positive findings. Not retained.
- **SESS-006/008/009/010**: low/info session-design notes. Not
  retained.
- **SSTI-001/002**: info — regex placeholder is not a template
  engine; injection markers are extracted not evaluated. Not
  retained.
- **MASS-008** (ChatRequest minimal): info, positive. Not retained.
- **CMDI-007** (`run_command` allows `python script.py`): medium
  60-confidence — by-design legitimate workflow per the architecture.
  Not retained.
- **CMDI-010** (`EDITOR` env-var split): low, user owns their env.
  Not retained.
- **CMDI-011** (`git_blame` flag-check ordering): low 50-confidence,
  not exploitable due to `EnsureWithinRoot` first.
  Not retained.
- **RACE-003/006/009/011** (per-loop tool cache invalidation, seed
  ownership comment, leaf-lock LRU manipulation, failure-LRU
  re-aliasing): medium-to-info latent / design-fragility notes.
  Not retained.
- **SQLI-001, sc-jwt, sc-ldap, sc-nosqli, sc-xxe, sc-graphql,
  sc-iac, sc-deserialization, sc-crypto, sc-secrets** all returned
  "no findings" by virtue of "no surface in DFMC". Confirmed by
  dependency / code grep evidence. Not retained.

## Cross-skill merges (notable)

The merges below combined findings from 3+ skills:

- **VULN-001** merged 8 skills (sc-authz, sc-lang-go, sc-api-security,
  sc-business-logic, sc-auth, sc-file-upload, sc-csrf,
  sc-privilege-escalation) on the `source="user"` `engine.CallTool`
  bypass — single largest cross-cut.
- **VULN-002** merged 6 skills (sc-websocket, sc-lang-go, sc-auth,
  sc-api-security, sc-csrf, sc-header-injection) on the
  WebSocket-CheckOrigin-true gap.
- **VULN-009** merged 5 skills (sc-csrf, sc-authz, sc-path-traversal,
  sc-file-upload, sc-lang-go) on the workspace/apply tool-lifecycle
  bypass + path-prefix bug + post-write verification ordering bug.
- **VULN-013** merged 3 skills (sc-data-exposure, sc-authz × 2) on
  file-API serving secrets without classification.
- **VULN-018** merged 4 skills (sc-path-traversal, sc-file-upload × 2,
  sc-mass-assignment) on the magicdoc CLI/web write primitive.
- **VULN-026** merged 4 skills (sc-auth, sc-lang-go, sc-api-security,
  sc-cors) on the empty-token GET / shortcut and CLI/web middleware
  divergence.
- **VULN-028** merged 3 skills (sc-auth, sc-session, sc-websocket) on
  `?token=` query-param leakage.
- **VULN-029** merged 3 skills (sc-auth, sc-session × 2) on no
  rotation/expiry/identity.
- **VULN-031** merged 3 skills (sc-authz × 3) on Drive/Task/Conversation
  ID cross-session access.
- **VULN-032** merged 3 skills (sc-business-logic × 2, sc-mass-assignment)
  on PATCH /api/v1/tasks/{id} state-machine and reparent issues.
- **VULN-037** merged 3 skills (sc-data-exposure, sc-csrf, sc-cors) on
  SSE event payload leakage.
- **VULN-073** merged 2 skills (sc-ci-cd × 14, sc-docker × 14) into
  one cluster preserving per-finding detail in the source files.
