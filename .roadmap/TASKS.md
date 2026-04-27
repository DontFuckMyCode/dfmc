# DFMC Roadmap — Tasks

> Granular task breakdown for achieving GA-readiness. Ordered by priority.

---

## P0 — COVERAGE IMPROVEMENTS

**Prerequisite**: Run `go test -cover ./... > /tmp/cov_before.txt 2>&1 && cat /tmp/cov_before.txt` before starting to establish baseline.

---

### P0.1 `ui/cli` Coverage (48.1% → 65%)

**Est**: 6h | **Dep**: None | **Files**: `ui/cli/cli_*_test.go`

- [ ] **P0.1.1** — Create `ui/cli/cli_admin_test.go`
  - Table-driven tests for `dfmc doctor` (happy path, store-locked, no-config)
  - `dfmc completion` (bash/zsh/fish/pwsh flags, output non-empty)
  - `dfmc init` (verifies .dfmc/config.yaml created)

- [ ] **P0.1.2** — Create `ui/cli/cli_analysis_test.go`
  - Table-driven tests for `dfmc analyze --json --full --security --complexity --deps --dead-code --duplication`
  - `dfmc scan` (--json flag, colored output presence)
  - `dfmc map --format (ascii|json|dot|svg) --depth N --filter PATTERN`

- [ ] **P0.1.3** — Create `ui/cli/cli_ask_chat_test.go`
  - `dfmc ask "text"` arg parsing, --file/--symbol/--race/--json flags
  - `dfmc chat` flag parsing (interactive portion cannot be unit-tested, skip)

- [ ] **P0.1.4** — Create `ui/cli/cli_remote_test.go`
  - `dfmc remote start --grpc-port 7778 --ws-port 7779 --auth token`
  - `dfmc remote list` (calls /api/v1/drive/active, parses JSON response)
  - `dfmc remote stop <id>` (calls /api/v1/drive/:id/stop)

- [ ] **P0.1.5** — Create `ui/cli/cli_plugin_skill_test.go`
  - `dfmc plugin list` (empty and with plugins)
  - `dfmc skill list` (empty and with skills)
  - `dfmc skill run <name> --args` passes through to skill executor

- [ ] **P0.1.6** — Verify coverage > 65% with `go test -cover ui/cli/...`

---

### P0.2 `ui/tui` Coverage (58.9% → 65%)

**Est**: 4h | **Dep**: None | **Files**: `ui/tui/*_test.go`

- [ ] **P0.2.1** — Create `ui/tui/chat_commands_test.go`
  - Slash command parsing: `/help`, `/clear`, `/branch`, `/provider`, `/model`, `/context`, `/memory`, `/tools`, `/skills`, `/cost`, `/save`, `/load`, `/diff`, `/apply`, `/undo`, `/exit`
  - Unknown command returns error string
  - Args extraction for commands that take args

- [ ] **P0.2.2** — Create `ui/tui/panel_states_test.go`
  - Verify `applyDefaults()` sets correct initial values
  - Panel state transitions: Chat → Files → Status (keyboard navigation)
  - Panel state structs (`memoryPanelState`, `codemapPanelState`) fields

- [ ] **P0.2.3** — Create `ui/tui/engine_events_test.go`
  - Event type routing: `drive:*` → Drive panel update
  - Event type routing: `agent:*` → Agent status badge
  - Event type routing: `provider:*` → Provider indicator
  - Unknown event types are logged and dropped (no crash)

- [ ] **P0.2.4** — Create `ui/tui/intent_test.go`
  - Badge text for `DecisionNew` → "NEW"
  - Badge text for `DecisionResume` → "RESUME"
  - Badge text for `DecisionClarify` → "CLARIFY"

- [ ] **P0.2.5** — Verify coverage > 65% with `go test -cover ui/tui/...`

---

### P0.3 `internal/mcp` Coverage (52.3% → 65%)

**Est**: 5h | **Dep**: None | **Files**: `internal/mcp/*_test.go`

- [ ] **P0.3.1** — Create `internal/mcp/mcp_server_test.go`
  - Start real listener on `:0` port
  - Send JSON-RPC `initialize` request → verify capabilities response
  - Send `tools/list` → verify tool list in response
  - Send `pings` → verify pong
  - Send invalid method → verify `-32601 Method not found`

- [ ] **P0.3.2** — Create `internal/mcp/mcp_bridge_test.go`
  - `MCPToolSpec → ToolSpec` translation with known input/output
  - `MCPRequestJSON → ToolRequest` parsing
  - `ToolResult → MCPResponseJSON` round-trip
  - Handle invalid MCP JSON (malformed JSON, missing required fields)

- [ ] **P0.3.3** — Verify coverage > 65% with `go test -cover internal/mcp/...`

