# DFMC Security Architecture

## 1. Technology Stack

### Go Version
- **Go 1.25.0** (from `go.mod`)

### Key Dependencies
| Dependency | Version | Purpose |
|------------|---------|---------|
| `gorilla/websocket` | v1.5.3 | WebSocket support for web UI real-time communication |
| `go.etcd.io/bbolt` | v1.4.3 | Embedded key-value store for conversation/memory persistence |
| `github.com/charmbracelet/bubbletea` | v1.3.10 | TUI framework |
| `github.com/tree-sitter/go-tree-sitter` | v0.25.0 | AST parsing for code analysis |
| `golang.org/x/time` | v0.15.0 | Rate limiting (token bucket per IP) |
| `github.com/tetratelabs/wazero` | v1.11.0 | WebAssembly runtime (for benchmark sandboxing) |
| `gopkg.in/yaml.v3` | v3.0.1 | Config file parsing |

### Web Framework
- **Standard library `net/http`** — NOT a web framework. The `ui/web/server.go` `Server` struct wraps `http.ServeMux` directly with custom middleware layers.
- HTTP server timeouts are hardened: `ReadHeaderTimeout=5s`, `ReadTimeout=30s`, `WriteTimeout=2min`, `IdleTimeout=2min`, `MaxHeaderBytes=1MiB`.

### IPC/RPC Mechanisms
- **MCP (Model Context Protocol)**: `internal/mcp/` — external tool server bridge. Loads MCP clients from config, exposes remote tools as native tools via `mcpToolAdapter`.
- **WebSocket**: `gorilla/websocket` — real-time SSE stream for web workbench.
- **exec.Command** (no shell): Git tools and hook subprocesses use `exec.CommandContext` with argv-only invocation to prevent shell injection.

---

## 2. Application Type

DFMC is a **CLI tool with three optional surfaces**:

1. **CLI** (`dfmc ask`, `dfmc chat`, `dfmc tool run`, etc.) — `ui/cli/cli.go`
2. **TUI** (`dfmc tui`) — `ui/tui/` — interactive terminal UI via bubbletea
3. **Web Server** (`dfmc serve`) — `ui/web/server.go` — embedded HTTP server with REST API + WebSocket

Entry point: `cmd/dfmc/main.go` → `cli.Run(ctx, eng, os.Args[1:], version)` → subcommand dispatcher.

---

## 3. Entry Points

### CLI Commands (`ui/cli/cli.go`)
```
ask, chat, tui, analyze, map, tool, scan, memory, conversation,
serve, config, context, prompt, magicdoc, plugin, skill, remote,
drive, provider, model, providers, doctor, hooks, approvals, mcp, update, completion, man
```
Plus skill shortcuts: `review, explain, refactor, debug, test, doc, generate, audit, onboard`
Typo guard: warns on misspellings before routing to `ask`.

### Web Routes (`ui/web/server.go`)
```
GET  /                                  → workbench HTML
GET  /healthz                           → health check
GET  /ws                                → WebSocket upgrade
POST /api/v1/chat                       → streaming chat
POST /api/v1/ask                        → single-turn completion
GET  /api/v1/status                     → engine status
POST /api/v1/tools/{name}               → tool execution
GET  /api/v1/tools                      → tool listing
GET  /api/v1/providers                  → provider listing
GET  /api/v1/conversation               → active conversation
POST /api/v1/drive/{id}/resume         → resume a drive session
...and 40+ more routes for context, prompts, memory, workspace, tasks
```

### TUI Handlers (`ui/tui/`)
- `ui/tui/approver.go` — Modal approval popup wired to engine's `Approver` interface
- `ui/tui/engine_events_loop.go` — Agent loop event subscriber for streaming updates
- `ui/tui/slash_picker_modal.go` — Slash command palette

### MCP Server (`internal/mcp/`)
- `bridge.go` — Exposes MCP server tools as native `tools.Tool` implementations
- `client.go` / `server.go` — MCP protocol implementation

---

## 4. Data Flow

### User Input to LLM
```
CLI/TUI/Web Input
    ↓
engine.Ask() / engine.Chat()
    ↓
intent.Router.Evaluate()   [state-aware request normalizer, internal/intent/]
    ↓
provider.Router.Complete() / provider.Router.Stream()   [internal/provider/router.go]
    ↓
LLM Provider (Anthropic / OpenAI / Google / OpenAI-compatible)
    ↓
Tool Calls → engine.executeToolWithLifecycle()
```

