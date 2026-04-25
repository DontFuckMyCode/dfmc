# DFMC Security Recon — Architecture Report

Phase 1 reconnaissance of the DFMC ("Don't Fuck My Code") code intelligence
assistant. This report drives downstream `sc-lang-*` and Phase 2 hunter
skills. All paths are absolute; line refs use `file:NN` form.

---

## 1. Technology Stack Detection

### Language

- **Go 1.25** is the only first-party source language. Module path
  `github.com/dontfuckmycode/dfmc` (`go.mod:1-3`). 100% of `internal/`,
  `cmd/`, `ui/`, `pkg/` is Go.
- No JS/TS/Python/Rust/Java/C# source in the project. The only non-Go
  artifacts are:
  - `shell/Formula.rb` and `shell/homebrew-tapFormula.rb` — Homebrew
    distribution recipes
  - `shell/completions.{bash,fish,zsh}` — shell completions
  - `ui/web/static/index.html` — embedded single-page workbench (loaded via
    `//go:embed`, served by `dfmc serve`)

### Frameworks / Libraries (`go.mod`)

- **HTTP**: `net/http` standard library (no Gin/Echo/Fiber). Routes
  registered with Go 1.22+ `mux.HandleFunc("METHOD /path", ...)` syntax.
- **WebSocket**: `github.com/gorilla/websocket v1.5.3`
- **TUI**: `github.com/charmbracelet/bubbletea v1.3.10`, `lipgloss v1.1.0`
- **Storage**: `go.etcd.io/bbolt v1.4.3` (single-process embedded KV)
- **AST**: `github.com/tree-sitter/go-tree-sitter v0.25.0` plus language
  grammars for go, javascript, typescript, python (CGO-required;
  fallback to regex extractor when `CGO_ENABLED=0`)
- **WASM**: `github.com/tetratelabs/wazero v1.11.0` — used by
  `internal/pluginexec/` for sandboxed plugin execution
- **HTML parsing**: `golang.org/x/net/html`
- **YAML**: `gopkg.in/yaml.v3 v3.0.1`
- **Rate limit**: `golang.org/x/time/rate`
- No HTTP client SDKs for LLM providers — all providers (Anthropic,
  OpenAI, DeepSeek, Kimi, Z.ai, Alibaba, MiniMax, Google AI Studio, Ollama)
  are hand-rolled REST clients in `internal/provider/`

### Build / Tooling

- `Makefile` (Windows-oriented, uses `NUL`)
- `Dockerfile` (newly added)
- `.github/` workflows present (CI added per CLAUDE.md "all 20 phases done")
- No `package.json`, `pom.xml`, `Cargo.toml`, `requirements.txt`,
  `pyproject.toml`, etc.

### Databases / Persistence

- **bbolt** files under `.dfmc/` (project state) and `~/.dfmc/` (global)
  for memory tiers, conversations, task store, drive runs, and storage
  lock. See `internal/storage/store.go`.
- No SQL, no Redis, no external DB clients.

---

## 2. Application Type Classification

DFMC is a **multi-modal local agent runtime**, distributed as a single
Go binary. Concretely it is the union of:

1. **CLI tool** — `dfmc <command> [args]` with ~30 subcommands
   (`ui/cli/cli.go:68-141`).
2. **Bubble Tea TUI application** — `dfmc tui` (`ui/tui/tui.go`, ~3000
   lines).
3. **Embedded HTTP+SSE+WebSocket API server** — `dfmc serve` on
   `localhost:7777` (default), serves an embedded HTML workbench plus
   `/api/v1/*` JSON API and `/ws` event stream.
4. **Optional remote server** — `dfmc remote start` on
   `localhost:7778`/`7779` (gRPC-port reserved but currently HTTP+WS only,
   `cli_remote_start.go:65-66`).
5. **MCP server** — `dfmc mcp`, JSON-RPC 2.0 over stdio for IDE hosts
   (Claude Desktop, Cursor, VSCode).
6. **MCP client** — connects to user-configured external MCP servers and
   imports their tools into the agent loop (`internal/mcp/client.go`,
   config under `MCPConfig` in `config_types.go:39-50`).

The Engine (`internal/engine/engine.go:68-100`) is the central hub; every
UI is a thin shell over it.

---

