# DFMC Refactor Report

**Date:** 2026-05-15
**Last updated:** 2026-05-15 (Sprint 1 fixes applied — §1 items closed)
**Scope:** Full-codebase audit for refactor opportunities, tech debt, and architectural drift.
**Methodology:** Static measurement (LOC, file count, function count) + targeted file:line verification of every claim before write-up. Speculative findings have been omitted.

## Status

- ✅ **Sprint 1 — Latent bugs** (§1) — applied. Two bug fixes + two regression tests + one documented exception in CLAUDE.md + `internal/repolint` tripwire to keep the `_ = err` pattern out forever. See §1 entries for the closing commit summary.
- ⏳ Sprint 2 — Decisions (§4.1, §2.1) — awaiting product call.
- 🟡 Sprint 3 — Cleanup carving — partially started. `interface{} → any` cleanup done (§6.1); `chat_commands_*` collapse and `tui_test.go` split still pending.
- ⏳ Sprint 4 — God-object decomposition — not started.

---

## 0. Executive Summary

DFMC is a healthy, well-organized Go codebase of **~130k LOC across 777 source files** (371 test files — a 48% test ratio, which is good). `go vet ./...` is clean. There are **no critical bugs** that I can verify; the issues below are about long-term maintainability, drift, and a small number of latent correctness bugs in lower-traffic tools.

The codebase consciously uses a "split big types across many sibling files" pattern (`engine_<topic>.go`, `agent_loop_*.go`, `chat_commands_*.go`). That pattern is fine for navigation but is now hiding two real god-objects:

- **`engine.Engine` carries 246 methods** across 96 files.
- **`tui.Model` carries 115 methods** across 266 files.

Both should be considered for **interface segregation** so callers depend on capability subsets rather than the whole hub.

The three most actionable concrete items (each verified at file:line):

| # | File:line                                          | Severity | Effort |
| - | -------------------------------------------------- | -------- | ------ |
| 1 | `internal/tools/symbol_rename.go:203-205`          | Med      | XS     |
| 2 | `internal/tools/symbol_move.go:237-239`            | Med      | XS     |
| 3 | `internal/engine/engine_drive_spec.go:42`          | Med      | XS     |

Details below.

---

## 1. Verified Latent Bugs (fix these first)

These are real, file:line-confirmed correctness issues. All three are small patches.

### 1.1 `symbol_rename` silently discards `os.WriteFile` errors — ✅ FIXED