---

### P0.4 `internal/pluginexec` Coverage (49.5% → 65%)

**Est**: 4h | **Dep**: None | **Files**: `internal/pluginexec/*_test.go`

- [ ] **P0.4.1** — Create `internal/pluginexec/wasm_test.go`
  - Create minimal valid WASM binary (use wazero to compile inline WAT: `(module (func (export "main") (result i32) (i32.const 42)))`)
  - `LoadWasm(path)` → returns `*Plugin` with correct name/version
  - WASM plugin with invalid/malformed binary → returns error
  - WASM plugin memory limit enforcement

- [ ] **P0.4.2** — Create `internal/pluginexec/script_test.go`
  - Create temp Python/Shell script plugin with `plugin.yaml`
  - `LoadScript(path)` → returns `*Plugin`
  - Unknown script interpreter → returns error
  - Script plugin stdin/stdout JSON-RPC round-trip

- [ ] **P0.4.3** — Create `internal/pluginexec/client_test.go`
  - `ScriptPluginClient.Send(ctx, method, params)` with piped subprocess
  - Receive handles `{"jsonrpc":"2.0","id":1,"result":...}`
  - Receive handles `{"jsonrpc":"2.0","id":1,"error":...}`
  - Timeout on unresponsive script plugin

- [ ] **P0.4.4** — Verify coverage > 65% with `go test -cover internal/pluginexec/...`

---

### P0.5 `cmd/dfmc` Coverage (44.1% → 60%)

**Est**: 2h | **Dep**: None | **Files**: `cmd/dfmc/*_test.go`

- [ ] **P0.5.1** — Create `cmd/dfmc/main_test.go` additions
  - `dfmc --help` exits with code 0
  - `dfmc --version` exits with code 0, output contains version string
  - `dfmc <unknown-command>` exits with non-zero and shows usage
  - Start `dfmc chat` in subprocess, send SIGINT → verifies clean exit within 5s

- [ ] **P0.5.2** — Verify coverage > 60% with `go test -cover cmd/dfmc/...`

---

**P0 COVERAGE BONUS** (optional, if time allows):
- [ ] Add `internal/taskstore` coverage from 74.4% → 80%+
  - `store_test.go` already has `tree_test` coverage, add `id_test.go` integration

---

## P1 — MISSING FEATURES

---

### P1.1 Google Gemini Provider

**Est**: 4h | **Dep**: None | **Files**: `internal/provider/google.go`, `internal/config/defaults.go`, `internal/provider/router.go`

- [ ] **P1.1.1** — Audit `internal/provider/google.go`
  - Verify `Complete(ctx, req) (*CompletionResponse, error)` method exists
  - Verify `Stream(ctx, req) (<-chan StreamEvent, error)` method exists
  - Verify `CountTokens(content string) int` method exists
  - Verify request format matches `generateContent` REST API
  - Verify response parsing: `candidates[0].content.parts` → `CompletionResponse`

- [ ] **P1.1.2** — Add Google config to `internal/config/defaults.go`
  - Add `Google struct { APIKey, Model string; MaxTokens int }` to `Config`
  - Add `google:` section to `DefaultConfig()`
  - Add `GOOGLE_AI_API_KEY` env var support (already in config cascade)

- [ ] **P1.1.3** — Wire Google into provider router
  - In `NewRouter(cfg)` → add `providers["google"] = NewGoogleProvider(cfg.Google.APIKey, cfg.Google.Model)`
  - Add "google" to `primary` or `fallback` list in router

- [ ] **P1.1.4** — Create `internal/provider/google_test.go`
  - Test request building (marshal/unmarshal round-trip)
  - Test response parsing (verify fields extracted correctly)
  - Test error handling (quota exceeded, invalid API key, network error)
  - Test streaming SSE parsing for `generateContent` streaming endpoint

- [ ] **P1.1.5** — Add to `dfmc status` and `dfmc doctor` provider list

---

### P1.2 Codemap Bbolt Serialization

**Est**: 8h | **Dep**: None | **Files**: `internal/codemap/graph.go`, `internal/codemap/engine.go`

- [ ] **P1.2.1** — Design `GraphSnapshot` struct in `internal/codemap/graph.go`
  - Add `GraphSnapshot` with `Version`, `Nodes []NodeSnapshot`, `Edges []EdgeSnapshot`, `FileHashes map[string]string`
  - Add `NodeSnapshot` and `EdgeSnapshot` parallel to existing `Node` and `Edge` types
  - Add `func (g *DirectedGraph) ToSnapshot() *GraphSnapshot`
  - Add `func SnapshotToGraph(s *GraphSnapshot) *DirectedGraph`

