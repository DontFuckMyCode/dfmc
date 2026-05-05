# DFMC Security Architecture Map

**Date**: 2026-05-05
**Module**: `github.com/dontfuckmycode/dfmc`
**Go version**: 1.25 (toolchain go1.26.2)
**Scope**: Phase 1 RECON -- security-relevant architecture mapping

---

## 1. Network-Facing Surfaces

### 1.1 HTTP/SSE Server (`dfmc serve` -- port 7777 default)

**Entry point**: `ui/web/server.go` -> `Server.Start()` -> `NewHTTPServer()`

| Endpoint | Method | Purpose | Sensitivity |
|----------|--------|---------|-------------|
| `/healthz` | GET | Liveness probe | Low -- bypasses bearer token |
| `/api/v1/status` | GET | Engine state | Medium -- exposes config metadata |
| `/api/v1/chat` | POST | Streaming LLM conversation (SSE) | High -- drives full agent loop |
| `/api/v1/ask` | POST | Single-turn LLM completion | High -- drives provider calls |
| `/api/v1/tools/{name}` | POST | Direct tool execution | **Critical** -- executes shell, writes files |
| `/api/v1/skills/{name}` | POST | Skill dispatch | High |
| `/api/v1/analyze` | POST | Code analysis with LLM | Medium |
| `/api/v1/workspace/apply` | POST | Apply patches to filesystem | **Critical** |
| `/api/v1/drive` | POST | Start autonomous plan/execute loop | **Critical** |
| `/api/v1/drive/{id}/resume` | POST | Resume autonomous loop | **Critical** |
| `/api/v1/files/{path...}` | GET | Read arbitrary project files | High |
| `/api/v1/config` | GET | Expose running configuration | Medium |
| `/ws` | GET | SSE event stream | Medium |
| `/api/v1/ws` | GET | Bidirectional WebSocket (JSON-RPC) | **Critical** |

**Middleware stack** (outer to inner):
1. `bearerTokenMiddleware` (only when `auth=token`)
2. `rateLimitMiddleware` (30 rps/IP, burst 60)
3. `securityHeaders` (CSP, X-Content-Type-Options, X-Frame-Options)
4. `hostAllowlistMiddleware` (Host header check, default: `127.0.0.1`, `localhost`)
5. `limitRequestBodySize` (4 MiB cap)
6. `contentTypeEnforcementMiddleware` (rejects non-JSON on POST/PATCH/PUT)

**Bind safety**: When `auth=none`, the server forces loopback bind (`127.0.0.1`) regardless of `--host` flag. Non-loopback bind requires `--auth=token`.

### 1.2 WebSocket (`/api/v1/ws`)

**Location**: `ui/web/server_ws.go`

- **Upgrade gating**: origin check via `checkWebSocketOrigin`, per-IP connection cap (8), global cap (64)
- **Per-connection rate limit**: 5 rps, burst 10
- **Frame size cap**: 64 KiB inbound (`wsReadLimit`)
- **Ping/pong** for half-open detection (30s interval, 60s read deadline)
- **Methods exposed**: `chat`, `ask`, `tool`, `drive.start/stop/status`, `events.subscribe/unsubscribe`, `ping`
- **Tool dispatch** uses `CallToolFromSource(ctx, name, params, engine.SourceWS)` -- goes through approval gate

### 1.3 MCP Server (stdio JSON-RPC 2.0)

**Location**: `internal/mcp/server.go`

- Line-delimited JSON-RPC over stdin/stdout
- Frame cap: 16 MiB (`MaxFrameBytes`)
- Exposes `tools/list` and `tools/call` after `initialize` handshake
- **No authentication** -- relies on the parent process's stdio isolation
- Tool calls routed through the bridge (direct `tools.Engine` or engine-level dispatch depending on host)

### 1.4 MCP Client (subprocess spawning)

**Location**: `internal/mcp/client.go`

- Spawns external MCP server processes (`exec.Command`)
- **Env scrubbing**: `security.ScrubEnv()` removes `*_API_KEY`, `*_TOKEN`, `*_SECRET`, etc. from inherited env
- Only operator-supplied `env:` map entries are forwarded to subprocess
- Client-side timeout: context-based