## 3. Entry Points Mapping

### 3.1 CLI Subcommands (`ui/cli/cli.go:68-161`)

Dispatcher in `Run()`. Top-level commands:

| Command | Handler file | Effect |
|---|---|---|
| `help` / `-h` / `--help` | `cli.go:69` | Prints help |
| `status` | `cli_admin.go` | Engine status |
| `version` | `cli_admin.go` | Version info |
| `init` | `cli_admin.go` | Scaffold `.dfmc/` |
| `ask` | `cli_ask_chat.go` | One-shot LLM ask |
| `chat` | `cli_ask_chat.go` | Interactive REPL |
| `tui` | `cli_ask_chat.go` | Launch Bubble Tea TUI |
| `analyze` | `cli_analysis.go` | Codebase analyze |
| `map` | `cli_analysis.go` | Codemap render |
| `tool` | `cli_analysis.go` | Direct tool invocation |
| `scan` | `cli_analysis.go` | Run `internal/security/Scanner` |
| `memory` | `cli_analysis.go` | Memory tier admin |
| `conversation` / `conv` | `cli_analysis.go` | Conv mgmt |
| `serve` | `cli_remote.go:40` | **Embedded HTTP server (port 7777)** |
| `config` | `cli_config.go` | Config admin (incl. `sync-models`) |
| `context` | `cli_context.go` | Context-budget admin |
| `prompt` | `cli_prompt.go` | Prompt library admin |
| `magicdoc` | `cli_magicdoc.go` | MAGIC_DOC.md generator |
| `plugin` | `cli_plugin_skill.go` | Plugin mgmt |
| `skill` | `cli_skill.go` | Skill mgmt |
| `review` / `explain` / `refactor` / `debug` / `test` / `doc` / `generate` / `audit` / `onboard` | `cli_plugin_skill.go` | Skill shortcuts |
| `remote` | `cli_remote.go` | **Remote subcommands (start/stop/client)** |
| `drive` | `cli_drive.go` | Autonomous Drive runner |
| `provider` / `model` / `providers` | `provider_cli.go` | LLM provider mgmt |
| `doctor` | `cli_doctor.go` | Diagnostics (degraded-startup safe) |
| `hooks` | `hooks_cli.go` | Hooks admin |
| `approvals` / `approve` / `permissions` | `approvals_cli.go` | Approval rules |
| `mcp` | `cli_mcp.go:19` | **MCP server on stdio** |
| `update` | `cli_update.go` | Self-update |
| `completion` | `cli_completion.go` | Shell completion |
| `man` | `cli_admin.go` | Manpage |

Unknown commands fall through to `runAsk()` (so `dfmc what is foo` =
`dfmc ask "what is foo"`).

`dfmc remote` has its own subcommand tree (`cli_remote*.go`) — start/stop
plus a full HTTP client surface that mirrors the web API.

### 3.2 HTTP Routes (`ui/web/server.go:188-257`)

All routes mounted by `setupRoutes()`. Path-templated with Go 1.22+
`{name}` syntax. Listens on `127.0.0.1:7777` by default; auto-rewrites
non-loopback bind to `127.0.0.1` when `auth=none` (`server.go:152-160`).

