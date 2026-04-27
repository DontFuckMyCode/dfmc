# DFMC Roadmap — Implementation

> Technical design for closing the remaining GA gaps.

---

## 1. COVERAGE IMPROVEMENTS

### 1.1 `ui/cli` (48.1% → 65%+)

**Pattern**: Existing `ui/cli/cli_test.go` has basic tests. Need to expand.

Current coverage gaps (from `go test -cover` per-file analysis):
- `cli_admin.go` — admin commands (doctor, completion, man, init)
- `cli_analysis.go` — analyze/scan/map commands
- `cli_ask_chat.go` — ask/chat commands
- `cli_remote.go` — remote subcommands
- `cli_plugin_skill.go` — plugin/skill commands

**Strategy**: Add table-driven tests per command file.

```
cli_admin.go test matrix:
- dfmc doctor      → happy path, store locked scenario, no config
- dfmc completion  → bash/zsh/fish/pwsh flags
- dfmc man         → output non-empty
- dfmc init        → creates .dfmc/, already has basic test

cli_analysis.go test matrix:
- dfmc analyze     → --json, --full, --security, --complexity, --deps, --dead-code, --duplication, path arg
- dfmc scan        → --json, colored output
- dfmc map         → --format (ascii/json/dot/svg), --depth, --filter

cli_ask_chat.go test matrix:
- dfmc ask         → text arg, --file, --symbol, --race, --json
- dfmc chat        → flags parsing only (interactive harder to test)

cli_remote.go test matrix:
- dfmc remote start → flags (grpc-port, ws-port, auth)
- dfmc remote list   → calls /api/v1/drive/active
- dfmc remote stop   → calls /api/v1/drive/:id/stop

cli_plugin_skill.go test matrix:
- dfmc plugin list   → empty and with plugins
- dfmc skill list    → empty and with skills
- dfmc skill run     → passes args through
```

**Key files to modify**: `ui/cli/cli_admin.go`, `ui/cli/cli_analysis.go`, `ui/cli/cli_ask_chat.go`, `ui/cli/cli_remote.go`, `ui/cli/cli_plugin_skill.go` — each gets a `cli_*_test.go` sibling with table-driven tests.

### 1.2 `ui/tui` (58.9% → 65%+)

**Pattern**: `ui/tui/tui_test.go` uses `NewForTest()`. Current gaps:
- Panel state transitions
- Slash command dispatch (`chat_commands.go`)
- Engine event handling (`engine_events.go`)
- Intent decision badge rendering

**Strategy**: The TUI is hard to unit-test fully (bubble tea Model). Focus on:
1. `chat_commands_test.go` — slash command parsing, unknown commands
2. `panel_states_test.go` — verify default values and transition logic
3. `engine_events_test.go` — event type routing (drive:*, agent:*, provider:*)
4. `intent_test.go` — badge text generation for each Decision type

The `renderChatView` requires `m.ui.toolStripExpanded = true` to show per-chip fields — ensure all existing tests that check chip content set this flag.

### 1.3 `internal/mcp` (52.3% → 65%+)

**Pattern**: `protocol_test.go` and `client_test.go` exist but coverage is thin.

Current gaps:
- `server.go` — JSON-RPC request/notification dispatch
- `bridge.go` — tool translation between MCP ↔ DFMC formats

**Strategy**: 
1. `mcp_server_test.go` — start real listener on `:0`, send JSON-RPC requests, verify responses
2. `mcp_bridge_test.go` — test `MCPToolSpec → ToolSpec` and `MCPRequest → ToolRequest` translations with known inputs

The MCP protocol is JSON-RPC 2.0. Test each method:
- `initialize` → returns capabilities
- `tools/list` → returns tool list
- `tools/call` → routes to engine
- `pings` → simple ping/pong
- Invalid method → `-32601 Method not found`

### 1.4 `internal/pluginexec` (49.5% → 65%+)

**Pattern**: `manager_test.go` tests Go plugin loading only. Script and WASM not tested.

Current gaps:
- `wasm.go` — `loadWasmPlugin` not exercised
- `manager.go` — `loadScriptPlugin` path not tested
- `client.go` — stdio JSON-RPC round-trip

