# Don't Fuck My Code (DFMC)

Your Code Deserves Better.

DFMC is a code intelligence assistant written in Go. It combines local code analysis (AST + codemap + security heuristics) with a provider router that can fall back to offline mode when API providers are unavailable.

Status: Alpha (actively under development)

## Current State

Implemented now:
- CLI entrypoint and command router
- Config loading (defaults + global + project + env)
- Engine lifecycle and event bus
- Local AST extraction (regex-based v1)
- CodeMap graph (nodes/edges, cycles, hotspots, path traversal)
- Provider router with automatic offline fallback
- Live provider clients:
  - Anthropic Messages API
  - OpenAI-compatible Chat Completions API (`openai`, `deepseek`, `generic` with `base_url`)
- Streaming support:
  - `chat` now uses provider stream path (SSE for Anthropic/OpenAI-compatible providers)
- Context builder for relevant code snippets
- Tool engine (`read_file`, `write_file`, `edit_file`, `list_dir`, `grep_codebase`)
- Skill commands (`skill list/info/run`) and built-in shortcuts (`review`, `explain`, `refactor`, `test`, `doc`)
- Plugin commands (`plugin list/info/install/remove/enable/disable`) with config-backed enable state
- Web API server (`dfmc serve`) with status, codemap, tools, memory, files, chat SSE
- Conversation persistence (JSONL)
- Memory store (working + episodic + semantic via bbolt buckets)
- Security scan (regex patterns for secrets and common vulnerability indicators)
- Analyze pipeline with optional `--security`, `--dead-code`, `--complexity`

Planned next:
- Real tree-sitter integration
- Streaming-native provider transport (SSE) and richer tool-calling formats
- Richer tool catalog and skill execution pipeline
- TUI + WebUI + remote control

## Quick Start

Requirements:
- Go 1.23+
- Windows/Linux/macOS

### 1) Build

```bash
go build ./cmd/dfmc
```

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
go run ./cmd/dfmc ask "auth middleware nasil calisiyor?"
```

If provider API keys are not configured, DFMC automatically uses offline mode with local-context response generation.

### 3.1) Interactive streaming chat

```bash
go run ./cmd/dfmc chat
```

Chat slash commands:
- `/help`, `/exit`, `/clear`, `/save`, `/load <id>`, `/history [limit]`
- `/provider [name]`, `/model [name]`
- `/branch [name]`, `/branch list`, `/branch create <name>`, `/branch switch <name>`, `/branch compare <a> <b>`
- `/context show`, `/memory`, `/tools`, `/skills`, `/diff`, `/undo`, `/run <skill> [input]`, `/cost`

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
go run ./cmd/dfmc tool run grep_codebase --pattern "ErrProviderUnavailable" --max_results 10
go run ./cmd/dfmc map --format dot
go run ./cmd/dfmc map --format svg > codemap.svg
```

### 6.1) Run Web API

```bash
go run ./cmd/dfmc serve --host 127.0.0.1 --port 7788
go run ./cmd/dfmc serve --host 127.0.0.1 --port 7788 --open-browser=false
DFMC_WEB_TOKEN=change-me go run ./cmd/dfmc serve --host 127.0.0.1 --port 7788 --auth token
```

Endpoints:
- `GET /api/v1/status`
- `POST /api/v1/chat` (SSE)
- `GET /api/v1/codemap`
- `GET /api/v1/context/budget?q=...`
- `GET /api/v1/context/recommend?q=...`
- `GET /api/v1/context/brief?max_words=...&path=...`
- `POST /api/v1/analyze`
- `GET /api/v1/providers`
- `GET /api/v1/tools`
- `POST /api/v1/tools/:name`
- `GET /api/v1/skills`
- `POST /api/v1/skills/:name`
- `GET /api/v1/memory`
- `GET /api/v1/conversation`
- `POST /api/v1/conversation/new`
- `POST /api/v1/conversation/save`
- `POST /api/v1/conversation/load`
- `GET /api/v1/conversation/branches`
- `POST /api/v1/conversation/branches/create`
- `POST /api/v1/conversation/branches/switch`
- `GET /api/v1/conversation/branches/compare?a=...&b=...`
- `GET /api/v1/prompts`
- `GET /api/v1/prompts/stats?max_template_tokens=...&allow_var=...`
- `GET /api/v1/prompts/recommend?q=...`
- `POST /api/v1/prompts/render`
- `GET /api/v1/magicdoc`
- `POST /api/v1/magicdoc/update`
- `GET /api/v1/conversations`
- `GET /api/v1/conversations/search?q=...`
- `GET /api/v1/files`
- `GET /api/v1/files/:path`
- `GET /ws` (event stream, SSE)

