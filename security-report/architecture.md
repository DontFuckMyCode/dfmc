# DFMC — Architecture & Attack Surface Map

**Phase:** 1 (Recon)
**Date:** 2026-04-30
**Module:** `github.com/dontfuckmycode/dfmc`
**Language:** Go 1.25.0 (single language; CGO conditionally enabled for tree-sitter)

---

## Detected Stack

| Layer | Technology |
|-------|------------|
| Runtime | Go 1.25, single static binary (`cmd/dfmc`) |
| TUI | `bubbletea` 1.3.10 + `lipgloss` 1.1.0 + `termenv` 0.16.0 |
| Web | `net/http` stdlib + `gorilla/websocket` 1.5.3 |
| Storage | `go.etcd.io/bbolt` 1.4.3 (single embedded file lock per project) |
| AST | `tree-sitter/go-tree-sitter` 0.25.0 + per-language grammars (Go/JS/TS/Python) — CGO required, regex fallback at `internal/ast/backend_stub.go` for `!cgo` |
| Plugins | `tetratelabs/wazero` 1.11.0 (Wasm) + `os/exec` (subprocess JSON-RPC) |
| Config | `gopkg.in/yaml.v3` 3.0.1 |
| Net helpers | `golang.org/x/net` 0.53.0, `golang.org/x/time` 0.15.0 (rate limiter) |

`application_type`: **multi-modal developer tool** — embedded HTTP server, CLI, TUI, MCP server, all sharing a single `engine.Engine` core.

---

## Entry Points (attack surface)

### CLI surface (`ui/cli/`)
- All commands defined in `cli.go` `switch cmd`. Subcommand bodies in `cli_<domain>.go` siblings.
- User-driven only; no network. Reads/writes project files. **Trust boundary: local user.**

### TUI surface (`ui/tui/`)
- `bubbletea` reactor; user interacts via keyboard. Same engine pointer as CLI.
- Bracketed-paste mode + paste-window heuristic in `chat_key.go`/`paste_test.go`.
- **Trust boundary: local user.**

### Web/API surface (`ui/web/`) — `dfmc serve` on `127.0.0.1:7777`

| Path | Handler | File |
|------|---------|------|
| `/` | Workbench HTML (embedded) | `server.go` |
| `/api/v1/ask` | Single-turn JSON | `server_chat.go` |
| `/api/v1/chat` | SSE stream | `server_chat.go` |
| `/ws` | WebSocket events feed | `server_ws.go` |
| `/api/v1/status` | Health/config | `server_status.go` |
| `/api/v1/context/*` | Context inspection | `server_context.go` |
| `/api/v1/tools/*`, `/api/v1/skills/*`, `/api/v1/codemap`, `/api/v1/memory/*` | Tool/skill/provider mgmt | `server_tools_skills.go` |
| `/api/v1/conversation/*` | Conversation CRUD + branches | `server_conversation.go` |
| `/api/v1/workspace/*` | Diff/patch/apply | `server_workspace.go` |
| `/api/v1/files/*` | File listing/content | `server_files.go` |
| `/api/v1/drive/*` | Drive cockpit | `server_drive.go` |
| `/api/v1/tasks/*` | Task store CRUD | `server_task.go` |
| `/api/v1/admin/*` | scan/doctor/hooks/config | `server_admin.go` |

**Defaults:**