### Tool Execution Pipeline (`engine_tools.go:executeToolWithLifecycle`)
```
executeToolWithLifecycle(ctx, name, params, source)
    ↓
[1] checkSubagentAllowlist()     — subagent tool allowlist gate
    ↓
[2] requiresApproval(source)     — config-gated tool check
    ↓ [if gated + non-user source]
    askToolApproval() → Approver.RequestApproval()
    ↓ [denied] → return error, recordDenial, publish tool:denied event
    ↓
[3] Hooks: EventPreTool          — fire pre_tool hooks
    ↓
[4] executeToolWithPanicGuard()  — defer/recover wrapping Tools.Execute()
    ↓ [panic] → publish tool:panicked, return error with truncated stack
    ↓
[5] invalidateContextForTool()  — mark file as modified/read
    ↓
[6] Hooks: EventPostTool        — fire post_tool hooks
    ↓
return Result
```

### Intent Layer (`internal/intent/router.go`)
- Small LLM classifier before each Ask
- Rewrites vague messages ("fix it", "devam et") into self-contained instructions
- Fail-open by default (`FailOpen=true`): any error returns `Fallback(raw)` so engine never blocks
- Timeout: 1500ms default, configurable via `cfg.TimeoutMs`
- Provider cascade: configured provider → `anthropic` → `openai` → `google`

### Provider Routing (`internal/provider/router.go`)
- **Primary + Fallback chain**: `ResolveOrder()` returns `[requested, primary, ...fallback, offline]`
- **Circuit breaker**: per-provider health tracking, skips providers that have tripped recently
- **Throttle retry**: 429/503 → Retry-After or exponential backoff, up to 3 retries
- **Model chain retry**: each provider can list multiple models with automatic fallback
- **Context overflow handling**: compacts messages and retries same model before moving to fallback
- **Stream recovery**: mid-stream provider swap on connection drop, with telemetry observer

---

## 5. Trust Boundaries

### Approval / Hooks System (`internal/hooks/hooks.go`)
- **Events**: `user_prompt_submit`, `pre_tool`, `post_tool`, `session_start`, `session_end`
- **Condition grammar**: `tool_name == X`, `tool_name != X`, `tool_name ~ substring`
- **Timeout**: 30s default per hook, overridable per entry
- **Sequential dispatch**: deterministic ordering, not parallel
- **Process group isolation**: SIGKILL reaches entire process tree on timeout (Unix `Setpgid`, Windows equivalent)
- **Output cap**: 1 MiB per stream (stdout/stderr) prevents unbounded memory growth
- **Secret scrubbing**: env vars matching `*_API_KEY`, `*_TOKEN`, `AWS_*`, etc. are stripped before hook subprocess

**Config permission check**: `hooks.CheckConfigPermissions()` warns (non-fatal) if config file is group/world-writable on Unix (VULN-036). Skipped on Windows.

### Tool Permission System (`internal/engine/approver.go`)
- **Approver interface**: `RequestApproval(ctx, ApprovalRequest) ApprovalDecision`
- **Three implementations**:
  - `stdinApprover` (CLI): interactive y/n on TTY; auto-deny in CI/non-TTY
  - `teaApprover` (TUI): async modal popup via bubbletea program
  - `webApprover` (web): `DFMC_APPROVE=yes|no` environment variable
- **Implicit deny**: no approver registered → deny by default (fail-safe)
- **Two-knob gate**: `DFMC_APPROVE=yes` auto-approves read-only tools; `DFMC_APPROVE_DESTRUCTIVE=yes` additionally approves destructive tools
- **Source-based differentiation**: `SourceWeb`, `SourceWS`, `SourceMCP` use `RequireApprovalNetwork` list; `SourceAgent`/`SourceSubagent` use `RequireApproval`
- **`*` wildcard**: gates everything when `*` in the require_approval list

### Web Authentication (`ui/web/server.go`)
- **Bearer token**: `auth=token` mode validates `Authorization: Bearer <token>` via constant-time comparison
- **Loopback enforcement**: `auth=none` forces loopback bind (VULN-049 warning if chosen with non-loopback)
- **Origin checking**: WebSocket Origin header validated against allowlist; `*` in allowlist is rejected
- **Trusted proxy list**: `X-Forwarded-For` only honored when direct peer is in trusted proxies
- **Security headers**: CSP `default-src 'self'`, `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`