```
GET    /                                 — workbench HTML (embedded)
GET    /healthz                          — liveness (always 200, no auth)
GET    /api/v1/status
GET    /api/v1/commands
GET    /api/v1/commands/{name}
POST   /api/v1/chat                      — streaming SSE chat
POST   /api/v1/ask                       — single-turn ask (race optional)
GET    /api/v1/codemap
GET    /api/v1/context/{budget,recommend,brief}
GET    /api/v1/providers
GET    /api/v1/skills
GET    /api/v1/tools                     — list registered tools
GET    /api/v1/tools/{name}              — tool spec
POST   /api/v1/tools/{name}              — *** invoke a tool with arbitrary params ***
POST   /api/v1/skills/{name}             — invoke skill
POST   /api/v1/analyze                   — codebase analyze
GET    /api/v1/memory
GET    /api/v1/conversation              — active conv
POST   /api/v1/conversation/new
POST   /api/v1/conversation/save
POST   /api/v1/conversation/load
POST   /api/v1/conversation/undo
GET    /api/v1/conversation/branches
POST   /api/v1/conversation/branches/{create,switch}
GET    /api/v1/conversation/branches/compare
GET    /api/v1/prompts                   — list
GET    /api/v1/prompts/stats
GET    /api/v1/prompts/recommend
POST   /api/v1/prompts/render
GET    /api/v1/prompt/debug
GET    /api/v1/magicdoc
POST   /api/v1/magicdoc/update
GET    /api/v1/conversations             — search/list
GET    /api/v1/conversations/search
GET    /api/v1/workspace/diff
GET    /api/v1/workspace/patch
POST   /api/v1/workspace/apply           — *** patch application ***
GET    /api/v1/files                     — *** project file listing ***
GET    /api/v1/files/{path...}           — *** project file content ***
GET    /api/v1/scan                      — security scanner
GET    /api/v1/doctor
GET    /api/v1/hooks
GET    /api/v1/config
POST   /api/v1/drive                     — start drive run
GET    /api/v1/drive
GET    /api/v1/drive/{id}
POST   /api/v1/drive/{id}/resume
POST   /api/v1/drive/{id}/stop
DELETE /api/v1/drive/{id}
GET    /api/v1/drive/active
GET    /api/v1/tasks                     — task store CRUD
POST   /api/v1/tasks
GET    /api/v1/tasks/{id}
PATCH  /api/v1/tasks/{id}
DELETE /api/v1/tasks/{id}
GET    /api/v1/tasks/{tree,children,ancestors,roots}
GET    /api/v1/langintel
GET    /ws                               — *** SSE event stream ***
GET    /api/v1/ws                        — *** WebSocket upgrade ***
POST   /api/v1/ws                        — WebSocket upgrade (POST variant)
```

**Sensitive HTTP sinks** (these reach into the host filesystem / shell):

- `POST /api/v1/tools/{name}` — invokes any registered tool by name
  with caller-supplied params, including `run_command`, `write_file`,
  `edit_file`, `apply_patch`, `git_commit`, `web_fetch`. Routes through
  `engine.CallTool` → `executeToolWithLifecycle`.
- `GET /api/v1/files/{path...}` — reads any file in project root
  (`server_files.go:45-105`)
- `POST /api/v1/workspace/apply` — applies arbitrary unified-diff patches
- `GET /api/v1/scan`, `GET /api/v1/doctor` — disclose findings/secrets

### 3.3 WebSocket / SSE Endpoints

- `GET /ws` — SSE event stream of engine `EventBus` (`drive:*`,
  `agent:*`, `tool:*`, `provider:*`, `intent:*`)
- `GET /api/v1/ws` and `POST /api/v1/ws` — Gorilla WebSocket upgrade for
  bidirectional JSON-RPC remote control (`server_ws.go`).
  **`CheckOrigin` returns `true` unconditionally** (`server_ws.go:32-35`)
  — see Trust Boundaries.

### 3.4 MCP Tools (`internal/mcp/server.go`, `ui/cli/cli_mcp.go`,
`cli_mcp_drive.go`, `cli_mcp_task.go`)

JSON-RPC 2.0 over stdio. The bridge exposes:

- All registered backend tools (the same set as the agent loop sees:
  ~50+ tools from `internal/tools/`).
- 6 synthetic Drive tools: `dfmc_drive_start`, `dfmc_drive_status`,
  `dfmc_drive_active`, `dfmc_drive_list`, `dfmc_drive_stop`,
  `dfmc_drive_resume` (`cli_mcp_drive.go`).
- Task store synthetic tools (`cli_mcp_task.go`).

Drive MCP handlers route through `driveMCPHandler` **NOT** through
`engine.CallTool` to avoid recursive LLM steps and to skip the approval
gate — explicit design decision noted in CLAUDE.md.

### 3.5 Tool Engine (`internal/tools/engine.go:147-...`)

Registered tools (file-ops, search, shell, git, web, planning,
reasoning) — these are the data sinks reached by the LLM via the agent
loop, by `CallTool` from the user, by the web API, and by MCP:

- File: `read_file`, `write_file`, `edit_file`, `apply_patch`, `list_dir`
- Search/AST: `grep_codebase`, `glob`, `find_symbol`, `codemap`,
  `ast_query`, `dependency_graph`, `semantic_search`, `test_discovery`,
  `disk_usage`