**Strategy**:
1. Add `pluginexec_wasm_test.go` — create temp `.wasm` file (minimal valid WASM), test `LoadWasm(path)` returns `*Plugin` or error
2. Add `pluginexec_script_test.go` — create temp script plugin (Python/Shell/Node), test `LoadScript(path)` path
3. Add `client_test.go` for `ScriptPluginClient.Send/Receive` with piped subprocess

WASM test approach: use a minimal compiled WASM binary (even a "hello" function that returns 42). If compiling is too heavy for test, use the `wazero` compiler to compile from WebAssembly text format (WAT) inline in the test.

### 1.5 `cmd/dfmc` (44.1% → 60%+)

**Pattern**: `main_test.go` exists. Gap is signal handling and lifecycle.

**Strategy**: Test the deferred signal handlers and graceful shutdown path:
- Send `SIGINT` to a subprocess running `dfmc chat` → verify it exits cleanly
- Test `--help`, `--version` exit codes

---

## 2. MISSING FEATURES

### 2.1 Google Gemini Provider

**Current state**: `internal/provider/google.go` exists with `GoogleProvider` struct, but `Complete()` method calls are not routed. The router in `router.go` has a `providers` map that needs `google` key.

**What to check first**:
```bash
grep -n "func (p \*GoogleProvider) Complete" internal/provider/google.go
grep -n "google" internal/provider/router.go
grep -n "google" internal/config/defaults.go
```

**Implementation steps**:

1. **`internal/provider/google.go`** — verify `Complete(ctx, req *CompletionRequest) (*CompletionResponse, error)` and `Stream(ctx, req *CompletionRequest) (<-chan StreamEvent, error)` exist and are correct for the Gemini `generateContent` API.

2. **`internal/config/defaults.go`** — add to `Config` struct:
   ```go
   Google struct {
       APIKey string `yaml:"api_key"`
       Model  string `yaml:"model"`  // default: "gemini-2.5-pro"
       MaxTokens int `yaml:"max_tokens"`
   } `yaml:"google"`
   ```
   And add to `DefaultConfig()`.

3. **`internal/provider/router.go`** — in `NewRouter(cfg)`, add:
   ```go
   if cfg.Google.APIKey != "" {
       providers["google"] = NewGoogleProvider(cfg.Google.APIKey, cfg.Google.Model)
   }
   ```
   And add "google" to fallback chain.