---

## 6. Security Controls Already in Place

### Git Flag Injection Prevention (`internal/tools/git_runner.go`)
```go
func rejectGitFlagInjection(kind, value string) error {
    if value == "" || !strings.HasPrefix(value, "-") {
        return nil
    }
    return fmt.Errorf("git: %s value %q starts with `-`; refused to prevent flag injection (CVE-2018-17456 class)...")
}
```
Blocks any user-supplied ref/branch/path value prefixed with `-` before passing to `git` argv.

**Blocked git arguments** (never allowed, must use explicit `run_command`):
- `--no-verify`, `--no-gpg-sign`, `--amend`, `-i`, `--interactive`, `--force`, `-f`, `--hard`, `--no-checkout`
- `--exec=`, `--receive-pack=`, `--upload-pack=`

### Tool Approval System
- Approver interface with three UI-specific implementations
- Implicit deny on nil approver
- Source-based differentiation (user bypass, network requires approval)
- `RequireApprovalNetwork` separate from `RequireApproval` for independent network traffic lockdown

### Panic Guard (`internal/engine/engine_tools.go`)
```go
func (e *Engine) executeToolWithPanicGuard(ctx context.Context, name string, params map[string]any) (res tools.Result, err error) {
    defer func() {
        if r := recover(); r != nil {
            stack := debug.Stack()
            err = fmt.Errorf("tool %s panicked: %v\n%s", name, r, truncateStackForError(stack))
            res = tools.Result{}
            // publish tool:panicked event
        }
    }()
    return e.Tools.Execute(ctx, name, tools.Request{...})
}
```
Stack truncated to 2048 bytes. Panic never unwinds to caller.

### Input Normalization (`internal/tools/params.go`)
`normalizeToolParams()` rewrites non-canonical aliases to canonical names:
- `file`/`filepath`/`target` → `path`
- `start`/`from`/`lineStart` → `line_start`
- `old`/`new` → `old_string`/`new_string`
- etc.

Also enforces bounds: `line_end - line_start + 1 > 400` → capped at 400 lines; `timeout_ms > 120_000` → capped at 2 minutes.

### Path Containment (`internal/tools/engine.go:EnsureWithinRoot`)
Two-layer escape prevention:
1. **Syntactic**: `filepath.Abs` + `filepath.Rel`; any `..` prefix means escape
2. **Symbolic**: `filepath.EvalSymlinks` on both root and path; re-check containment

Handles new-file creation via existing-ancestor resolution (dangling symlink ancestor is walked up to find a real directory).

### Read-Before-Mutate Gate
- `readGateStrict` (write_file): requires prior `read_file` snapshot AND hash match
- `readGateLenient` (edit_file): requires prior `read_file` snapshot only (hash drift is tolerated since edit_file has its own anchor validation)
- **TOCTOU prevention**: for read_file, hash is taken from emitted content (in-memory), not from disk
- **LRU eviction**: snapshot map bounded to 256 entries

### Rate Limiting
- Per-IP token bucket: 30 req/s, burst 60
- 10-minute GC for stale IP buckets
- `X-Forwarded-For` only honored from trusted proxies; uses rightmost (most trusted) IP

### Content-Type Enforcement
`contentTypeEnforcementMiddleware` rejects non-JSON Content-Types on POST/PATCH/PUT before body decoding (VULN-050 fix).

### Hook Secret Scrubbing
```go
cmd.Env = append(security.ScrubEnv(os.Environ(), nil), hookEnv(event, payload)...)
```
Strips API keys, tokens, AWS credentials before passing env to user-configured hook subprocess.

---

## 7. External Integrations

### LLM Providers (`internal/provider/`)
| Provider | Protocol | Auth |
|----------|----------|------|
| `anthropic` | HTTP (Anthropic) | `ANTHROPIC_API_KEY` |
| `openai` | HTTP (OpenAI) | `OPENAI_API_KEY` |
| `google` / `gemini` | HTTP (Google) | `GOOGLE_API_KEY` |
| `openai-compatible` | HTTP (OpenAI-compatible) | per-profile API key |
| `offline` | Built-in | None (canned analyzer response) |