- [ ] **P1.2.2** — Add bbolt Save/Load methods in `internal/codemap/graph.go`
  - `func (g *DirectedGraph) SaveBucket(bucket *bbolt.Bucket, key string) error`
  - `func LoadGraphFromBucket(bucket *bbolt.Bucket, key string) (*DirectedGraph, error)`
  - Handle version mismatch (v1 vs v2 schema)

- [ ] **P1.2.3** — Modify `NewEngine(root, cachePath)` in `internal/codemap/engine.go`
  - Accept `cachePath string` parameter
  - On init: try to load graph from `cachePath/codemap.db`
  - Compute `hashFile(path)` for all indexed files → compare to stored hashes
  - If all match: skip indexer, return loaded graph
  - If any mismatch: run indexer normally, then save to cache

- [ ] **P1.2.4** — Add `func hashFile(path string) (string, error)` helper
  - Use `sha256` of file content
  - Handle missing files (treat as hash mismatch → invalidate cache)

- [ ] **P1.2.5** — Create `internal/codemap/graph_save_load_test.go`
  - Round-trip: graph → snapshot → graph, verify node/edge counts match
  - Empty graph round-trip
  - Large graph round-trip (1000+ nodes)

- [ ] **P1.2.6** — Create `internal/codemap/engine_cache_test.go`
  - Cache hit scenario: load same files twice, second time uses cache
  - Cache miss scenario: cache exists but file changed → rebuild
  - Cache invalid scenario: cache file deleted → rebuild
  - Cache with 0 files (new project) → builds and saves

---

### P1.3 WASM Plugin Wiring

**Est**: 3h | **Dep**: None | **Files**: `internal/pluginexec/manager.go`, `internal/pluginexec/wasm.go`

- [ ] **P1.3.1** — Verify `internal/pluginexec/wasm.go` is complete
  - Check `loadWasmPlugin(ctx context.Context, manifest *PluginManifest) (*Plugin, error)` exists
  - Verify it uses wazero runtime
  - Verify memory limit (256MB default) is set
  - Verify host function bindings are sandboxed

- [ ] **P1.3.2** — Add WASM case to `LoadPlugin` switch in `internal/pluginexec/manager.go`
  ```go
  case "wasm":
      return loadWasmPlugin(ctx, manifest)
  ```

- [ ] **P1.3.3** — Add WASM-specific manifest validation
  - Required fields: `entry` (path to .wasm file), `memory_limit` (optional, default 256MB)
  - Validate `.wasm` file exists and is valid WASM binary (magic bytes `\0asm`)

---

## P2 — CLEANUP

---

### P2.1 Clear all TODO/FIXME/XXX markers

**Est**: 6h | **Dep**: None | **Files**: All `.go` files (non-test)

- [ ] **P2.1.1** — Run `grep -rn "TODO\|FIXME\|XXX\|BUG\|HACK" --include="*.go" . | grep -v "_test.go" | grep -v vendor > /tmp/todos.txt`
  - Review each marker, classify as: Real bug / Future feature / Noise / Deferred
  - Create tasks for real bugs and important future features
  - Remove noise/deferred markers

- [ ] **P2.1.2** — Fix all real bugs immediately (inline in same PR)

- [ ] **P2.1.3** — Create GitHub issues or add to TASKS.md for future features

- [ ] **P2.1.4** — Remove all noise/deferred markers from source

---

### P2.2 Incremental File Watcher (2.16 partial)

**Est**: 5h | **Dep**: P1.2 (codemap cache) | **Files**: `internal/ast/watcher.go`, `internal/codemap/engine.go`

- [ ] **P2.2.1** — Create `internal/ast/watcher.go`
  - `type Watcher struct { fsnotifyWatcher *fsnotify.Watcher; engine *Engine }`
  - `func NewWatcher(root string, engine *Engine) (*Watcher, error)`
  - On file change: compute SHA256, compare to stored hash
  - If changed: call `ast.Parser.ParseFileIncremental(path)` → update symbols
  - Fire `file:changed` event on engine EventBus

- [ ] **P2.2.2** — Add `Watcher.Start()` and `Watcher.Close()` methods
  - `Start()` runs the watcher loop in goroutine
  - `Close()` stops the watcher cleanly

- [ ] **P2.2.3** — Wire watcher into `engine.Init()` lifecycle
  - Start watcher after AST indexer completes
  - Stop watcher on engine shutdown

- [ ] **P2.2.4** — Create `internal/ast/watcher_test.go`
  - Create temp file, modify, verify `file:changed` event fires
  - Verify incremental parse runs (not full re-parse)

---

### P2.3 Auto Self-Update (20.2 partial)

**Est**: 4h | **Dep**: None | **Files**: `internal/commands/update.go`

- [ ] **P2.3.1** — Audit `internal/commands/update.go`
  - Find `Check()` function — verify it calls GitHub releases API
  - Find where it outputs "update available" message