### 1.5 Remote (gRPC + WS)

**Location**: `ui/cli/cli_remote.go`
- gRPC port 7778, WS port 7779 (configurable)
- `auth: "token"` by default
- Disabled by default (`remote.enabled: false`)

---

## 2. Trust Boundaries

### 2.1 User Input -> Engine

```
User (TUI/CLI) -----> engine.Ask() / engine.CallTool()
                      Source = "user" -> bypasses approval gate
```

- TUI and CLI calls are tagged `SourceUser` and skip the approval gate entirely
- The user is considered the trust root

### 2.2 Network -> Engine (Web/WS/MCP)

```
HTTP/WS/MCP -----> engine.CallToolFromSource(ctx, name, params, SourceWeb|SourceWS|SourceMCP)
                   |
                   v
              requiresApproval() [checks RequireApprovalNetwork list]
                   |
                   v
              Approver.RequestApproval() [deny-by-default when no approver]
```

- Default `RequireApprovalNetwork: ["*"]` means ALL tools require approval from network sources
- The `webApprover` checks `DFMC_APPROVE` / `DFMC_APPROVE_DESTRUCTIVE` env vars
- **Without explicit env var opt-in, all gated tools from network sources are denied**

### 2.3 LLM Output -> Tool Execution (Agent Loop)

```
LLM Response (tool_call) --> agent_loop_native.go
                             |
                             v
                        executeToolWithLifecycle()
                             |
                             v
                     Source = "agent" or "subagent"
                             |
                             v
                     requiresApproval() [checks RequireApproval list]
                             |
                             v
                     Approver.RequestApproval() [TUI prompts user]
```

- LLM-initiated tool calls are tagged `SourceAgent`/`SourceSubagent`
- Subject to `RequireApproval` list (empty by default -- **no gate by default for agent calls**)
- Protected by: panic guard, per-tool timeout, read-before-mutate gate
- Meta-tool boundary prevents recursive meta-dispatch

### 2.4 External API Calls (Providers)

```
Engine --> Provider Router --> Anthropic/OpenAI/Google/DeepSeek/etc.
                              |
                              v
                        API Key in request header (HTTPS)
```

- API keys stored in config (`providers.profiles.*.api_key`) or env vars
- Transmitted over HTTPS to provider endpoints
- No credential rotation mechanism
- Provider HTTP client has configurable timeout

---

## 3. Authentication / Authorization

| Surface | Mechanism | Default |
|---------|-----------|---------|
| HTTP API | Bearer token (DFMC_WEB_TOKEN env) | **None** (auth=none, loopback only) |
| WebSocket | Same bearer token check + origin validation | Same as HTTP |
| MCP Server | None (stdio isolation) | N/A |
| MCP Client | Env scrub (no secrets leaked to child) | Active |
| Remote (gRPC/WS) | Token-based | Disabled by default |
| Tool approval (network) | Two-knob env gate | **Deny all** |
| Tool approval (agent) | Configurable list | **Allow all** (empty list) |
| File API | Path traversal guard + secret file redaction | Active |

**Key finding**: The agent loop's tool execution has NO approval gate by default (`RequireApproval` is empty). An LLM that receives a malicious prompt injection can freely execute `run_command`, `write_file`, `edit_file` etc. without user confirmation unless the operator explicitly configures `require_approval`.

---

## 4. Shell / Command Execution Paths

### 4.1 `run_command` Tool

**Location**: `internal/tools/command.go`

| Control | Mechanism |
|---------|-----------|
| Kill switch | `security.sandbox.allow_shell: false` disables entirely |
| Shell interpreters | Blocked: bash, sh, cmd, powershell, etc. (`isBlockedShellInterpreter`) |
| Shell metacharacters | Detected and refused (`detectShellMetacharacter`) |
| Script runner eval | Blocked: `node -e`, `python -c`, etc. (`hasScriptRunnerWithEvalFlag`) |
| Binary blocklist | rm, mkfs, sudo, shutdown, etc. (`isBlockedBinary`) |
| Arg sequence blocklist | `git reset --hard`, `git push --force`, etc. |
| User-configured blocklist | Substring match on joined command+args |
| Output cap | 4 MiB per stream (stdout/stderr) |
| Timeout | Configurable (default 30s, capped by config) |
| Working directory | Constrained to project root via `EnsureWithinRoot` |