**Was at** [`internal/tools/symbol_rename.go:203-205`](internal/tools/symbol_rename.go#L203-L205):
```go
if err := os.WriteFile(fpath, []byte(strings.Join(lines, "\n")), 0644); err != nil {
    _ = err
}
```
The change was still appended to the success record below. **A failed write reported success.** The user saw "Renamed X → Y in N files" when the disk write actually failed (read-only filesystem, AV lock on Windows, full disk).

**Fix shipped:** Added `Failed int` to `renameImpact`; populated `failed []string` slice in `Data` when writes fail; suppressed the failing path from `changes[]`; updated `Output` to surface `"— N file(s) failed to write"`. Regression test `TestSymbolRename_WriteErrorSurfaces` in `symbol_rename_test.go` pre-populates the read-snapshot cache, chmods one target to `0o444`, and asserts the failure surfaces in all four places.

### 1.2 `symbol_move` same pattern — ✅ FIXED

**Was at** [`internal/tools/symbol_move.go:237-239`](internal/tools/symbol_move.go#L237-L239) — identical anti-pattern.

**Fix shipped:** Same shape (`Failed int` in `moveImpact`, `failed []string` in `Data`, locked path absent from `changes[]`). Also collapsed the per-match read/write loop into one read + one write per file — previously the original code re-read the same file once per match, which on a 500-reference symbol meant 500 reads. The refactor is incidental but kept because it was on the critical path of the fix. Regression test `TestSymbolMove_WriteErrorSurfaces` ships with it.

These were the **only two** `_ = err` instances in non-test production code (grepped whole tree). Adding a CI lint rule to ban the pattern in non-test code is the cheapest way to keep them out — that work is still pending.

### 1.3 `engine_drive_spec.go` bypasses the lifecycle funnel — 📝 DOCUMENTED AS EXCEPTION

**At** [`internal/engine/engine_drive_spec.go:30-52`](internal/engine/engine_drive_spec.go#L30-L52), the helper constructs and calls `SpecToTodoTool.Execute()` directly instead of routing through `CallToolFromSource`.

**Original recommendation was** to route it through the lifecycle. After verification, this is **not viable** as a straight fix:

- `CallToolFromSource` calls `e.requireReady("tool call")`, which gates on engine state `StateReady|StateServing|StateShuttingDown`.
- Three existing tests in [`engine_drive_spec_test.go`](internal/engine/engine_drive_spec_test.go) build partial `Engine{Config, ProjectRoot, Tools}` structs without calling `Init`, so `State()` returns the zero-state and `requireReady` would reject the call. The function is also called from CLI/web/MCP code paths early in their lifecycles.
- `spec_to_todo` is a pure read-only markdown parser with its own `EnsureWithinRoot` guard. The hook/approval surface adds no safety value here.

**Resolution shipped:** Documented the bypass as the second deliberate exception (alongside MCP Drive) in [CLAUDE.md](CLAUDE.md) (under "Things that bite") and added an inline comment on `TodosFromSpecFile` explaining the rationale. Future bypasses now need a comparable written justification.

---

## 2. Architectural Drift: Two God-Objects

### 2.1 `engine.Engine` has 246 methods

Grep confirms: `grep -rc "^func (e \*Engine)" internal/engine --include="*.go"` → **246 methods**, spread across 96 files (max 14 per file in [`engine_passthrough_conversation.go`](internal/engine/engine_passthrough_conversation.go) and [`agent_parked.go`](internal/engine/agent_parked.go)).

The sibling-file pattern keeps individual files readable but **every consumer still depends on the entire `*Engine` symbol**. Three UIs (`ui/cli`, `ui/tui`, `ui/web`) hold a `*engine.Engine` pointer and reach into whichever surface they need. There is no compile-time enforcement that, e.g., the TUI shouldn't be calling `e.invalidateContextForTool` directly.

**Recommended refactor (incremental, no big-bang):**

1. Introduce *capability interfaces* in `pkg/types` (or a new `internal/engine/api` sub-package). Examples:
   - `type Asker interface { Ask(ctx, req) (Response, error); AskStream(...) }`
   - `type ToolDispatcher interface { CallTool(...); CallToolFromSource(...) }`
   - `type ContextProvider interface { Status(); ContextBudget(); ContextRecommendation() }`
   - `type Approver interface { SetApprover(...); ApprovalState() }`
2. UI constructors take only the capabilities they need (`func NewChatTab(ask Asker, tools ToolDispatcher)`).
3. `*Engine` keeps implementing them all — no rewrite — but each test file no longer needs to spin up a full engine just to test a screen.

This is **a 2-3 day refactor** done incrementally per capability; no breakage on the way.

### 2.2 `tui.Model` has 115 methods

Same pattern in `ui/tui`. Largest method-count files: `input_paste.go` (19 Model methods), `telegram_panel.go` (18). 266 non-test files share one Model. The model struct itself is well-organized — sub-states are grouped per the documented `panel_states.go` convention — but **methods attach to the root model**, so the same indirection problem applies.

**Recommended refactor:** Lift panel-specific behaviour off `*Model` and onto the panel-state structs already in [`panel_states.go`](ui/tui/panel_states.go). The Model becomes a router that owns `*panelStates` and delegates. Pure-display helpers on a sub-state are easier to test in isolation and harder to misuse.

---

## 3. Package Sprawl

### 3.1 `ui/tui` is 335 files

This is the elephant in the room. Two hot-spot clusters drive most of it:

- **`chat_commands_*` cluster:** 13 non-test files, 1866 LOC, average ~143 LOC each. Smallest is [`chat_commands_flow.go`](ui/tui/chat_commands_flow.go) at 57 LOC. Files like `_flow.go`, `_panels.go`, `_providers.go`, `_session.go` are below the threshold where splitting is helpful. **Recommendation: collapse to ~4 files** (`chat_commands.go` dispatcher + `chat_commands_handlers.go` + `chat_commands_keys.go` + `chat_commands_panel_handlers.go`).

- **`provider_panel_*` cluster:** 20 non-test files, 4304 LOC, average ~215 LOC each. The bigger files justify themselves (key catalog, key picker, actions), but **5 files concern the key picker alone** — consolidate to 2.

These are pure carving: no behaviour change, no API change, much faster grep+ripgrep across the package.

### 3.2 `internal/engine/agent_loop_*` — 21 files / 4296 LOC

Less clear-cut. The split tracks distinct loop concerns (phases, parallel, native flow, caching, autonomous, autocontinue, events, limits, prompt). Files <150 LOC: `agent_loop_state.go` (78), `agent_loop_native_flow.go` (121), `agent_loop_autonomous_resume.go` (139), `agent_loop_native_entry.go` (156), `agent_loop_native_helpers.go` (149). **Verdict: not over-split** — each file is one cohesive concept and Go's compiler doesn't care about file count. Leave alone unless someone wants a fresh pair of eyes on the loop, in which case merging by *phase* (entry → phase → result) might help comprehension.

---

## 4. Dead / Half-Wired Code

### 4.1 `internal/session` — 1857 LOC of Phase-4 scaffolding

[`internal/session/engine_bridge.go:19`](internal/session/engine_bridge.go#L19):
```go
// TODO(phase4): Bridge to internal/engine/engine.go
type EngineProvider interface { ... }
```
The package defines an `EngineProvider` interface, a `Session.AttachEngine` method, multi-agent attention queues, and an agent tree. **Only 4 files in `ui/tui` import it** (`agent_session.go`, `render_panels.go`, `tui_lifecycle.go`, `shortcut_global_groups.go`). The agent code path (`agent.go:400`) does call `engine.ExecuteTool`, so it's not strictly dead — but the integration is partial: the panel exists, the agent struct exists, but the engine-side hooks for multi-agent dispatch aren't wired (the TODO is the receipt).

**Decision needed (not a fix):**
- **A — Finish Phase 4:** complete the engine bridge, wire multi-agent for real (worktree-isolated agents that the user can switch between).
- **B — Delete it:** if multi-agent is not a near-term priority, removing the package would shed ~1.8k LOC of carrying cost and remove the `agent_session.go` complexity from the TUI.

The current "half-wired" state pays the maintenance tax of both options without delivering the feature.

### 4.2 Tail-end of TODO/FIXME comments (production code only)

After filtering domain noise (Drive uses "TODO" as a noun for tasks), the **actual** TODO/FIXME markers in production code are very few:

| File:line                                            | Note                                              |
| ---------------------------------------------------- | ------------------------------------------------- |
| `internal/session/engine_bridge.go:19`               | Phase-4 bridge (see §4.1)                         |
| `internal/provider/google.go:196`                    | "Update Provider interface to allow context-aware counting" — wants a Provider interface change |
| `internal/provider/offline_analyzer_test.go:108`     | "TODO: remove this hack" in test                  |

`google.go:196` is the only "design needs revisiting" marker. It signals the `Provider.CountTokens` shape lacks a `context.Context`. Easy to fix when next touching that interface.

---

## 5. Test Coverage Patterns

`grep` for `strings.Contains(err.Error(), ...)`: 60+ hits, **all in test files**. Production code uses the typed-sentinel pattern documented in CLAUDE.md — that's the right discipline. No action needed.

Test files are large but split per-concern, mirroring the production layout. Hot-spots:
- [`ui/tui/tui_test.go`](ui/tui/tui_test.go) — 5908 LOC. By far the largest test file. Worth a second look: it likely tests too many surfaces at once and should split per-panel.
- [`ui/tui/engine_events_test.go`](ui/tui/engine_events_test.go) — 2639 LOC.
- [`internal/tools/engine_test.go`](internal/tools/engine_test.go) — 1413 LOC.

Production-vs-test LOC ratio is **108750:21397**, which is healthy for a project this size.

---

## 6. Cross-Cutting Observations (low-priority)

### 6.1 `interface{}` vs `any` — ✅ DONE
Production-code `interface{}` type literals are now zero. Three spots changed: [`internal/engine/engine.go`](internal/engine/engine.go) (`attachSessionProvider` var + `SetAttachProvider` signature), [`internal/tools/git_review.go`](internal/tools/git_review.go) (`mustMarshal` param), [`ui/tui/tui_lifecycle.go`](ui/tui/tui_lifecycle.go) (function literal at `engine.SetAttachProvider`). One legitimate occurrence kept: [`internal/tools/auto_tool.go:295`](internal/tools/auto_tool.go#L295) emits the literal string `"interface{}"` as part of code generation output — that is the *value*, not the type. Remaining `interface{}` matches in the tree are inside `// ...` comments and were left alone (cosmetic only).

### 6.2 `pkg/jsonutil.MustMarshal` is the only production panic
[`pkg/jsonutil/jsonutil.go:14`](pkg/jsonutil/jsonutil.go#L14) is the only `panic()` in non-test production code. It's a `Must*` helper, properly documented. **No issue** — the codebase has clean panic discipline.

### 6.3 Web server is well-shaped
[`ui/web/`](ui/web/) — 43 files, max 314 LOC ([`server_middleware.go`](ui/web/server_middleware.go)). Cleanly partitioned by domain. **No refactor needed.**

### 6.4 `node_modules` checked into the tree
[`ui/web/frontend/node_modules/`](ui/web/frontend/node_modules/) and [`ui/web/app/node_modules/`](ui/web/app/node_modules/) contain vendor JS. Worth confirming `.gitignore` correctly excludes them (the build artefacts shouldn't be in repo). Run:
```bash
git ls-files | grep node_modules | head
```
If hits appear, untrack them with `git rm -r --cached`.

**Bonus observation:** `go test ./...` currently walks INTO those `node_modules` paths because the `flatted` npm package happens to ship a Go subdirectory ([`ui/web/{app,frontend}/node_modules/flatted/golang/pkg/flatted/flatted.go`](ui/web/frontend/node_modules/flatted/golang/pkg/flatted/flatted.go)). The Go test runner picks them up as packages (logged as `[no test files]` in CI). Adding a `go.work` exclude or a `package main` build tag at the Makefile level would shave that off and prevent any future `flatted` Go upgrade from accidentally landing in the test surface.

### 6.5 `Makefile` is Windows-only
CLAUDE.md already calls this out (uses `NUL`, `rmdir /s /q`). A POSIX-compatible alternative (or a `Taskfile.yml` if mage/just/task is preferred) would lower the friction for any future contributor on Linux/macOS. Low priority.

---

## 7. Suggested Roadmap

Concrete sequencing if these items are tackled:

**Sprint 1 — Latent bugs (½ day): ✅ DONE**
1. ✅ Fix `symbol_rename`/`symbol_move` error swallowing (§1.1, §1.2).
2. ✅ `engine_drive_spec.go` lifecycle question resolved — documented as exception (§1.3).
3. ✅ Added `internal/repolint` package — `TestNoSilentErrSwallow` walks all production .go files and fails on any `_ = err`; `TestNoSilentErrSwallow_MatcherSelfCheck` keeps the matcher honest. The rule supports an inline opt-out (`// repolint:allow _=err — <reason>`) for documented exceptions.

**Sprint 2 — Decisions (1 meeting):**
4. Decide on `internal/session`: finish Phase 4 or delete (§4.1).
5. Decide whether to invest in capability-interface refactor (§2.1).

**Sprint 3 — Cleanup carving (1-2 days, non-breaking):**
6. Collapse `chat_commands_*` into ~4 files (§3.1).
7. Consolidate `provider_panel_*` key-picker files (§3.1).
8. Split `ui/tui/tui_test.go` per-panel (§5).
9. ✅ Replace remaining `interface{}` with `any` (§6.1).
10. ✅ `_ = err` regression tripwire ([`internal/repolint/repolint_test.go`](internal/repolint/repolint_test.go)).
11. Exclude `node_modules/**/flatted/golang` from `go test ./...` (§6.4).

**Sprint 4 — God-object decomposition (incremental, 2-3 days if approved):**
10. Introduce capability interfaces, switch UIs over package by package (§2.1, §2.2).

None of this changes user-facing behaviour; all of it lowers the cost of every future change.

---

## Appendix A — Numbers used in this report

| Metric                                                | Value     |
| ----------------------------------------------------- | --------- |
| Total Go source files                                 | 777       |
| Test files                                            | 371 (48%) |
| Total LOC (incl. tests)                               | 130,147   |
| Non-test LOC                                          | 108,750   |
| `internal/engine` files                               | 161       |
| `internal/engine` `*Engine` methods                   | **246**   |
| `internal/tools` files                                | 147       |
| `ui/tui` files                                        | 335       |
| `ui/tui` `*Model` methods                             | **115**   |
| `ui/web` files                                        | 43        |
| `internal/session` LOC (the half-wired package)       | 1,857     |
| Production-code `_ = err` instances (pre-fix)         | 2         |
| Production-code `_ = err` instances (post-fix)        | 0         |
| Production-code `panic()` instances                   | 1 (`Must*` helper) |
| Production-code `strings.Contains(err.Error(),…)`     | 0         |
| Real TODO/FIXME markers in production code            | 3         |

All numbers re-derivable from the working tree as of commit `8ab9c9c`.

---

## Appendix B — Methodology / Caveats

- Every file:line citation in this document was opened and confirmed before being written.
- `staticcheck` is not installed on this machine; `go vet ./...` was clean. A staticcheck run on CI is the appropriate gate (CLAUDE.md says CI already enforces this).
- This audit deliberately avoided opining on naming, comment style, or "would I write it this way?" aesthetics. The findings are all either:
  (a) a verifiable correctness/safety issue, or
  (b) a measurable structural metric (file count, method count, LOC) that the team can choose to act on.
- The memory note "audit subagents over-claim" was respected: claims from delegated explorations were re-verified against the source before being repeated here. Three potential findings that did not survive verification were dropped from this report:
  - A claim of copy-paste between `chat_timeline_format.go` and `chat_timeline_format_detail.go` (verified: distinct concerns, not duplication).
  - A claim that the stats panel doesn't degrade on 100-col terminals (verified: it uses an 88-col threshold per `stats_panel.go:43`, which is correct per the memory note).
  - A claim that internal/session is dead scaffolding (verified: partially wired — see §4.1, which is a softer "decide" recommendation rather than "delete").
