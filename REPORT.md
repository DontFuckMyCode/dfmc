# DFMC Code Review Report

**Date:** 2026-04-17  
**Reviewer:** DFMC (automated deep review)  
**Scope:** Hotspot files + core engine, AST, codemap, config, context, conversation, hooks, web server, TUI  
**Files examined:** 20+ source files (~8K lines read)  

---

## Summary

| Severity | Count |
|----------|-------|
| Critical | 1     |
| High     | 3     |
| Medium   | 4     |
| Low      | 3     |

No security vulnerabilities in the form of credential leaks, SQL injection, or unvalidated external input were found. The codebase shows strong defensive coding (nil-safe dispatchers, bounded buffers, panic guards, lock-ordering discipline). Findings cluster around **performance hot paths**, **a shell-injection surface**, and **maintainability of the monolithic TUI**.

---

## Critical

### C1 — Regex compiled per-parse in AST regex fallback (CPU waste ×10K+)

**File:** `internal/ast/engine.go:230–300`  
**Impact:** Every call to `extractSymbolsRegex` compiles 6–8 `regexp.MustCompile` patterns inside the function body. For a medium monorepo (~10K files), this means ~60K–80K unnecessary regex compilations during initial indexing. `regexp.MustCompile` is not free — it allocates and parses the AST each time.  
**Evidence:** The `case "typescript"` branch alone compiles 6 regexes; `"python"` compiles 3; `"rust"` compiles 4. No package-level `var` caching is visible.  
**Fix:** Move all `regexp.MustCompile` calls to package-level `var` declarations. The patterns are literals and never change at runtime. Example:

