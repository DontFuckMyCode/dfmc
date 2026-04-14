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
- Tool engine (`read_file`, `list_dir`, `grep_codebase`)
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

### 4) Analyze codebase

```bash
go run ./cmd/dfmc analyze
go run ./cmd/dfmc analyze --security --dead-code --complexity
go run ./cmd/dfmc analyze --full --json
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
go run ./cmd/dfmc tool run grep_codebase --pattern "ErrProviderUnavailable" --max_results 10
```

### 6.1) Run Web API

```bash
go run ./cmd/dfmc serve --host 127.0.0.1 --port 7788
```

Endpoints:
- `GET /api/v1/status`
- `POST /api/v1/chat` (SSE)
- `GET /api/v1/codemap`
- `POST /api/v1/analyze`
- `GET /api/v1/providers`
- `GET /api/v1/tools`
- `POST /api/v1/tools/:name`
- `GET /api/v1/skills`
- `POST /api/v1/skills/:name` (placeholder response)
- `GET /api/v1/memory`
- `GET /api/v1/files`
- `GET /api/v1/files/:path`

### 6.2) Manage config

```bash
go run ./cmd/dfmc config list
go run ./cmd/dfmc config get providers.primary
go run ./cmd/dfmc config set context.include_tests true
go run ./cmd/dfmc config edit
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
```

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
- `dfmc serve`

Scaffolded (placeholder):
- `plugin`, `skill`, `remote`, `review`, `explain`, `refactor`, `test`, `doc`

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