- Git: `git_status`, `git_diff`, `git_branch`, `git_log`, `git_blame`,
  `git_commit`, `git_worktree_{list,add,remove}`, `gh_pr`
- **Shell**: `run_command` (`internal/tools/command.go:21`)
- Web: `web_fetch`, `web_search` (`internal/tools/web.go`)
- Planning/reasoning: `task_split`, `orchestrate`, `delegate_task`,
  `think`, `todo_write`
- Refactor: `symbol_rename`, `symbol_move`
- Bench/spec: `benchmark`, `patch_validation`, `project_info`
- Meta: `tool_search`, `tool_help`, `tool_call`, `tool_batch_call`
  (registered in `meta.go:138-141`)

### 3.6 File Watchers / Schedulers

None. DFMC has no filesystem watchers. The Drive runner is a
poll-based scheduler over the engine event bus, not a fs-watch.

### 3.7 Drive Runner Entry Points

- `dfmc drive "<task>"` (CLI, `cli_drive.go`)
- `/drive <task>` (TUI)
- `POST /api/v1/drive` (HTTP)
- `dfmc_drive_start` (MCP)

The driver fans out to up to N parallel sub-agents which independently
run the full agent loop with full tool access (`internal/drive/driver.go`).

---

## 4. Data Flow Map

### Sources (untrusted input)

1. **Stdin** — TUI keystrokes, CLI `chat` REPL, MCP JSON-RPC frames
2. **HTTP request bodies** — POST endpoints under `/api/v1/*` (capped
   at 4 MiB per request, `server.go:314`)
3. **WebSocket frames** — `/api/v1/ws` JSON-RPC messages
4. **LLM provider responses** — tool calls emitted by remote LLMs (these
   become tool invocations the engine executes!)
5. **External MCP servers** — DFMC connects to user-configured MCP
   servers as a client and imports their tools (`MCPConfig` in
   `config_types.go:39-50`); tool descriptions, names, JSON schemas,
   and outputs from these servers flow into the agent loop's tool
   registry and tool result stream
6. **Config files** — `~/.dfmc/config.yaml`, `<project>/.dfmc/config.yaml`,
   project `.env` (auto-loaded at startup, `config.go:62-70`)
7. **Environment variables** — `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`,
   `DEEPSEEK_API_KEY`, `KIMI_API_KEY`, `ZAI_API_KEY`,
   `ALIBABA_API_KEY`, `MINIMAX_API_KEY`, `GOOGLE_AI_API_KEY`,
   `DFMC_KEYLOG`, `DFMC_WEB_TOKEN`, `DFMC_REMOTE_TOKEN`, `DFMC_APPROVE`
8. **Web pages** fetched by `web_fetch`/`web_search`

### Processing (engine + tool engine)

User prompt → `engine.Ask` → `Intent.Evaluate` (state-aware sub-LLM,
fail-open, `internal/intent/router.go`) → main LLM completion →
optional tool call cycle:

```
LLM tool_call → Engine.executeToolWithLifecycle
  → approval gate (askToolApproval)        [gates non-user sources]
  → pre_tool hook fire (Hooks.Fire)
  → executeToolWithPanicGuard
       → tools.Engine.Execute
            → strip _reason → publish tool:reasoning event
            → normalizeToolParams (alias rewrites: old→old_string, etc.)
            → EnsureReadBeforeMutation gate (edit/write/apply_patch)
            → tool.Execute(req)
  → invalidateContextForTool (cache clear)
  → post_tool hook fire
```

`executeToolWithLifecycle` is the **single mandated entry** for every
tool invocation (`engine_tools.go:218`). MCP Drive tools deliberately
bypass it (called out in CLAUDE.md).

### Sinks (where untrusted input ends up)

