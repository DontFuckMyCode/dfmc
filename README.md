# Don't Fuck My Code (DFMC)

Your Code Deserves Better.

DFMC is a code intelligence assistant written in Go. It combines local code analysis (AST + codemap + security heuristics) with a provider router that can fall back to offline mode when API providers are unavailable.

Status: Alpha (actively under development)

## Current State

Implemented:
- CLI entrypoint, command router, and shell completion (`bash`/`zsh`/`fish`/`powershell`)
- Config hierarchy (defaults → `~/.dfmc/config.yaml` → `.dfmc/config.yaml` → env → CLI flags) with `.env` auto-load
- Models.dev-backed provider profile sync (`dfmc config sync-models`)
- Engine lifecycle with event bus, panic-guarded tool execution, and lock-ordered state
- AST extraction via tree-sitter (Go, JavaScript, TypeScript, Python) with regex fallback when built without CGO
- CodeMap graph (symbols/edges, cycles, hotspots, path traversal, DOT/SVG export)
- Provider router with automatic fallback and always-available offline provider:
  - Anthropic Messages API
  - Google AI (Gemini)
  - OpenAI-compatible Chat Completions (covers `openai`, `deepseek`, `kimi`, `zai`, `alibaba`, `minimax`, `generic`, `ollama`)
- Streaming + native tool-calling loop with approval gate, pre/post hooks, panic guard, and context-lifecycle auto-compact / auto-handoff / autonomous resume
- Tool engine — built-in registry:
  - **File**: `read_file`, `write_file`, `edit_file`, `apply_patch`, `list_dir`
  - **Search/nav**: `grep_codebase`, `glob`, `find_symbol`, `codemap`, `ast_query`
  - **Shell**: `run_command` (allowlist + sandbox)
  - **Git**: `git_status`, `git_diff`, `git_log`, `git_blame`, `git_branch`, `git_commit`, `git_worktree_list`/`_add`/`_remove`
  - **Web**: `web_fetch`, `web_search`
  - **Planning / sub-agents**: `task_split`, `orchestrate`, `delegate_task`
  - **Reasoning**: `think`, `todo_write`
  - **Meta layer** (exposed to tool-capable providers): `tool_search`, `tool_help`, `tool_call`, `tool_batch_call` — keeps the wire-level tool list short and the protocol stable across providers
- Intent router — state-aware sub-LLM that routes `resume`/`new`/`clarify` before every Ask (fail-open)
- Drive — autonomous plan/execute loop that breaks a task into a DAG of TODOs and schedules sub-agents in parallel (`dfmc drive`, `/drive` in TUI)
- Hooks — user-configured shell commands on `user_prompt_submit`, `pre_tool`, `post_tool`, `session_start`/`_end` with per-entry timeout and `shell: false` payload safety
- Tool approval gate with per-tool auto-approve and denial logging
- MCP server (`dfmc mcp`) exposing the tool registry plus six synthetic Drive tools (`dfmc_drive_start`/`_status`/`_active`/`_list`/`_stop`/`_resume`) for IDE hosts (Claude Desktop, Cursor, VSCode)
- Skill commands (`skill list/info/run`) and built-in shortcuts (`review`, `explain`, `refactor`, `debug`, `test`, `doc`, `generate`, `audit`, `onboard`)
- Plugin commands (`plugin list/info/install/remove/enable/disable`) with config-backed enable state and manifest validation
- Web API server (`dfmc serve`) with status, codemap, tools, memory, files, chat SSE, Drive cockpit, task CRUD, workspace diff/patch
- Terminal workbench (`dfmc tui`) with Chat, Status, Files, Patch, Setup, Tools, Drive, and Tasks panels
- Remote mode (`dfmc remote start` + `dfmc remote <verb>`) for headless operation over gRPC/WebSocket
- Conversation persistence (JSONL) with branching, search, and compare
- Memory store (working + episodic + semantic via bbolt buckets)
- Task store (bbolt-backed) shared by `todo_write`, HTTP `/api/v1/tasks/*`, and MCP
- Security scan (regex patterns for secrets and common vulnerability indicators)
- Analyze pipeline with optional `--security`, `--dead-code`, `--complexity`, `--deps`, `--full`, `--magicdoc`