```go
var (
    reJSFunc       = regexp.MustCompile(`^\s*(?:export\s+)?(?:default\s+)?function\s+([A-Za-z_]\w*)\s*\(`)
    reJSClass      = regexp.MustCompile(`^\s*(?:export\s+)?(?:default\s+)?(?:abstract\s+)?class\s+([A-Za-z_]\w*)\b`)
    // ... etc
)
```

**Validation:** `go test ./internal/ast/... -bench=BenchmarkExtract -count=5` before/after should show ≥5× throughput improvement on the regex path.

---

## High

### H1 — LRU cache eviction uses O(n) linear scan per write

**File:** `internal/ast/engine.go:400–420`  
**Impact:** `parseCache.Set` does `for i, p := range c.order` to find and remove an existing entry from the order slice before re-appending it. With `defaultParseCacheSize = 10000`, every cache write that hits an existing key does an O(n) scan. During initial indexing (all writes are new keys), eviction still scans from the front. On a long-running `dfmc serve` with high churn, this becomes a measurable CPU sink.  
**Evidence:** `internal/ast/engine.go:404–408`:
```go
for i, p := range c.order {
    if p == path {
        c.order = append(c.order[:i], c.order[i+1:]...)
        break
    }
}
```
**Fix:** Add an `index map[string]int` to `parseCache` that maps path → position in `c.order`. On Set, if the path exists, use the index for O(1) removal. On eviction, update the displaced entry's index. Alternatively, replace the hand-rolled LRU with `container/list` (standard library doubly-linked list) for O(1) move-to-front.

### H2 — Shell injection surface in hook command execution

**File:** `internal/hooks/hooks.go:241–243`  
**Impact:** User-configured hook commands are executed via `exec.CommandContext(ctx, "sh", "-c", command)`. If an attacker can write to the DFMC config file (e.g., via a malicious git checkout of `.dfmc/config.yaml`), they achieve arbitrary code execution on every hook event. The security scanner (`internal/security/`) flags `exec.Command` with concatenation in user code but does not audit the hook dispatcher itself.  
**Evidence:** `internal/hooks/hooks.go:243`: `return exec.CommandContext(ctx, "sh", "-c", command)`  
**Fix (defense in depth):**  
1. Add a config-file permission check at load time: warn if `.dfmc/config.yaml` is group/world-writable.  
2. Document in the config schema that hook commands run with full shell interpretation and should be treated as trusted.  
3. Optionally, add an allowlist mode (`hooks.allow_only: ["git", "make"]`) that bypasses `sh -c` and uses `exec.Command(name, args...)` directly.

### H3 — Monolithic tui.go at 4475 lines

**File:** `ui/tui/tui.go`  
**Impact:** The file contains Model definition, Update, View, Init, message types, helper functions, and rendering. At 4475 lines it exceeds what any reviewer can hold in working memory. Changes to the chat renderer risk breaking the patch viewer because they share the same file scope. The project already splits domain methods (`chat_key.go`, `chat_commands.go`, `patch_view.go`, `engine_events.go`) but the core Update/View dispatch remains in the monolith.  
**Evidence:** `ui/tui/tui.go` — 4475 total lines per `read_file` metadata.  
**Fix:** Extract `Model.Update` and `Model.View` into `update.go` and `view.go` with per-tab handler methods already living in their respective files. The Model struct definition and constructor can stay in `tui.go`. Target: `tui.go` < 500 lines (struct + constants + New + Run).

---

## Medium

### M1 — Tree-sitter parse error collection truncates at 8 without indicator

**File:** `internal/ast/treesitter_cgo.go:421`  
**Impact:** `collectTreeSitterParseErrors` silently stops appending after 8 errors. A file with 50 syntax errors will only report 8, with no `[...and 42 more]` marker. Consumers (TUI error display, codemap diagnostics) have no way to know the list is incomplete, potentially leading users to believe they've fixed all errors when they haven't.  
**Evidence:** `internal/ast/treesitter_cgo.go:421`: `if node == nil || len(errs) >= 8 { return }`  
**Fix:** After the walk, if the cap was hit, append a synthetic `ParseError{Line: -1, Column: -1, Message: "...and N more errors omitted"}`. Or return a `Truncated bool` field on `ParseResult`.

### M2 — Web server missing Content-Security-Policy headers

**File:** `ui/web/server.go:220–228`  
**Impact:** The workbench HTML is served with only `Content-Type`. No `Content-Security-Policy`, `X-Content-Type-Options`, or `X-Frame-Options` headers are set. If the workbench loads external scripts (e.g., from a CDN) or the user's browser has a compromised extension, there's no browser-enforced boundary. The embedded HTML (`//go:embed static/index.html`) reduces the attack surface but does not eliminate it if the embedded template includes inline scripts.  
**Evidence:** `ui/web/server.go:225–227`:
```go
func (s *Server) handleIndex(w http.ResponseWriter, _ *http.Request) {
    w.Header().Set("Content-Type", "text/html; charset=utf-8")
    _, _ = w.Write([]byte(renderWorkbenchHTML()))
}
```
**Fix:** Add security headers as a middleware (consistent with the existing `limitRequestBodySize` middleware pattern):
```go
func securityHeaders(h http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'")
        w.Header().Set("X-Content-Type-Options", "nosniff")
        w.Header().Set("X-Frame-Options", "DENY")
        h.ServeHTTP(w, r)
    })
}
```

### M3 — Conversation save race window after lock release

**File:** `internal/conversation/manager.go:261–280`  
**Impact:** `SaveActive` takes `RLock`, snapshots the messages, releases the lock, then calls `store.SaveConversationLog` outside the lock. This is correct for not blocking readers during I/O, but creates a window where another goroutine could modify `active.Branches[active.Branch]` (e.g., a new user message) between the snapshot and the persist. The save would then be stale — missing the latest message. In practice, saves are triggered by explicit user action or shutdown, making the window small, but it's a correctness gap.  
**Evidence:** `internal/conversation/manager.go:268–278`:
```go
snapshot := make([]types.Message, len(msgs))
copy(snapshot, msgs)
store := m.store
m.mu.RUnlock()         // ← lock released
return store.SaveConversationLog(id, snapshot)  // ← stale if msgs mutated
```
**Fix:** Use a version counter or seqno on `active`. After `SaveConversationLog` returns, check if the seqno has advanced; if so, re-snapshot and retry (at most once). Alternatively, use a dedicated save-serializing mutex so saves are ordered but reads remain concurrent.

### M4 — context.Background() in engine Shutdown for hook dispatch

**File:** `internal/engine/engine.go:300`  
**Impact:** The `session_end` hook fires under `context.Background()` with a 5-second timeout. If the parent context was cancelled (e.g., SIGINT), the hook still gets a fresh 5s window — which is intentional for cleanup. However, there's no logging or event when the 5s timeout expires. If a hook hangs beyond 5s, the context cancellation is silent and the engine proceeds to close storage, potentially while the hook is still running (the hook process-group kill is async on some platforms).  
**Evidence:** `internal/engine/engine.go:300–304`:
```go
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()
e.Hooks.Fire(ctx, hooks.EventSessionEnd, hooks.Payload{...})
```
**Fix:** After `Fire`, check `ctx.Err()`. If it's `context.DeadlineExceeded`, log a warning to stderr and publish a `hook:timeout` event so the operator knows a shutdown hook was killed.

---

## Low

### L1 — Inconsistent nil-engine guard in TUI hot path

**File:** Multiple files under `ui/tui/`  
**Impact:** Some access points guard `m.eng != nil` (e.g., `command_picker.go:530`, `chat_key.go:395`), others don't (e.g., `context_panel.go:214` calls `m.eng.ContextBudgetPreview` without a nil check). If `eng` is ever nil during a panel render, the TUI panics. The `NewModel` constructor always sets `eng`, so this is defensive only — but the inconsistency makes future refactors risky.  
**Fix:** Add a nil-safe accessor `func (m Model) engine() *engine.Engine { return m.eng }` or enforce the guard at every call site via a lint rule.

### L2 — Hardcoded API keys / model identifiers in config seed profiles

**File:** `internal/config/config.go:734–752`  
**Impact:** `modelsDevSeedProfiles()` contains hardcoded `BaseURL` values for each provider. These are not secrets, but they're deployment-specific (e.g., `https://api.deepseek.com/v1`). If a provider changes their base URL, a code change + release is required instead of a config update.  
**Fix:** Move these to an external JSON/YAML file or the existing `models.dev` catalog fetch, keeping the Go map as a last-resort fallback only.

### L3 — BFS in Graph.Descendants/Ancestors uses slice-as-queue (amortized O(n) shift)

**File:** `internal/codemap/graph.go:202–260`  
**Impact:** Both methods use `queue = queue[1:]` to dequeue, which causes O(n) memory shifts per dequeue. For deep dependency graphs with thousands of nodes, this adds unnecessary allocation pressure.  
**Evidence:** `internal/codemap/graph.go:222`: `queue = queue[1:]`  
**Fix:** Use index-based dequeue (`head++`) or a `container/list` queue. For the typical DFMC graph size (4483 nodes per the project brief), this is negligible, but it's a standard Go performance pattern worth applying.

---

## Positive Observations

1. **Lock ordering documented in code** (`internal/engine/engine.go:56–61`) — explicit comment preventing deadlocks.
2. **Bounded buffers everywhere** — `pendingQueueCap`, `hookOutputCap`, `maxRequestBodyBytes`, `maxBatchCalls` prevent OOM from untrusted input.
3. **Panic guard on TUI crash** (`ui/tui/tui.go:490–515`) — ANSI reset sequences restore terminal state.
4. **Proper shutdown staging** — Engine cancels background goroutines before closing stores.
5. **Nil-safe dispatcher pattern** — `Hooks.Fire` and `Intent.Router` are no-op on nil, avoiding nil-pointer panics across the codebase.
6. **Edge key composite** (`internal/codemap/graph.go:31`) — Previously fixed bug where edge type collisions silently overwrote entries; now uses `(Node, Type)` composite key.

---

## Residual Risk

- **No fuzz / property tests** for the AST parser or codemap builder. A malformed source file could trigger a regex panic or infinite loop in the tree-sitter walker.
- **Web server has no auth** — `ui/web/server.go` binds without authentication. Acceptable for localhost-only use, but risky if exposed on a network interface.
- **Test coverage gap on the TUI Update path** — `tui_test.go` is 3779 lines but mostly covers slash commands and model switching; the core Update dispatch (key events, window resize, error states) has sparse coverage.
