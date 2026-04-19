# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

**DFMC** ("Don't Fuck My Code") — a code intelligence assistant distributed as a single Go binary. It combines local code analysis (AST + codemap + security heuristics) with a multi-provider LLM router that falls back to an offline provider when API keys are missing or calls fail. Three UIs (CLI, bubbletea TUI, embedded Web API) all drive the same `engine.Engine`.

Module path: `github.com/dontfuckmycode/dfmc`. Go 1.25.

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
- `Tools` ([internal/tools](internal/tools/)) — backend registry: file ops (`read_file`, `write_file`, `edit_file`, `apply_patch`, `list_dir`), search (`grep_codebase`, `glob`, `find_symbol`, `codemap`, `ast_query`), shell (`run_command`), git (`git_status`/`_diff`/`_log`/`_blame`/`_branch`/`_commit`/`_worktree_*`), web (`web_fetch`, `web_search`), planning (`task_split`, `orchestrate`, `delegate_task`), reasoning (`think`, `todo_write`). Tool-capable providers see the four meta tools (`tool_search`/`tool_help`/`tool_call`/`tool_batch_call`) and dispatch backend tools through them — this keeps the model's tool list short and the protocol stable across providers. Bounded loop in [internal/engine/agent_loop_native.go](internal/engine/agent_loop_native.go); park-and-resume in [agent_parking.go](internal/engine/agent_parking.go); per-tool execution + lifecycle in [engine_tools.go](internal/engine/engine_tools.go).
  - **Context-gathering layer order** (cheapest → most precise): `grep_codebase` (text discovery) → `codemap` (project signatures-only outline) → `find_symbol` (semantic locate with full scope) → `read_file` (raw byte/line fetch). The `system.tools.read_stack` template in [system_prompts.yaml](internal/promptlib/defaults/system_prompts.yaml) teaches this ordering by default; tool-level prompts repeat it. Skipping straight to `read_file` on a guessed path costs more than starting with discovery.
  - **`find_symbol`** ([internal/tools/find_symbol.go](internal/tools/find_symbol.go)) is the language-aware "locate this name with its full scope" tool. AST-driven for Go/JS/TS/Python/Java/Rust/C-family; brace-balanced (C-family) or indent-based (Python) scope walk; HTML mode for `id="X"` / `class="X"` / `<TAG>`. `parent` arg disambiguates receivers/classes (`(s *Server) Start` vs `(c *Client) Start` → `parent="Server"`). `fallback: true` in the response means tree-sitter couldn't parse and a regex extractor was used — results are best-effort, verify before acting.
  - **`grep_codebase`** ([internal/tools/builtin.go](internal/tools/builtin.go)) supports `context`/`before`/`after` (capped at 50 per side, ripgrep-style `--`-separated blocks where `:` marks matches and `-` marks context), `include`/`exclude` glob filters (array or comma-string with doublestar), `case_sensitive` (false prefixes `(?i)`), and `respect_gitignore` (top-level `.gitignore` reader; negation patterns NOT honoured).
  - **`codemap`** ([internal/tools/codemap.go](internal/tools/codemap.go)) renders a signatures-only project outline — use ONCE per session for orientation, never per-file. Output is markdown grouped by file with per-symbol line numbers; bodies are intentionally absent (those live behind `find_symbol`).
  - **`read_file`** caps the default window at 200 lines and surfaces `total_lines`, `returned_lines`, `truncated`, and `language` so the model can decide whether to widen the range without a second probe round. The engine's `normalizeToolParams` injects the default range BEFORE Execute runs — `truncated` is set whenever `returned < total`, regardless of whether the caller passed a range.
  - **Tool errors are self-teaching** — every "missing required field" reply uses `missingParamError` (in [internal/tools/builtin.go](internal/tools/builtin.go)) which lists the params keys actually sent + the canonical example + a tool-specific confusion hint. When adding a new tool, use this helper for required-param validation; bare `fmt.Errorf("X is required")` causes the model to loop on the same broken shape.
  - **Param-name aliasing for known typo traps** ([internal/tools/engine.go](internal/tools/engine.go) `normalizeToolParams`): `edit_file` accepts `old`/`new` as aliases for `old_string`/`new_string`; `write_file` accepts `text`/`body`/`data` for `content`. These are the JS/Python edit-tool conventions weaker models reach for from training. The aliases are silently rewritten to canonical names BEFORE Execute runs so the call succeeds first try.
  - **Meta-tool boundary** ([internal/tools/meta.go](internal/tools/meta.go)): `tool_call` and `tool_batch_call` refuse to dispatch other meta tools. The refusal message names the right action (e.g. "drop the wrapper, put backend tools directly in calls[]") via `metaInBatchHint` so the model recovers in one round. `tool_call` ALSO auto-unwraps a single layer of `{name:"tool_call", args:{name:"<backend>", args:{...}}}` for the same reason.
  - **`apply_patch` runs through the same per-target read-before-mutate gate** as `edit_file`/`write_file` (see [internal/tools/engine.go](internal/tools/engine.go)'s `EnsureReadBeforeMutation` and the per-file check in [apply_patch.go](internal/tools/apply_patch.go)). New files in the diff are exempt; modifies and deletes require a prior `read_file` snapshot or the engine refuses the patch.
  - **Git tools refuse `-`-prefix user values** ([internal/tools/git.go](internal/tools/git.go)'s `rejectGitFlagInjection`) on every `ref`/`revision`/`branch`/`path`/`paths` argument — git treats `--upload-pack=cmd` as a flag, not a ref (CVE-2018-17456 class). Static flags we add ourselves (`--no-color`, `--cached`, `--porcelain`) are unaffected.
- `Memory` ([internal/memory/store.go](internal/memory/store.go)) — working + episodic + semantic tiers in bbolt.
- `Conversation` ([internal/conversation/manager.go](internal/conversation/manager.go)) — JSONL-persisted conversations with branching.
- `Storage` ([internal/storage/store.go](internal/storage/store.go)) — bbolt handle. Returns `ErrStoreLocked` when another DFMC process holds it; `cmd/dfmc/main.go` has a degraded-startup allow-list (`help`, `version`, `doctor`, `completion`, `man`) that runs without init.
- `EventBus` — fan-out used by TUI, web `/ws` SSE stream, and remote control.

### UIs

All three UI packages keep their entry/dispatch file lean and split feature code into sibling files. When adding a command/handler, find the right sibling rather than dropping it into the entry file.

- [ui/cli/cli.go](ui/cli/cli.go) (~175 lines) — only `Run()` and `parseGlobalFlags`. **Global flags must come BEFORE the command** (`dfmc --provider offline review ...`), because `parseGlobalFlags` stops at the first non-flag token. Subcommand bodies live in [cli_admin.go](ui/cli/cli_admin.go), [cli_ask_chat.go](ui/cli/cli_ask_chat.go), [cli_analysis.go](ui/cli/cli_analysis.go), [cli_remote.go](ui/cli/cli_remote.go), [cli_plugin_skill.go](ui/cli/cli_plugin_skill.go), [cli_output.go](ui/cli/cli_output.go), [cli_utils.go](ui/cli/cli_utils.go).
- [ui/tui/tui.go](ui/tui/tui.go) (~4400 lines) — bubbletea Model/View root. The hot paths split out: [update.go](ui/tui/update.go) (reducer), [chat_key.go](ui/tui/chat_key.go) (chat composer keyboard router), [chat_commands.go](ui/tui/chat_commands.go) (slash dispatcher), [engine_events.go](ui/tui/engine_events.go) (engine event handler).
  - **Tool-strip collapse**: per-message tool chip block is collapsed by default (one-line `▸ tools · N calls · X ok · Y fail · ~T tok · Dms` summary plus a `read_file ×3, edit_file ×2 — /tools to expand` fingerprint). `/tools` toggles the global `m.ui.toolStripExpanded` flag; `/tools list` keeps the previous behaviour of printing the registered backend tool catalog. Tests that assert per-chip rendering must set `m.ui.toolStripExpanded = true` manually before calling `renderChatView`.
  - **TODO strip**: when `engine.Tools.TodoSnapshot()` is non-empty, [theme.go](ui/tui/theme.go) `renderTodoStrip` prints a one-line `▸ TODOs · N done · M doing · K pending → <active form>` under the runtime card so the user can see plan progress between turns. Empty when there are no todos (no strip, no noise).
  - **Sub-agent badge**: chips whose tool is `delegate_task`/`orchestrate` or whose status starts with `subagent-` render with an accent-color `SUBAGENT` prefix in front of the tool name (`isSubagentToolChip` in [theme.go](ui/tui/theme.go)). The collapsed summary also separates them out as `· N sub-agents` so fan-out is visible at a glance.
- [ui/web/server.go](ui/web/server.go) (~215 lines) — `New`, `setupRoutes`, lifecycle. HTTP+SSE on port 7777 (`dfmc serve`). Handlers split by domain: [server_status.go](ui/web/server_status.go), [server_chat.go](ui/web/server_chat.go), [server_context.go](ui/web/server_context.go), [server_tools_skills.go](ui/web/server_tools_skills.go), [server_conversation.go](ui/web/server_conversation.go), [server_workspace.go](ui/web/server_workspace.go), [server_files.go](ui/web/server_files.go), [server_admin.go](ui/web/server_admin.go) (scan/doctor/hooks/config). Workbench HTML lives in [ui/web/static/index.html](ui/web/static/index.html) and is loaded via `//go:embed`.
- `dfmc remote` subcommands in [cli_remote.go](ui/cli/cli_remote.go) are clients against a running `dfmc serve` (or `dfmc remote start` which launches gRPC+WS on 7778/7779).

### Config hierarchy

[internal/config/config.go](internal/config/config.go) merges: built-in defaults → `~/.dfmc/config.yaml` → `<project>/.dfmc/config.yaml` → env vars (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `DEEPSEEK_API_KEY`, `KIMI_API_KEY`, `ZAI_API_KEY`, `ALIBABA_API_KEY`, `MINIMAX_API_KEY`, `GOOGLE_AI_API_KEY`) → CLI flags. Project-root `.env` is auto-loaded at startup (process env still wins).

`dfmc config sync-models` rewrites the `providers.profiles.*` block from https://models.dev/api.json, preserving API keys. Whenever the provider catalog looks stale, use sync-models rather than editing by hand.

The native tool loop has tunable knobs under `agent.*`:

- **Caps**: `max_tool_steps` (default 60), `max_tool_tokens` (default 250000), `max_tool_result_chars` (32K), `max_tool_result_data_chars` (12K), `parallel_batch_size` (4).
- **Round shaping**: `tool_round_soft_cap` (synthesis nudge), `tool_round_hard_cap` (force `tool_choice=none`), `budget_headroom_divisor` (preflight margin).
- **Autonomous resume** (`autonomous_resume`, default `"auto"` → ON): when the loop hits `max_tool_tokens` mid-task, the autonomous wrapper force-compacts history and re-enters the loop transparently — the user sees one continuous answer instead of having to type `/continue` between every park. Set `"off"` (or `"manual"`/`"false"`/`"no"`/`"0"`) to revert to the old "park and wait" behavior; useful for CI runs that must hard-stop after one budget. `resume_max_multiplier` (default 10) is the cumulative ceiling: total work across all auto-resumes for a single root ask is capped at `max_tool_steps × multiplier` and `max_tool_tokens × multiplier`. Hit the ceiling and the wrapper surfaces `agent:loop:auto_resume_refused` so the user knows the auto-progression bottomed out and a manual `/continue` (or scope refinement) is needed.
- **Tool self-narration** (`tool_reasoning`, default `"auto"` → ON): every tool's JSON schema gets an optional virtual `_reason` field. The model is nudged in the system prompt to fill it with a one-sentence "why" ("checking how the SSE handler closes the stream"); [tools.Engine.Execute](internal/tools/engine.go) strips it before dispatch and republishes via the engine's [tool:reasoning](internal/engine/engine.go) event so the TUI chip and web activity feed render the WHY above the tool result. Set `"off"` to skip the system-prompt nudge and the publisher entirely. The strip itself always runs (so tools never see the field as input). Pinned by [reason_test.go](internal/tools/reason_test.go) and [tool_reasoning_test.go](internal/engine/tool_reasoning_test.go).
- **Context lifecycle** (under `agent.context_lifecycle`): `enabled`, `auto_compact_threshold_ratio` (default 0.7 — proactive compact when used/budget crosses this), `auto_handoff_threshold_ratio` (0.9), `keep_recent_rounds` (3), `handoff_brief_max_tokens` (500).

All zero-defaults fall back to the values in [internal/config/defaults.go](internal/config/defaults.go); raise the caps for high-context models (1M-window Opus, etc.) instead of fighting the defaults. The TUI suppresses the "press Enter to resume" banner when `autonomous_pending: true` is on the parked event, so a budget exhaust under autonomous mode looks like a continuous response with a small `↻ auto-resuming after compact` chip in the timeline rather than a parked banner the user might race to dismiss.

### Prompt library

[internal/promptlib](internal/promptlib/) loads from:
1. `internal/promptlib/defaults/*.yaml` (embedded)
2. `~/.dfmc/prompts` (global overrides)
3. `.dfmc/prompts` (project overrides)

Formats: `.yaml`, `.json`, `.md` (YAML frontmatter + body). Composition via `compose: replace | append`. Task/role/language axes pick overlays. Rendered prompts are token-budgeted against the runtime provider's `max_context`.

### Context injection from the user query

`[[file:path/to/file.go]]` and `[[file:path#L10-L80]]` markers in any user query are resolved, budgeted, and injected into the system prompt as compressed snippets. Triple-backtick fenced blocks in the query are treated as explicit injected context. Both are used by `ask`, `chat`, `review`, `explain`, TUI chat, and the web `/api/v1/chat` SSE.

### Drive: autonomous plan/execute loop

`dfmc drive "<task>"` (CLI) and `/drive <task>` (TUI) run a self-driving loop on top of the engine: a planner LLM call breaks the task into a DAG of TODOs, then a scheduler walks ready TODOs through the regular sub-agent surface (`engine.RunSubagent`) until everything reaches a terminal state. Each TODO is a fresh sub-conversation seeded with a brief of prior work — context stays bounded without losing the thread.

Lives in [internal/drive](internal/drive/):

- [planner.go](internal/drive/planner.go) — JSON DAG planner (cycle detection, code-fence stripping, dep validation). Planner LLM call goes through `Engine.NewDriveRunner().PlannerCall` → `Providers.Complete` directly (no tool loop, no intent layer, no history).
- [scheduler.go](internal/drive/scheduler.go) — `readyBatch` returns up to N TODOs that have all deps Done AND don't conflict on `file_scope` with any Running TODO or each other. Empty `file_scope` means "owns everything" — runs alone.
- [driver.go](internal/drive/driver.go) — main loop. Worker goroutines (capped by `MaxParallel`, default 3) fan out per batch; results channel drained as workers finish; new ready TODOs dispatched into freed slots.
- [persistence.go](internal/drive/persistence.go) — bbolt `drive-runs` bucket, JSON-encoded per run. `Save` after every state transition; `dfmc drive resume <id>` re-enters from the persisted state.
- [drive_adapter.go](internal/engine/drive_adapter.go) — engine-side `Engine.NewDriveRunner` that wires the `drive.Runner` interface to `Providers.Complete` (planner) and `RunSubagent` (executor); `Engine.PublishDriveEvent` mirrors driver events into the engine event bus.

Per-tag provider routing (Phase 3): `Config.Routing` maps a TODO `provider_tag` (planner emits `plan|code|review|test|research`) to a provider profile name; the executor sends that as `Model` to RunSubagent. CLI: `--route plan=opus --route code=sonnet --route test=haiku`. Unmapped tags fall back to the engine default (no error). Lookup is case-insensitive on the tag side.

Termination paths: `RunDone` (everything terminal, no Blocked-driven stop), `RunFailed` (`MaxFailedTodos` consecutive blocks, planner returned 0 TODOs, deadlock), `RunStopped` (ctx cancelled, `MaxWallTime` exceeded — both resumable). The driver's drain phase pulls already-queued worker results before stamping the final status; in-flight workers get a 2-second grace window via `drainGraceWindow`.

Drive events (`drive:*`) flow through the same `engine.EventBus` as `agent:*` and `provider:*`:

```
drive:run:start          drive:plan:start | drive:plan:done | drive:plan:failed
drive:todo:start         drive:todo:done | drive:todo:blocked | drive:todo:skipped | drive:todo:retry
drive:run:warning        drive:run:done | drive:run:stopped | drive:run:failed
```

TUI subscribes via [engine_events.go](ui/tui/engine_events.go); CLI prints them to stderr inline and renders a final summary on stdout. The web workbench (`/` from `dfmc serve`) renders a Drive Cockpit panel that mirrors the same surface — start runs, watch the active list, drill into a run's TODO ladder; live updates piggyback on the existing `/ws` SSE stream by debouncing on any `drive:*` event.

Drive is also exposed over MCP for IDE hosts (Claude Desktop, Cursor, VSCode). `dfmc mcp` advertises six synthetic tools alongside the regular registry: `dfmc_drive_start`, `dfmc_drive_status`, `dfmc_drive_active`, `dfmc_drive_list`, `dfmc_drive_stop`, `dfmc_drive_resume`. They live in [cli_mcp_drive.go](ui/cli/cli_mcp_drive.go) and route through `driveMCPHandler`, NOT `engine.CallTool` — that keeps the approval gate / hooks dispatch from special-casing them, and prevents an LLM step from recursively triggering another LLM step. Wire shapes match the HTTP `/api/v1/drive/*` payloads one-for-one.

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
- `read_file`'s `truncated` flag means "the file extends past what you got" — it can't distinguish "caller asked for a slice" from "default 200-line cap kicked in" because `normalizeToolParams` injects the default range BEFORE Execute runs. Tests that pass a narrow `line_start`/`line_end` on a big file will see `truncated: true`; that's the contract, not a regression.
- TUI tool-strip is collapsed by default. Tests asserting per-chip rendering MUST set `m.ui.toolStripExpanded = true` before `renderChatView`, otherwise they'll get the one-line summary and miss per-call fields like `+1.3k tok` or per-chip durations.
- `tool_call`/`tool_batch_call` refuse to dispatch other meta tools — never put `tool_search`/`tool_help`/`tool_call`/`tool_batch_call` inside another meta tool's `name` or `calls[]`. The refusal hint names the right action, but adding new meta tools needs the same `metaInBatchHint` entry to keep self-teaching parity.