## Quick Start

Requirements:
- Go 1.25+
- Windows / Linux / macOS
- A C toolchain **if you want full-fidelity tree-sitter AST** (see note below)

### 1) Build

```bash
# Full build (tree-sitter for Go/JS/TS/Python)
CGO_ENABLED=1 go build -o bin/dfmc ./cmd/dfmc

# Minimal build (falls back to regex AST — symbol/codemap accuracy degrades)
go build -o bin/dfmc ./cmd/dfmc
```

> With `CGO_ENABLED=0` the build still succeeds but `dfmc status` / `dfmc doctor` will report `ast_backend: regex`. If symbol extraction behavior looks wrong, check the backend before blaming the code.

### 2) Initialize in project

```bash
go run ./cmd/dfmc init
```

This creates:
- `.dfmc/config.yaml`
- `.dfmc/knowledge.json`
- `.dfmc/conventions.json`

### 3) Ask a question

```bash
go run ./cmd/dfmc ask "how does auth middleware work?"
go run ./cmd/dfmc status
go run ./cmd/dfmc --json status --query "security audit auth middleware"
go run ./cmd/dfmc --json status --query "security audit auth middleware" --runtime-tool-style function-calling --runtime-max-context 1000
```
`status` JSON output includes `context_tuning_suggestions` when a query is provided.

If provider API keys are not configured, DFMC automatically uses offline mode with local-context response generation.
Project-root `.env` files are auto-loaded during startup, and existing process env vars still take precedence.
For command execution policy, prefer `security.sandbox.allow_command`; the legacy `allow_shell` key is still accepted, but it disables the whole `run_command` tool rather than just shell interpreters.

### 3.1) Interactive streaming chat

```bash
go run ./cmd/dfmc chat
```

Chat slash commands:
- `/help`, `/exit`, `/clear`, `/save`, `/load <id>`, `/history [limit]`
- `/provider [name]`, `/model [name]`
- `/branch [name]`, `/branch list`, `/branch create <name>`, `/branch switch <name>`, `/branch compare <a> <b>`
- `/context show`, `/memory`, `/tools`, `/skills`, `/diff`, `/undo`, `/apply [--check] [patch.diff]`, `/run <skill> [input]`, `/cost`

When a configured provider is tool-capable, DFMC can now run a bounded local tool loop during chat turns: the model may request one tool at a time, receive the tool result, and continue toward a final answer.

### 3.2) Terminal workbench

```bash
go run ./cmd/dfmc tui
go run ./cmd/dfmc tui --no-alt-screen
```

The first TUI shell includes:
- `Chat` panel with streaming answers
- `Status` panel with AST/codemap runtime signals
- `Files` panel with project browsing and file preview
- `Patch` panel for worktree diff, latest assistant patch, touched-file hints, check/apply, and conversation undo
- `Setup` panel for switching provider/model from configured profiles
- `Tools` panel for preset read-only tool runs inside the terminal workbench
- Chat now has the same first local tool bridge as CLI chat, so provider-backed sessions can read/list/grep, run guarded commands, and perform guarded file edits through the agent loop
- Chat transcript now surfaces patch provenance for assistant responses that emit diffs
- Chat transcript now also surfaces tool usage summaries for assistant responses that used local tools
- Binary-looking files are preview-guarded instead of dumping terminal garbage

Files panel shortcuts:
- `j/k` or arrow keys move selection, `enter` reloads preview, `r` refreshes file list
- `p` pins/unpins the selected file so chat requests automatically carry its `[[file:...]]` marker
- `i` inserts `[[file:...]]` for the selected file into the chat prompt and switches to `Chat`
- `e` prepares an "Explain selected file" prompt
- `v` prepares a review prompt for the selected file

Patch panel shortcuts:
- `d` refreshes worktree diff, `l` reloads the latest assistant patch
- `n` / `b` switch between files touched by the latest patch and narrow the patch preview to that file
- `j` / `k` switch between hunks inside the current patch file
- `f` jumps to the most relevant patched file in `Files` view, preferring the pinned file when possible
- `c` runs patch check, `a` applies, `u` undoes the last conversation turn

