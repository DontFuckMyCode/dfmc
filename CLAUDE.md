# CLAUDE.md

Guidance for Claude Code (claude.ai/code) when working in this repository.

## Project

**DFMC** ("Don't Fuck My Code") is a single-binary Go code intelligence assistant. It combines local analysis (AST, codemap, security heuristics) with a multi-provider LLM router and an offline fallback. CLI, Bubble Tea TUI, and embedded Web API all drive the same `internal/engine.Engine`.

Module: `github.com/dontfuckmycode/dfmc`. Go 1.25.

## Build, test, lint

Only the `Makefile`'s `clean` (`rmdir /s /q`) and `VERSION` (`NUL`) recipes are Windows-bound; `make test`, `test-race`, `lint`, `vuln`, and `security` shell out to `go`/tooling and work anywhere. Prefer direct `go` commands for narrow loops:

```bash
CGO_ENABLED=1 go build -o bin/dfmc.exe ./cmd/dfmc
CGO_ENABLED=1 go test -race -count=1 ./...   # == make test-race
go test ./internal/engine/...
go test ./internal/engine -run TestAgentLoop -v
go vet ./...
gofmt -w $(git ls-files '*.go')
staticcheck ./...
make lint       # vet + staticcheck + golangci-lint
make security   # govulncheck + gosec
```

**CGO matters.** Tree-sitter bindings require CGO. With `CGO_ENABLED=0`, the build can still pass but AST falls back to regex (`internal/ast/backend_stub.go`); `dfmc status` / `dfmc doctor` report `ast_backend: regex`. On Windows, `CGO_ENABLED=1` and `go test -race` require `gcc` on `PATH`.

## Architecture

### Engine is the hub

`internal/engine.Engine` is constructed in `cmd/dfmc/main.go`, owns the main subsystems, and is shared by all UIs. `engine.go` handles construction/lifecycle/state; most methods live in `engine_<topic>.go` siblings. Grep `func (e *Engine)` before editing an Engine method.

Load-bearing Engine files:

- `engine_tools.go` — `CallTool` / `CallToolFromSource` outer wrapper and `tool:error` / `tool:complete` events.
- `engine_tools_lifecycle.go` — `executeToolWithLifecycle`, the required safety funnel for tool calls: subagent/skill/path gates, approval, hooks, panic guard, timeout/denial/panic events, and shared `Event.Seq` stamping. New tool entry points must route through this or `CallTool` unless a documented exception applies.
- `engine_meta_hooks.go` — unwraps `tool_call` / `tool_batch_call` so hooks and side effects reach inner backend tools.
- `engine_context.go` — context budget, recommendations, chunking, reserve breakdown.
- `engine_prompt.go` — system prompt and prompt runtime/recommendation logic.
- `engine_ask.go`, `engine_ask_stream.go`, `engine_ask_history*.go` — Ask paths, streaming, history trimming.
- `engine_intent.go` — builds intent `Snapshot` and calls `Intent.Evaluate` before Ask.
- `engine_passthrough.go` — status, memory, conversation, provider passthroughs.
- `engine_analyze.go` — analysis, dead-code/complexity passes, text strippers.

Tool lifecycle events share a monotonic `Event.Seq` from `Engine.allocToolEventSeq()`; subscribers dedupe failure telemetry on `(Type, Seq)`.

### Engine-owned subsystems