### 6.2) Manage config

```bash
go run ./cmd/dfmc config list
go run ./cmd/dfmc config get providers.primary
go run ./cmd/dfmc config set context.include_tests true
go run ./cmd/dfmc config edit
go run ./cmd/dfmc context budget --query "security audit auth middleware"
go run ./cmd/dfmc context recommend --query "debug [[file:internal/auth/service.go]]"
go run ./cmd/dfmc context recent
go run ./cmd/dfmc context brief --max-words 240
go run ./cmd/dfmc context brief --path docs/BRIEF.md --max-words 180
```

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
go run ./cmd/dfmc --provider offline review "auth modulu icin riskleri bul"

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
go run ./cmd/dfmc remote ask --url http://127.0.0.1:7779 --message "auth middleware'i acikla"
go run ./cmd/dfmc remote tools --url http://127.0.0.1:7779
go run ./cmd/dfmc remote skills --url http://127.0.0.1:7779
go run ./cmd/dfmc remote prompt list --url http://127.0.0.1:7779
go run ./cmd/dfmc remote prompt render --url http://127.0.0.1:7779 --task security --query "auth audit"
go run ./cmd/dfmc remote prompt render --url http://127.0.0.1:7779 --task security --query "auth audit" --runtime-tool-style function-calling --runtime-max-context 1000
go run ./cmd/dfmc remote prompt recommend --url http://127.0.0.1:7779 --query "auth audit"
go run ./cmd/dfmc remote prompt stats --url http://127.0.0.1:7779 --max-template-tokens 450
go run ./cmd/dfmc remote magicdoc show --url http://127.0.0.1:7779
go run ./cmd/dfmc remote magicdoc update --url http://127.0.0.1:7779 --title "Remote Brief"
go run ./cmd/dfmc remote context budget --url http://127.0.0.1:7779 --query "security audit auth middleware"
go run ./cmd/dfmc remote context recommend --url http://127.0.0.1:7779 --query "debug [[file:internal/auth/service.go]]"
go run ./cmd/dfmc remote context brief --url http://127.0.0.1:7779 --max-words 240 --path docs/BRIEF.md
go run ./cmd/dfmc remote tool read_file --url http://127.0.0.1:7779 --param path=README.md --param line_start=1 --param line_end=5
go run ./cmd/dfmc remote skill review --url http://127.0.0.1:7779 --input "auth katmanini incele"
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
go run ./cmd/dfmc prompt render --task planning --language go --query "roadmap cikar"
go run ./cmd/dfmc prompt render --task review --var context_files="- internal/auth/middleware.go:1-120"
go run ./cmd/dfmc prompt render --query "auth audit" --runtime-tool-style function-calling --runtime-max-context 1000
go run ./cmd/dfmc prompt recommend --query "security audit auth middleware"
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

Available:
- `dfmc init`
- `dfmc version`
- `dfmc ask`
- `dfmc chat` (basic REPL)
- `dfmc analyze`
- `dfmc scan`
- `dfmc map`
- `dfmc tool`
- `dfmc memory`
- `dfmc conversation`
- `dfmc config`
- `dfmc prompt`
- `dfmc magicdoc`
- `dfmc plugin`
- `dfmc skill`
- `dfmc review`
- `dfmc explain`
- `dfmc refactor`
- `dfmc test`
- `dfmc doc`
- `dfmc serve`
- `dfmc remote`
- `dfmc doctor`
- `dfmc completion`
- `dfmc man`

Scaffolded (placeholder):
- none

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

## Project Structure

```text
cmd/dfmc                 # binary entrypoint
internal/engine          # orchestration lifecycle
internal/config          # config loading/default/validation
internal/storage         # bbolt + artifact store
internal/ast             # AST extraction v1
internal/codemap         # dependency/symbol graph
internal/context         # context builder
internal/provider        # provider router + offline fallback
internal/tools           # tool registry/executor
internal/security        # security scanner
internal/conversation    # conversation persistence
internal/memory          # memory system
ui/cli                   # CLI commands
pkg/types                # shared types and errors
```

## Notes on Accuracy and Safety

- Security and dead-code analysis are heuristic in current alpha.
- Results are useful for triage, not final security sign-off.
- False positives/negatives are expected while the engine is still evolving.

## Development

Run tests:

```bash
go test ./...
```

Run formatted build loop:

```bash
gofmt -w ./...
go test ./...
go build ./cmd/dfmc
```

## License

Apache 2.0 (intended project license).