4. **`internal/provider/google_test.go`** — add provider tests:
   - Request building for `generateContent` format
   - Response parsing (Gemin's `candidates[0].content.parts` → `CompletionResponse`)
   - Streaming SSE parsing
   - Error handling (quota exceeded, invalid API key)

### 2.2 Codemap Bbolt Serialization

**Current state**: `internal/codemap/graph.go` has `DirectedGraph` struct with `Nodes` and `Edges`. Every startup runs `indexer.go` to rebuild. No cache.

**Design**:

```
Startup flow:
1. engine.Init() → codemap.NewEngine(root, cachePath)
2. NewEngine checks if cachePath/.codemap.db exists
3. If exists: compute hash of all indexed files
4. Compare hash against stored hash in bbolt
5. If match → load graph from bbolt, skip indexer
6. If mismatch → run indexer, save new graph

Cache validity:
- Store per-file hash (SHA256 of content) in bbolt
- Also store total file count and last-index time
- On load: verify ALL tracked files still exist and hashes match
- Any mismatch → invalidate cache, rebuild

Graph serialization format (bbolt key/value):
- key: "graph:v1" → value: JSON-encoded GraphSnapshot
- key: "file:<path>" → value: "<sha256-hash>"
- key: "meta" → value: JSON-encoded { fileCount, lastIndex, version }
```

**Implementation steps**:

1. **`internal/codemap/graph.go`** — add `GraphSnapshot` struct:
   ```go
   type GraphSnapshot struct {
       Version   int
       Nodes     []NodeSnapshot
       Edges     []EdgeSnapshot
       FileHashes map[string]string
   }
   type NodeSnapshot struct {
       ID, Symbol, File, Package string
       InDegree, OutDegree, Depth int
       Cluster string
   }
   type EdgeSnapshot struct {
       Source, Target string
       Kind   EdgeKind
       Weight float64
       File   string
       Line   int
   }
   ```

2. Add `func (g *DirectedGraph) Save(bucket *bbolt.Bucket) error` and `func Load(bucket *bbolt.Bucket) (*DirectedGraph, error)`.

3. **`internal/codemap/engine.go`** — modify `NewEngine(root, cachePath)`:
   - Accept `cachePath string`
   - Try to load from cache on startup
   - Invalidate and rebuild if hash mismatch
   - Save to cache on `Build()` completion

4. Add `func hashFile(path string) (string, error)` helper.

5. Add tests:
   - `graph_save_load_test.go` — round-trip serialization
   - `engine_cache_test.go` — cache hit/miss scenarios

### 2.3 WASM Plugin Wiring

**Current state**: `internal/pluginexec/wasm.go` has `loadWasmPlugin(manifest *PluginManifest) (*Plugin, error)` using wazero. `manager.go` has switch for `go` and `script` types but missing `wasm` case.

**Implementation steps**:

1. Check `internal/pluginexec/wasm.go` for `func loadWasmPlugin(ctx context.Context, manifest *PluginManifest) (*Plugin, error)` — if it exists, it's ready.

2. **`internal/pluginexec/manager.go`** — add to `LoadPlugin` switch:
   ```go
   case "wasm":
       return loadWasmPlugin(ctx, manifest)
   ```

3. Add manifest validation — WASM plugins need `.wasm` entry file, memory limit, allowed host bindings.

4. Add tests (see P0 coverage section).

---

## 3. CLEANUP

### 3.1 TODO/FIXME Clearing

**Count**: 224 markers across non-test `.go` files.

**Classification**:

```
grep -rn "TODO\|FIXME\|XXX\|BUG\|HACK" --include="*.go" . \
  | grep -v "_test.go" | grep -v vendor | sort

Categories:
A. Real bugs     → fix immediately
B. Future features → create task, keep comment
C. Deferred work → if not needed, remove comment
D. Noise/typo    → remove
```

**Pattern to apply**:
- `TODO(user):` — user-facing, create task
- `TODO(impl):` — implementation gap, high priority
- `FIXME:` — bug, must fix
- `XXX:` — known issue, must fix
- `HACK:` — temporary, must address

### 3.2 Incremental File Watcher (2.16 partial)

**Current state**: Tree-sitter incremental parsing point exists in `ast/parser.go`, but no `fsnotify` watcher wires into it.

**Implementation**:
1. Add `Watcher` struct in `ast/watcher.go` using `fsnotify` package.
2. On file change: compute hash, compare to stored hash, call `Parser.ParseFileIncremental(path)` if content changed.
3. Update codemap edges for changed file.
4. Fire `file:changed` event on bus.

### 3.3 Auto Self-Update

**Current state**: `dfmc update --check` queries GitHub releases API but does not download or replace the binary.

**Implementation**:
1. `update.go` already has `Check()` function — extend with `Download(release, path)` 
2. Verify downloaded binary checksum against GitHub-published checksums
3. Replace binary atomically (write to temp, rename on restart)
4. Use `os.Executable()` to find current binary path
5. Note: Windows requires admin or special flags for Program Files writes. Suggest `~/.dfmc/bin/` for user-level installs.

---

## 4. POLISH

### 4.1 Homebrew Tap

**Current state**: `shell/Formula.rb` template exists.

**To publish**:
1. Create `homebrew-dfmc` repo or submit to `homebrew-core`
2. Real formula needs:
   - Correct sha256 for each release asset
   - Runtime dependency on Go (for build from source) OR provide prebuilt bottle
   - Test with `brew install --dry-run`

### 4.2 Shell Completions

Already have `shell/completions.go` that generates scripts. Add tests for each shell output format.

### 4.3 Man Pages

`shell/gen_man.go` generates from CLI help. Add full set of man pages:
- `dfmc.1` — main page
- `dfmc-chat.1`, `dfmc-ask.1`, `dfmc-analyze.1` — subcommands
- Install to `$HOME/.local/share/man/man1/` on `dfmc install`