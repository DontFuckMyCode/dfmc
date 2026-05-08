# DFMC Tool Plans — Inventory, Justification & Refactor Roadmap

> Living document. Captures **what each tool does, why it exists, whether it is
> truly necessary, and what refactors are worth pursuing.**

---

## 1. Complete Tool Inventory

| # | Tool | Category | Risk | Summary |
|---|------|----------|------|--------|
| 1 | `read_file` | File Read | read | Fetch raw file contents, optionally scoped to a line range |
| 2 | `write_file` | File Write | write | Create or overwrite a text file |
| 3 | `edit_file` | File Write | write | Surgical exact-string replacement in a text file |
| 4 | `apply_patch` | File Write | write | Apply a unified diff (multi-hunk, multi-file) |
| 5 | `list_dir` | File Read | read | Inspect directory contents |
| 6 | `glob` | File Read | read | Match project files against a glob pattern |
| 7 | `grep_codebase` | Search | read | Regex search across project files |
| 8 | `find_symbol` | Search | read | Locate a symbol by name, return its lexical scope |
| 9 | `ast_query` | Search | read | Parse a source file and return symbols/imports/language |
| 10 | `semantic_search` | Search | read | Find AST nodes by type and name pattern |
| 11 | `codemap` | Search | read | Signatures-only project overview grouped by file |
| 12 | `dependency_graph` | Search | read | Structured import/call dependency graph for impact analysis |
| 13 | `test_discovery` | Search | read | Find test files and test functions covering a source path/symbol |
| 14 | `symbol_rename` | Refactor | write | Rename a symbol across a file or the entire project |
| 15 | `symbol_move` | Refactor | write | Move a symbol to another file and update all references |
| 17 | `project_info` | Search | read | Structured project metadata (module, Go version, deps) |
| 18 | `git_status` | Git | read | Working-tree status (porcelain v1) |
| 19 | `git_diff` | Git | read | Unified diff of working tree, a revision, or path subset |
| 20 | `git_log` | Git | read | Recent commits as {hash, author, subject} entries |
| 21 | `git_blame` | Git | read | Per-line authorship for a file |
| 22 | `git_branch` | Git | read | List branches, report current HEAD |
| 23 | `git_commit` | Git | write | Stage named paths and commit with a message |
| 24 | `git_worktree_list` | Git | read | Enumerate all linked worktrees |
| 25 | `git_worktree_add` | Git | write | Create a new linked worktree for an existing ref/new branch |
| 26 | `git_worktree_remove` | Git | write | Detach and delete a linked worktree |
| 27 | `gh_pr` | GitHub | read | Structured PR summary with reviews, checks, diff stats |
| 28 | `run_command` | Execute | execute | Execute one binary in the project sandbox (no shell) |
| 29 | `patch_validation` | Execute | execute | Dry-run a unified-diff patch + optionally run build/test to validate |
| 30 | `benchmark` | Execute | read | Run Go benchmarks, return structured ns/op, allocs/op, MB/s |
| 31 | `web_search` | Web | execute | Search the web (DuckDuckGo HTML), returns title/url/snippet |
| 32 | `web_fetch` | Web | execute | GET a URL, return its body (HTML stripped to text) |
| 33 | `todo_write` | Meta | read | Maintain a session-scoped todo list |
| 34 | `think` | Meta | read | Record a reasoning step into the tool trace (no side effects) |
| 35 | `task_split` | Planning | read | Decompose a task into subtasks for sub-agent fan-out |
| 36 | `delegate_task` | Planning | execute | Spawn a bounded sub-agent for a focused task |
| 37 | `orchestrate` | Planning | execute | One-shot decomposition + sub-agent fan-out + summary aggregation |
| 38 | `tool_search` | Meta | read | Discover backend tools by query |
| 39 | `tool_help` | Meta | read | Fetch full schema/usage for a named backend tool |
| 40 | `tool_call` | Meta | execute | Dispatch a single backend tool |
| 41 | `tool_batch_call` | Meta | execute | Dispatch several backend tools in parallel |

---

## 2. Necessity Analysis

### 2.1 Essential Tools — No Question

These form the core feedback loop (read → reason → edit → verify). Removing
any of them would cripple the agent.

| Tool | Why Essential |
|------|--------------|
| `read_file` | The agent *must* see file contents before mutating them. Read-before-write is a hard rule. |
| `write_file` | Creating new files or doing full rewrites. No alternative. |
| `edit_file` | The default tool for targeted changes. Exact-string match avoids line-offset drift. |
| `apply_patch` | Multi-hunk, multi-file edits in one atomic call. `edit_file` cannot do this. |
| `grep_codebase` | Cheapest discovery layer. Use-first when you don't know where to look. |
| `glob` | File-shape discovery when you need paths, not content. |
| `find_symbol` | Structured symbol lookup — faster than grepping for `func FooBar`. |
| `ast_query` | AST-backed outlines; understands structure regex cannot. |
| `run_command` | Build, test, lint — the verification loop. Irreplaceable. |
| `todo_write` | Multi-step work tracking; prevents the agent from losing its plan mid-session. |
| `tool_search` / `tool_help` / `tool_call` / `tool_batch_call` | The meta-tool surface — how the agent discovers and invokes *other* tools. |