Setup panel shortcuts:
- `j/k` moves across configured providers
- `enter` applies the selected provider and its configured model to the current TUI session

Tools panel shortcuts:
- `j/k` moves across registered tools
- `enter` runs the selected tool with the current params
- `r` reruns the current tool preset
- `e` opens the inline param editor, `enter` saves, `esc` cancels, `x` resets back to defaults
- `read_file` uses the pinned/selected file, `list_dir` uses the selected file's directory, `grep_codebase` uses the current chat input or selected filename stem
- `run_command` defaults to a safe `go version` preset and can be edited for bounded test/build/format commands
- `run_command` also shows repo-aware suggestions like `go test`, `go build`, `pytest`, `npm test`, or `cargo test` when the project layout matches
- Quoted values are supported in params, so `content="hello world"` stays intact
- `write_file` and `edit_file` can now run from TUI when you provide explicit params; mutation safety checks still come from the tool engine

Chat commands:
- `/help`, `/status`, `/context`, `/tools`, `/diff`, `/patch`, `/undo`, `/apply [--check]`
- `/providers` lists configured providers
- `/provider NAME` switches the active provider inside the TUI session
- `/models` shows the configured model for the active provider
- `/model NAME` overrides the active model inside the TUI session
- `/tools` also points to the `Tools` panel (`F6`) for preset execution

### 4) Analyze codebase

```bash
go run ./cmd/dfmc analyze
go run ./cmd/dfmc analyze --security --dead-code --complexity
go run ./cmd/dfmc analyze --deps
go run ./cmd/dfmc analyze --full --json
go run ./cmd/dfmc analyze --full --magicdoc
```

### 5) Run security scan

```bash
go run ./cmd/dfmc scan
go run ./cmd/dfmc --json scan
```

### 6) Use tool engine

```bash
go run ./cmd/dfmc tool list
go run ./cmd/dfmc tool run read_file --path internal/engine/engine.go --line_start 1 --line_end 40
go run ./cmd/dfmc tool run write_file --path tmp/demo.txt --content "hello"
go run ./cmd/dfmc tool run edit_file --path tmp/demo.txt --old_string hello --new_string hi
go run ./cmd/dfmc tool run apply_patch --patch @changes.diff
go run ./cmd/dfmc tool run grep_codebase --pattern "ErrProviderUnavailable" --max_results 10
go run ./cmd/dfmc tool run find_symbol --name Engine --parent Engine
go run ./cmd/dfmc tool run codemap
go run ./cmd/dfmc tool run glob --pattern "internal/**/*.go"
go run ./cmd/dfmc tool run run_command --command go --args "version"
go run ./cmd/dfmc tool run git_status
go run ./cmd/dfmc tool run git_diff --cached
go run ./cmd/dfmc tool run web_fetch --url https://models.dev/api.json
go run ./cmd/dfmc map --format dot
go run ./cmd/dfmc map --format svg > codemap.svg
```

**Context-gathering layer order** (cheapest → most precise): `grep_codebase` (text discovery) → `codemap` (project signatures-only outline) → `find_symbol` (semantic locate with full scope) → `read_file` (raw byte/line fetch). Skipping straight to `read_file` on a guessed path costs more than starting with discovery.

### 6.0) Drive — autonomous plan/execute

```bash
go run ./cmd/dfmc drive "add a health check endpoint and wire it into doctor"
go run ./cmd/dfmc drive "refactor agent_loop" --max-parallel 4 --route plan=opus --route code=sonnet --route test=haiku
go run ./cmd/dfmc drive list
go run ./cmd/dfmc drive show <run-id>
go run ./cmd/dfmc drive resume <run-id>
go run ./cmd/dfmc drive stop <run-id>
```

Drive decomposes a task into a DAG of TODOs, then schedules ready ones in parallel through the sub-agent surface. Per-tag provider routing (`--route <tag>=<profile>`) sends each TODO to the best profile for that work — `plan`, `code`, `review`, `test`, `research`. Runs persist to bbolt and are resumable after Ctrl+C.

### 6.1) Run Web API

