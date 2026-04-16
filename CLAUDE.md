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

`internal/engine.Engine` (constructed in [cmd/dfmc/main.go](cmd/dfmc/main.go)) owns every subsystem and is passed by pointer into all three UIs:

- `AST` ([internal/ast](internal/ast/)) — tree-sitter when CGO is on, regex fallback otherwise. Parse metrics are tracked per-call.
- `CodeMap` ([internal/codemap](internal/codemap/)) — symbol/dependency graph built on top of AST; supports cycles, hotspots, path traversal, DOT/SVG export.
- `Context` ([internal/context/manager.go](internal/context/manager.go)) — ranks and compresses file snippets under a token budget before the LLM sees them. Core design principle: **every token sent is justified**.
- `Providers` ([internal/provider/router.go](internal/provider/router.go)) — router with a primary + fallback list. The offline provider is always registered; missing API keys yield a `PlaceholderProvider` that degrades gracefully instead of erroring. Protocols: `anthropic`, `openai`, `openai-compatible` (covers deepseek/kimi/zai/alibaba/generic/ollama).
- `Tools` ([internal/tools](internal/tools/)) — registry of `read_file`, `write_file`, `edit_file`, `list_dir`, `grep_codebase`, `run_command`. Tool-capable providers invoke these through a bounded agent loop in [internal/engine/agent_loop.go](internal/engine/agent_loop.go).
- `Memory` ([internal/memory/store.go](internal/memory/store.go)) — working + episodic + semantic tiers in bbolt.
- `Conversation` ([internal/conversation/manager.go](internal/conversation/manager.go)) — JSONL-persisted conversations with branching.
- `Storage` ([internal/storage/store.go](internal/storage/store.go)) — bbolt handle. Returns `ErrStoreLocked` when another DFMC process holds it; `cmd/dfmc/main.go` has a degraded-startup allow-list (`help`, `version`, `doctor`, `completion`, `man`) that runs without init.
- `EventBus` — fan-out used by TUI, web `/ws` SSE stream, and remote control.

### UIs

- [ui/cli/cli.go](ui/cli/cli.go) — single `Run()` that dispatches on `cmd := rest[0]`. **Global flags must come BEFORE the command** (`dfmc --provider offline review ...`), because `parseGlobalFlags` stops at the first non-flag token.
- [ui/tui/tui.go](ui/tui/tui.go) — bubbletea workbench with Chat/Status/Files/Patch/Setup/Tools panels.
- [ui/web/server.go](ui/web/server.go) — HTTP+SSE server (`dfmc serve`), default port 7777. Mirrors most CLI commands as `/api/v1/*` endpoints.
- `dfmc remote` subcommands in `ui/cli` are clients against a running `dfmc serve` (or `dfmc remote start` which launches gRPC+WS on 7778/7779).

### Config hierarchy

[internal/config/config.go](internal/config/config.go) merges: built-in defaults → `~/.dfmc/config.yaml` → `<project>/.dfmc/config.yaml` → env vars (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `DEEPSEEK_API_KEY`, `KIMI_API_KEY`, `ZAI_API_KEY`, `ALIBABA_API_KEY`, `MINIMAX_API_KEY`, `GOOGLE_AI_API_KEY`) → CLI flags. Project-root `.env` is auto-loaded at startup (process env still wins).

`dfmc config sync-models` rewrites the `providers.profiles.*` block from https://models.dev/api.json, preserving API keys. Whenever the provider catalog looks stale, use sync-models rather than editing by hand.

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
- When adding a new CLI command, wire it in the `switch cmd` block in `ui/cli/cli.go` **and** register the corresponding `/api/v1/*` handler in `ui/web/server.go` if it should be remotely accessible — the `dfmc remote <cmd>` client in `ui/cli` then needs a thin passthrough. The three layers are kept in sync by convention, not by codegen.
- Tests under `internal/engine` and `ui/cli` frequently construct a temp project with `.dfmc/` scaffolding; mirror the existing patterns rather than hand-rolling new fixtures.