### 2.2 Important but Could Be Emulated (at a Cost)

| Tool | What Would Replace It | Cost of Removal |
|------|----------------------|-----------------|
| `codemap` | `grep_codebase` for `func ` + `type ` patterns | Lose structured call-graph overview; much slower orientation |
| `dependency_graph` | `grep_codebase` for import paths | Lose transitive dependency data; impact analysis becomes guesswork |
| `test_discovery` | `glob` for `*_test.go` + `grep` for `func Test` | Lose coverage correlation; agent won't know *which* tests cover a symbol |
| `semantic_search` | `ast_query` + manual filtering | More round-trips; weaker for cross-file structural queries |
| `symbol_rename` | `apply_patch` with manual find-replace | Agent must discover all references itself; high risk of missed sites |
| `symbol_move` | `write_file` + `edit_file` + `apply_patch` multi-step | Same risk as rename, plus import path updates are error-prone |
| `patch_validation` | `run_command git apply --check` + `run_command go build` | Two commands instead of one; no structured diff+build combo |
| `benchmark` | `run_command go test -bench` | Lose structured ns/op output; agent must parse raw terminal text |

### 2.3 Potentially Redundant — Merge or Remove Candidates

| Tool | Issue | Recommendation |
|------|-------|---------------|
| `git_status` | `run_command git status --porcelain` produces equivalent output | **Keep** — structured JSON is more reliable than parsing CLI output; but consider merging into a `git` multiplexer (see §3) |
| `git_diff` | `run_command git diff` works | **Keep** — same reason as `git_status`; structured diff is safer |
| `git_log` | `run_command git log --oneline -n` works | **Keep** — but merge into `git` multiplexer |
| `git_blame` | `run_command git blame` works | **Keep** — structured per-line output is much richer than raw blame |
| `git_branch` | `run_command git branch -a` works | **Merge** into `git` multiplexer; trivial output to parse |
| `git_commit` | `run_command git commit` works but requires staging + message in one call | **Keep** — write-risk tool with safety guards (refuses wildcards, --amend) that `run_command` cannot enforce |
| `git_worktree_list` | `run_command git worktree list` | **Merge** into `git` multiplexer; very low usage frequency |
| `git_worktree_add` | `run_command git worktree add` | **Merge** into `git` multiplexer; write-risk but guardable via subcommand param |
| `git_worktree_remove` | `run_command git worktree remove` | **Merge** into `git` multiplexer; same as add |
| `project_info` | Could be computed from `go.mod` + `glob` | **Keep** — cheap, structured, used once per session for orientation |
| `list_dir` | `run_command` with `ls`/`dir` equivalent | **Keep** — cross-platform, structured, no shell required |
| `web_fetch` | Subset of `run_command curl` | **Keep** — `run_command` cannot call `curl` in sandboxed environments; HTML-to-text stripping is valuable |
| `benchmark` | `run_command go test -bench` | **Keep** for structured output, but low priority; could be removed if maintenance burden grows |

---

## 3. Refactor Plan: Git Tool Consolidation

### Current State (8 separate tools)

```
git_status, git_diff, git_log, git_blame, git_branch,
git_commit, git_worktree_list, git_worktree_add, git_worktree_remove
```

### Proposed State (2 tools)

#### `git` — Unified Git Multiplexer

```
Arguments:
  subcommand: enum(status|diff|log|blame|branch|worktree_list|worktree_add|worktree_remove)
  ...subcommand-specific args (path, revision, range, etc.)

Risk mapping:
  status, diff, log, blame, branch, worktree_list → read
  commit, worktree_add, worktree_remove           → write
```

**Benefits:**
- Reduces tool count by 7 (9 → 2: `git` + `git_commit` stays separate if write-guard logic is complex)
- Single schema to document and maintain
- The agent sees one `git` tool instead of scrolling past 9 entries
- Subcommand `enum` makes invalid calls impossible
- Risk is derived from subcommand, not from the tool itself

**Implementation notes:**
- Keep `git_commit` as a separate tool if its safety guards (refuse wildcards, refuse `--amend`) are hard to express in a multiplexer
- Otherwise, merge it too with a `commit` subcommand and apply the same guards inside the handler
- Worktree subcommands share the same parameter shape (path + ref), making the multiplexer natural

#### Migration Path

1. Create the `git` multiplexer tool that delegates to the existing handler functions
2. Register both old names and new `git` tool simultaneously (compatibility)
3. Update `tools.md` to recommend the `git` tool
4. After one release cycle, deprecate old individual names
5. Remove old names in the next breaking release

---