```bash
go run ./cmd/dfmc serve --host 127.0.0.1 --port 7788
go run ./cmd/dfmc serve --host 127.0.0.1 --port 7788 --open-browser=false
DFMC_WEB_TOKEN=change-me go run ./cmd/dfmc serve --host 127.0.0.1 --port 7788 --auth token
```

Endpoints:
- `GET /healthz`
- `GET /api/v1/status`
- `GET /api/v1/commands`, `GET /api/v1/commands/{name}`
- `POST /api/v1/ask`, `POST /api/v1/chat` (SSE)
- `GET /api/v1/codemap`
- `GET /api/v1/context/budget?q=...&runtime_provider=...&runtime_model=...&runtime_tool_style=...&runtime_max_context=...`
- `GET /api/v1/context/recommend?q=...&runtime_provider=...&runtime_model=...&runtime_tool_style=...&runtime_max_context=...`
- `GET /api/v1/context/brief?max_words=...&path=...`
- `POST /api/v1/analyze`
- `GET /api/v1/providers`
- `GET /api/v1/tools`, `GET /api/v1/tools/{name}`, `POST /api/v1/tools/{name}`
- `GET /api/v1/skills`, `POST /api/v1/skills/{name}`
- `GET /api/v1/memory`
- `GET /api/v1/conversation`, `POST /api/v1/conversation/new|save|load|undo`
- `GET /api/v1/conversation/branches`, `POST /api/v1/conversation/branches/create|switch`, `GET /api/v1/conversation/branches/compare?a=...&b=...`
- `GET /api/v1/prompts`, `GET /api/v1/prompts/stats`, `GET /api/v1/prompts/recommend`, `POST /api/v1/prompts/render`
- `GET /api/v1/magicdoc`, `POST /api/v1/magicdoc/update`
- `GET /api/v1/conversations`, `GET /api/v1/conversations/search?q=...`
- `GET /api/v1/workspace/diff`, `GET /api/v1/workspace/patch`, `POST /api/v1/workspace/apply`
- `GET /api/v1/files`, `GET /api/v1/files/{path...}`
- `GET /api/v1/scan`, `GET /api/v1/doctor`, `GET /api/v1/hooks`, `GET /api/v1/config`
- `POST /api/v1/drive`, `GET /api/v1/drive`, `GET /api/v1/drive/{id}`, `POST /api/v1/drive/{id}/resume|stop`, `DELETE /api/v1/drive/{id}`, `GET /api/v1/drive/active`
- `GET /api/v1/tasks`, `POST /api/v1/tasks`, `GET /api/v1/tasks/{id}`, `PATCH /api/v1/tasks/{id}`, `DELETE /api/v1/tasks/{id}`
- `GET /ws` (event stream, SSE)

### 6.2) Manage config

