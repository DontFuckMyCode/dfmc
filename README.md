# DFMC — Don't Fuck My Code

<p align="center">
  <img src="assets/dfmc_banner.png" alt="DFMC Banner" width="100%">
</p>

DFMC is a single-binary code intelligence assistant written in Go. It
combines local code analysis (tree-sitter AST, dependency graph,
security taint pass) with a multi-provider LLM router behind one tool
loop, exposed through five surfaces: CLI, TUI, HTTP/SSE, remote
client, and MCP server. Alpha-stage software; pre-1.0 minor bumps may
include breaking changes.

## Surface model

DFMC is TUI-first. The Bubble Tea workbench is the reference operator
surface for terminology, slash-command behavior, panels, and workflow
shape. CLI and the React 19 WebUI should match the TUI whenever the
medium allows it, preferably by sharing the same package, formatter,
API, or store semantics. When a feature is genuinely interactive-only,
the other surfaces should say so explicitly and point at the nearest
equivalent command instead of silently drifting.

## Install

Build from source (Go `1.25.0`+):

```bash
go build -o bin/dfmc ./cmd/dfmc            # regex AST, works everywhere
CGO_ENABLED=1 go build -o bin/dfmc ./cmd/dfmc   # tree-sitter AST (Go/JS/TS/Python)
```

Without CGO the binary still runs but the AST layer falls back to
regex extraction. `dfmc doctor` reports the active backend.