| Sink | Reached via | Guard |
|---|---|---|
| `os/exec.CommandContext` for arbitrary binaries | `run_command` tool | `command.go:296-317` block list, shell-interp block, shell-metachar detection, eval-flag block, blocked-arg sequences, `EnsureWithinRoot` on path commands, output cap 4 MiB |
| `os.WriteFile` / `os.Create` | `write_file`, `apply_patch`, `edit_file`, `MagicDocUpdate` | `EnsureWithinRoot` (`engine.go:788`), read-before-mutation gate, hash-equality guard for write/patch (strict mode) |
| `os.ReadFile` | `read_file`, `web/server_files.go`, `grep`, `codemap`, `ast_query` | `EnsureWithinRoot` / `resolvePathWithinRoot` (`server_files.go:143-183`) including symlink-escape check |
| `os/exec` for `git` | git tools in `internal/tools/git*.go` | `rejectGitFlagInjection` on every `ref/revision/branch/path` arg (`git.go`); destructive-arg sequences blocked (`command.go:364-381`: `git reset --hard`, `clean -fd*`, `checkout --`, `restore --source`, `push --force*`) |
| `net/http.Client` outbound | `web_fetch`, `web_search`, `audit_deps` HEAD probes, LLM provider clients | `safeTransport.DialContext` SSRF guard at connect time (loopback/private/link-local rejected, `web.go:24-48`); 5-redirect cap; 20s default timeout |
| Conversation JSONL | `conversation.Manager` save | Project-rooted under `.dfmc/`, no special guard; bbolt locked single-process |
| Stdout/Stderr | always | TUI/CLI render |

---

## 5. Trust Boundaries

### Authentication

- **CLI / TUI / MCP-stdio**: implicit trust. The user owns the process;
  there's no authn layer.
- **`dfmc serve` (web)**: optional. `Web.Auth` config = `none` (default)
  or `token` (`server.go:131-150`). When `auth=none` AND the bind host
  is non-loopback, `normalizeBindHost` silently rewrites the bind host
  back to `127.0.0.1` (`server.go:152-160`) — the server refuses to
  listen on a public interface unauthenticated. `runServe` *also*
  refuses to start in that configuration unless `--insecure` is given
  (`cli_remote.go:66-77`).
- **`dfmc remote start`**: same model. `Remote.Auth` config; default
  `token`. Bearer token comes from `--token` flag or `DFMC_REMOTE_TOKEN`
  / `DFMC_WEB_TOKEN` env (`cli_remote.go:32-38`,
  `cli_remote_start.go:25-44`).
- **WebSocket `/api/v1/ws`**: gated by the same bearer-token middleware
  as REST when auth=token. `CheckOrigin` returns `true`
  (`server_ws.go:32-35`) — **CSRF/cross-origin WS hijack possible if
  auth=none on a non-loopback bind, but the bind-host normalization
  prevents that combo from being reachable in practice.**

### Authorization

- **Tool approval gate** — `executeToolWithLifecycle` consults
  `Approver` for non-user sources (`engine_tools.go:225-244`). Three
  approver impls:
  - `newStdinApprover()` — CLI/TUI prompt
  - `newWebApprover()` — `DFMC_APPROVE=yes|no`-driven, deny-by-default
    (`ui/web/approver.go`, registered in `web.New()` at
    `server.go:147-148`)
  - TUI modal approver (`ui/tui/approver.go`)
- **Skill tool policy** — active skills can constrain Preferred/Allowed
  tool lists (`engine_tools.go:55-90`).
- **Tool blocked-command list** — destructive binaries
  (`rm`, `del`, `format`, `mkfs`, `dd`, etc.), privilege escalators
  (`sudo`, `doas`, `su`, `runas`, `pkexec`), shutdown/reboot, and
  broad killers (`killall`, `pkill`) — all hardcoded in
  `command.go:338-358`. Shell interpreters always blocked
  (`command.go:598-606`). Script-runner inline-eval flags blocked
  (`command.go:610-633`: `node -e`, `python -c`, `perl -e`, `ruby -e`,
  `php -r`, `pwsh -c`).

### Rate limiting

- **HTTP per-IP token bucket** — 30 req/sec, burst 60
  (`server.go:277-367`, `newPerIPLimiter`). `X-Forwarded-For` honoured
  for IP extraction with the rationale that remote clients can't reach
  the server unauthenticated through a proxy (`server.go:370-390`).
- **No rate limit on stdio MCP / CLI**. No global cooldown.

### Input validation

- **HTTP body cap**: 4 MiB (`server.go:314-326`, `MaxBytesReader`)
- **Header size cap**: 1 MiB (`server.go:290`)
- **Read/write/idle timeouts**: 30s/2m/2m (`server.go:286-291`)
- **Path traversal**:
  - `internal/tools/engine.go:788` `EnsureWithinRoot` — symlink-aware,
    refuses `..`-escapes and resolved-symlink escapes
  - `ui/web/server_files.go:143` `resolvePathWithinRoot` — same shape,
    handles paths the caller is about to *create* (deepest-existing
    ancestor walk)