**Gap**: `run_command` does NOT block arbitrary binaries -- only specific known-dangerous ones. An attacker could invoke `curl`, `wget`, `nc`, or custom binaries to exfiltrate data.

### 4.2 Git Tools

**Location**: `internal/tools/git.go`, `internal/tools/git_runner.go`

- All git commands go through `runGit()` -- no shell, direct `exec.Command`
- `blockedGitArg()` refuses: `--no-verify`, `--amend`, `--force`, `--exec=`, `--upload-pack=`
- `rejectGitFlagInjection()` refuses any user-supplied ref/path starting with `-` (CVE-2018-17456 class)
- Bounded output capture (4 MiB)
- Per-call timeout (default 30s, max 120s)

### 4.3 Lifecycle Hooks

**Location**: `internal/hooks/hooks.go`

- Shell-based command execution triggered by lifecycle events
- Env scrubbed via `security.ScrubEnv()` before subprocess spawn
- Hard timeout per hook (default 30s)
- **Project hooks disabled by default** (`allow_project: false`)
- Project config permission check (`isProjectConfigSecure`) blocks group/world-writable configs
- Hooks are best-effort -- failures never block tool calls

---

## 5. File System Access Patterns

### 5.1 Path Traversal Protection

**Location**: `internal/tools/engine.go` -> `EnsureWithinRoot()`

- Two-layer defense: lexical (`filepath.Abs` + `filepath.Rel`) and symbolic (`filepath.EvalSymlinks`)
- Resolves symlinks on both root and target
- For non-existent targets (new files), resolves deepest existing ancestor
- **All file-mutating tools route through this**

### 5.2 Read-Before-Mutate Gate

| Tool | Mode | Enforcement |
|------|------|-------------|
| `edit_file` | Lenient (requires prior snapshot, tolerates hash drift) | Engine gate |
| `write_file` | Strict (requires prior snapshot + hash equality) | Engine gate |
| `apply_patch` | Strict per target file | Tool-level per-file check |

- Prevents blind writes/edits to files the model hasn't read
- Per-path mutex (`pathLocks`) serialises concurrent mutations to the same file

### 5.3 Web File API

**Location**: `ui/web/server_files.go`

- `resolvePathWithinRoot()` -- same symlink-aware containment check
- `security.LooksLikeSecretFile()` redacts `.env`, `id_rsa`, `credentials.json`, etc.
- File listing skips `.git`, `.dfmc`, `node_modules`, `vendor` directories

### 5.4 Sensitive Directories

- `.dfmc/` -- project state (bbolt DB, knowledge, conventions, config)
- `~/.dfmc/` -- global config, data dir, plugins
- `~/.dfmc/data/dfmc.db` -- bbolt database (single-writer lock)

---

## 6. Secret Handling

### 6.1 API Key Storage

| Location | Protection |
|----------|------------|
| `~/.dfmc/config.yaml` | File permissions (written `0o600`) |
| `<project>/.dfmc/config.yaml` | Permission check on load (`isProjectConfigSecure`) |
| `.env` file (project root) | Loaded at startup, process env takes priority |
| Process environment | Standard env var protection |

### 6.2 Secret Flow

```
config.yaml / .env / $ENV  -->  Config.Providers.Profiles[*].APIKey
                                       |
                                       v
                                Provider HTTP request (Authorization header)
                                       |
                                       v
                                External API (Anthropic/OpenAI/etc.)
```

### 6.3 Secret Scrubbing

- `security.ScrubEnv()`: Removes `*_API_KEY`, `*_TOKEN`, `*_SECRET`, AWS keys, etc. from subprocess environments
- Applied to: MCP client subprocesses, lifecycle hooks
- `security.LooksLikeSecretFile()`: Blocks web API from serving credential files

