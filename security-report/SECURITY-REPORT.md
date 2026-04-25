# Security Assessment Report

**Project:** DFMC ("Don't Fuck My Code") — `github.com/dontfuckmycode/dfmc`
**Date:** 2026-04-25
**Scanner:** security-check v1.0.0 (48-skill AI-powered static analysis pipeline)
**Risk Score:** 10/10 (Critical Risk)
**Branch:** `fix/restore-original-line-endings`

---

## 1. Executive Summary

A security assessment was performed on **DFMC**, a single-binary Go code-intelligence assistant that combines local AST/codemap analysis with a multi-provider LLM router and exposes three UIs (CLI, Bubble Tea TUI, embedded HTTP+WS workbench) plus an MCP server, all driving the same `engine.Engine`. The scan used **38 specialized security skills** (24 from a prior pass + 14 in this round) across 30+ vulnerability categories. The codebase is **Go 1.25 only**, ~290 internal source files, with HTML and YAML as auxiliary surfaces.

The phase-1 reconnaissance detected a Go-only stack; phase-2 fanned out to all 30+ applicable hunter skills (language-specific JS/TS/Python/Java/Rust/C# scanners were skipped by design — those languages exist only as scanner *targets* inside `internal/security/astscan_*.go`). The phase-3 verifier collapsed **218 raw findings** to **73 verified findings** through cross-skill duplicate detection (notably the `source="user"` bypass surfaced in 8 different skill reports).

### Threat Model Note (read this before scoring shock)

DFMC is a **single-user local-binary** distributed via Homebrew/Docker. Network exposure is `localhost:7777` for `dfmc serve` (and `7778`/`7779` for `dfmc remote`). "Critical" findings here do **not** mean "unauthenticated attacker on the public internet". They mean *any local browser tab on the operator's machine, or any compromised local process, can drive the API* — which, given the convergence of (a) wide-open WebSocket origin policy, (b) approval-gate bypass on the network surface, and (c) Phase-7 tools that can run arbitrary binaries / write arbitrary paths, **is enough to land arbitrary code execution as the operator with zero user interaction beyond visiting a hostile webpage**.

The risk score of 10 is unusual for a local-only tool. It reflects the convergence of the three chains in "Top Risks" below. Each individually would be High; together they are Critical.

### Key Metrics

| Metric | Value |
|--------|-------|
| Total Verified Findings | 73 |
| Critical | 4 |
| High | 19 |
| Medium | 22 |
| Low | 16 |
| Informational | 12 |

### Top Risks (chained scenarios — these are what take the score to 10)

1. **Drive-by RCE from any browser tab.** WebSocket `CheckOrigin: return true` (VULN-002) + `engine.CallTool` hardcodes `source="user"` so the approval gate is dormant on the network surface (VULN-001) + Phase-7 tools `patch_validation.validation_command` (VULN-004) / `benchmark.target` (VULN-005) accept arbitrary command strings = **any web page the operator visits can `new WebSocket('ws://127.0.0.1:7777/api/v1/ws')` and shell out as the operator**. With `auth=none` (the default, VULN-007) no further preconditions; with `auth=token`, DNS rebinding (VULN-003) closes the gap.
2. **Arbitrary file write across the user's home directory.** Phase-7 tools `symbol_move.to_file` (VULN-006), `symbol_rename.file` (VULN-015), `semantic_search.file` (VULN-016), `disk_usage.path` (VULN-017), `magicdoc --path` (VULN-018) all skip `EnsureWithinRoot` that older tools enforce. A single agent loop step or a single `POST /api/v1/tools/symbol_move` writes an attacker-chosen file anywhere the dfmc process can reach — including `~/.dfmc/config.yaml` (hooks subkey → persistent RCE on next launch).
3. **Local-account credential theft.** `Config.Save` writes `~/.dfmc/config.yaml` with mode `0o644` (VULN-014), and the contents include plaintext provider API keys. The web file API serves `.env`/`id_rsa`/`credentials.json` verbatim with no leaf-name classification (VULN-013), and the `/ws` SSE event stream re-broadcasts raw tool params including secrets (VULN-037). On any multi-user POSIX host or any host where the workbench is reachable from a hostile tab, every LLM API key is one HTTP request away.

---

## 2. Scan Statistics

| Statistic | Value |
|-----------|-------|
| Files Scanned | ~290 Go source files |
| Lines of Code | ~120 000 (Go-only first-party) |
| Languages Detected | Go 1.25 (100% of source); HTML (1 embedded workbench page); YAML (config) |
| Frameworks Detected | net/http stdlib, gorilla/websocket v1.5.3, bubbletea v1.3.10, bbolt v1.4.3, tree-sitter v0.25.0, wazero v1.11.0 |
| Skills Executed | 38 (across 2 passes) |
| Findings Before Verification | 218 |
| Duplicates Merged / FPs Downgraded | 145 |
| Final Verified Findings | 73 |

### Confidence Distribution

| Range | Count |
|-------|-------|
| Confirmed (90-100) | 28 |
| High Probability (70-89) | 27 |
| Probable (50-69) | 11 |
| Possible (30-49) | 6 |
| Low Confidence (0-29) | 1 |

### Finding Distribution by Category

| Vulnerability Category | Critical | High | Medium | Low | Info |
|-----------------------|---------:|-----:|-------:|----:|-----:|
| Authorization / Approval Gate | 1 | 3 | 2 | 0 | 0 |
| Authentication / Session | 0 | 2 | 5 | 4 | 0 |
| Command Injection / RCE | 2 | 1 | 3 | 0 | 0 |
| Path Traversal | 1 | 2 | 3 | 0 | 0 |
| WebSocket / CSRF / CORS | 1 | 2 | 4 | 1 | 0 |
| Data Exposure | 0 | 2 | 1 | 2 | 0 |
| Rate Limiting / DoS | 0 | 2 | 4 | 2 | 0 |
| Race Condition / Concurrency | 0 | 2 | 1 | 4 | 1 |
| Business Logic / Mass Assignment | 0 | 2 | 4 | 2 | 0 |
| XSS / Terminal Injection | 0 | 1 | 1 | 0 | 0 |
| SSRF | 0 | 0 | 0 | 2 | 0 |
| Privilege Escalation | 0 | 0 | 2 | 0 | 0 |
| Supply Chain / CI / Container | 0 | 1 (cluster) | 0 | 0 | 0 |
| Dependencies (Go modules) | 0 | 0 | 0 | 0 | ~10 (no exploitable CVE) |
| **TOTALS** | **4** | **19** | **22** | **16** | **12** |

---

## 3. Critical Findings

### VULN-001: HTTP / WebSocket / MCP tool dispatch hardcodes `source="user"`, bypassing the approval gate

- **Severity:** Critical
- **Confidence:** 95/100
- **CWE:** CWE-285 (Improper Authorization), CWE-269 (Improper Privilege Mgmt), CWE-862 (Missing Authorization), CWE-841 (Improper Enforcement of Behavioural Workflow)
- **OWASP Top 10 (2021):** A01 Broken Access Control
- **Location:**
  - `internal/engine/engine_tools.go:120` (CallTool tags `source="user"` unconditionally)
  - `internal/engine/engine_tools.go:225` (`if source != "user" && requiresApproval(name)` — gate skipped)
  - `ui/web/server_tools_skills.go:167`, `ui/web/server_ws.go:260`, `ui/cli/cli_mcp.go:134`
- **Reachability:** Direct (HTTP handler / WS frame / MCP frame → CallTool → Execute)

**Description:**
Every backend tool — including `run_command`, `write_file`, `edit_file`, `apply_patch`, `git_commit`, `web_fetch`, the new Phase-7 tools `patch_validation`/`benchmark`/`symbol_move` — is exposed at `POST /api/v1/tools/{name}`, the WebSocket `tool` JSON-RPC method, and MCP `tools/call`. All three handlers funnel through `engine.CallTool`, which unconditionally tags `source="user"`. The gate at `engine_tools.go:225` therefore never fires for these surfaces, regardless of `tools.require_approval` configuration. The `webApprover` (deny-by-default, `ui/web/approver.go`) is structurally unreachable on the HTTP/WS path.

**Vulnerable Code:**
```go
// internal/engine/engine_tools.go:120
func (e *Engine) CallTool(ctx context.Context, name string, params map[string]any) (*ToolResult, error) {
    return e.executeToolWithLifecycle(ctx, "user", name, params) // <-- source hardcoded
}

// internal/engine/engine_tools.go:225
if source != "user" && e.requiresApproval(name) {
    // approval gate runs only for "agent" / "subagent" sources; never for HTTP/WS/MCP
    ...
}
```

**Proof of Concept (conceptual):**
A token-holder (or, when chained with VULN-002+VULN-007, *any cross-origin browser tab*) sends `POST /api/v1/tools/run_command` with a body specifying the binary and args. The server invokes `CallTool` → `executeToolWithLifecycle("user", ...)` → approval check skipped → command runs.

**Impact:**
Approval gate is dormant on the entire network surface. Operators who configure `require_approval: ["run_command", "write_file"]` are silently exempt for the highest-risk callers. Combined with VULN-002, becomes an unauthenticated browser-to-RCE primitive.

**Remediation:**
```go
// HTTP handler
func (s *Server) handleToolExec(w http.ResponseWriter, r *http.Request) {
    ...
    result, err := s.engine.CallToolWithSource(ctx, "web", name, params) // <-- explicit source
}

// engine_tools.go: also accept "subagent", "web", "ws", "mcp" — gate everything except "user"
```
Reserve `"user"` for stdin-driven CLI/TUI only. The existing test `internal/engine/approver_test.go:150` pins the bypass; flip its assertion to drive the regression.

**References:** [CWE-285](https://cwe.mitre.org/data/definitions/285.html), [OWASP A01](https://owasp.org/Top10/A01_2021-Broken_Access_Control/)

---

### VULN-002: WebSocket upgrader accepts every Origin

- **Severity:** Critical (when chained with VULN-001 and `auth=none`); High otherwise
- **Confidence:** 95/100
- **CWE:** CWE-1385 (Missing Origin Validation in WebSockets), CWE-346 (Origin Validation Error), CWE-942 (Permissive Cross-domain Policy)
- **OWASP Top 10 (2021):** A05 Security Misconfiguration, A01 Broken Access Control
- **Location:** `ui/web/server_ws.go:29-35`

**Description:**
`wsUpgrader.CheckOrigin` is hard-coded to `return true`. No caller of `web.New` overrides it. Any web page in the operator's browser can `new WebSocket('ws://127.0.0.1:7777/api/v1/ws')` from any origin and hold a long-lived bidirectional channel.

**Vulnerable Code:**
```go
// ui/web/server_ws.go:29-35
var wsUpgrader = websocket.Upgrader{
    ReadBufferSize:  1024,
    WriteBufferSize: 1024,
    CheckOrigin: func(r *http.Request) bool {
        return true // <-- accept-all
    },
}
```

**Proof of Concept (conceptual):**
Operator visits `https://attacker.example/`. The page runs:
```
const ws = new WebSocket('ws://127.0.0.1:7777/api/v1/ws');
ws.onopen = () => ws.send(JSON.stringify({method:'tool', params:{name:'run_command', args:{...}}}));
```
With `auth=none` (default, VULN-007) the upgrade succeeds and the tool runs (via the bypass in VULN-001). With `auth=token` the standard `WebSocket` constructor cannot pass an `Authorization` header — but DNS rebinding (VULN-003) re-opens the door.

**Impact:**
Cross-origin browser-to-RCE primitive. Localhost bind does **not** mitigate — any process on the host (browser tab included) can hit `127.0.0.1`.

**Remediation:**
```go
var wsUpgrader = websocket.Upgrader{
    CheckOrigin: func(r *http.Request) bool {
        origin := r.Header.Get("Origin")
        host := strings.SplitN(r.Host, ":", 2)[0]
        allowed := []string{
            "http://127.0.0.1:" + port,
            "http://localhost:" + port,
        }
        return slices.Contains(allowed, origin) || (origin == "" && (host == "127.0.0.1" || host == "localhost"))
    },
}
```
Expose `Server.SetAllowedOrigins([]string)` for operator override.

**References:** [CWE-1385](https://cwe.mitre.org/data/definitions/1385.html), [Gorilla WebSocket origin docs](https://pkg.go.dev/github.com/gorilla/websocket#Upgrader)

---

### VULN-004: Arbitrary command execution via `validation_command` parameter in `patch_validation`

- **Severity:** Critical
- **Confidence:** 95/100
- **CWE:** CWE-77 (Command Injection), CWE-78 (OS Command Injection), CWE-94 (Code Injection), CWE-22 (Path Traversal via `project_root`)
- **OWASP Top 10 (2021):** A03 Injection
- **Location:** `internal/tools/patch_validation.go:64-68, 99, 134-153, 224`

**Description:**
The Phase-7 `patch_validation` tool accepts a free-form `validation_command` string and an arbitrary `project_root` override, then runs `exec.CommandContext` with `cmd.Dir = projectRoot`. **None** of `RunCommandTool`'s protections — `isBlockedShellInterpreter`, `isBlockedBinary`, `hasScriptRunnerWithEvalFlag`, `EnsureCommandAllowed`, `EnsureWithinRoot` on the binary path or on `project_root` — apply. A single tool call gives arbitrary code execution as the dfmc process owner, with attacker-chosen working directory. The same file also reads patch targets via `os.ReadFile(projectRoot + "/" + targetPath)` (string concatenation, no `EnsureWithinRoot`), an arbitrary file read primitive on its own.

**Vulnerable Code:**
```go
// internal/tools/patch_validation.go (paraphrased)
parts := strings.Fields(validationCommand)
cmd := exec.CommandContext(runCtx, parts[0], parts[1:]...) // <-- attacker-controlled binary + args
cmd.Dir = projectRoot                                       // <-- attacker-controlled CWD
out, err := cmd.CombinedOutput()
```

**Proof of Concept (conceptual):**
Single tool call: `tools/patch_validation` with `{"validation_command": "<binary> <args>", "project_root": "/"}`. None of the run_command guards run. The tool spec also (incorrectly) declares `Risk: RiskRead`.

**Impact:**
Arbitrary code execution as the dfmc process owner, reachable from agent loop, MCP, web tool dispatch, CLI `dfmc tool`. Combined with VULN-001/002, reachable from any browser tab.

**Remediation:**
Either remove `validation_command` and require callers to dry-run via `apply_patch` and run validators separately through `run_command`, OR route through `RunCommandTool.Execute` so the full block-list / shell-interpreter / eval-flag / allowed-binary chain applies:
```go
// Delegate to RunCommandTool so all guards engage
res, err := e.engine.CallToolWithSource(ctx, source, "run_command", map[string]any{
    "command": parts[0],
    "args":    parts[1:],
    "cwd":     projectRoot,
})
```
Wrap the `project_root` override with `EnsureWithinRoot(req.ProjectRoot, override)`. Reclassify Risk to `RiskCommand`.

**References:** [CWE-77](https://cwe.mitre.org/data/definitions/77.html), [OWASP A03](https://owasp.org/Top10/A03_2021-Injection/)

---

### VULN-006: Arbitrary file write via `symbol_move` `to_file` parameter

- **Severity:** Critical
- **Confidence:** 95/100
- **CWE:** CWE-22 (Path Traversal → arbitrary file write)
- **OWASP Top 10 (2021):** A01 Broken Access Control, A05 Security Misconfiguration
- **Location:** `internal/tools/symbol_move.go:124-128, 244, 250`

**Description:**
The Phase-7 `symbol_move` tool builds the destination path as `filepath.Join(projectRoot, toFile)` with no containment check. `to_file="../../tmp/pwned.go"` (or an absolute path) produces an out-of-root path; the tool then `os.MkdirAll(destDir, 0755)` and `os.WriteFile(destPath, newFileContent, 0644)`. The destination is **not** subject to `EnsureReadBeforeMutation`.

**Vulnerable Code:**
```go
// internal/tools/symbol_move.go:124-128
destPath := filepath.Join(projectRoot, toFile) // <-- no EnsureWithinRoot
if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil { ... }
if err := os.WriteFile(destPath, newFileContent, 0644); err != nil { ... }
```

**Proof of Concept (conceptual):**
Tool call writes Go-shaped source to `~/.dfmc/hooks` config (graduating to RCE on next DFMC launch), or to `~/.bashrc`, or to `~/.ssh/authorized_keys`. The agent loop or any HTTP/MCP caller can issue this in one step.

**Impact:**
Arbitrary file write across the user's filesystem, scoped only to the dfmc process owner's permissions. Trivially graduates to persistent RCE via dotfile/hooks-config overwrite.

**Remediation:**
```go
abs, err := EnsureWithinRoot(projectRoot, toFile)
if err != nil {
    return nil, fmt.Errorf("symbol_move: to_file escapes project root: %w", err)
}
// Wire EnsureReadBeforeMutation if abs already exists
if _, err := os.Stat(abs); err == nil {
    if err := e.EnsureReadBeforeMutation(abs, readGateStrict); err != nil { return nil, err }
}
destPath := abs
```
Refuse absolute paths with an actionable error pointing the caller at relative-to-root usage.

**References:** [CWE-22](https://cwe.mitre.org/data/definitions/22.html)

---

## 4. High Findings

### VULN-003: No `Host:` header validation — DNS rebinding bypasses loopback-only bind

- **Severity:** High | **Confidence:** 80/100 | **CWE:** CWE-350, CWE-352, CWE-1385
- **Location:** `ui/web/server.go:131-160`, `ui/web/server_ws.go:92-106`

The intended mitigation for cross-origin browser access is "loopback-only bind". DNS rebinding circumvents this: an attacker page first resolves `evil.example.com` to a public IP (passing initial CORS), then re-binds to `127.0.0.1` so subsequent fetches go to the local DFMC. DFMC nowhere validates `r.Host` against a loopback allowlist. With VULN-001/VULN-002 present, this is the full-RCE pathway from any internet origin in the user's browser.

**Remediation:** Validate `r.Host` against `{127.0.0.1:<port>, localhost:<port>}` allowlist on every request entering `securityHeaders` middleware; reject mismatch with HTTP 421 Misdirected Request:
```go
func hostAllowlist(allowed []string, next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if !slices.Contains(allowed, r.Host) {
            http.Error(w, "host not allowed", http.StatusMisdirectedRequest)
            return
        }
        next.ServeHTTP(w, r)
    })
}
```

---

### VULN-005: `go test` flag injection via `target` / `cpuprofile` / `memprofile` in `benchmark`

- **Severity:** High | **Confidence:** 85/100 | **CWE:** CWE-88, CWE-78
- **Location:** `internal/tools/benchmark.go:75-110`

No `--` separator between `go test` flags and the positional `target`, and no `rejectGitFlagInjection`-style refusal of leading `-`. A `target = "-exec=<wrapper>"` (or `-toolexec=…`, `-o=/path/anywhere`) makes `go test` shell out to a user-supplied wrapper. `cpuprofile`/`memprofile` accept arbitrary paths so the profiler doubles as a write primitive.

**Vulnerable Code:**
```go
args := []string{"test", "-bench=.", "-run=^$"}
if cpuprofile != "" { args = append(args, "-cpuprofile", cpuprofile) }
args = append(args, target) // <-- positional, no `--` separator, leading `-` not refused
exec.CommandContext(ctx, "go", args...)
```

**Remediation:** Insert `--` between flags and target; refuse leading `-` on every user-supplied argument; route `target`/`cpuprofile`/`memprofile` through `EnsureWithinRoot`.

---

### VULN-007: `auth=none` is the default; new operators get an unauthenticated network-local API exposing tool/shell/filesystem

- **Severity:** High | **Confidence:** 85/100 | **CWE:** CWE-1188, CWE-306
- **Location:** `internal/config/defaults.go`, `ui/web/server.go:131-150`, `ui/cli/cli_remote.go:40-77`

Default `Web.Auth = "none"`. Bind-host normalization forces 127.0.0.1 in that case (so non-loopback attack is closed); but on the loopback interface the API is wide open to any local process and any cross-origin browser tab (VULN-002). The CLI emits no warning about the default; the workbench HTML doesn't surface "you are unauthenticated".

**Remediation:** Default to `auth=token` with auto-generated token written to `~/.dfmc/web.token` mode 0600 and printed to stderr at startup:
```go
if cfg.Web.Auth == "" || cfg.Web.Auth == "none" {
    tok := generateToken(32) // crypto/rand
    os.WriteFile(filepath.Join(home, ".dfmc/web.token"), []byte(tok), 0o600)
    fmt.Fprintf(os.Stderr, "DFMC: web token written to ~/.dfmc/web.token (mode 0600)\n")
    cfg.Web.Auth = "token"; cfg.Web.Token = tok
}
```

---

### VULN-008: `RequireApproval` defaults to empty list — even agent-initiated calls bypass the gate

- **Severity:** High | **Confidence:** 90/100 | **CWE:** CWE-1188
- **Location:** `internal/engine/approver.go:94-106`, `internal/config/config_types.go:382-395`, `internal/config/defaults.go`

`requiresApproval` returns true only when the tool name is on the explicit list. Default config has the list empty, so even agent-initiated calls — which **do** reach the gate (`source="agent"` triggers the check) — bypass it because no tool is on the list. Combined with VULN-001, the practical reality is "the approval gate is dormant in default configuration".

**Remediation:** Ship sane default:
```yaml
tools:
  require_approval:
    - run_command
    - write_file
    - apply_patch
    - delegate_task
    - git_commit
    - patch_validation
    - benchmark
```
Or `["*"]` as the strictest default, letting operators subtract.

---

### VULN-009: `POST /api/v1/workspace/apply` writes project files via direct `git apply`, bypassing tool lifecycle

- **Severity:** High | **Confidence:** 90/100 | **CWE:** CWE-285, CWE-22, CWE-367, CWE-352
- **Location:** `ui/web/server_workspace.go:54-96, 215, 217-257, 251`

Three issues collide on this one handler: (1) it does **not** route through `engine.CallTool`, so the approval gate, pre/post hooks, and `EnsureReadBeforeMutation` strict-mode hash check are all bypassed; (2) post-write path verification runs *after* `git apply` has already modified files — the deny branch returns 400 but cannot un-apply; (3) the path-containment check at line 251 uses `strings.HasPrefix(absPath, root)` with no separator boundary, so `/proj-evil/foo` passes when `root="/proj"`.

**Remediation:**
```go
// Route through the lifecycle so approval + hooks + read-before-mutation engage
res, err := s.engine.CallToolWithSource(ctx, "web", "apply_patch", map[string]any{
    "patch": req.Patch,
})
// Reorder so --dry-run --porcelain enumerates paths BEFORE actual apply.
// Replace HasPrefix with filepath.Rel-based containment (matches EnsureWithinRoot semantics).
```

---

### VULN-010: Per-IP rate limiter trivially bypassed via `X-Forwarded-For`

- **Severity:** High | **Confidence:** 95/100 | **CWE:** CWE-770, CWE-348
- **Location:** `ui/web/server.go:374-390`

The comment justifies XFF trust by "remote clients cannot pass auth without a proxy" — but rate-limit middleware sits **before** bearer-token middleware, so unauthenticated requests are rate-limited (and brute-forceable) using the same XFF logic. Any client can rotate `X-Forwarded-For: <random>` and reset their bucket every request. Compounding bug: when XFF is honored, the function returns the **first** entry rather than the rightmost (proxy-trusted) one.

**Remediation:** Only honor XFF when `r.RemoteAddr` is in a configured trusted-proxy list (loopback by default); use the rightmost entry; document policy.

---

### VULN-011: MCP client subprocess inherits full parent env (LLM API keys + DFMC tokens)

- **Severity:** High | **Confidence:** 85/100 | **CWE:** CWE-200, CWE-526, CWE-78, CWE-668
- **Location:** `internal/mcp/client.go:35-48, 92-93, 198-222`

`cmd.Env = append(os.Environ(), env...)` copies every parent env var into every external MCP server subprocess: all of `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `DEEPSEEK_API_KEY`, `KIMI_API_KEY`, `ZAI_API_KEY`, `ALIBABA_API_KEY`, `MINIMAX_API_KEY`, `GOOGLE_AI_API_KEY`, `DFMC_WEB_TOKEN`, `DFMC_REMOTE_TOKEN`, `DFMC_APPROVE`. Sub-issues at the same call site: `exec.Command` (not `exec.CommandContext`), `sendSync` goroutine leaks one parked `ReadBytes` per cancelled call, `cmd.Dir` not set.

**Remediation:**
```go
minEnv := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), ...}
for _, k := range cfg.EnvPassthrough { // operator opt-in list
    if v, ok := os.LookupEnv(k); ok { minEnv = append(minEnv, k+"="+v) }
}
cmd := exec.CommandContext(ctx, bin, args...)
cmd.Env = append(minEnv, env...)
cmd.Dir = cfg.Sandbox // config-specified
```

---

### VULN-013: Web file API serves project secrets verbatim (`.env`, `id_rsa`, `credentials.json`)

- **Severity:** High | **Confidence:** 95/100 | **CWE:** CWE-200, CWE-552, CWE-538
- **Location:** `ui/web/server_files.go:45-105, 107-131, 143-183`

`GET /api/v1/files/{path...}` returns raw bytes of any in-root path. Project-root `.env` (auto-loaded at startup) reads in one HTTP request. Same for `id_rsa`, `*.pem`, `credentials.json`. Compounded by AUTHZ-008: writes via `write_file` tool also lack classification, so writing a malicious `.dfmc/config.yaml` (hooks shell command) → persistent RCE. The TUI has `looksLikeSecretFile` (`ui/tui/secret_redact.go`); the web handler does not import it.

**Remediation:** Promote `looksLikeSecretFile` to a shared package; have `handleFileContent` substitute `{redacted: true}` for any matching path; apply the same predicate as a forbidden-leafs check on writes via `EnsureWithinRoot`.

---

### VULN-014: `Config.Save` writes `~/.dfmc/config.yaml` (plaintext API keys) with mode `0o644`

- **Severity:** High | **Confidence:** 95/100 | **CWE:** CWE-732, CWE-200
- **Location:** `internal/config/config.go:175-187`, `internal/config/config_models_dev.go:125`

Provider profiles store plaintext `APIKey`. The file is written world-readable (`0o644`). On any multi-user POSIX host, every other local account can `cat ~/.dfmc/config.yaml` and harvest keys. The bbolt store correctly uses `0o600` (`internal/storage/store.go:71`); `config.yaml` is the outlier.

**Remediation:**
```go
if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil { return err }
if err := os.WriteFile(path, data, 0o600); err != nil { return err }
```
Optionally split secrets into `~/.dfmc/credentials.yaml` mirroring `~/.aws/credentials`.

---

### VULN-015: Arbitrary file modification via `symbol_rename` `file` parameter

- **Severity:** High | **Confidence:** 90/100 | **CWE:** CWE-22
- **Location:** `internal/tools/symbol_rename.go:124, 188, 198, 271`

`file="../../etc/hosts"` → `filepath.Join(projectRoot, file)` produces an out-of-root path. Read at `findRenameMatches` line 271 is direct `os.ReadFile(filePath)` and surfaces line content in `changes[].fullLine`. Write is harder (read-before-mutation gate) but read alone leaks `~/.ssh/config`, `~/.aws/credentials`, `/etc/passwd`.

**Remediation:** `EnsureWithinRoot(projectRoot, file)` at line 123-124 before the join.

---

### VULN-019: WebSocket has no per-connection rate limit; no `SetReadLimit`

- **Severity:** High | **Confidence:** 95/100 | **CWE:** CWE-770, CWE-799
- **Location:** `ui/web/server_ws.go:108-122`

After upgrade, `readLoop` is unbounded. Each message dispatches `chat`/`ask`/`tool` synchronously. A single WS connection can spam unlimited rounds and exhaust LLM quota / bill the operator. No `SetReadLimit` means a single 100 MB JSON-RPC frame is buffered into memory.

**Remediation:**
```go
c.conn.SetReadLimit(64 * 1024)
lim := rate.NewLimiter(rate.Limit(5), 10) // 5 rps, burst 10
for {
    if err := lim.Wait(ctx); err != nil { return }
    c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
    ...
}
```
Add ping/pong heartbeat (see VULN-020).

---

### VULN-024: `taskstore.UpdateTask` is read-modify-write across two transactions

- **Severity:** High | **Confidence:** 90/100 | **CWE:** CWE-362, CWE-367
- **Location:** `internal/taskstore/store.go:74-86, 411-454`

Load and Save are separate bbolt transactions with the mutator running between. Concurrent `PATCH /api/v1/tasks/{id}` calls or concurrent `OnTaskBlocked`/`OnTaskUnblocked` walks load the same baseline, both apply, both save → second writer silently overwrites the first.

**Remediation:** Wrap entire load+mutate+save in a single `db.Update(...)` transaction so bbolt's writer lock serializes correctly.

---

### VULN-025: TOCTOU between read-before-mutation gate and `os.WriteFile` under concurrent sub-agents

- **Severity:** High | **Confidence:** 80/100 | **CWE:** CWE-367
- **Location:** `internal/tools/engine.go:516-546, 650-697` + `tool.Execute(ctx, req)` at `:457`

After the gate clears, the tool's own `os.WriteFile` runs. Two sub-agents passing the gate simultaneously, then writing in succession, lose one set of changes silently — drift only detected on the third write.

**Remediation:** Per-absPath `sync.Mutex` held from gate-check through `Execute` for `write_file`/`apply_patch`.

---

### VULN-027: Bearer token persisted in browser `localStorage`

- **Severity:** High | **Confidence:** 85/100 | **CWE:** CWE-922, CWE-1004 (parallel)
- **Location:** `ui/web/static/index.html:703-747`

The token survives tab close, browser restart, and server-process restart. A future XSS in the workbench, a malicious browser extension, or DevTools-shoulder-surfing reads it instantly. CSP `script-src 'self'` blocks inline-script injection (mitigation, not elimination).

**Remediation:** Prefer in-memory storage with session-cookie fallback (HttpOnly + SameSite=Strict + Secure-when-HTTPS); re-prompt on workbench reload.

---

### VULN-032: `PATCH /api/v1/tasks/{id}` mass-assigns state without transition validation

- **Severity:** High | **Confidence:** 90/100 | **CWE:** CWE-915, CWE-841
- **Location:** `ui/web/server_task.go:159-218`

Caller can flip `state` arbitrarily, set `done` on never-started tasks (forge completion), reparent any task to any other (creating cycles that `ValidateTree` only detects on demand), clear `error`/`blocked_reason`. Drive runs that depend on task tree state are tricked into believing work was done that wasn't.

**Remediation:** Replace `map[string]any` decode with explicit `TaskPatchRequest` struct exposing only `title`, `detail`, `summary`, `labels`. Reject `state` writes from HTTP; add `POST /api/v1/tasks/{id}/transition` with state-machine validator. Run cycle detection on every reparent.

---

### VULN-033: `POST /api/v1/tasks` accepts client-controlled `id` allowing task overwrite

- **Severity:** High | **Confidence:** 90/100 | **CWE:** CWE-915, CWE-284
- **Location:** `ui/web/server_task.go:25-44, 104-107`, `internal/taskstore/store.go:24-42`

Submitting `{"title":"x", "id":"<existing>"}` silently overwrites the existing task with attacker fields. Original history is lost. `SaveTask` does unconditional `b.Put` (silent overwrite). Drive TODOs persisted as tasks → guess-the-ID pre-create attack.

**Remediation:** Drop `ID` from `TaskCreateRequest`; always allocate via `taskstore.NewTaskID()`. If explicit-ID is needed, separate admin endpoint that refuses on collision.

---

### VULN-038: TUI tool-output ANSI / OSC injection (terminal-control bytes pass through unfiltered)

- **Severity:** High | **Confidence:** 85/100 | **CWE:** CWE-150
- **Location:** `internal/engine/agent_loop_events.go:139`, `internal/engine/agent_loop.go:376-389`, `internal/tools/web.go:182-217`

Hostile `web_fetch` returning `\x1b[2J\x1b[H` clears the user's terminal; `\x1b]0;You have been pwned\x07` rewrites window title; OSC 8 hyperlinks embed phishing links inside plausible "documentation" text. Modern emulators (kitty, iTerm2, Windows Terminal, WezTerm) honor OSC 8 universally. LLM-emitted assistant text flows the same path. `htmlToText` in `web_fetch` doesn't filter.

**Remediation:** Strip C0 (`\x00-\x1f` except `\t`,`\n`) and C1 (`\x80-\x9f`) at the engine publish boundary (`agent_loop_events.go`), so TUI and web both benefit. One regex, applied once.

---

### VULN-042: `/api/v1/tasks` pagination has no upper bound on `limit`

- **Severity:** High | **Confidence:** 95/100 | **CWE:** CWE-770
- **Location:** `ui/web/server_task.go:63-66`; same pattern at `server_drive.go:126-145` and likely on `/api/v1/conversations`, `/api/v1/conversation/branches`, `/api/v1/memory`, `/api/v1/files`.

`?limit=999999999` triggers unbounded read + JSON-encode. 30 such requests/sec via the per-IP limiter (or more via VULN-010's XFF bypass) → server-process OOM.

**Remediation:** Cap every list endpoint at 200-500 with default 100.

---

### VULN-052: `dfmc remote start` exposes full WS surface on a network-reachable port

- **Severity:** High (when `--insecure --auth=none --host 0.0.0.0` used) | **Confidence:** 90/100 | **CWE:** CWE-1385, CWE-668
- **Location:** `ui/cli/cli_remote_start.go:58-66`

`web.New()` is reused, so `wsUpgrader` is shared. With `--insecure`, anyone on the network can WS-upgrade with no origin/Host/auth check. AUTH-009 (no `--insecure` confirmation prompt) compounds.

**Remediation:** Even under `--insecure`, retain strict `CheckOrigin` allow-list (or require explicit `--allow-any-origin`). Require `DFMC_ALLOW_INSECURE=1` env var alongside `--insecure`.

---

### VULN-073: CI/CD + Docker supply-chain hardening cluster

- **Severity:** High (cumulative cluster — broken release pipeline is the headline) | **Confidence:** 90/100 | **CWE:** CWE-829, CWE-494, CWE-345, CWE-250, CWE-1357, CWE-538, CWE-732
- **Location:** `.github/workflows/ci.yml`, `.github/workflows/release.yml`, `Dockerfile`, missing `.dockerignore`

Aggregate of 28 supply-chain findings preserved as one VULN to keep the report focused. Headline:
- **CICD-002:** `actions/upload-release-asset@v4` does not exist — release pipeline is broken AND a typo-squatting target.
- **CICD-001:** Every action pinned by mutable major-version tag (`@v4`, `@v5`).
- **CICD-003:** No artifact signing (no cosign, no SLSA, no GPG); `dfmc update` and Homebrew installs cannot verify provenance.
- **CICD-004:** `ci.yml` has no `permissions:` block — `GITHUB_TOKEN` blast radius is unbounded.
- **CICD-008:** `${{ github.ref_name }}` interpolated into `run:` blocks — script injection via crafted git tag name.
- **DOCKER-001:** No `.dockerignore` — `COPY . .` eats `.env`, `.git/`, `.dfmc/`, `security-report/` on local `docker build`.
- **DOCKER-002:** Base images not pinned by digest.
- **DOCKER-003:** Runtime container runs as root; combined with DFMC's broad tool surface, a tool-call escape lands as container-root.

**Remediation:** SHA-pin all actions; replace the broken release-asset action; add cosign keyless signing or SLSA generator; add top-level `permissions: contents: read` to `ci.yml`; per-job permissions in `release.yml`; move `${{ github.ref_name }}` to `env:` interpolation; create `.dockerignore`; pin Docker base images by digest; add non-root `USER`; add `HEALTHCHECK`; consider distroless runtime.

---

## 5. Medium Findings

| ID | Title | CWE | Location |
|----|-------|-----|----------|
| VULN-012 | Hooks subprocess receives every API key in env | CWE-200, CWE-526 | `internal/hooks/hooks.go:197` |
| VULN-016 | Arbitrary file read in `semantic_search` via `file` parameter | CWE-22 | `internal/tools/semantic_search.go:149, 202` |
| VULN-017 | Filesystem topology disclosure via `disk_usage` `path` parameter | CWE-22 | `internal/tools/disk_usage.go:67-71` |
| VULN-018 | CLI `magicdoc --path <abs>` writes anywhere; web silently falls back | CWE-22, CWE-915 | `ui/cli/cli_magicdoc.go:124-132`, `ui/web/server_context.go:300-324` |
| VULN-020 | WebSocket has no ping/pong heartbeat; half-open conns accumulate | CWE-400, CWE-770 | `ui/web/server_ws.go:92-106` |
| VULN-021 | WebSocket has no per-IP active-connection cap | CWE-770 | `ui/web/server.go:357-367` |
| VULN-022 | `wsConn.send` race on closed channel; cleanup panics | CWE-362, CWE-672 | `ui/web/server_ws.go:73-85, 295-312` |
| VULN-023 | WebSocket handlers use `context.Background()` | CWE-404, CWE-770 | `ui/web/server_ws.go:124-153` |
| VULN-026 | `bearerTokenMiddleware` empty-token shortcut for `GET /` (web/CLI divergence) | CWE-287, CWE-862 | `ui/web/server.go:401-419`, `ui/cli/cli_remote_server.go:64-93` |
| VULN-028 | `/ws` SSE accepts token via `?token=` query parameter | CWE-598 | `ui/cli/cli_remote_server.go:87-90` |
| VULN-029 | No token rotation, no expiry, no revocation, no per-client identity | CWE-613, CWE-287 | `ui/web/server.go:181-186, 36-43` |
| VULN-030 | Token can be planted via URL hash, then auto-stored | CWE-598 | `ui/web/static/index.html:707-735` |
| VULN-031 | Drive/Task/Conversation IDs accessible cross-conceptual-session within auth perimeter | CWE-639 | `ui/web/server_drive.go:150-303`, `server_task.go`, `server_conversation.go` |
| VULN-034 | `POST /api/v1/drive` accepts unbounded `MaxParallel`/`MaxTodos`/`AutoApprove` | CWE-915, CWE-770, CWE-284 | `ui/web/server_drive.go:36-48, 82-93` |
| VULN-035 | Sub-agent `allowed_tools` is a prompt hint, not enforced sandbox | CWE-269, CWE-1357 | `internal/engine/subagent.go:31-56`, `internal/tools/delegate.go:73-119` |
| VULN-036 | `hooks.CheckConfigPermissions` is dead code — group/world-writable config silently grants RCE | CWE-732, CWE-269, CWE-1188 | `internal/hooks/hooks.go:300-314` |
| VULN-037 | SSE `/ws` event stream broadcasts raw tool params/output_preview/error — secrets exfil | CWE-200, CWE-532, CWE-942 | `internal/engine/agent_loop_events.go:100-168` |
| VULN-039 | Workbench `escapeHTML` does not escape quotes; SVG `title=` interpolates user content | CWE-79 | `ui/web/static/index.html:676-681, 894-898, 968-990` |
| VULN-043 | No concurrency cap on Drive runs per client / globally | CWE-770 | `ui/web/server_drive.go:55-121` |
| VULN-044 | SSE `/api/v1/chat` has no per-stream wall-clock cap; cleared write deadline | CWE-770 | `ui/web/server_chat.go:60-114` |
| VULN-045 | Provider router has no client-side outbound rate limit | CWE-770 | `internal/provider/throttle.go`, `router.go` |
| VULN-050 | `handleToolExec` doesn't validate `Content-Type` (CORS-simple POST bypasses preflight) | CWE-352, CWE-942 | `ui/web/server_tools_skills.go:151-173` |
| VULN-053 | gh_runner flag-injection check is one-sided (refuses `-x`, allows `--jq=$(...)`) | CWE-88 | `internal/tools/gh_runner.go:60-65` |
| VULN-054 | `run_command` allow-list misses indirection bypass (`env sudo`, `nice sudo`, ...) | CWE-78 | `internal/tools/command.go:296-358` |
| VULN-055 | `git_worktree_remove` `path` arg not constrained to project root | CWE-22, CWE-78 | `internal/tools/git_worktree.go:189-218` |
| VULN-056 | Hooks shell-mode default + `args:` mode skips block-list | CWE-77, CWE-78 | `internal/hooks/hooks.go:122-127, 246-249` |
| VULN-059 | Drive `RunPrepared` preserves caller-supplied `Todos` slice (latent contract trap) | CWE-915, CWE-841 | `internal/drive/driver.go:98-219` |
| VULN-060 | Drive `Resume` trusts persisted `Done` TODO state without integrity chain | CWE-841 | `internal/drive/driver.go:229-283` |
| VULN-061 | Auto-resume cumulative ceiling has one-budget overshoot per cycle | CWE-770, CWE-841 | `internal/engine/agent_loop_autonomous.go:113-184` |
| VULN-068 | `WorkspaceApplyRequest.Source` accepts caller-supplied free-form string (latent) | CWE-915 | `ui/web/server.go:90-94`, `server_workspace.go:54-95` |

---

## 6. Low Findings

| ID | Title | CWE | Location |
|----|-------|-----|----------|
| VULN-040 | Tool panic guard returns full Go runtime stack to LLM-visible error | CWE-209 | `internal/engine/engine_tools.go:174-216` |
| VULN-041 | Verbose error responses echo internal details (`err.Error()` in 70+ handlers) | CWE-209 | `ui/web/server_*.go` (72 occurrences) |
| VULN-046 | `applyUnifiedDiffWeb` and `gitWorkingDiffWeb` use `context.Background()` | CWE-400 | `ui/web/server_workspace.go:108-110, 215, 234` |
| VULN-047 | SSE event drop counter mismatch — per-subscriber buffer 64 vs bus 1024 | CWE-665 | `ui/web/server_ws.go:174-193` |
| VULN-048 | Hooks `Fire` lacks per-fire panic guard around pre/post dispatch | CWE-755 | `internal/hooks/hooks.go`; `internal/engine/engine_tools.go:253-298` |
| VULN-051 | WS `events.subscribe` is a stub that never registers (latent leak path) | CWE-200 | `ui/web/server_ws.go:281-293` |
| VULN-057 | SSRF — provider router has no `safeTransport` guard; project config can point `base_url` at internal IPs | CWE-918 | `internal/provider/openai_compat.go`, `anthropic.go`, `google.go` |
| VULN-058 | SSRF — `dfmc update --host` and `config sync-models` URL override use stdlib transport without guard | CWE-918 | `internal/config/config_models_dev.go:77-103`, `ui/cli/cli_update.go:157-214` |
| VULN-062 | Conversation `BranchSwitch` race during in-flight `Ask` corrupts history | CWE-362, CWE-841 | `internal/conversation/manager.go:146-153, 174-185` |
| VULN-063 | `BranchCreate` allows orphan / control-char / path-style branch names | CWE-20 | `internal/conversation/manager.go:155-172` |
| VULN-065 | Drive registry `IsActive`/`register` two-step is non-atomic | CWE-362 | `internal/drive/driver.go:116`, `driver_loop.go:35-118` |
| VULN-066 | Drive drainage 2-second grace window leaks worker goroutines | CWE-833 | `internal/drive/driver_loop.go:333-376` |
| VULN-067 | bbolt 1-second lock timeout can't distinguish stale-lock from live-lock | CWE-362 | `internal/storage/store.go:71-83` |
| VULN-069 | `PromptRenderRequest.Vars` and `AnalyzeRequest.Path` lack bounds / validation | CWE-915 | `ui/web/server.go:96-109, 58-71` |
| VULN-070 | Token entry uses `window.prompt()` (plaintext, no masking) | CWE-200 | `ui/web/static/index.html:758, 1321` |
| VULN-071 | `--insecure` flag has no confirmation prompt / env-var double-confirm | CWE-1295 | `ui/cli/cli_remote.go:66-77` |
| VULN-072 | `web.New` reads auth-mode from config rather than runtime `--auth` flag (config drift) | CWE-665 | `ui/cli/cli_remote_start.go:58-66` |

---

## 7. Informational

- **VULN-049** — `Server.New` silently rebinds non-loopback host to 127.0.0.1 when `auth=none` (no stderr message). Operational observability gap.
- **VULN-064** — EventBus `Publish` lock-order with `droppedMu` is implicit (latent deadlock potential if a future caller holds `droppedMu` first).

### Positive observations and intentional non-findings

The verifier eliminated several phase-2 candidates as false positives or by-design:

- **No SQL injection / NoSQL injection / LDAP / XXE / GraphQL / deserialization / open-redirect / JWT** surface — confirmed by repo-wide grep zero-matches and dependency-audit absence of relevant libraries.
- **No CSRF tokens** is a *deliberate* mitigation strategy (bearer-token-in-header, not cookies) — works *if* loopback bind is preserved and Origin is validated. Both prerequisites are weakened (VULN-002/VULN-003).
- **`web_fetch` SSRF guard** (`internal/tools/web.go:24-48`) is solid: DNS-rebind safe at dial time, rejects loopback/private/link-local at the dial layer, 5-redirect cap, 20s default timeout.
- **`run_command` block-list** is comprehensive for direct invocation (shell interpreters, eval flags, destructive args, sudo/privilege escalators) — gap is only the indirection wrappers in VULN-054.
- **bbolt store** correctly uses `0o600` — only `config.yaml` (VULN-014) is the outlier.
- **Go 1.25 stdlib HTTP** correctly rejects CRLF in headers; SSE/WS payloads are JSON-encoded; no `r.Host` echoing — header-injection class is empty.
- **Dependencies:** `gopkg.in/yaml.v3` v3.0.1 is the *fixed* version of CVE-2022-28948; `golang.org/x/net` v0.53.0 postdates known HTTP/2 CVE waves; no `dgrijalva/jwt-go` or other deprecated libraries. (Verify periodically with `govulncheck ./...`.)

### Notable cross-skill merges

| VULN | Skills merged |
|------|---------------|
| VULN-001 | 8 (sc-authz, sc-lang-go, sc-api-security, sc-business-logic, sc-auth, sc-file-upload, sc-csrf, sc-privilege-escalation) |
| VULN-002 | 6 (sc-websocket, sc-lang-go, sc-auth, sc-api-security, sc-csrf, sc-header-injection) |
| VULN-009 | 5 (sc-csrf, sc-authz, sc-path-traversal, sc-file-upload, sc-lang-go) |
| VULN-018 | 4 (sc-path-traversal, sc-file-upload × 2, sc-mass-assignment) |
| VULN-026 | 4 (sc-auth, sc-lang-go, sc-api-security, sc-cors) |
| VULN-073 | 28 (sc-ci-cd × 14 + sc-docker × 14) |

---

## 8. Remediation Roadmap

### Phase 1: Immediate (1-3 days) — break the chains

The four Critical findings + the WebSocket origin / `source="user"` chain together form the headline drive-by-RCE pathway. Fix all five before anything else.

| # | Finding | Effort | Impact |
|---|---------|--------|--------|
| 1 | VULN-001 — Tag HTTP/WS/MCP-initiated calls with `"web"`/`"ws"`/`"mcp"` source; subject to RequireApproval like `"agent"` | Medium | Critical |
| 2 | VULN-002 — Replace `CheckOrigin: return true` with loopback-allowlist | Low | Critical |
| 3 | VULN-003 — Add Host header allowlist middleware (DNS rebinding mitigation) | Low | High |
| 4 | VULN-004 — Route `patch_validation.validation_command` through `RunCommandTool` (or remove); reclassify to `RiskCommand`; `EnsureWithinRoot` on `project_root` | Medium | Critical |
| 5 | VULN-006 — `EnsureWithinRoot(projectRoot, toFile)` + read-before-mutation gate on `symbol_move` destination | Low | Critical |
| 6 | VULN-007 — Default `auth=token` with auto-generated token at `~/.dfmc/web.token` (mode 0600), printed to stderr | Medium | High |
| 7 | VULN-008 — Ship sane default `RequireApproval` list for high-risk tools | Low | High |

### Phase 2: Short-term (1-2 weeks) — Phase-7 tool retrofit + workspace lifecycle

| # | Finding | Effort | Impact |
|---|---------|--------|--------|
| 8 | VULN-005 — `--` separator + leading-`-` refusal + `EnsureWithinRoot` on `target`/`cpuprofile`/`memprofile` in `benchmark` | Low | High |
| 9 | VULN-009 — Route `/api/v1/workspace/apply` through `engine.CallTool("apply_patch")`; reorder so `--dry-run` enumerates before apply; replace `HasPrefix` with `filepath.Rel` containment | Medium | High |
| 10 | VULN-015 — `EnsureWithinRoot` on `symbol_rename.file` | Low | High |
| 11 | VULN-016 — `EnsureWithinRoot` on `semantic_search.file` | Low | Medium |
| 12 | VULN-017 — `EnsureWithinRoot` on `disk_usage.path` | Low | Medium |
| 13 | VULN-018 — Pin web magicdoc writes to `.dfmc/magic/`; CLI refuses absolute paths; both route through read-before-mutation | Low | Medium |
| 14 | VULN-024 — Wrap taskstore load+mutate+save in single `db.Update` transaction | Low | High |
| 15 | VULN-025 — Per-absPath `sync.Mutex` for write_file/apply_patch | Medium | High |
| 16 | VULN-019 — `SetReadLimit` + per-conn rate.Limiter + ping/pong heartbeat | Low | High |
| 17 | VULN-032 — Drop `state` from PATCH; explicit struct decoder; transition endpoint with state-machine validator | Medium | High |
| 18 | VULN-033 — Drop `id` from `TaskCreateRequest`; allocate via `taskstore.NewTaskID()` | Low | High |

### Phase 3: Medium-term (1-2 months) — credentials, CSRF, file-API, supply chain

| # | Finding | Effort | Impact |
|---|---------|--------|--------|
| 19 | VULN-014 — `Config.Save` mode 0600 + parent dir 0700 | Low | High |
| 20 | VULN-013 — Promote `looksLikeSecretFile` to shared package; redact in web file API | Medium | High |
| 21 | VULN-027 / VULN-029 / VULN-030 / VULN-070 — Token rotation endpoint; in-memory storage with re-prompt; drop hash-bootstrap; password-input modal | Medium | High |
| 22 | VULN-010 — XFF only honoured behind trusted-proxy list | Low | High |
| 23 | VULN-011 / VULN-012 — Scrub `*_API_KEY`/`*_TOKEN`/`*_SECRET` from MCP and hook subprocess env; opt-in passthrough list | Low | High |
| 24 | VULN-037 — Redact `tool:call.params` and `tool:result.output_preview/error` before publish | Medium | Medium |
| 25 | VULN-038 — Strip C0/C1 control bytes at engine publish boundary | Low | High |
| 26 | VULN-073 — Full CI/CD + Docker hardening (SHA-pin actions, fix release-asset action, cosign signing, `.dockerignore`, non-root USER, distroless, HEALTHCHECK) | High | High |
| 27 | VULN-031 — Stamp drive/task/conversation records with originating-token fingerprint | Medium | Medium |
| 28 | VULN-035 — Enforce `allowed_tools` at `executeToolWithLifecycle` for `source="subagent"`, OR rename to `preferred_tools` | Low | Medium |
| 29 | VULN-036 — Wire `hooks.CheckConfigPermissions` into startup + doctor | Low | Medium |
| 30 | VULN-042 — Cap every list endpoint at 200-500 default 100 | Low | High |

### Phase 4: Hardening (ongoing) — defense in depth, error-message scrubbing, observability

| # | Finding(s) | Effort | Impact |
|---|------------|--------|--------|
| 31 | VULN-020 / VULN-021 / VULN-043 / VULN-044 — WS heartbeat, per-IP active-conn cap, drive-run cap, SSE wall-clock cap | Medium | Medium |
| 32 | VULN-040 / VULN-041 — Centralized error sanitizer (sentinel mapping + `"internal error"` fallback); strip stack from LLM-visible error | Medium | Low |
| 33 | VULN-022 / VULN-023 / VULN-046 / VULN-047 / VULN-048 — WS race fixes, request-context propagation, drop-counter alignment, hook panic guard | Medium | Low |
| 34 | VULN-050 — Reject non-`application/json` POST/PATCH with 415 | Low | Medium |
| 35 | VULN-053 — `gh_runner` per-subcommand flag allowlist (mirror `rejectGitFlagInjection` shape) | Low | Medium |
| 36 | VULN-054 — Add `env`/`nice`/`nohup`/`xargs`/`time`/`chroot` to indirection block-list | Low | Medium |
| 37 | VULN-055 — `git_worktree_remove` constrain path to project root or `git worktree list` output | Low | Medium |
| 38 | VULN-057 / VULN-058 — Apply `safeTransport` to provider HTTP clients when `base_url` is non-default; same for update/sync-models clients | Medium | Low |
| 39 | VULN-059 / VULN-060 / VULN-061 — Drive `RunPrepared` reset; integrity chain on resumed Done state; per-attempt step-budget cap | Medium | Medium |
| 40 | VULN-062 / VULN-063 / VULN-065 / VULN-066 / VULN-067 — Concurrency hardening (in-flight-ask flag, branch-name validation, atomic register, drainage close, lock-timeout PID marker) | Medium | Low |
| 41 | VULN-068 / VULN-069 — Closed enums for `Source`; bound `Vars` count/length; verify `EnsureWithinRoot` on `AnalyzeRequest.Path` | Low | Low |
| 42 | VULN-049 / VULN-064 — Stderr message on bind rewrite; document EventBus lock order | Low | Info |

---

## 9. Methodology

This assessment was performed using **security-check v1.0.0**, an AI-powered static analysis pipeline that uses large language model reasoning to detect security vulnerabilities. The pipeline ran in four phases:

### Pipeline Phases

1. **Phase 1 — Reconnaissance.** Automated codebase mapping detected the Go-only stack (Go 1.25, no JS/TS/Python/Rust/Java/C# first-party source), the three UI surfaces (CLI, TUI, embedded HTTP+WS), the MCP server/client architecture, the Engine-as-hub design, and the trust boundaries documented in `architecture.md`. Output drives Phase-2 skill activation.
2. **Phase 2 — Vulnerability hunting.** 38 specialized skills ran across 30+ vulnerability categories: 24 from a prior pass plus 14 in this round. Activated set covered OWASP Top 10 (A01-A10), CWE Top 25, language-specific Go deep scan (`sc-lang-go`), supply-chain skills (`sc-ci-cd`, `sc-docker`, dependency audit), web-app primitives (`sc-csrf`, `sc-cors`, `sc-websocket`, `sc-auth`, `sc-session`, `sc-rate-limiting`), code-execution primitives (`sc-rce`, `sc-cmdi`, `sc-path-traversal`, `sc-ssrf`), data-exposure / mass-assignment / business-logic, and 7 single-skill no-finding-by-design domains (SQLi, NoSQLi, JWT, LDAP, XXE, GraphQL, deserialization). Skills that returned "no surface" (e.g. `sc-sqli`, `sc-jwt`, `sc-graphql`) were retained as positive-coverage observations rather than dropped, so the absence is provable.
3. **Phase 3 — Verification.** `sc-verifier` collapsed **218 raw findings** to **73 verified findings** through cross-skill duplicate detection. Notable merges: VULN-001 (8 skills landed on the `source="user"` bypass), VULN-002 (6 skills on the WebSocket origin), VULN-073 (28 supply-chain findings clustered as one cumulative entry to keep focus on runtime application risks). 145 findings were either merged or recalibrated to Info per the SKILL.md severity-recalculation rules — none were eliminated outright as fully false-positive.
4. **Phase 4 — Reporting.** This document. CVSS v3.1 mapping per the SKILL.md severity tables, calibrated to DFMC's local-binary threat model (see Section 1 — "Critical" here means "any local browser tab" not "internet-facing unauthenticated", with the exception of the `--insecure --host 0.0.0.0` configuration covered in VULN-052).

### Risk Score Calculation

Per the SKILL.md formula:
- 4 Critical × 2.0 = 8.0
- 19 High × 1.0 = 19.0
- 22 Medium × 0.3 = 6.6
- 16 Low × 0.1 = 1.6
- Modifier: `auth=none` default + empty `RequireApproval` default (effectively "no authentication / no authorization controls" in default config) → +1.0
- Modifier: convergence of approval-gate bypass + accept-all WebSocket origin + arbitrary-binary tools (RCE primitive class) → no formal score modifier in SKILL.md, but the chain is what justifies a 10/10 cap.

Raw sum = 8.0 + 19.0 + 6.6 + 1.6 + 1.0 = **36.2**, clamped to the 1-10 range = **10/10**. The clamp is severe — this is the highest possible rating. Several mitigating factors prevent the *practical* exploitation surface from being internet-class: (a) the bind-host normalisation forces 127.0.0.1 when `auth=none`, (b) `dfmc serve` refuses non-loopback `auth=none` without `--insecure`, (c) the workbench is the only DFMC-published web origin and CSP blocks inline-script. So the practical attack scenario is "operator visits hostile webpage" rather than "anyone on the internet". This is still Critical, but readers should not panic-read this as "DFMC is shipping a 10/10 RCE to the public internet".

### Limitations

- **Static analysis only.** No runtime testing, no dynamic analysis, no fuzzing, no actual penetration testing. Skills inferred behaviour from source code + LLM reasoning.
- **AI-based reasoning may miss vulnerabilities requiring deep domain knowledge** — cryptographic protocol bugs, business-logic invariants tied to the agent loop's model semantics, etc.
- **Confidence scores are estimates, not guarantees.** A 95/100 confidence still permits ~5% chance of false positive; readers should validate the cited `file:line` positions before remediation.
- **Custom business logic flaws may require manual review** — the `executeToolWithLifecycle` invariant ("single mandated entry") was honored across the codebase except where explicitly exempted (Drive MCP handlers per CLAUDE.md), but a future reviewer should confirm by greping `tools.Engine.Execute` direct call sites.
- **Dependency CVE attribution is best-effort.** The dependency-audit was based on training-data familiarity with public Go advisories; a `govulncheck ./...` invocation with network access is the only authoritative source for CVE attribution at exact pinned versions.
- **Phase-7 tool surface was added in the most recent commits.** Several findings (VULN-004/005/006/015/016/017) cluster on this surface specifically because the older-tool guards (`EnsureWithinRoot`, `EnsureReadBeforeMutation`, `RunCommandTool` block-list chain) were not retrofitted. A retrofit pass per CLAUDE.md "Things that bite" item *("New tool-surface entry points MUST call `executeToolWithLifecycle` …")* would close most of this cluster.

---

## 10. Disclaimer

This security assessment was performed using automated AI-powered static analysis. It does not constitute a comprehensive penetration test or security audit. The findings represent potential vulnerabilities identified through code pattern analysis and large language model reasoning. False positives and false negatives are possible despite the verification phase.

This report should be used as a **starting point** for security remediation, not as a definitive statement of the application's security posture. A professional security audit by qualified security engineers — including dynamic testing, fuzzing, and threat-model review by humans familiar with the agent-loop trust model — is recommended before any deployment that exposes DFMC to networks, multi-user systems, or sensitive data.

The risk score of **10/10** in this report reflects the convergence of approval-gate bypass + WebSocket origin policy + Phase-7 tool surface in the *default configuration*. Operators running DFMC with `auth=token`, a non-empty `RequireApproval` list, and the Phase-1 mitigations applied will see the practical risk drop substantially. The score is tied to the shipped defaults, not the maximum-hardened deployment.

Generated by **security-check** — github.com/ersinkoc/security-check
