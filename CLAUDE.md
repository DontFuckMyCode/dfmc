# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

**DFMC** ("Don't Fuck My Code") — a code intelligence assistant distributed as a single Go binary. It combines local code analysis (AST + codemap + security heuristics) with a multi-provider LLM router that falls back to an offline provider when API keys are missing or calls fail. Three UIs (CLI, bubbletea TUI, embedded Web API) all drive the same `engine.Engine`.

Module path: `github.com/dontfuckmycode/dfmc`. Go 1.24.

## Build, test, lint

The `Makefile` is Windows-oriented (uses `NUL`, `rmdir /s /q`). Prefer invoking `go` directly in bash:

```bash
# Build (CGO required for tree-sitter AST — see below)
CGO_ENABLED=1 go build -o bin/dfmc.exe ./cmd/dfmc

# Fast dev run (no binary)
go run ./cmd/dfmc <command> [args]

# Full test suite (what Makefile uses)
CGO_ENABLED=1 go test -race -count=1 ./...

# Single package / single test
go test ./internal/engine/...
go test ./internal/engine -run TestAgentLoop -v

# Lint / format
go vet ./...
gofmt -w .
```

**CGO matters.** Tree-sitter bindings (`tree-sitter-go`, `-javascript`, `-typescript`, `-python`) require CGO. With `CGO_ENABLED=0` the build still succeeds but AST silently falls back to the regex extractor in `internal/ast/backend_stub.go`, and `dfmc status` / `dfmc doctor` will report `ast_backend: regex`. If AST behavior looks wrong, check the backend before blaming the code.

## Architecture

### Engine is the hub

`internal/engine.Engine` (constructed in [cmd/dfmc/main.go](cmd/dfmc/main.go)) owns every subsystem and is passed by pointer into all three UIs.

The Engine type itself is split by domain across sibling files; [engine.go](internal/engine/engine.go) keeps construction/lifecycle/state and the rest live in:

- [engine_tools.go](internal/engine/engine_tools.go) — `CallTool`, panic-guarded execution, approval/hooks lifecycle
- [engine_context.go](internal/engine/engine_context.go) — context budget/recommendations/tuning + chunk building + reserve breakdown
- [engine_prompt.go](internal/engine/engine_prompt.go) — `buildSystemPrompt`, `PromptRecommendation*`, `promptRuntime*`
- [engine_ask.go](internal/engine/engine_ask.go) — `Ask`, `AskRaced`, `AskWithMetadata`, `StreamAsk`, history trimming
- [engine_passthrough.go](internal/engine/engine_passthrough.go) — `Status`, memory/conversation/provider passthrough surface
- [engine_analyze.go](internal/engine/engine_analyze.go) — `AnalyzeWithOptions`, dead-code/complexity passes, text strippers

Subsystems owned by the Engine:

- `AST` ([internal/ast](internal/ast/)) — tree-sitter when CGO is on, regex fallback otherwise. Parse metrics are tracked per-call.
- `CodeMap` ([internal/codemap](internal/codemap/)) — symbol/dependency graph built on top of AST; supports cycles, hotspots, path traversal, DOT/SVG export.
- `Context` ([internal/context/manager.go](internal/context/manager.go)) — ranks and compresses file snippets under a token budget before the LLM sees them. Core design principle: **every token sent is justified**.
- `Providers` ([internal/provider/router.go](internal/provider/router.go)) — router with a primary + fallback list. The offline provider is always registered; missing API keys yield a `PlaceholderProvider` that degrades gracefully instead of erroring. Protocols: `anthropic`, `openai`, `openai-compatible` (covers deepseek/kimi/zai/alibaba/generic/ollama).
- `Tools` ([internal/tools](internal/tools/)) — registry of `read_file`, `write_file`, `edit_file`, `list_dir`, `grep_codebase`, `run_command`. Tool-capable providers invoke these through a bounded agent loop in [internal/engine/agent_loop_native.go](internal/engine/agent_loop_native.go); park-and-resume semantics live in [agent_parking.go](internal/engine/agent_parking.go).
- `Memory` ([internal/memory/store.go](internal/memory/store.go)) — working + episodic + semantic tiers in bbolt.
- `Conversation` ([internal/conversation/manager.go](internal/conversation/manager.go)) — JSONL-persisted conversations with branching.
- `Storage` ([internal/storage/store.go](internal/storage/store.go)) — bbolt handle. Returns `ErrStoreLocked` when another DFMC process holds it; `cmd/dfmc/main.go` has a degraded-startup allow-list (`help`, `version`, `doctor`, `completion`, `man`) that runs without init.
- `EventBus` — fan-out used by TUI, web `/ws` SSE stream, and remote control.

### UIs

All three UI packages keep their entry/dispatch file lean and split feature code into sibling files. When adding a command/handler, find the right sibling rather than dropping it into the entry file.