### 6.4 Bearer Token (Web API)

- Stored in `DFMC_WEB_TOKEN` env var
- Compared with `crypto/subtle.ConstantTimeCompare` (timing-safe)
- Only active when `auth=token` is configured
- `/healthz` always bypasses auth

---

## 7. Third-Party Dependencies (Supply Chain)

### Direct Dependencies

| Package | Risk Category | Purpose |
|---------|---------------|---------|
| `github.com/gorilla/websocket v1.5.3` | Network | WebSocket upgrade/framing |
| `go.etcd.io/bbolt v1.4.3` | Storage | Embedded key-value database |
| `github.com/tree-sitter/go-tree-sitter v0.25.0` | Native (CGO) | AST parsing |
| `github.com/tree-sitter/tree-sitter-go v0.25.0` | Native (CGO) | Go grammar |
| `github.com/tree-sitter/tree-sitter-javascript v0.25.0` | Native (CGO) | JS grammar |
| `github.com/tree-sitter/tree-sitter-python v0.25.0` | Native (CGO) | Python grammar |
| `github.com/tree-sitter/tree-sitter-typescript v0.23.2` | Native (CGO) | TS grammar |
| `github.com/tetratelabs/wazero v1.11.0` | Runtime | WASM runtime (plugin execution) |
| `github.com/charmbracelet/bubbletea v1.3.10` | UI | TUI framework |
| `github.com/charmbracelet/lipgloss v1.1.0` | UI | Terminal styling |
| `golang.org/x/net v0.53.0` | Network | HTML tokenizer, supplementary net |
| `golang.org/x/time v0.15.0` | Rate limiting | Token-bucket rate limiter |
| `gopkg.in/yaml.v3 v3.0.1` | Config | YAML parsing |

### Supply Chain Observations

- **Tree-sitter grammars** are CGO-bound native code -- memory safety relies on C implementations
- **wazero** (WASM runtime) is used for plugin execution -- sandbox boundary
- **gorilla/websocket** is maintained but has historically had DoS vectors
- **bbolt** is append-only B+ tree -- no encryption at rest
- No vendoring detected (uses module proxy)
- No `go.sum` integrity check automation visible in CI config

---

## 8. SSRF Protection

**Location**: `internal/tools/web.go`

- Custom `http.Transport` with `DialContext` that resolves DNS and checks IP at connect time
- Blocks: loopback, private (RFC1918), link-local unicast, link-local multicast
- Applied to `web_fetch` and `web_search` tools
- Redirect limit: 5 hops
- DNS rebinding mitigation: IP checked after resolution, not before

---

## 9. Identified Trust Boundary Weaknesses

| ID | Boundary | Finding |
|----|----------|---------|
| TB-1 | Agent -> Tool | No approval gate by default for agent-initiated tools |
| TB-2 | LLM -> run_command | Arbitrary binary execution (only known-dangerous blocked) |
| TB-3 | MCP Server | No authentication on stdio -- relies on process isolation |
| TB-4 | Config files | API keys in plaintext YAML -- no encryption at rest |
| TB-5 | Web auth=none | Loopback bind is the only protection for unauthenticated mode |
| TB-6 | bbolt store | No encryption at rest for conversations/memory/tasks |
| TB-7 | Provider routing | API keys transmitted in-memory through multiple layers |
| TB-8 | Hook execution | Shell=true hooks can execute arbitrary commands (global config gated) |

---

## 10. Input Validation Summary

| Input Source | Validation | Location |
|--------------|------------|----------|
| HTTP body | 4 MiB size cap, JSON content-type enforcement | `server.go` middleware |
| HTTP Host header | Allowlist (default: localhost) | `server_origin.go` |
| WebSocket Origin | Allowlist (default: localhost) | `server.go` `checkWebSocketOrigin` |
| WebSocket frames | 64 KiB read limit | `server_ws.go` |
| MCP frames | 16 MiB cap | `mcp/server.go` |
| Tool file paths | `EnsureWithinRoot` (lexical + symlink resolution) | `tools/engine.go` |
| Tool params | `normalizeToolParams` aliases, `missingParamError` self-teaching | `tools/engine.go`, `tools/builtin.go` |
| Git arguments | `blockedGitArg`, `rejectGitFlagInjection` | `tools/git_runner.go` |
| Shell commands | Binary blocklist, arg sequence blocklist, shell metachar detection | `tools/command.go` |
| URLs (web_fetch) | Scheme check (http/https only), SSRF guard at dial time | `tools/web.go` |
| Config files | 1 MiB size cap, YAML parse, permission check | `config/config.go` |
| .env files | Placeholder rejection, no shell expansion | `config/config_env.go` |