Released versions ship as cross-platform archives, a GHCR Docker
image, and a Homebrew tap — see [CHANGELOG.md](CHANGELOG.md) and
the [Releases](https://github.com/dontfuckmycode/dfmc/releases) page.

## First run

```bash
dfmc init           # creates .dfmc/ with config.yaml + scaffolding
dfmc doctor         # diagnostics: providers, keys, AST backend, store
dfmc status         # one-line summary
dfmc ask "explain this project"
```

`dfmc init` is optional — most commands auto-create missing project
state at startup.

## Provider keys

Two supported ways to set provider API keys. **Process env still
works** but it is no longer the recommended path.

**Inside TUI (`dfmc tui`)** — interactive, masked input, persisted:

```
/key list                            # status of every provider + source
/key set anthropic sk-ant-…          # writes ~/.dfmc/config.yaml (0600)
/key clear anthropic
/key migrate                         # imports existing .env keys
```

**From the CLI**:

```bash
dfmc config set --global providers.profiles.anthropic.api_key sk-ant-…
```

`--global` writes to `~/.dfmc/config.yaml`; without it, writes to
project `.dfmc/config.yaml`. Config merge order:
**defaults → `~/.dfmc/config.yaml` → project `.dfmc/config.yaml` →
process env → CLI flags** (later wins). The persisted files are the
recommended path because they survive shell restarts and the TUI
unmasks them on `/key list`.

Project-root `.env` is read at startup only to fill *missing*
process env slots — it never overrides anything already set. Treat
it as legacy compatibility, not a fresh-setup path.

Known provider env-var aliases (recognised when present):
`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GOOGLE_AI_API_KEY`,
`DEEPSEEK_API_KEY`, `KIMI_API_KEY` (or `MOONSHOT_API_KEY`),
`MINIMAX_API_KEY`, `ZAI_API_KEY`, `ALIBABA_API_KEY`.

## CLI

Run `dfmc help` for the live command index. Selected entries:

| Group | Commands |
|---|---|
| Query | `ask`, `chat`, `tui`, `review`, `explain`, `refactor`, `debug`, `test`, `doc`, `audit`, `onboard` |
| Analysis | `analyze`, `scan`, `map`, `context`, `magicdoc` |
| Tools | `tool list`, `tool show <name>`, `tool run <name> [--param k=v]` |
| Config | `config list\|get\|set\|sync-models\|edit` |
| State | `memory`, `conversation`, `agents`, `drive`, `prompt`, `skill`, `plugin` |
| Serve | `serve`, `remote`, `mcp` |
| Ops | `doctor`, `hooks`, `approvals`, `status`, `version`, `update`, `completion`, `man` |

Global flags must precede the subcommand:

```bash
dfmc --provider offline ask "summarise local context"
dfmc --json status
dfmc --data-dir .dfmc-alt doctor
```

Race two providers for the same prompt:

```bash
dfmc ask --race --race-providers anthropic,openai "compare reasoning"
```

## TUI

```bash
dfmc tui
```

| Key | Panel |
|---|---|
| `F1` | Chat (composer + transcript) |
| `F2` | Files |
| `F3` | Patch |
| `F4` | Workflow |
| `F5` | Activity |
| `F6` | Memory |
| `F7` | Conversations |
| `F8` | Providers |
| `F9` | Status |
| `F10` | CodeMap |
| `F11` | Tools |
| `F12` | Security |
| `Shift+F1..F8` | Prompts, Plans, Context, Orchestrate, Shortcuts, Contexts, ProviderLog, Telegram |
| `Ctrl+Shift+T` / `Alt+O` | ToolStatus |
| `Ctrl+B` | Fuzzy panel switcher |
| `Ctrl+P` | Slash-command palette |
| `Alt+1..8` | Mirror `F1..F8` (for terminals that swallow F-keys) |

Type `/help` inside Chat for the live slash-command list.

Task-store views are exposed as `/tasks list`, `/tasks tree`,
`/tasks roots`, `/tasks show <id>`, and `/tasks clear`. The CLI chat
slash layer and WebUI Tasks panel intentionally mirror those views.

## HTTP, remote, MCP

```bash
dfmc serve --host 127.0.0.1 --port 7777
DFMC_WEB_TOKEN=secret dfmc serve --auth token

DFMC_REMOTE_TOKEN=secret dfmc remote start --auth token --ws-port 7779
dfmc remote ask --url http://127.0.0.1:7779 --token secret --message "…"

dfmc mcp           # MCP server on stdio for IDE hosts
```

The embedded WebUI is built from the React 19/Vite app under
`ui/web/src`. It uses Tailwind CSS v4, shadcn-style local primitives,
lucide-react icons, and a responsive dark/light theme while keeping the
same TUI-first surface contract:

```bash
cd ui/web
npm install
npm run check
npm run build
```

`npm run build` writes the embedded assets to `ui/web/static/`.

`serve` and `remote start` share the same HTTP+WebSocket API.
Non-loopback hosts refuse `--auth=none` unless `--insecure` is
passed. MCP exposes the regular tool registry plus six synthetic
Drive control tools.

## Context injection in queries

Both work in `ask`, `chat`, `review`, the TUI composer, and the web
`/api/v1/chat` SSE:

```
[[file:internal/auth/middleware.go]]
[[file:internal/auth/middleware.go#L10-L80]]
```

Triple-backtick fenced blocks inside the query are also pulled in as
explicit context.

## Defaults worth knowing

From [internal/config/defaults.go](internal/config/defaults.go):

```yaml
context:
  max_files: 20
  max_tokens_total: 12000
  max_tokens_per_file: 2000
  max_history_tokens: 24000
  max_history_messages: 120

agent:
  max_tool_steps: 60
  max_tool_tokens: 250000
  max_tool_result_chars: 3200
  parallel_batch_size: 4
  autonomous_resume: auto
  auto_continue: auto
```

Operational env vars (still honoured): `DFMC_WEB_TOKEN`,
`DFMC_REMOTE_TOKEN`, `DFMC_TELEGRAM_TOKEN`,
`DFMC_TELEGRAM_ALLOWED_USERS`, `DFMC_APPROVE`,
`DFMC_APPROVE_DESTRUCTIVE`, `DFMC_NO_COLOR`, `NO_COLOR`,
`DFMC_PROFILE`, `VISUAL`, `EDITOR`.

## Hooks and safety

Lifecycle shell hooks (`user_prompt_submit`, `pre_tool`, `post_tool`,
`session_start`, `session_end`) fire best-effort and never block a
tool call. Project-local hooks are **disabled** by default; enable
per-repo with `hooks.allow_project: true` only after auditing the
hook list.

Agent-initiated write / shell / network-sensitive tools route through
the approval gate. CLI prompts on stdin; TUI surfaces a modal; web
and MCP surfaces require pre-authorised allowlists (they can't ask
interactively).

Secret-shaped parent env vars are scrubbed before hook and plugin
child processes. Override with `DFMC_UNSAFE_HOOKS=1` only when the
hook genuinely needs writable config access.

## Build, test, lint

```bash
go test -count=1 ./...
CGO_ENABLED=1 go test -race -count=1 ./...      # requires gcc on PATH
go vet ./...
staticcheck ./...
gofmt -l $(git ls-files '*.go')
```

CI gates on `go vet` + `staticcheck`. The bundled `Makefile` is
Windows-oriented (`NUL`, `rmdir /s /q`); direct `go` invocations are
the portable path.

Additional tooling (optional):

```bash
golangci-lint run          # comprehensive linting (see .golangci.yml)
govulncheck ./...          # dependency vulnerability scan
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for the full development workflow,
branch naming, and PR process. See [SECURITY.md](SECURITY.md) for
vulnerability reporting and security best practices.

## Project layout (selected)

```
cmd/dfmc              binary entrypoint
internal/ast          tree-sitter + regex AST extraction
internal/codemap      symbol / dependency graph
internal/config       config loading, defaults, env hydration
internal/conversation JSONL conversations + branches
internal/context      ranked snippet selection, prompt budgeting
internal/drive        autonomous plan / execute runner
internal/engine       orchestration, agent loop, lifecycle
internal/hooks        lifecycle shell hooks
internal/intent       state-aware turn classifier
internal/memory       working / episodic / semantic tiers
internal/mcp          MCP server + bridge
internal/provider     provider implementations + router
internal/security     scanner (regex + AST taint)
internal/skills       skill registry
internal/storage      bbolt store
internal/tools        tool registry + meta-tool layer
ui/cli                CLI surface
ui/tui                Bubble Tea TUI
ui/web                HTTP / SSE / WebSocket server
pkg/types             shared public types
```

Other supporting packages live under `internal/` (applog, bot, coach,
commands, errors, langintel, pathsafe, planning, pluginexec, promptlib,
providerlog, repolint, sessionutil, supervisor, taskstore, tokens).
For a deeper architecture map see [CLAUDE.md](CLAUDE.md).

## Specification-Driven Development (SDD)

The `sdd` skill (`internal/skills/sdd/`) provides autonomous
specification-driven development: it clarifies vague requests through
AI-guided questions, produces a `SPEC.md`, generates implementation
tasks, and executes them in order until done — like Drive agent but
full-cycle. Activate via the skill system or `/skill sdd` in the TUI.

## License

MIT — see [LICENSE](LICENSE).