- [ ] **P2.3.2** — Add `Download(release Release, destPath string) error`
  - Download asset from `release.Assets[0].BrowserDownloadURL`
  - Verify SHA256 against `release.Assets[0].SHA256` (if published)
  - Write to temp file first, then rename

- [ ] **P2.3.3** — Add `ReplaceBinary(tmpPath, destPath) error`
  - On Unix: `os.Rename(tmpPath, destPath)` (atomic if same filesystem)
  - On Windows: copy to dest, schedule deletion on reboot (Win has restrictions on replacing running binary)
  - For Windows: use `os.Rename` and warn user to restart manually if rename fails

- [ ] **P2.3.4** — Add `--force` flag to bypass version check

- [ ] **P2.3.5** — Create `internal/commands/update_test.go`
  - Mock GitHub releases API response
  - Test `Check()` returns correct version info
  - Test `Download()` with mock HTTP server

---

## P3 — POLISH

---

### P3.1 Homebrew Tap

**Est**: 2h | **Dep**: P1.1 (Google provider wired, so version is stable) | **Files**: `shell/Formula.rb`

- [ ] **P3.1.1** — Review `shell/Formula.rb` template
  - Verify `url`, `sha256`, `version` substitutions are correct
  - Verify `depends_on "go"` for source build OR prebuilt bottle

- [ ] **P3.1.2** — Decide: publish to `homebrew-core` (requires contribution) or maintain own tap (`dontfuckmycode/homebrew-dfmc`)
  - Own tap is faster to set up: `brew tap dontfuckmycode/dfmc`
  - For own tap: push formula to `homebrew-dfmc` GitHub repo

---

### P3.2 Shell Completions Test Coverage

**Est**: 1h | **Dep**: None | **Files**: `shell/completions_test.go`

- [ ] **P3.2.1** — Create `shell/completions_test.go`
  - `TestBashCompletions()` → run `dfmc completion bash`, verify non-empty
  - `TestZshCompletions()` → run `dfmc completion zsh`, verify non-empty
  - `TestFishCompletions()` → run `dfmc completion fish`, verify non-empty
  - `TestPowershellCompletions()` → run `dfmc completion pwsh`, verify non-empty
  - Parse output — verify no duplicate flag definitions

---

### P3.3 Man Page Full Set

**Est**: 2h | **Dep**: None | **Files**: `shell/gen_man.go`, `man/` directory

- [ ] **P3.3.1** — Create `man/` directory with full man page set:
  - `man/man1/dfmc.1` — main page
  - `man/man1/dfmc-chat.1`, `dfmc-ask.1`, `dfmc-analyze.1`, `dfmc-serve.1`, etc.
  - Use `shell/gen_man.go` output as base, verify formatting

- [ ] **P3.3.2** — Add `dfmc install` command to install man pages
  - Copies `man/` to `$HOME/.local/share/man/man1/`
  - Creates `.dfmc/` cache dir if not exists

---

## TASK SUMMARY

| ID | Task | Est | Priority | Phase |
|----|------|-----|----------|-------|
| P0.1.1–6 | ui/cli coverage 48%→65% | 6h | P0 | Coverage |
| P0.2.1–5 | ui/tui coverage 59%→65% | 4h | P0 | Coverage |
| P0.3.1–3 | internal/mcp coverage 52%→65% | 5h | P0 | Coverage |
| P0.4.1–4 | internal/pluginexec coverage 50%→65% | 4h | P0 | Coverage |
| P0.5.1–2 | cmd/dfmc coverage 44%→60% | 2h | P0 | Coverage |
| P1.1.1–5 | Google Gemini provider | 4h | P1 | Features |
| P1.2.1–6 | Codemap bbolt serialization | 8h | P1 | Features |
| P1.3.1–3 | WASM plugin wiring | 3h | P1 | Features |
| P2.1.1–4 | Clear 224 TODO/FIXME markers | 6h | P2 | Cleanup |
| P2.2.1–4 | Incremental file watcher | 5h | P2 | Cleanup |
| P2.3.1–5 | Auto self-update | 4h | P2 | Cleanup |
| P3.1.1–2 | Homebrew tap | 2h | P3 | Polish |
| P3.2.1 | Shell completions tests | 1h | P3 | Polish |
| P3.3.1–2 | Full man page set | 2h | P3 | Polish |
| **TOTAL** | | **56h** | | |

**Execution order**:
1. Run baseline coverage → P0 tasks in parallel (5 workstreams)
2. P0 done → P1.1 (Gemini), P1.2 (codemap), P1.3 (WASM)
3. P1 done → P2 tasks (cleanup parallel)
4. P2 done → P3 tasks (polish)