## 4. Refactor Plan: Remove `disk_usage` — DONE

Removed in 2026-05-08: registry binding, handler, tests, schema, and
docs entries (`docs/tools.md`, `ARCHITECTURE.md`, `internal/context/prompt_render_tools.go`'s
toolGroup switch). Rationale that justified the removal:

- **Usage frequency:** Extremely low. The agent almost never needed raw disk bytes.
- **Overlap:** `project_info` already gives file count, language breakdown, and dependency count.
- **Alternative:** `glob` + `run_command du` covers the edge case.

If a future workload needs disk metrics back, prefer adding a
`subcommand: disk_usage` to `project_info` over re-introducing a
standalone tool.

---

## 5. Refactor Plan: Merge Worktree Tools into `git`

This is covered by §3 but worth calling out specifically because the worktree
tools are the weakest members of the current set:

| Tool | Usage Frequency | Risk if Merged |
|------|----------------|-----------------|
| `git_worktree_list` | Very rare | None — read-only, simple output |
| `git_worktree_add` | Very rare | Low — write, but guardable via subcommand |
| `git_worktree_remove` | Very rare | Low — same as add |

Merging these into `git` with `subcommand: worktree_list|worktree_add|worktree_remove`
eliminates 3 tool registrations for functionality that 95% of sessions never touch.

---

## 6. Tools That Should Stay As-Is

### `web_search` + `web_fetch` — Keep Both

- **`web_search`** is essential for looking up docs, error messages, API changes, and
  library compatibility. The agent cannot solve "what does this deprecation notice mean?"
  without it.
- **`web_fetch`** complements search by following a URL and extracting readable text.
  Together they form the **read-the-web** pipeline: search → pick result → fetch → extract.
- These cannot be replaced by `run_command curl` because:
  - Sandboxed environments may not have `curl`
  - HTML-to-text stripping is non-trivial
  - Rate limiting and caching belong at the tool level, not in a raw HTTP call

### `gh_pr` — Keep (but consider expanding)

- GitHub PRs are the primary merge path for most users.
- Structured output (reviews, checks, diff stats) is richer than `gh pr view --json`.
- Could be expanded to `gh` multiplexer (issues, actions, releases) if demand exists.
- For now, keep as-is since PR is the only GitHub operation the agent routinely needs.

### `benchmark` — Keep (low priority)

- Structured output (ns/op, allocs/op, MB/s) is valuable for performance work.
- Low usage frequency means this is a **keep but don't invest** tool.
- If maintenance burden grows, replace with `run_command go test -bench` + parsing.

---

## 7. Orchestration & Planning Tools — Keep All

| Tool | Why |
|------|------|
| `task_split` | Decomposes complex requests into ordered subtasks; feeds `delegate_task` |
| `delegate_task` | Spawns sub-agents with focused context; essential for fan-out |
| `orchestrate` | One-shot decomposition + fan-out + aggregation; highest-level planning tool |
| `todo_write` | Session-scoped task tracking; prevents plan drift in long sessions |
| `think` | Reasoning trace; no side effects but improves transparency and debugging |

These tools have no `run_command` alternative. They operate on the agent's own
control flow, not on the filesystem. Removing any would degrade multi-step
reasoning capability.

---

## 8. Summary: Refactor Priority Matrix

| Priority | Action | Tools Affected | Estimated Effort | Impact |
|----------|--------|----------------|-------------------|--------|
| **P0** | Consolidate git tools into `git` multiplexer | `git_status`, `git_diff`, `git_log`, `git_blame`, `git_branch`, `git_worktree_list`, `git_worktree_add`, `git_worktree_remove` | Medium (new multiplexer + migration) | -7 tool registrations, simpler mental model |
| **P0** | Keep `git_commit` separate or merge with guards | `git_commit` | Small | Safety guard preservation |
| ~~**P1**~~ | ~~Remove `disk_usage`~~ — **DONE 2026-05-08** | `disk_usage` | Small | -1 tool, less docs surface |
| **P2** | Consider `gh` multiplexer if demand grows | `gh_pr` → `gh` | Medium | Future-proofing |
| **P2** | Consider `benchmark` removal if maintenance cost rises | `benchmark` | Small | -1 tool |
| **P3** | Evaluate `project_info` + `codemap` overlap | Both | Research | Possible merge |

---

## 9. Final Count Projection

| State | Tool Count |
|-------|-----------|
| Pre-cleanup baseline | 41 |
| Current (after disk_usage removal, 2026-05-08) | 40 |
| After P0 (git consolidation) | 33 |
| After P2 (benchmark removal) | 32 |
| Theoretical minimum (aggressive) | ~28 |

The theoretical minimum would require merging `project_info` into `codemap`, removing
`benchmark`, and merging `gh_pr` into a `gh` multiplexer. Each of these has trade-offs
documented above.

---

*Last updated: 2026-05-08*
*Author: DFMC autonomous analysis*