Provider selection: primary + fallback chain, circuit breaker per provider, throttle retry with Retry-After support, model chain retry with context compaction.

### bbolt Database (`internal/storage/store.go`)
- Embedded key-value store
- Stores: conversation history, memory, task persistence
- Locked: concurrent sessions are rejected with `ErrStoreLocked`
- Graceful shutdown: `eng.Shutdown()` waits for in-flight conversation saves before closing

### File System Access
- Tools: `read_file`, `write_file`, `edit_file`, `apply_patch`, `list_dir`, `glob`, `grep_codebase`
- All paths validated via `EnsureWithinRoot()` — no escape from project root
- `read_file` enforces 400-line window cap
- `write_file`/`edit_file` require prior `read_file` snapshot (read-before-mutate gate)
- `apply_patch` enforces per-target read gate directly

### Shell Command Execution
- `run_command` tool: `exec.CommandContext` (no shell by default)
- Optional shell via `shell: true` config (bypasses blocked commands)
- Blocked commands list from config: `cfg.Tools.Shell.BlockedCommands`
- Output cap: 4 MiB per stream
- Per-call timeout: default 30s, configurable per call via `timeout_ms`

### MCP External Servers (`internal/mcp/`)
- External MCP servers loaded at engine init via `loadMCPClients()`
- Bridge adapter exposes MCP tools as native `tools.Tool` via `mcpToolAdapter`
- MCP tools can be shadowed by native tools (native wins)
- Tools are listed in same registry as native tools

---

## 8. Sensitive Paths / Files

### Config Loading (`internal/config/`)
- `config.Load()` — loads from `~/.dfmc/config.yaml` (global) and `.dfmc/config.yaml` (project)
- `config.ConfigPaths()` — returns global + project config paths
- VULN-036 warning on group/world-writable config files (Unix only)
- Schema: `config.HooksConfig`, `config.ProvidersConfig`, `config.IntentConfig`, `config.ToolsConfig`, `config.WebConfig`, `config.SecurityConfig`

### Storage (`internal/storage/store.go`)
- `storage.Open(dataDir)` — opens bbolt DB
- Database file: `$DATA_DIR/dfmc.db` (default `~/.dfmc/`)
- Stores: `ConversationManager`, `MemoryStore`, `TaskStore`
- `ErrStoreLocked` — returned when another session has the DB open
- `Shutdown` ordering: drain Conversation async saves → persist Memory → close Tools → close Storage

### Memory (`internal/memory/store.go`)
- `memory.New(storage.Store)` — in-memory episodic memory
- `Memory.Load()` — loads from bbolt (non-fatal if it fails)
- `memoryDegraded` flag set on Load failure — engine keeps running with empty store
- Persisted on `eng.Shutdown()` via `Memory.Persist()`

---

## Summary: Security Model

```
dfmc process (single binary)
├── CLI surface     → stdinApprover (interactive/auto-deny)
├── TUI surface     → teaApprover (modal popup, 30s timeout)
├── Web surface     → webApprover (DFMC_APPROVE env var)
│   └── HTTP server (bearer token auth, rate limiting, origin checking)
│       └── WebSocket upgrade
│
├── Engine          → intent.Router (state-aware request normalization)
│   └── provider.Router (primary + fallback chain, circuit breaker)
│       └── LLM Providers (Anthropic, OpenAI, Google, OpenAI-compatible, Offline)
│
├── Tools Engine    → 30+ native tools + MCP bridge tools
│   ├── File I/O   (path containment + read-before-mutate gate)
│   ├── Git tools  (blocked flags + CVE-2018-17456 guard)
│   ├── Shell      (no-shell exec, blocked commands, timeout cap)
│   └── Code analysis (AST via tree-sitter)
│
├── Hooks Dispatcher (shell subprocess, secret scrubbing, output cap)
│
└── Storage         → bbolt (conversation, memory, tasks)
```

**Defense in depth layers**:
1. Path containment (syntactic + symbolic)
2. Read-before-mutate gate
3. Git flag injection prevention
4. Tool approval gate (source-differentiated)
5. Sub-agent allowlist
6. Panic guard around all tool execution
7. Input normalization + bounds enforcement
8. Rate limiting (per-IP)
9. Content-type enforcement on web API
10. Secret scrubbing for hook subprocesses
11. Process group isolation for hooks
12. Config permission warnings (VULN-036)