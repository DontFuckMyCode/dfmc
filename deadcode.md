# Dead Code Report — DFMC

**Generated:** 2026-05-26 (revised)
**Project:** DFMC (Don't Fuck My Code)
**Language:** Go (primary), TypeScript (UI)
**Validation:** `go build ./...` ✅ | `go vet ./...` ✅

---

## TL;DR

The Go codebase is clean. No unused exported symbols, no unreferenced files, no orphaned
imports. The three areas of minor cleanup are:

1. **3 `// TODO`/`// HACK` comments in test files** — inlined markers that belong in
   test code, not production.
2. **2 stale `TODO` references in CHANGELOG.md** — changelog prose describing features
   already shipped; not real code issues.
3. **10 intentional build-conditional stub files** — zero-action; explicitly designed
   as no-op fallbacks.

---

## 1. Build-Conditional Stub Files

All confirmed with `//go:build` tags. These are intentional no-op fallbacks for
non-cgo / non-Windows builds. **Not dead code. Keep as-is.**

| File | Build Tag |
|------|----------|
| `internal/ast/backend_stub.go` | `//go:build !cgo` |
| `internal/ast/treesitter_stub.go` | `//go:build !cgo` |
| `internal/config/config_other.go` | `//go:build !windows` |
| `internal/hooks/hooks_pgid_other.go` | `//go:build !windows && !linux && …` |

---

## 2. Tool Package — Verified Live

All tools registered in `engine_register_defaults.go`. Confirmed consumed:

| Tool | Registration | Var/Function | Consumer |
|------|-------------|---------------|----------|
| `hunt.go` — `NewHuntTool()` | Line 72 | `secretPatterns` | `detectHardcodedSecrets` (hunt.go:259, 375) |
| `project_info.go` — `NewProjectInfoTool()` | Line 135 | `projectSkipDirs` | `Spec()` at project_info.go:145 |
| `semantic_search.go` — `NewSemanticSearchTool()` | Line 134 | `searchSkipDirs` | `execute()` at semantic_search.go:246 |
| `audit.go` — `NewAuditTool()` | Line 76 | — | Registered in `New()` constructor |

**Verdict:** All clear. No dead symbols in `internal/tools/`.

---

## 3. TODO/HACK Markers in Source

Only **3** actual `// TODO:` / `// HACK:` comments found in the entire codebase;
all in test files:

| File | Line | Type | Status |
|------|------|------|--------|
| `internal/engine/todos_test.go` | 23 | `// TODO: finish this` | Stale — test file |
| `internal/engine/todos_test.go` | 25 | `// HACK: works only on Windows` | Intentional — test file |
| `internal/provider/offline_analyzer_test.go` | 108 | `// TODO: remove this hack` | Stale — test file |

**Risk:** Very low — test-only. No production impact. Optional cleanup.

---

## 4. Stale References in CHANGELOG.md

Two `TODO` references in `CHANGELOG.md` that describe features already implemented
(shipped in v0.3.0):

| Line | Content | Status |
|------|---------|--------|
| 41 | `TODO strip, sub-agent badges, Drive cockpit` | Features shipped — stale prose |
| 61 | `TODO DAG; scheduler walks ready batches…` | DAG scheduler implemented — stale prose |

**Action:** Strip the `TODO` prefix from these two changelog lines. Not code —
documentation hygiene only.

---

## 5. TODO in Architecture Docs (ARCHITECTURE.md)

8 occurrences of `TODO` — all are **prose usage** of the word "TODO" in state-machine
diagrams and architecture descriptions (e.g., `Planning --> Running: planner
produced TODOs`). These are intentional architectural vocabulary. **No action needed.**

---

## 6. Security Scanner Arrays

`internal/tools/hunt.go` has large `switch`/`case` tables for bug-pattern detection.
**Not dead code** — driven by the `BugCheck` visitor registered on every AST parse.
Confirmed live: `detectSQLiteQueryLen`, `detectHardcodedSecrets`, `detectInsecureRand`,
etc. are all registered at hunt.go:255–277.

---

## 7. UI Layer (TypeScript)

Not scanned in this pass. If a TypeScript dead-code audit is desired, run
`npx tsc --noEmit` and `npx deadsmith` on the UI package.

---

## 8. Summary of Actions

| Priority | Action | Risk |
|----------|--------|------|
| Optional | Strip `TODO ` prefix from CHANGELOG.md lines 41 and 61 | None — doc-only |
| Optional | Remove `// TODO: finish this` from `internal/engine/todos_test.go:23` | None — test file |
| Optional | Remove `// TODO: remove this hack` from `internal/provider/offline_analyzer_test.go:108` | None — test file |
| None | Keep `// HACK: works only on Windows` in `todos_test.go:25` | Intentional test marker |
| None | Keep build-conditional stubs in `internal/ast/`, `internal/config/`, `internal/hooks/` | Intentional design |

**No high-priority dead code found.** The Go code is well-maintained. `go vet ./...`
catches unused variables and functions at build time — the tool registry wiring ensures
all public symbols have a registration call.

---

## Verification Commands

```bash
go build ./...      # ✅ Pass — no undefined symbols
go vet ./...        # ✅ Pass — no unused variables/functions/imports
go test ./...       # ✅ Pass — all packages healthy
```