- **Tool param validation**: each tool surfaces self-teaching errors
  via `missingParamError` (`internal/tools/builtin.go`); known typo
  aliases rewritten silently (`old`→`old_string`, etc.).
- **Git flag injection**: `rejectGitFlagInjection` on every user-supplied
  ref/branch/path argument (`internal/tools/git.go`) — refuses `-`-prefix
  values that git would treat as flags.

### CSRF / CORS

- **No CORS headers set anywhere** in `ui/web/`. Same-origin only by
  browser default; the embedded workbench is itself served from `/`.
- **CSRF**: no token. Mitigated by:
  - default loopback bind (cookies don't apply — auth is bearer-token)
  - bearer token in `Authorization` header (not cookie), so a malicious
    cross-origin form/script cannot read `localStorage`-stored tokens
    without an XSS in the workbench
  - `WebSocket CheckOrigin: true` is the **lone CORS-relevant gap** —
    if auth=none the WS endpoint accepts any origin
- **Security headers** (`server.go:122-129`):
  - `Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self'`
  - `X-Content-Type-Options: nosniff`
  - `X-Frame-Options: DENY`

---

## 6. External Integrations

### LLM Providers (`internal/provider/`)

Hand-rolled REST clients, no SDKs. All go through `Router` with
primary + fallback chain. Offline placeholder always registered
(`offline.go`, `placeholder.go`) so a missing API key degrades gracefully.

| Provider | File | Endpoint |
|---|---|---|
| Anthropic | `anthropic.go` | `https://api.anthropic.com` |
| OpenAI | `openai_compat.go` | `https://api.openai.com` |
| OpenAI-compatible (DeepSeek, Kimi, Z.ai, Alibaba/Qwen, MiniMax, Ollama, generic) | `openai_compat.go` | per-profile baseURL |
| Google AI Studio | `google.go` | `https://generativelanguage.googleapis.com` |
| Offline fallback | `offline.go` | local heuristic responses |

Streams supported (SSE for OpenAI/Anthropic, custom for others).
Throttle/retry in `throttle.go`.

### MCP

- **Server** (`dfmc mcp`, `internal/mcp/server.go`): JSON-RPC 2.0 over
  stdio. Exposes engine tool registry to IDE hosts.
- **Client** (`internal/mcp/client.go`): connects to user-configured
  external MCP servers. Tool descriptions and outputs from those servers
  enter the agent loop's context — **a hostile MCP server can poison
  tool descriptions, return spoofed tool_results, and influence the
  agent's reasoning.** Spawns server subprocesses with command/args/env
  from project config.

### Web

- `web_fetch` (any HTTP/HTTPS URL) — SSRF-guarded
- `web_search` (DuckDuckGo HTML endpoint, `https://html.duckduckgo.com/html/`)

### Other

- **bbolt** — no network. Local file lock (`storage.ErrStoreLocked` if
  another DFMC process holds it; `cmd/dfmc/main.go:60-77` allow-list).
- **models.dev** — `dfmc config sync-models` fetches
  `https://models.dev/api.json` to repopulate provider profiles
  (`config_models_dev.go`).
- **Self-update** — `dfmc update` (`cli_update.go`) downloads release
  binaries.
- **GitHub CLI** — `gh_pr` tool shells out to `gh` if installed
  (`internal/tools/gh_pr.go`, `gh_runner.go`).

---

## 7. Authentication Architecture

### Local CLI / TUI

Trust is implicit. Everything runs as the invoking user.

### Web (`dfmc serve`)

```
ListenAndServe
  ↓
[bearerTokenMiddleware]  ← only when Web.Auth=="token"
  ↓
[rateLimitMiddleware (per-IP token bucket)]
  ↓
[securityHeaders]  (CSP, nosniff, frame-deny)
  ↓
[limitRequestBodySize 4MiB]
  ↓
mux
```

`bearerTokenMiddleware` (`server.go:401-419`):
- Constant-time compare via `subtle.ConstantTimeCompare` against
  `"Bearer " + token`.
- `/healthz` exempt (returns 200 unauthenticated).
- `GET /` with empty token allowed (so a token-mode server with empty
  token serves the workbench HTML — questionable; relies on `runServe`
  refusing empty token at startup, `cli_remote.go:62-65`).
- Token comes from `DFMC_WEB_TOKEN` env or `--token` flag.

### Remote (`dfmc remote start`)

Identical to web auth path. Same bearer-token middleware composed
(`cli_remote_start.go:60-63`). Default `auth=token`, env
`DFMC_REMOTE_TOKEN`. Refuses non-loopback + auth=none unless
`--insecure`.

### MCP server (`dfmc mcp`)

No authentication — runs over stdio. The IDE host launching the
subprocess is implicitly trusted.

### Approval system (orthogonal to authn)

The approval gate (`Approver` interface) is consulted for **agent-
initiated** tool calls, not human-initiated ones. Authentication does
not alter approval — even an authenticated web client invoking `POST
/api/v1/tools/run_command` with `source="user"` skips the approval
gate (the explicit `if source != "user"` check at
`engine_tools.go:225`).

---

## 8. File Structure Analysis

### Sensitive paths

- `~/.dfmc/config.yaml` — global config (provider profiles, API key
  references, hooks, security policy)
- `<project>/.dfmc/config.yaml` — project overrides (also: hook entries
  but only honoured if `Hooks.AllowProject=true` in global)
- `<project>/.dfmc/` — bbolt files (memory, conversations, tasks,
  drive runs), knowledge.json, conventions.json, magic/MAGIC_DOC.md
- `<project>/.env` — auto-loaded at startup (`config.go:62-70`,
  process env still wins). Common API-key location.
- `~/.dfmc/prompts`, `<project>/.dfmc/prompts` — prompt overlays
- `<project>/.project/` — design docs (gitignored)

### Embedded assets

- `internal/promptlib/defaults/*.yaml` — system/role/task prompts
  (embedded via `//go:embed`)
- `ui/web/static/index.html` — single-page workbench (embedded in
  binary, served by `dfmc serve`)
- `assets/` — fonts/branding

### Ignore boundaries

- `.dfmc/` is gitignored as project state. Tests build temp project
  with `.dfmc/` scaffolding.

### Hooks file location

- Hooks defined in config under `hooks.entries.<event>[]`. Project-
  level hooks only run if `hooks.allow_project=true` at global level
  (`config.go:39-58`). Each hook is a shell command executed with a
  hard 30s default timeout (`internal/hooks/hooks.go`).

---

## 9. Detected Security Controls

### Source code scanner — `internal/security/`

- `scanner.go` — secret regex catalog + entropy filter + vuln pattern
  catalog (CWE/OWASP-tagged). Used by `dfmc scan` and `GET
  /api/v1/scan`.
- `astscan.go`, `astscan_credentials.go`, `astscan_go.go`,
  `astscan_javascript.go`, `astscan_python.go` — AST-driven scanner
  passes (Go primary; JS/Python are scanner targets, not source langs).
- `audit_deps.go` — lockfile-aware dependency audit (parses `go.sum`,
  npm lock, etc.); detects unpinned git refs, unknown versions.
- `false_positive_test.go` — pinned suppression rules.
- `gitroot.go` — git root resolver (used by scanner to scope walks).

### Tool engine guards (`internal/tools/`)

- `engine.go:788` `EnsureWithinRoot` — symlink-safe path-containment
- `engine.go:447, 664` `EnsureReadBeforeMutation` — refuses edit/write
  without a recent `read_file` snapshot. Two modes: `readGateLenient`
  for `edit_file` (anchor-string check is the safety net),
  `readGateStrict` for `write_file`/`apply_patch` (hash equality
  required to detect concurrent writes).
- `command.go:296-317` `ensureCommandAllowed` — binary block list,
  destructive arg-sequence detection, user-configured pattern list
- `command.go:427-454` `detectShellMetacharacter` — refuses shell-line
  syntax in the binary slot of `run_command`
- `command.go:610-633` script-runner eval-flag block list
- `git.go` `rejectGitFlagInjection` — refuses `-`-prefix user values
  on ref/branch/path args
- `web.go:24-48` `safeTransport` — DNS-rebinding-safe SSRF guard at
  dial time; rejects loopback, private (RFC1918), link-local
- Result truncation (32 KiB output, 12 KiB data) — limits LLM context
  blow-up
- Output capture cap on `run_command` (4 MiB stdout + 4 MiB stderr,
  `command.go:19,145-148`)

### Engine-level gates (`internal/engine/`)

- `executeToolWithLifecycle` — mandated single entry: approval gate +
  pre/post hooks + panic guard (`engine_tools.go:155-217`)
- `executeToolWithPanicGuard` — recovers panics from any tool, attaches
  truncated stack trace to error
- Skill tool policy enforcement (`engine_tools.go:55-90`)
- Read-before-mutation enforcement (`tools/engine.go`)
- Tool denial event publishing (`tool:denied`)

### Web server middleware (`ui/web/server.go`)

- Bearer-token auth (constant-time)
- Per-IP rate limit (30 r/s, burst 60)
- Security headers (CSP self-only, nosniff, frame-deny)
- 4 MiB body cap
- Server timeouts (read 30s, write 2m, idle 2m, header 5s)
- Bind-host normalization (force loopback when auth=none)
- Refuses unauthenticated non-loopback bind without `--insecure`
- WebSocket: bearer-token gated when auth=token; `CheckOrigin=true`
  (gap noted)

### Hooks system (`internal/hooks/`)

- Lifecycle dispatch on user_prompt_submit, pre_tool, post_tool,
  session_start/end
- Best-effort: hook failures logged but don't block tool calls
- Per-hook timeout (default 30s)
- Project-hooks gate: project-level hooks only run if
  `hooks.allow_project=true` at global level (mitigates
  malicious-repo-hooks-on-clone)
- Process-group cleanup (`hooks_pgid_*.go` per-OS)

### TUI security tests

- `ui/tui/security.go` and `ui/tui/security_test.go` — TUI-side
  security/approval rendering tests

### Other

- `cmd/dfmc/main.go:60-77` — degraded-startup allow-list (only `help`,
  `version`, `doctor`, `completion`, `man`, `update` continue if
  Engine.Init fails — prevents partial-init footguns)
- `internal/storage/store.go` — single-process bbolt lock with
  `ErrStoreLocked`

### What's NOT present

- **No CSRF tokens.** Bearer-token-in-header model + loopback-bind
  default is the mitigation strategy.
- **No CORS configuration.** Same-origin by browser default.
- **No content-type whitelist on tool POSTs** beyond JSON parse.
- **No HTTPS termination in-process.** `dfmc serve` is HTTP-only;
  `cli_remote_start.go:65` constructs `http://...` URLs explicitly.
  Operators must front with a TLS proxy if exposing remotely.
- **No request signing** for outbound LLM calls beyond the provider's
  own bearer-token convention.
- **No audit trail persistence** for tool denials beyond the EventBus
  (which is volatile).
- **No sandboxing of external MCP server subprocesses** — they inherit
  DFMC's process privileges plus configured env vars.
- **`websocket.Upgrader.CheckOrigin` returns `true`** unconditionally
  (`server_ws.go:32-35`). Documented but not gated on origin.

---

## 10. Language Detection — Phase 2 Activation List

DFMC source code is **Go-only**. Activate:

- **`sc-lang-go`** — primary. Drives the Phase 2 deep scan.

Do NOT activate `sc-lang-typescript`, `sc-lang-javascript`,
`sc-lang-python`, `sc-lang-rust`, `sc-lang-java`, `sc-lang-csharp`,
`sc-lang-php` — these languages exist only as **scanner targets** for
DFMC's own `internal/security/astscan_*.go` (i.e. DFMC parses other
languages to scan them, but contains no source in those languages).

Auxiliary surfaces also worth a pass:

- **YAML** — config files (`config_types.go` defines schema, but YAML
  parsing happens in `internal/config/config.go:101-113` via
  `gopkg.in/yaml.v3`). Worth dedicated inspection for unsafe deserial.
- **HTML** (embedded workbench) — single embedded `index.html`; XSS
  surface is bounded by CSP `script-src 'self'`.
- **Dockerfile** — newly added; image-level review optional.
- **Shell completion / Homebrew formula** (`shell/`) — distribution
  artifacts; low priority.