- [ui/cli/cli.go](ui/cli/cli.go) (~175 lines) — only `Run()` and `parseGlobalFlags`. **Global flags must come BEFORE the command** (`dfmc --provider offline review ...`), because `parseGlobalFlags` stops at the first non-flag token. Subcommand bodies live in [cli_admin.go](ui/cli/cli_admin.go), [cli_ask_chat.go](ui/cli/cli_ask_chat.go), [cli_analysis.go](ui/cli/cli_analysis.go), [cli_remote.go](ui/cli/cli_remote.go), [cli_plugin_skill.go](ui/cli/cli_plugin_skill.go), [cli_output.go](ui/cli/cli_output.go), [cli_utils.go](ui/cli/cli_utils.go).
- [ui/tui/tui.go](ui/tui/tui.go) (~4400 lines) — bubbletea Model/View root. The hot paths split out: [update.go](ui/tui/update.go) (reducer), [chat_key.go](ui/tui/chat_key.go) (chat composer keyboard router), [chat_commands.go](ui/tui/chat_commands.go) (slash dispatcher), [engine_events.go](ui/tui/engine_events.go) (engine event handler).
- [ui/web/server.go](ui/web/server.go) (~215 lines) — `New`, `setupRoutes`, lifecycle. HTTP+SSE on port 7777 (`dfmc serve`). Handlers split by domain: [server_status.go](ui/web/server_status.go), [server_chat.go](ui/web/server_chat.go), [server_context.go](ui/web/server_context.go), [server_tools_skills.go](ui/web/server_tools_skills.go), [server_conversation.go](ui/web/server_conversation.go), [server_workspace.go](ui/web/server_workspace.go), [server_files.go](ui/web/server_files.go), [server_admin.go](ui/web/server_admin.go) (scan/doctor/hooks/config). Workbench HTML lives in [ui/web/static/index.html](ui/web/static/index.html) and is loaded via `//go:embed`.
- `dfmc remote` subcommands in [cli_remote.go](ui/cli/cli_remote.go) are clients against a running `dfmc serve` (or `dfmc remote start` which launches gRPC+WS on 7778/7779).

### Config hierarchy

[internal/config/config.go](internal/config/config.go) merges: built-in defaults → `~/.dfmc/config.yaml` → `<project>/.dfmc/config.yaml` → env vars (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `DEEPSEEK_API_KEY`, `KIMI_API_KEY`, `ZAI_API_KEY`, `ALIBABA_API_KEY`, `MINIMAX_API_KEY`, `GOOGLE_AI_API_KEY`) → CLI flags. Project-root `.env` is auto-loaded at startup (process env still wins).

`dfmc config sync-models` rewrites the `providers.profiles.*` block from https://models.dev/api.json, preserving API keys. Whenever the provider catalog looks stale, use sync-models rather than editing by hand.

The native tool loop has tunable knobs under `agent.*`: `max_tool_steps`, `max_tool_tokens`, `max_tool_result_chars`, `max_tool_result_data_chars`, plus `tool_round_soft_cap` (synthesis nudge), `tool_round_hard_cap` (force `tool_choice=none`), and `budget_headroom_divisor` (preflight margin). All zero-defaults fall back to the values in [internal/config/defaults.go](internal/config/defaults.go); raise the caps for high-context models (1M-window Opus, etc.) instead of fighting the defaults.

### Prompt library

[internal/promptlib](internal/promptlib/) loads from:
1. `internal/promptlib/defaults/*.yaml` (embedded)
2. `~/.dfmc/prompts` (global overrides)
3. `.dfmc/prompts` (project overrides)

Formats: `.yaml`, `.json`, `.md` (YAML frontmatter + body). Composition via `compose: replace | append`. Task/role/language axes pick overlays. Rendered prompts are token-budgeted against the runtime provider's `max_context`.

### Context injection from the user query

`[[file:path/to/file.go]]` and `[[file:path#L10-L80]]` markers in any user query are resolved, budgeted, and injected into the system prompt as compressed snippets. Triple-backtick fenced blocks in the query are treated as explicit injected context. Both are used by `ask`, `chat`, `review`, `explain`, TUI chat, and the web `/api/v1/chat` SSE.

## Per-project state

`.dfmc/` (gitignored) is **project state, not source** — do not commit changes to it as part of normal work:

- `config.yaml` — project overrides (provider profiles, context budgets, tool allowlist, shell timeouts/blocked commands)
- `knowledge.json`, `conventions.json` — populated by `dfmc init` and `dfmc analyze`
- `magic/MAGIC_DOC.md` — low-token project brief auto-injected into system prompts when present (`dfmc magicdoc update|show`)
- bbolt files (memory, conversations) — these are why only one `dfmc` process at a time can open the store; see `ErrStoreLocked` handling in main.go

`.project/` holds design specs (`SPECIFICATION.md`, `IMPLEMENTATION.md`, `TASKS.md`, `BRANDING.md`) — also gitignored but useful reading for architectural intent.

## Things that bite

- Forgetting `CGO_ENABLED=1` silently downgrades AST to regex — no error.
- Putting global flags after the subcommand (`dfmc review --provider offline ...`) — they'll be passed to the command, not `parseGlobalFlags`.
- Two `dfmc` processes on the same project: second one hits `ErrStoreLocked`. `dfmc doctor` is whitelisted for degraded startup so it still runs.
- When adding a new CLI command: add the case to the `switch cmd` block in [ui/cli/cli.go](ui/cli/cli.go), put the body in the matching `cli_<domain>.go` sibling, register the corresponding `/api/v1/*` handler in [ui/web/server.go](ui/web/server.go) `setupRoutes` with the body in `server_<domain>.go`, and add a `dfmc remote <cmd>` client in [cli_remote.go](ui/cli/cli_remote.go). The four layers are kept in sync by convention, not by codegen.
- When modifying an Engine method: it lives in one of the `engine_*.go` siblings, not `engine.go` itself. Use Grep to find it.
- Tests under `internal/engine` and `ui/cli` frequently construct a temp project with `.dfmc/` scaffolding; mirror the existing patterns rather than hand-rolling new fixtures.