- Bind: `127.0.0.1` (host normalized in `normalizeBindHost`); `0.0.0.0` only with `auth: token`.
- Origin allow-list: `["http://127.0.0.1","http://localhost"]`; `*` explicitly rejected (`server.go:252-258`).
- Auth: `none|token` (token mode requires `DFMC_WEB_TOKEN`; constant-time compare).
- Headers: `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, CSP `default-src 'self'`.
- Rate limit: per-WS-connection `wsRPS/wsBurst`; global+per-IP connection cap (`wsConnLimiter`).
- SSE: per-chunk write deadline 15s (`writeSSEWithDeadline`).

### MCP surface (`internal/mcp/` + `cli_mcp.go`)

- `dfmc mcp` exposes tool registry + 6 synthetic Drive tools over JSON-RPC (stdin/stdout).
- Driven by IDE hosts (Claude Desktop, Cursor). Trust boundary: local IDE.
- Env scrubbing on subprocess spawn (`security.ScrubEnv` strips API keys before forwarding).

### Tool surface (`internal/tools/`)

- Backend tools dispatched via meta-tool layer (`tool_search`/`tool_help`/`tool_call`/`tool_batch_call`).
- Each tool execution funnels through `Engine.executeToolWithLifecycle` → approval gate → pre/post hooks → panic guard.
- Path validation: `EnsureWithinRoot` + `filepath.EvalSymlinks` on both root and target (`engine.go:865-869`).
- Read-before-mutation gate: `edit_file` lenient mode, `write_file`/`apply_patch` strict mode.
- Git/gh runners: argv-only `exec.Command`, `rejectGitFlagInjection` on every ref/path arg.
- Shell command: `run_command` with timeout cap 120s, blocked-binary list.

### Hooks surface (`internal/hooks/`)

- User-configured shell commands fired on lifecycle events.
- Per-hook timeout (default 30s, configurable). Output capped at `hookOutputCap` per stream.
- Env-var injection: keys sanitized via `sanitizeEnvKey`; values currently pass through unchanged when shell mode is enabled (re-verify in sc-cmdi).

---

## Trust Boundaries

| Boundary | Direction | Mediator |
|----------|-----------|----------|
| User → CLI/TUI | trusted | none (full local privileges) |
| Browser → Web API | semi-trusted | localhost bind, origin check, optional token |
| External WS client → `/ws` | semi-trusted | origin check, rate limiter, ctx cancel cascade |
| LLM provider → engine | untrusted | response parsing (defensive), no `eval` |
| LLM tool call → `tools.Engine.Execute` | scoped | approval gate, hooks, panic guard |
| Plugin subprocess → engine | untrusted | JSON-RPC frame, env scrub, output cap |
| Filesystem | local | bbolt store (single OS file lock), config files |
| Network egress | provider HTTP | TLS, `web_fetch` tool |

---

## Subsystems Owning State

```
Engine
├── Storage      bbolt — single-file lock; ErrStoreLocked when contended
├── Memory       3 tiers (working/episodic/semantic), bbolt buckets
├── Conversation JSONL persisted, async save with WaitGroup drain
├── AST          tree-sitter (CGO) or regex stub
├── CodeMap      symbol/dependency graph
├── Context      ranked snippet builder, token-budgeted
├── Providers    router with primary+fallback+offline cascade
├── Tools        backend registry + meta-tool layer + read-gate
├── Hooks        user shell commands, sanitized env, panic-guarded
├── TaskStore    bbolt-backed task persistence
├── EventBus     fan-out, recover-guarded subscribers, drop-on-full
└── Drive        autonomous plan/execute loop (planner + scheduler)
```

---

## Files Worth Auditing

| Sensitive concern | Files |
|-------------------|-------|
| Path traversal | `internal/tools/engine.go` (EnsureWithinRoot, EvalSymlinks), `internal/context/injected.go`, `ui/web/server_files.go`, `ui/tui/filesystem.go` |
| Command exec | `internal/tools/builtin.go` (run_command), `internal/tools/git_runner.go`, `internal/tools/gh_runner.go`, `internal/hooks/hooks.go`, `internal/pluginexec/client.go`, `internal/tools/patch_validation.go` |
| Auth/Authz | `ui/web/server.go` (constant-time token compare), `internal/engine/approver.go` |
| WS/Origin | `ui/web/server_ws.go`, `ui/web/server.go` checkWebSocketOrigin |
| Crypto/secrets | `internal/security/env_scrub.go`, `internal/memory/store.go` (crypto/rand IDs) |
| Subprocess env | `internal/security/scrub_env.go`, `internal/mcp/client.go` |
| Race/TOCTOU | `internal/engine/engine.go` indexer cancel/wait, `internal/conversation/manager.go` saveWg drain |

---
