# Changelog

All notable user-facing changes to DFMC are recorded here. The format
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and
versions follow [Semantic Versioning](https://semver.org/).

DFMC is alpha-stage software. Pre-1.0 minor bumps may include
breaking changes; pre-1.0 patch bumps stay backwards-compatible.

## [Unreleased]

## [0.1.0] - 2026-05-16

First tagged release. The binary has been in daily use; this version
freezes a known-good snapshot for distribution.

### Added — user surfaces

- **CLI** with `ask`, `chat`, `tui`, `analyze`, `scan`, `map`, `drive`,
  `serve`, `remote`, `mcp`, `tool`, `config`, `context`, `memory`,
  `conversation`, `skill`, `plugin`, `prompt`, `magicdoc`, `doctor`,
  `update`, `completion`, `man`, `version`.
- **TUI workbench** (Bubble Tea) with 17 panels reachable via
  `F1`-`F12` + `Shift+F1`-`F5`; chat composer, paste blocks, tool-chip
  collapse, TODO strip, sub-agent badges, Drive cockpit.
- **HTTP/SSE server** (`dfmc serve`) and **remote client** (`dfmc remote`)
  sharing one HTTP+WebSocket API with optional bearer auth.
- **MCP server** (`dfmc mcp`) exposing the tool registry plus six Drive
  control tools for IDE hosts (Claude Desktop, Cursor, VS Code).
- **Telegram bot** integration.

### Added — engine

- **Provider router** with primary + fallback cascade across
  Anthropic, OpenAI, Google, DeepSeek, Kimi/Moonshot, MiniMax, Zhipu,
  Alibaba, generic OpenAI-compatible, and an offline degradation path.
  Profile catalog seeded from models.dev (refreshable via
  `dfmc config sync-models`).
- **Native tool loop** with provider-native tool calling
  (Anthropic `tool_use`, OpenAI `tool_calls`, Google function-calling),
  meta-tool layer (`tool_call` / `tool_batch_call` / `tool_search` /
  `tool_help`), parallel batches, approval gate, per-target
  read-before-mutation, panic guard, denial telemetry.
- **Drive** — autonomous plan/execute runner. Planner LLM produces a
  TODO DAG; scheduler walks ready batches through `RunSubagent` with
  file-scope conflict avoidance, MaxParallel cap, MaxFailedTodos
  stop condition, per-tag provider routing.
- **Intent layer** — state-aware sub-LLM normalises follow-up turns
  ("fix it", "do that again") into resume / new / clarify decisions.
  Fail-open by default.
- **Coach** — trajectory hint generator that surfaces "you might be
  going in circles" warnings derived from recent tool-call traces.
- **Hooks** — user-configurable shell hooks on `user_prompt_submit`,
  `pre_tool`, `post_tool`, `session_start`, `session_end`. Best-effort,
  per-hook timeout, never blocks the loop.
- **Skills + plugins** — installable skill registry; plugin execution
  runtime (Go, script, WASM).
- **Memory** — working / episodic / semantic tiers in bbolt; search,
  list, add, clear.
- **Conversations** — JSONL persistence, branches, compare, undo,
  load.
- **Auto-resume** — when a turn hits its tool-budget cap, the engine
  force-compacts and re-enters the loop transparently.
- **CtxTokenCounter optional capability** — providers that have an
  upstream `:countTokens` endpoint (today: Google Gemini) can expose
  precise counts without breaking the fat Provider interface.

### Added — code intelligence

- **AST engine** — tree-sitter (CGO) for Go, JS/JSX, TS/TSX, Python;
  regex fallback for the same languages plus Rust, Ruby, Java when
  CGO is off. ParseResult carries `Symbols[]`, `Imports[]`,
  `ImportAliases[]`, `Errors[]`, `Hash`, `Backend`. Per-call metrics.
- **Stable Walk API** for rule authors over symbol lists.
- **Workspace symbol index** with cross-file call resolution
  (same-file > unique > ambiguous-nil tiebreakers).
- **Import alias resolution** as `(Module, Symbol, Local)` triplets
  for Python, JS/TS, Rust, Go.
- **CodeMap** — symbol/dependency graph; cycles, hotspots, path
  traversal; DOT/SVG export.
- **Context manager** — ranks and compresses file snippets under a
  token budget before the LLM sees them.
- **Security scanner** — regex pass plus AST taint pass.
  - Taint analysis for Go, Python, JS/TS across command-execution,
    SQL-injection, and path-traversal sinks.
  - Function-scope taint isolation (no false positives from
    same-named idents in unrelated functions).
  - React inline-HTML sinks, Vue `v-html`, Angular
    `bypassSecurityTrust*`.
  - Ruby (`system`, `Open3`, `eval`, `Marshal.load`, ActiveRecord raw
    SQL) and Java (Runtime shell, JDBC family, `ObjectInputStream`,
    `ScriptEngine.eval`, XXE, weak hash) rules.

### Added — release machinery

- **CI** (`.github/workflows/ci.yml`) gates `go vet` + `staticcheck`
  on every push.
- **Release** (`.github/workflows/release.yml`) builds cross-platform
  binaries via GoReleaser on `v*` tag push.
- **GoReleaser** matrix: Linux + macOS CGO builds, Windows non-CGO
  build. GHCR Docker image. Homebrew tap auto-publish.
- **Shell completions** (`dfmc completion`) for bash/zsh/fish/PowerShell.
- **Man pages** (`dfmc man --format markdown`).
- **`dfmc update`** subcommand prints a per-OS upgrade command.

### Fixed

- **CGO build break** — `internal/ast/treesitter_cgo.go` referenced a
  non-existent `types.Location` literal on 11 sites since 2026-05-05.
  CGO_ENABLED=1 builds now compile cleanly.
- **Symbol field parity** — tree-sitter extractors now populate
  `Column` (from `StartPoint().Column`) and `Signature` (slice up to
  body block) matching what the regex fallback already produced.
- **Tool approval funnel** — every tool call routes through
  `executeToolWithLifecycle` (approval gate + pre/post hooks +
  panic guard + denial telemetry). Two documented exceptions: MCP
  Drive surface (recursive-LLM avoidance) and `spec_to_todo` helper
  (engine may not be `StateReady`).
- **`tool_call`/`tool_batch_call` meta nesting** — refuse to dispatch
  other meta tools; auto-unwrap a single layer of `tool_call`
  wrapping a backend tool.
- **Read-gate modes** — `edit_file` uses lenient gate (anchor check
  is sufficient); `write_file` and `apply_patch` use strict gate
  (no anchor safety net).
- **Git tool flag injection** — every `ref` / `revision` / `branch` /
  `path` arg rejects `-`-prefix values (CVE-2018-17456 class).
- **API keys moved to user-home** — `/key` slash and `dfmc config`
  write to `~/.dfmc/config.yaml` (mode 0600); `.env` retained as
  load-time fallback only.

### Known limitations

- Tree-sitter AST requires `CGO_ENABLED=1` with a C toolchain on
  PATH. Without it, the binary still builds and runs but falls back
  to regex extraction (`dfmc doctor` reports `ast_backend: regex`).
- Only one `dfmc` process at a time can open the project bbolt
  store; the second hits `ErrStoreLocked`. `dfmc doctor` is
  whitelisted for degraded startup so diagnostics still work.
- Global flags must precede the subcommand:
  `dfmc --provider offline ask "..."`, NOT
  `dfmc ask --provider offline "..."`.
- Security and dead-code findings are heuristic; intended for
  triage, not gating.

[Unreleased]: https://github.com/dontfuckmycode/dfmc/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/dontfuckmycode/dfmc/releases/tag/v0.1.0