```bash
go run ./cmd/dfmc config list
go run ./cmd/dfmc config get providers.primary
go run ./cmd/dfmc config set context.include_tests true
go run ./cmd/dfmc config sync-models
go run ./cmd/dfmc config sync-models --rewrite-base-url=false
go run ./cmd/dfmc config edit
go run ./cmd/dfmc context budget --query "security audit auth middleware"
go run ./cmd/dfmc context budget --query "security audit auth middleware" --runtime-tool-style function-calling --runtime-max-context 1000
go run ./cmd/dfmc context recommend --query "debug [[file:internal/auth/service.go]]"
go run ./cmd/dfmc context recommend --query "debug [[file:internal/auth/service.go]]" --runtime-tool-style function-calling --runtime-max-context 1000
go run ./cmd/dfmc context recent
go run ./cmd/dfmc context brief --max-words 240
go run ./cmd/dfmc context brief --path docs/BRIEF.md --max-words 180
```
When `runtime-max-context` is low (`<=12000`), context budgeting automatically switches to more aggressive compression; for very tight windows (`<=8000`), doc slices are auto-disabled.
`dfmc --json context recommend ...` includes directly actionable config updates under `tuning_suggestions`.
`dfmc config sync-models` refreshes provider models, protocols, output token limits, and context windows from [models.dev](https://models.dev/api.json), while preserving API keys.

### 7) Use memory and conversation commands

```bash
go run ./cmd/dfmc memory working
go run ./cmd/dfmc memory list --tier episodic --limit 20
go run ./cmd/dfmc memory search --query auth --tier episodic
go run ./cmd/dfmc memory add --tier episodic --key "note" --value "important detail"
go run ./cmd/dfmc memory clear --tier semantic

go run ./cmd/dfmc conversation list
go run ./cmd/dfmc conversation search middleware
go run ./cmd/dfmc conversation active
go run ./cmd/dfmc conversation save
go run ./cmd/dfmc conversation undo
go run ./cmd/dfmc conversation load conv_20260414_101500.123
go run ./cmd/dfmc conversation branch list
go run ./cmd/dfmc conversation branch create experiment
go run ./cmd/dfmc conversation branch switch experiment
go run ./cmd/dfmc conversation branch compare main experiment
```

### 8) Use skills and plugin controls

```bash
go run ./cmd/dfmc skill list
go run ./cmd/dfmc skill info review
go run ./cmd/dfmc --provider offline review "find risks in the auth module"

go run ./cmd/dfmc plugin list
go run ./cmd/dfmc plugin install --name my-plugin --enable ./path/to/my-plugin.py
go run ./cmd/dfmc plugin install --name my-plugin --enable https://example.com/plugins/my-plugin.mjs
go run ./cmd/dfmc plugin install --enable ./path/to/plugin-bundle.zip
go run ./cmd/dfmc plugin enable my-plugin
go run ./cmd/dfmc plugin remove my-plugin
go run ./cmd/dfmc plugin disable my-plugin
```

Plugin install supports directories, `.zip` bundles, and these file types:
- `.so`, `.dll`, `.dylib`, `.wasm`, `.js`, `.mjs`, `.py`, `.sh`
If a plugin directory has `plugin.yaml` / `plugin.yml`, `plugin list` and `plugin info` show manifest metadata (`name`, `version`, `type`, `entry`). During install, manifest `entry` is validated for path safety and existence.

Note: global flags must come before the command (`dfmc --provider ... review ...`).

### 9) Remote mode (headless)

```bash
go run ./cmd/dfmc remote status
go run ./cmd/dfmc remote status --live --url http://127.0.0.1:7779
go run ./cmd/dfmc remote probe --url http://127.0.0.1:7779
go run ./cmd/dfmc remote events --url http://127.0.0.1:7779 --type "*" --timeout 10s --max 20
go run ./cmd/dfmc remote ask --url http://127.0.0.1:7779 --message "explain the auth middleware"
go run ./cmd/dfmc remote tools --url http://127.0.0.1:7779
go run ./cmd/dfmc remote skills --url http://127.0.0.1:7779
go run ./cmd/dfmc remote prompt list --url http://127.0.0.1:7779
go run ./cmd/dfmc remote prompt render --url http://127.0.0.1:7779 --task security --query "auth audit"
go run ./cmd/dfmc remote prompt render --url http://127.0.0.1:7779 --task security --query "auth audit" --runtime-tool-style function-calling --runtime-max-context 1000
go run ./cmd/dfmc remote prompt recommend --url http://127.0.0.1:7779 --query "auth audit"
go run ./cmd/dfmc remote prompt recommend --url http://127.0.0.1:7779 --query "auth audit" --runtime-tool-style function-calling --runtime-max-context 1000
go run ./cmd/dfmc remote prompt stats --url http://127.0.0.1:7779 --max-template-tokens 450
go run ./cmd/dfmc remote magicdoc show --url http://127.0.0.1:7779
go run ./cmd/dfmc remote magicdoc update --url http://127.0.0.1:7779 --title "Remote Brief"
go run ./cmd/dfmc remote context budget --url http://127.0.0.1:7779 --query "security audit auth middleware"
go run ./cmd/dfmc remote context budget --url http://127.0.0.1:7779 --query "security audit auth middleware" --runtime-tool-style function-calling --runtime-max-context 1000
go run ./cmd/dfmc remote context recommend --url http://127.0.0.1:7779 --query "debug [[file:internal/auth/service.go]]"
go run ./cmd/dfmc remote context recommend --url http://127.0.0.1:7779 --query "debug [[file:internal/auth/service.go]]" --runtime-tool-style function-calling --runtime-max-context 1000
go run ./cmd/dfmc remote context brief --url http://127.0.0.1:7779 --max-words 240 --path docs/BRIEF.md
go run ./cmd/dfmc remote tool read_file --url http://127.0.0.1:7779 --param path=README.md --param line_start=1 --param line_end=5
go run ./cmd/dfmc remote skill review --url http://127.0.0.1:7779 --input "review the auth layer"
go run ./cmd/dfmc remote analyze --url http://127.0.0.1:7779 --full
go run ./cmd/dfmc remote analyze --url http://127.0.0.1:7779 --full --magicdoc
go run ./cmd/dfmc remote files list --url http://127.0.0.1:7779 --limit 100
go run ./cmd/dfmc remote files get README.md --url http://127.0.0.1:7779
go run ./cmd/dfmc remote memory working --url http://127.0.0.1:7779
go run ./cmd/dfmc remote memory list --url http://127.0.0.1:7779 --tier semantic
go run ./cmd/dfmc remote conversation list --url http://127.0.0.1:7779 --limit 20
go run ./cmd/dfmc remote conversation search --url http://127.0.0.1:7779 --query "auth"
go run ./cmd/dfmc remote conversation active --url http://127.0.0.1:7779
go run ./cmd/dfmc remote conversation new --url http://127.0.0.1:7779
go run ./cmd/dfmc remote conversation save --url http://127.0.0.1:7779
go run ./cmd/dfmc remote conversation load --url http://127.0.0.1:7779 --id conv_20260414_101500.123
go run ./cmd/dfmc remote conversation branch list --url http://127.0.0.1:7779
go run ./cmd/dfmc remote conversation branch create --url http://127.0.0.1:7779 --name experiment
go run ./cmd/dfmc remote conversation branch switch --url http://127.0.0.1:7779 --name experiment
go run ./cmd/dfmc remote conversation branch compare --url http://127.0.0.1:7779 --a main --b experiment
go run ./cmd/dfmc remote codemap --url http://127.0.0.1:7779 --format dot
DFMC_REMOTE_TOKEN=change-me go run ./cmd/dfmc remote start --auth token --grpc-port 7778 --ws-port 7779
```

### 10) Run diagnostics

```bash
go run ./cmd/dfmc doctor
go run ./cmd/dfmc doctor --network --timeout 3s
go run ./cmd/dfmc doctor --providers-only
go run ./cmd/dfmc doctor --fix
go run ./cmd/dfmc --json doctor
```

`doctor` now also reports `magicdoc.health` (missing/fresh/stale) and `prompt.health` (template warnings/token thresholds).

### 11) Generate shell completion

```bash
go run ./cmd/dfmc completion bash > dfmc.bash
go run ./cmd/dfmc completion zsh > _dfmc
go run ./cmd/dfmc completion fish > dfmc.fish
go run ./cmd/dfmc completion powershell > dfmc.ps1
```

### 12) Generate man page

```bash
go run ./cmd/dfmc man --format man > dfmc.1
go run ./cmd/dfmc man --format markdown > MANUAL.md
```

### 13) Prompt library (task/language aware)

```bash
go run ./cmd/dfmc prompt list
go run ./cmd/dfmc prompt render --query "security audit auth middleware"
go run ./cmd/dfmc prompt render --task planning --language go --query "create a roadmap"
go run ./cmd/dfmc prompt render --task review --var context_files="- internal/auth/middleware.go:1-120"
go run ./cmd/dfmc prompt render --task security --role security_auditor --query "auth audit"
go run ./cmd/dfmc prompt render --query "auth audit" --runtime-tool-style function-calling --runtime-max-context 1000
go run ./cmd/dfmc prompt recommend --query "security audit auth middleware"
go run ./cmd/dfmc prompt recommend --query "security audit auth middleware" --runtime-tool-style function-calling --runtime-max-context 1000
go run ./cmd/dfmc --json prompt render --query "auth audit" --runtime-tool-style function-calling --runtime-max-context 1000
go run ./cmd/dfmc prompt stats
go run ./cmd/dfmc prompt stats --max-template-tokens 450 --fail-on-warning
```

Inline file injection in user query (Claude Code-style):

```text
[[file:internal/auth/middleware.go]]
[[file:internal/auth/middleware.go#L10-L80]]
```

These markers are automatically resolved and injected into system prompt context.
Injection payload is budgeted automatically (compact/deep profile aware) to reduce token burn.

Inline fenced-code injection is also supported in query text:

~~~text
```go
func VerifyToken(token string) bool { return token != "" }
```
~~~

When using `--json` with `prompt render`, output now includes:
- `prompt_tokens_estimate`
- `prompt_budget_tokens`
- `prompt_trimmed`

Prompt templates are loaded from:
- built-in defaults (`internal/promptlib/defaults`)
- global overrides (`~/.dfmc/prompts`)
- project overrides (`.dfmc/prompts`)

Supported file formats in prompt library:
- `.yaml` / `.yml`
- `.json`
- `.md` (with optional YAML frontmatter)

Template composition modes:
- `compose: replace` (default): base template body
- `compose: append`: additive fragment (task/profile/language overlays)
- `role`: optional role axis for specialized prompt overlays (`planner`, `security_auditor`, `code_reviewer`, etc.)

Example overlay template:
```yaml
id: system.security.overlay
type: system
task: security
compose: append
priority: 90
body: |
  Security mode contract:
  - Report severity and exploitability preconditions.
```

### 14) Magic doc (low-token project brief)

```bash
go run ./cmd/dfmc magicdoc update
go run ./cmd/dfmc magicdoc update --title "Backend Brief"
go run ./cmd/dfmc magicdoc show
```

By default this writes `.dfmc/magic/MAGIC_DOC.md`.  
When present, DFMC injects a budgeted slice of this brief into the system prompt as `project_brief` to improve context continuity with lower token usage.

## Command Overview

Core:
- `dfmc init`, `dfmc status`, `dfmc version`, `dfmc doctor`, `dfmc update`
- `dfmc ask`, `dfmc chat`, `dfmc tui`
- `dfmc analyze`, `dfmc scan`, `dfmc map`

Agent / automation:
- `dfmc drive` — autonomous plan/execute loop (list/show/resume/stop)
- `dfmc tool` — run a single tool
- `dfmc hooks` — inspect configured hooks
- `dfmc approvals` (alias: `approve`, `permissions`) — manage tool auto-approve list

Provider / model:
- `dfmc providers`, `dfmc provider`, `dfmc model`

State:
- `dfmc memory`, `dfmc conversation` (alias: `conv`)
- `dfmc context`, `dfmc prompt`, `dfmc magicdoc`

Extensibility:
- `dfmc plugin`, `dfmc skill`
- `dfmc review`, `dfmc explain`, `dfmc refactor`, `dfmc debug`, `dfmc test`, `dfmc doc`, `dfmc generate`, `dfmc audit`, `dfmc onboard` (skill shortcuts)

Services / host integration:
- `dfmc serve` — HTTP + SSE workbench on port 7777
- `dfmc remote` — headless client against `dfmc remote start` (gRPC on 7778, WebSocket on 7779)
- `dfmc mcp` — Model Context Protocol server for IDE hosts

Config / scaffolding:
- `dfmc config`, `dfmc completion`, `dfmc man`

## Configuration

Hierarchy:
1. Built-in defaults
2. Global config (`~/.dfmc/config.yaml`)
3. Project config (`<project>/.dfmc/config.yaml`)
4. Environment variables (API keys, etc.)
5. CLI flags

Common env vars:
- `ANTHROPIC_API_KEY`
- `OPENAI_API_KEY`
- `GOOGLE_AI_API_KEY`
- `DEEPSEEK_API_KEY`
- `KIMI_API_KEY`
- `MINIMAX_API_KEY`
- `ZAI_API_KEY`
- `ALIBABA_API_KEY`

Token-efficient context tuning:
- `context.max_tokens_total`: hard cap for all retrieved code context per request.
- `context.max_tokens_per_file`: per-file chunk cap after compression.
- `context.max_history_tokens`: max token budget for prior conversation messages sent with each request.
- `context.compression`: `none|standard|aggressive` tradeoff between fidelity and token cost.
- Budgets are task-adaptive (`security/review/debug` gets more context; `planning/doc` gets leaner context).
- `context budget` preview now includes reserve breakdown (`prompt/history/response/tools`) and available context headroom.
- `context budget` preview also exposes task scaling coefficients and `[[file:...]]` marker count.
- System prompt tool-call policy is provider-aware (e.g., `function-calling` vs `tool_use`) and budget-aware.
- Prompt profile and render budget are runtime-aware (`max_context`, low-latency mode, task type).
- Final rendered system prompts are capped by a task/runtime token budget to avoid prompt bloat.
- `[[file:...]]` markers in the query focus retrieval to fewer files with deeper per-file slices.
- Triple-backtick code blocks in the query are treated as explicit injected context (budget-limited).
- When history exceeds budget, DFMC injects a compact history summary instead of dropping all older context.

Recommended baseline:

```yaml
context:
  max_files: 50
  max_tokens_total: 16000
  max_tokens_per_file: 2000
  max_history_tokens: 1200
  compression: standard
  include_tests: false
  include_docs: true
```

Hook safety:

- Project-local hooks are disabled by default; enable them explicitly with `hooks.allow_project: true` in your global config if you trust the repo.
- Prefer shell-free hooks for payload-safe automation:

```yaml
hooks:
  pre_tool:
    - name: audit
      command: go
      args: ["env", "GOOS"]
      shell: false
```

- Legacy `command: "echo hi && other"` hook entries still work through the platform shell, but treat them as trusted shell scripts. Avoid interpolating untrusted payload into shell syntax; use env vars or `args` instead.

## Project Structure

```text
cmd/dfmc                 # binary entrypoint
internal/engine          # orchestration lifecycle, agent loop, tool-exec gate
internal/config          # config loading/defaults/validation
internal/storage         # bbolt handle + artifact store
internal/ast             # tree-sitter (CGO) + regex fallback
internal/codemap         # dependency/symbol graph, DOT/SVG export
internal/context         # ranked context builder + budget/compression
internal/provider        # router, protocols (anthropic/openai/google/offline)
internal/tools           # tool registry + meta-tool layer + approval funnel
internal/drive           # autonomous plan/execute loop (planner + scheduler)
internal/intent          # state-aware sub-LLM request normalizer
internal/hooks           # user-configured lifecycle shell hooks
internal/coach           # trajectory-hint generator for agent loops
internal/supervisor      # shared task/executor types for drive + taskstore
internal/taskstore       # bbolt-backed task persistence (todo_write + HTTP/MCP)
internal/mcp             # MCP server + bridge (tool registry + Drive surface)
internal/security        # security scanner
internal/skills          # skill registry + shortcuts
internal/planning        # planning helpers
internal/pluginexec      # plugin execution runtime
internal/commands        # runtime slash-command registry
internal/promptlib       # task/language/role prompt library
internal/conversation    # JSONL persistence, branches
internal/memory          # working/episodic/semantic tiers
internal/tokens          # tokenization helpers
ui/cli                   # CLI entry, subcommands, remote client
ui/tui                   # bubbletea Model/View workbench
ui/web                   # HTTP/SSE server + Drive cockpit + task CRUD
pkg/types                # shared types and errors
```

## Notes on Accuracy and Safety

- Security and dead-code analysis are heuristic in current alpha.
- Results are useful for triage, not final security sign-off.
- False positives/negatives are expected while the engine is still evolving.
- Agent-initiated tool calls go through an approval gate and pre/post hooks. Auto-approve list is managed by `dfmc approvals`; project-local hooks are disabled by default (`hooks.allow_project: true` to opt in).
- Only one `dfmc` process at a time can open the bbolt store for a project; a second one hits `ErrStoreLocked`. `dfmc doctor` is whitelisted for degraded startup.

## Development

Run tests:

```bash
# Full race-enabled suite (what CI runs)
CGO_ENABLED=1 go test -race -count=1 ./...

# Single package / single test
go test ./internal/engine -run TestAgentLoop -v
```

Run formatted build loop:

```bash
gofmt -w ./...
go vet ./...
CGO_ENABLED=1 go test -race ./...
CGO_ENABLED=1 go build -o bin/dfmc ./cmd/dfmc
```

## License

Apache 2.0 (intended project license).