- `internal/ast` — tree-sitter with CGO, regex fallback otherwise.
- `internal/codemap` — symbol/dependency graph, cycles, hotspots, path traversal, DOT/SVG.
- `internal/context` — ranks/compresses snippets under token budget; every token sent should be justified.
- `internal/provider` — primary + fallback router. Offline provider is always registered; missing keys create graceful placeholders. Protocols: Anthropic, OpenAI, OpenAI-compatible.
- `internal/tools` — backend registry plus four meta tools: `tool_search`, `tool_help`, `tool_call`, `tool_batch_call`. Tool loop lives around `agent_loop_native.go`; parking in `agent_parking.go`; lifecycle in `engine_tools_lifecycle.go`.
- `internal/memory`, `internal/conversation`, `internal/storage` — bbolt-backed memory, JSONL conversations, store handle. A second DFMC process hits `ErrStoreLocked`; degraded commands like `doctor` still run.
- `internal/hooks` — best-effort lifecycle shell hooks; failures log but do not block.
- `internal/taskstore` — persisted `supervisor.Task` store for TODO, HTTP, and MCP APIs.
- `internal/langintel` — embedded per-language knowledge bases (Go/Java/C#/...) surfaced through engine init and `ui/web/server_langintel.go`.
- `internal/bot` — Telegram bridge wired into the Engine; controlled from `cmd/dfmc/main.go`/`startup_args.go`, the TUI `telegram_panel.go`, and web/help surfaces.
- `EventBus` — shared fan-out for TUI, web SSE, remote control.

### Tooling rules worth preserving

Context gathering order is cheapest to most precise: `grep_codebase` → `codemap` → `find_symbol` → `read_file`.

`find_symbol` is language-aware and returns full scope; `parent` disambiguates receivers/classes. `codemap` is a signatures-only outline and should generally be used once per session, not per file.

`read_file` defaults to a 200-line window and reports `total_lines`, `returned_lines`, `truncated`, and `language`. `truncated` means returned lines are fewer than total lines, even if the caller intentionally requested a slice.

Tool validation should use `missingParamError` for required params. `normalizeToolParams` handles common aliases (`edit_file` `old`/`new`; `write_file` `text`/`body`/`data`).

`tool_call` and `tool_batch_call` refuse nested meta tools. `apply_patch`, `edit_file`, and `write_file` enforce read-before-mutate; edits are lenient because exact-string anchors protect them, writes/patches are strict. Git tools reject user values starting with `-` to avoid flag injection.

### Intent layer

`internal/intent` classifies each user turn against an Engine `Snapshot` and routes to resume/new/clarify. It is fail-open: classifier errors fall back to `Fallback(raw)` and must not block the engine.

Tests that assert a specific intent decision should call `Intent.Evaluate` directly with a Snapshot. The normal engine path swallows classifier errors by design. Decisions emit `intent:decision` and the TUI shows `RESUME` / `NEW` / `CLARIFY` badges.

### UIs

UI entry files stay lean; put bodies in sibling files.

- `ui/cli/cli.go` — `Run()` and `parseGlobalFlags`. Global flags must come **before** the command (`dfmc --provider offline review ...`). Command bodies live in `cli_<domain>.go` siblings; remote clients in `cli_remote.go`.
- `ui/tui/tui.go` — Bubble Tea model/view root. Hot paths are split into `update.go`, `chat_key.go`, `chat_commands.go`, `engine_events.go`, `intent.go`, `drive.go`, etc. Panel state belongs in `panel_states.go`, not flat fields on `Model`.
- TUI gotchas: paste blocks use placeholders and `composeInput()` reconstruction; `/tools` expands collapsed tool chips; tests asserting chips must set `m.ui.toolStripExpanded = true`; `DFMC_KEYLOG=1` or `/keylog` dumps key events.
- `ui/web/server.go` — route setup and lifecycle for HTTP/SSE on 7777. Handler bodies live in `server_<domain>.go`; static workbench is embedded from `ui/web/static/index.html`.

When adding a new CLI command, keep CLI/web/remote surfaces in sync by convention: add the CLI switch, command body, web route/handler, and `dfmc remote <cmd>` client.

### Config and prompts

Config merge order: built-in defaults → `~/.dfmc/config.yaml` → `<project>/.dfmc/config.yaml` → env vars → CLI flags. Project `.env` is auto-loaded, but process env wins. `DFMC_KEYLOG` is read directly by TUI lifecycle code.

Use `dfmc config sync-models` to refresh `providers.profiles.*` from `https://models.dev/api.json` while preserving keys.

Important knobs live under `agent.*`: `max_tool_steps`, `max_tool_tokens`, result char caps, `parallel_batch_size`, `meta_call_budget`, `meta_depth_limit`, read snapshot/failure caps, round soft/hard caps, autonomous resume, tool reasoning, and context lifecycle thresholds. Zero values fall back to `internal/config/defaults.go`.

Prompt library load order: embedded `internal/promptlib/defaults/*.yaml`, global `~/.dfmc/prompts`, project `.dfmc/prompts`. Formats: YAML, JSON, Markdown with frontmatter. Composition supports `replace` / `append`.

User queries can inject context with `[[file:path]]`, `[[file:path#L10-L80]]`, and fenced code blocks. This is used by ask/chat/review/explain/TUI/web chat.

### Drive

`dfmc drive "<task>"` and TUI `/drive <task>` run an autonomous plan/execute loop. The planner creates a TODO DAG; the scheduler executes ready TODOs through `engine.RunSubagent`, using fresh sub-conversations to bound context.

Key files: `internal/drive/planner.go`, `scheduler.go`, `driver.go`, `run_planner.go`, `run_executor.go`, `run_drainer.go`, `persistence.go`; engine adapter in `internal/engine/drive_adapter.go`.

Provider routing maps planner tags (`plan|code|review|test|research`) through `Config.Routing`; unmapped tags fall back to the default provider. Drive emits `drive:*` events through the Engine bus and is surfaced in CLI, TUI, web, and MCP.

MCP Drive tools (`dfmc_drive_start/status/active/list/stop/resume`) live in `ui/cli/cli_mcp_drive.go` and route through `driveMCPHandler`, not `engine.CallTool`, to avoid recursive LLM/tool approval paths. This is a documented lifecycle bypass.

### Adjacent packages

- `internal/coach` — trajectory hints from tool-call traces.
- `internal/supervisor` — shared task/executor shapes for Drive, TODO, taskstore.
- `internal/mcp` — MCP server/bridge and regular tool registry exposure.
- `internal/pathsafe` — single canonical "is this path contained in that root" check (symlink-aware). Leaf package with no `internal/*` deps so both the tools read/write/edit gate and the `[[file:...]]` resolver can import it without a cycle. Use it, don't reimplement path containment.
- `internal/providerlog`, `internal/applog`, `internal/toolhistory` — durable JSONL audit/state under the data dir: `providerlog` records every `provider:complete` user→assistant turn; `applog` is the app event log; `toolhistory` persists learned coding patterns (its old tool-call logger was removed as dead code).
- `internal/taskview` — renders inline `/tasks` views over `taskstore`/`supervisor`.
- `internal/security`, `internal/skills`, `internal/planning`, `internal/pluginexec`, `internal/commands`, `internal/tokens` — feature packages; grep symbols before assuming shape.

## Per-project state

`.dfmc/` is gitignored project state, not source. Do not commit normal changes from it. It may contain config overrides, knowledge/conventions, `magic/MAGIC_DOC.md`, and bbolt stores.

`.project/` holds gitignored design specs. `docs/` is committed but historical; verify docs against code before relying on them.

## Things that bite

- Missing `CGO_ENABLED=1` silently downgrades AST to regex.
- Global flags after the subcommand are not parsed as globals.
- `internal/repolint` tests scan the whole tree for deliberately banned patterns; read the failing assertion before changing the test.
- Two DFMC processes on one project cause `ErrStoreLocked`; degraded commands like `doctor` still work.
- Engine methods usually live in `engine_*.go`, not `engine.go`.
- Engine/UI tests often build temp `.dfmc/` fixtures; mirror existing patterns. Provider-dependent tests usually use `scriptedProvider` for deterministic tool calls.
- Tool-strip chips are collapsed by default in TUI tests unless `m.ui.toolStripExpanded = true`.
- New tool-surface entry points must use `executeToolWithLifecycle` or `CallTool`. Existing exceptions: MCP Drive tools and `Engine.TodosFromSpecFile` calling pure read-only `SpecToTodoTool` during partial init paths.
- Intent is fail-open; do not introduce hard-fail paths in `internal/intent/router.go`.
- A real tool timeout can emit `tool:error`, `tool:timeout`, and failed `tool:result`; treat `tool:result` as the canonical failure signal.
- Use typed sentinel errors with `errors.Is`, not string matching: engine errors like `ErrEngineNil`, `ErrEngineNotInitialized`, `ErrNoParkedAgent`, `ErrSubagentConcurrencyLimit`; tool errors like `ErrEngineClosed`, `ErrMetaBudgetExhausted`, `ErrMetaDepthExceeded`.
- Path containment goes through `internal/pathsafe`, never a hand-rolled `strings.HasPrefix` on cleaned paths; it is the shared boundary for the tool write gate and the `[[file:...]]` injector.

## Project structure

```text
cmd/dfmc              # binary entrypoint
internal/engine       # orchestration, agent loop, lifecycle gate
internal/config       # config loading/defaults/validation
internal/storage      # bbolt store
internal/ast          # tree-sitter + regex fallback
internal/codemap      # dependency/symbol graph
internal/context      # ranked context builder
internal/provider     # provider router/protocols
internal/tools        # registry, meta tools, approval/read gates
internal/drive        # autonomous plan/execute loop
internal/intent       # request normalizer/router
internal/hooks        # lifecycle shell hooks
internal/coach        # loop trajectory hints
internal/taskstore    # persisted tasks/TODOs
internal/mcp          # MCP server/bridge
internal/security     # security scanner
internal/repolint     # CI grep tripwires
internal/promptlib    # prompt library
internal/conversation # JSONL conversations
internal/memory       # memory tiers
internal/langintel    # per-language knowledge bases
internal/bot          # Telegram bridge
internal/pathsafe     # canonical path-containment check
internal/providerlog  # provider-turn JSONL audit
internal/applog       # app event log
internal/toolhistory  # learned-pattern persistence
internal/taskview     # inline /tasks rendering
ui/cli                # CLI and remote client
ui/tui                # Bubble Tea workbench
ui/web                # HTTP/SSE workbench
pkg/types             # shared types/errors
```

<!-- dfmt:v1 begin -->
## Context Discipline

Prefer DFMT MCP tools over native tools for outputs that may be large or reused:

| Native | DFMT replacement |
| --- | --- |
| `Bash` | `dfmt_exec` |
| `Read` | `dfmt_read` |
| `WebFetch` | `dfmt_fetch` |
| `Glob` | `dfmt_glob` |
| `Grep` | `dfmt_grep` |
| `Edit` | `dfmt_edit` |
| `Write` | `dfmt_write` |

Every `dfmt_*` read/fetch/search/exec call must include an `intent` phrase describing the needed output. On DFMT failure, report the failed call briefly, then fall back to native tools; record a `dfmt_remember` `gap` note when practical. Native `Bash` and `Read` are acceptable for known-small outputs (<2 KB) that will not be reused.

After substantive decisions or findings, call `dfmt_remember` with tags such as `decision`, `finding`, or `summary`.
<!-- dfmt:v1 end -->

<!-- self-learn:v1 begin -->
## Self-Learning Mode

Keep the system prompt minimal. When you spot an improvement opportunity, say:

> `[PROMPT IMPROVEMENT] In this task we could have used this pattern more efficiently: ...`

Then add the learned pattern to notes, not the system prompt.

### Saved Patterns

- [2025-07-01] `shell/homebrew-tapFormula.rb` deletion: `git rm` gives `index.lock` error on Windows → delete file via OS, then `git add -u` to stage
<!-- self-learn:v1 end -->
