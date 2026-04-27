# DFMC Roadmap — Plan

> Strategic plan for reaching GA-quality completeness. All 20 phases are shipped; this roadmap closes the remaining gaps.

---

## 0. STATUS SNAPSHOT — 2026-04-26

| Area | Status | Notes |
|------|--------|-------|
| Phases | 20/20 ✅ | All phases complete |
| Tests | All green | `go test -race -count=1 ./...` passes |
| Security | 49 vulns fixed | VULN-001 through VULN-049 |
| Test coverage | 48–93% | Wide variance; low coverage in MCP, plugin, CLI, TUI |
| TODO/FIXME count | 224 | Inline placeholders in source |
| Code quality | Good | `go vet` clean, `gofmt` applied |

### Low-coverage packages (GA blocker)

| Package | Coverage | Impact |
|---------|----------|--------|
| `ui/cli` | 48.1% | All CLI commands |
| `ui/tui` | 58.9% | Interactive TUI |
| `internal/mcp` | 52.3% | MCP server/client/bridge |
| `internal/pluginexec` | 49.5% | WASM + script plugin loaders |
| `cmd/dfmc` | 44.1% | Entry point |

### Missing features from SPEC

| Feature | Phase | Gap |
|---------|-------|-----|
| Google Gemini provider | 4 | Spec §12.1.3 — not implemented |
| WASM plugin loader | 11 | `internal/pluginexec/wasm.go` exists but `manager.go` doesn't wire it |
| Graph serialization to bbolt | 3 | Every startup rebuilds codemap from scratch |
| Incremental file watcher | 2 | Point exists, watcher not wired |
| Auto self-update | 20 | `dfmc update --check` only, no self-replace |
| Homebrew tap | 20 | Formula template only, no real tap |

---

## 1. STRATEGIC GOALS

### 1.1 What "GA-ready" means for DFMC

A production-ready binary that:
- Passes all tests with no race conditions
- Has >65% coverage on every package
- Ships a Google Gemini provider option
- Caches codemap to bbolt for fast startup
- Has no TODO/FIXME placeholders in source
- Supports WASM plugin loading
- Can update itself via Homebrew or GitHub release

### 1.2 Priority ordering

```
P0 — Coverage gaps (tests must pass first)
  1. ui/cli        (48.1% → 65%+)
  2. ui/tui        (58.9% → 65%+)
  3. internal/mcp  (52.3% → 65%+)
  4. internal/pluginexec (49.5% → 65%+)
  5. cmd/dfmc      (44.1% → 60%+)

P1 — Missing features (GA quality)
  6. Google Gemini provider
  7. Codemap bbolt serialization (3.12)
  8. WASM plugin wiring in manager

P2 — Cleanup (code quality)
  9. Clear all 224 TODO/FIXME/XXX markers
  10. Incremental file watcher (2.16 partial)
  11. Auto-update self-replace (20.2 partial)

P3 — Polish (good to have)
  12. Homebrew tap publish
  13. Shell completions full test coverage
  14. Man page full set
```

### 1.3 Dependencies

```
P0 coverage work has no dependencies — can run in parallel.
P1 Google Gemini blocks on: provider interface (done), config (done), router (done).
P1 Codemap serialization blocks on: bbolt usage (done), codemap indexer (done).
P1 WASM wiring blocks on: wazero dep (already in go.mod), wasm.go (exists).
P2 TODO clearing is independent but tedious — can parallelize across files.
P3 all independent.
```

---

## 2. APPROACH

### 2.1 Coverage work

Each package needs targeted test additions. Pattern:
- Table-driven tests for CLI command parsing and flag handling
- Integration-style tests for MCP client/server that start real listeners on `:0` ports
- TUI tests use the existing `NewForTest()` pattern with a mock Engine
- Plugin exec tests use temp directories with fake plugin manifests

Coverage command:
```bash
go test -cover -coverprofile=coverage.out ./...
go tool cover -func=coverage.out | grep -E "ui/cli|ui/tui|internal/mcp|internal/pluginexec|cmd/dfmc"
```

Run this at the start and end of each P0 sprint to measure progress.

### 2.2 Google Gemini provider

Already have `internal/provider/google.go` and `google_tools.go`. The gap is:
- `GoogleProvider.Complete()` not wired to router
- Config fields not in `defaults.go`
- `providers.google` profile not in the config YAML examples

Steps:
1. Check `google.go` Complete/Stream methods exist and are correct
2. Add `google` to `defaults.go` config struct
3. Add to `router.go` provider map initialization
4. Add integration test with mock or real API key

### 2.3 Codemap bbolt serialization

Codemap indexer builds the graph on every startup. We cache it to bbolt and load on startup if files haven't changed.

Key insight from `internal/codemap/graph.go`: the graph has `Nodes` map and `Edges` map. We serialize the full graph to bbolt and validate with file hashes on load.

Implementation:
1. Add `CodemapOptions.CachePath` field to config
2. `codemap.NewEngine(..., cachePath string)` accepts optional bbolt file
3. On startup: check if cache file exists, hash all indexed files, compare
4. If match: load serialized graph from bbolt → skip indexer
5. If mismatch: run indexer, save new serialization
6. Add `codemap.Graph.Save(path)` and `codemap.Graph.Load(path)` using bbolt

### 2.4 WASM plugin wiring

`internal/pluginexec/wasm.go` exists and uses wazero. The gap is `manager.go` doesn't call it.

Pattern in `manager.go`:
```go
switch plugin.Type {
case "go":     return loadGoPlugin(ctx, manifest)
case "script": return loadScriptPlugin(ctx, manifest)
case "wasm":   return loadWasmPlugin(ctx, manifest)  // MISSING
}
```

Need to add the wasm branch and wire it to `wasm.go`.

### 2.5 TODO/FIXME clearing

224 markers across the codebase. Strategy:
- Run `grep -rn "TODO\|FIXME\|XXX\|BUG\|HACK" --include="*.go" . | grep -v _test.go | grep -v vendor`
- Categorize: real bugs, future features, deferred refactors
- Real bugs → fix now
- Future features → create issue/Epic if important, remove if noise
- Deferred refactors → if not needed, remove the comment

---

## 3. DELIVERABLES

Three files in `.roadmap/`:

| File | Purpose |
|------|---------|
| `PLAN.md` (this file) | Strategy, priorities, dependencies, approach |
| `IMPLEMENTATION.md` | Technical design for each item — how it works architecturally |
| `TASKS.md` | Granular task list — what to do, in what order, with estimates |

After this roadmap:
1. Review `.roadmap/TASKS.md`
2. Start with P0 (coverage) — parallelize across agents
3. Then P1 (missing features)
4. Then P2 cleanup
5. Then P3 polish