---

## 11. Component Diagram (Security View)

```
                                 INTERNET
                                    |
                    +---------------+---------------+
                    |                               |
            [LLM Providers]                  [Web Search]
            (API keys in headers)           (SSRF-guarded)
                    |                               |
                    v                               v
+-------------------------------------------------------------------+
|                        ENGINE (engine.go)                          |
|                                                                   |
|  +------------------+    +-------------------+                    |
|  | Provider Router  |    | Tools Engine      |                    |
|  | (router.go)      |    | (tools/engine.go) |                    |
|  | - throttle retry |    | - read gate       |                    |
|  | - circuit breaker|    | - path lock       |                    |
|  | - model chain    |    | - timeout         |                    |
|  +------------------+    | - panic guard     |                    |
|                          +-------------------+                    |
|                                  |                                |
|  +------------------+     +------+------+                         |
|  | Approval Gate    |     |             |                         |
|  | (approver.go)    |     v             v                         |
|  | - source-based   | [File Tools]  [Shell Tools]  [Git Tools]   |
|  | - two-knob web   | (EnsureWithin (blocked cmds  (flag inject  |
|  | - deny-by-default|  Root)        blocklists)     guard)        |
|  +------------------+                                             |
+-------------------------------------------------------------------+
         ^         ^         ^              ^
         |         |         |              |
    [TUI/CLI]   [HTTP]    [WebSocket]    [MCP stdio]
    Source=user  Source=web Source=ws     Source=mcp
    (bypasses    (approval  (approval    (approval
     gate)       gate)      gate)        gate)
```

---

## 12. Key Security Mechanisms (Prior VULN Fixes Referenced in Code)

| VULN ID | Fix Description | Location |
|---------|----------------|----------|
| VULN-010 | XFF rate-limit bypass -- rightmost IP, trusted-proxy gate | `server.go` `clientIPKey` |
| VULN-011 | MCP subprocess env credential leak -- ScrubEnv | `mcp/client.go` |
| VULN-013 | Web file API serving secret files -- LooksLikeSecretFile | `server_files.go` |
| VULN-019-023 | WebSocket DoS vectors -- frame limit, conn cap, rate limit, ctx cancel | `server_ws.go` |
| VULN-036 | Config permission warning for co-tenant injection | `cmd/dfmc/main.go` |
| VULN-048 | Hook panic containment | `hooks/hooks.go` |
| VULN-049 | Loopback bind enforcement for auth=none | `server.go` |
| VULN-050 | Content-Type enforcement (CSRF via form POST) | `server.go` |

---

## 13. Areas Requiring Deeper Audit (Phase 2 Targets)

1. **Agent loop prompt injection**: LLM-controlled tool dispatch with no default gate
2. **run_command scope**: Only named binaries are blocked; arbitrary executables can be invoked
3. **MCP bridge trust**: External MCP servers can call any registered tool via the bridge
4. **Plugin execution** (wazero): Need to assess sandbox escape vectors
5. **bbolt data at rest**: No encryption; conversation history, memory, API call logs accessible
6. **Config merge precedence**: Project config can override security settings (gated by `isProjectConfigSecure` but Windows ACL check is best-effort)
7. **Provider HTTP clients**: No certificate pinning; susceptible to MITM on non-standard base URLs
8. **Drive autonomous loop**: Planner LLM call directly drives TODO creation with no human review
9. **Subagent allowlist** (`internal/engine/subagent_allowlist.go`): Scope TBD
10. **Web API workspace/apply**: Accepts raw patches -- diff injection surface
