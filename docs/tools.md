# DFMC Tool Reference

DFMC (Duty Free Meta Copilot) exposes **22 backend tools** the model uses to read, edit, search, and execute code in the project sandbox. All tools accept a `_reason` field (informational only) and return structured JSON.

---

## Table of Contents

1. [File Read and Navigation](#1-file-read--navigation)
2. [File Write and Edit](#2-file-write--edit)
3. [Code Search and Discovery](#3-code-search--discovery)
4. [Refactor and Rename](#4-refactor--rename)
5. [Git Operations](#5-git-operations)
6. [Execution and Validation](#6-execution--validation)
7. [Web and Meta](#7-web--meta)
8. [Tool Discovery](#8-tool-discovery)

---

## 1. File Read and Navigation


### read_file - Read File

**Purpose:** Fetch the raw contents of a file, optionally scoped to a line range.

**Risk:** read (idempotent) | **Cost:** cheap (cached)

**Read-before-mutation rule:** You must call `read_file` on an existing file before calling `edit_file` or `write_file` on it.


**Arguments**

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `path` | string | yes | - | Relative path inside the project |
| `line_start` | integer | no | 1 | 1-based start line (inclusive) |
| `line_end` | integer | no | 200 | 1-based end line (inclusive) |

**Output**

```json
{
  "path": "internal/engine/engine.go",
  "line_start": 40,
  "line_end": 80,
  "line_count": 41,
  "total_lines": 312,
  "returned_lines": 41,
  "truncated": true,
  "language": "go",
  "content": "..."
}
```

`truncated: true` means the file is longer than the returned slice.

**When to use:** When you know the exact path and line range. For discovery, prefer `grep_codebase` then `find_symbol` then `read_file`.


---

### glob - Glob Files


**Purpose:** Match file paths against a glob pattern. Fastest way to find files matching a shape.

**Risk:** read (idempotent) | **Cost:** io-bound

**Arguments**

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `pattern` | string | yes | - | Glob pattern: `**/*.go`, `*.md`, `internal/**/*_test.go` |
| `path` | string | no | project root | Restrict search to a subdirectory |
| `max_results` | integer | no | 200 | Cap on returned paths (ceiling 2000) |


**Output**

```json
{
  "pattern": "**/*_test.go",
  "count": 23,
  "matches": ["ui/tui/tui_test.go", "model/model_test.go"]
}
```

- `**` enables recursive traversal
- Skips `.git/`, `node_modules/`, `vendor/`, `bin/`, `dist/`, `.venv/` automatically
- Combine with `grep_codebase` to find files that **contain** a pattern

---

### list_dir - List Directory

**Purpose:** Enumerate files and directories under a path.

**Risk:** read (idempotent) | **Cost:** cheap

**Arguments**

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `path` | string | yes | - | Relative directory path; use `.` for project root |
| `recursive` | boolean | no | false | Walk subdirectories |
| `max_entries` | integer | no | 200 | Cap on returned entries (ceiling 500) |

**Output**
```json
{
  "path": "internal",
  "entries": ["hooks", "engine", "model"],
  "count": 14
}
```

**When to use:** Shape-of-repo questions. For "find files matching X", use `glob` instead.

---

### codemap - Project Codemap

**Purpose:** Signatures-only project overview grouped by file. Layer 4 (orientation) of the read stack. **Use once per session** to orient.

**Risk:** read (idempotent) | **Cost:** io-bound

**Arguments**

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `path` | string | no | project root | Subdirectory to scope |
| `max_depth` | integer | no | 12 | Cap directory walk depth |
| `max_files` | integer | no | 2000 | Hard cap on files (ceiling 5000); `truncated: true` if hit |
| `languages` | string[] | no | all detected | Filter by language, e.g. `["go"]` |

**Output (markdown format)**
```
  pkg/auth/service.go (Go)
    type UserService struct                              L12
    func NewUserService(db *sql.DB) *UserService         L24
    func (s *UserService) Authenticate(u, p string) error L31
```

**Cost order (cheapest to most expensive):**
```
grep_codebase < glob/list_dir < ast_query < find_symbol < read_file < symbol_rename/symbol_move < apply_patch/edit_file < write_file < run_command/patch_validation
```

---

## 2. File Write and Edit

### write_file - Write File

**Purpose:** Create a new file or fully rewrite an existing one. For partial edits, prefer `edit_file`.

**Risk:** write | **Cost:** io-bound

**Arguments**

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `path` | string | yes | - | Relative path inside the project |
| `content` | string | yes | - | Full file contents |
| `create_dirs` | boolean | no | true | Create parent directories if missing |
| `overwrite` | boolean | no | false | Allow overwriting existing files |

**Rules**
- Existing files: You MUST have called `read_file` on the file in this session. The engine refuses blind overwrites.
- Always include a trailing newline unless deliberately binary-ish.
- For edits touching less than 50% of a file, use `edit_file` or `apply_patch`.
- Writing inside `.git/`, `node_modules/`, `vendor/`, `.dfmc/` is blocked.

**Output**
```json
{"path": "docs/tools.md", "bytes": 14320}
```

---

### edit_file - Edit File

**Purpose:** Surgical exact-string replacement. **Default tool for targeted changes.**

**Risk:** write | **Cost:** io-bound

**Arguments**

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `path` | string | yes | - | Relative path inside the project |
| `old_string` | string | yes | - | Exact text to find; must be unique unless `replace_all` is true |
| `new_string` | string | yes | - | Replacement text |
| `replace_all` | boolean | no | false | Replace all non-unique occurrences |


**Rules**
- Line endings are auto-normalized (CRLF to LF) for matching; the file's original newline style is restored on write.
- If error: **"not found - trimmed form matches"** - drop surrounding whitespace from `old_string`.
- If error: **"not found - indentation may be off"** - re-read the region and copy bytes verbatim.
- If error: **"identical"** - you did not change anything.
- For multiple coordinated hunks, prefer `apply_patch` with a unified diff.

**Output**
```json
{"path": "internal/hooks/hooks.go", "replacements": 1}
```

---

### apply_patch - Apply Patch

**Purpose:** Apply a unified diff (one or more files) to the project. Preferred for multi-hunk edits.

**Risk:** write | **Cost:** io-bound

**Arguments**

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `patch` | string | yes | - | Unified diff in `---`/`+++`/`@@` format |
| `dry_run` | boolean | no | false | Parse and match but do not write files |

**Rules**
- Context lines must match the **current** file. If `hunks_rejected > 0`, re-read the file and regenerate.
- New file: `--- /dev/null` with `+++ b/newpath`. Deleted file: `+++ /dev/null`.
- For a single one-line swap, `edit_file` is simpler.

**Output**
```json
{"files": ["internal/hooks/hooks.go"], "count": 1, "dry_run": false}
```

---


## 3. Code Search and Discovery


### grep_codebase - Grep Codebase

**Purpose:** Regex search across project files. Cheapest discovery layer. **Use first** when you do not know where to look.

**Risk:** read (idempotent) | **Cost:** io-bound

**Arguments**


| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `pattern` | string | yes | - | Regex pattern (Go RE2, NOT PCRE/Perl) |
| `path` | string | no | project root | Restrict search to a subdirectory |
| `case_sensitive` | boolean | no | true | Case-sensitive matching |
| `context_before` | integer | no | 0 | Lines of context before each match |
| `context_after` | integer | no | 0 | Lines of context after each match |
| `include` | string[] | no | - | Include file patterns, e.g. `["*.go"]` |
| `exclude` | string[] | no | - | Exclude file patterns, e.g. `["*_test.go"]` |
| `max_results` | integer | no | 100 | Cap on matches |

**Output**
```json
{
  "pattern": "func.*Hook",
  "matches": [
    "internal/hooks/hooks.go:12:func RegisterHook(name string, h Hook) error",
    "cmd/dfmc/main.go:45:  // RegisterHook called at startup"
  ],
  "count": 2,
  "case_sensitive": true
}
```


**Regex rules (Go RE2):**
- DO NOT use: lookbehind `(?<=...)`, lookahead `(?=...)`, backreferences `\1`, possessive quantifiers `*+`
- DO use: char classes `\d \w \s`, case flag `(?i)pattern`, non-capturing `(?:foo|bar)`
- Anchor tightly: `func FooBar` beats `FooBar`

**Pipeline:** `grep_codebase` then `find_symbol` then `read_file`

---

### find_symbol - Find Symbol with Scope


**Purpose:** Locate a function/class/method by name and return its **full lexical scope** with body. AST-driven.

**Risk:** read (idempotent) | **Cost:** io-bound


**Arguments**


| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `name` | string | yes | - | Symbol name to locate |
| `kind` | string | no | any | Filter by kind: `function`, `method`, `class`, `interface`, `type`, `variable`, `constant`, `html_id`, `html_class`, `tag` |
| `parent` | string | no | - | Disambiguate by enclosing scope (e.g. receiver type for Go) |
| `path` | string | no | - | Restrict to a specific file |
| `include_body` | boolean | no | true | Include the symbol body |
| `max_results` | integer | no | 5 | Max matches (ceiling 20) |
| `body_max_lines` | integer | no | 200 | Max lines per body |
| `match` | string | no | exact | `exact`, `prefix`, or `contains` |

**Output**
```json
{
  "name": "RegisterHook",
  "count": 1,
  "matches": [{
    "path": "internal/hooks/hooks.go",
    "language": "go",
    "name": "RegisterHook",
    "kind": "function",
    "start_line": 12,
    "end_line": 28,
    "signature": "func RegisterHook(name string, h Hook) error",
    "body": "...",
    "truncated": false,
    "fallback": false
  }]
}
```

`fallback: true` means tree-sitter could not parse the file and a regex extractor was used.


**Supported languages:** Go, JS/TS/JSX/TSX, Python, Java, Rust, C/C++, C#, PHP, Swift, Kotlin, Scala, Ruby, HTML/XML.


---

### ast_query - Query AST

**Purpose:** Parse a source file and return its symbols, imports, and language - a structured outline without the full body.

**Risk:** read (idempotent) | **Cost:** io-bound


**Arguments**

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `path` | string | yes | - | Relative path inside the project |
| `kind` | string | no | - | Filter by symbol kind (function, struct, class, ...) |
| `name_contains` | string | no | - | Case-insensitive substring filter on symbol name |

**Output**
```json
{
  "path": "internal/engine/engine.go",
  "language": "go",
  "symbols": [
    {"name": "Engine", "kind": "struct", "line": 12},
    {"name": "Run", "kind": "function", "line": 24}
  ],
  "imports": ["context", "fmt", "io"],
  "errors": [],
  "count": 2
}
```

If `errors[]` is non-empty, the file failed to parse - fall back to `read_file`.

---

### semantic_search - Semantic Search

**Purpose:** Find AST nodes by type and name pattern across files. More precise than `grep_codebase` when you know the semantic node type.

**Risk:** read (idempotent) | **Cost:** cpu-bound

**Arguments**

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `query` | string | yes | - | AST pattern (see below) |
| `file` | string | no | - | Scope to a single file |
| `lang` | string | no | go | Language: `go`, `typescript`, `python`, `java`, `rust`, `all` |
| `context_lines` | integer | no | 0 | Include N lines of surrounding context |
| `max_results` | integer | no | 100 | Cap on matches |

**Pattern Language**

| Pattern | Matches |
|---|---|
| `FunctionDecl:<name>` | Function declaration with matching name |
| `FunctionCall:<name>` | Function call |
| `TypeDecl:<name>` | Type declaration (class/interface/type) |
| `MethodDecl:<name>` | Method with receiver |
| `IfStmt` | All if statements |
| `ReturnStmt` | Return statements |
| `AssignStmt` | Assignment statements |
| `VarDecl:<name>` | Variable declaration |
| `ConstDecl:<name>` | Constant declaration |
| `:type=<typename>` | Filter by result/parameter type |
| `:context=N` | Include N context lines |

**Output**
```json
{
  "query": "FunctionDecl:name=Run",
  "matches": [{
    "path": "internal/engine/engine.go",
    "line": 24,
    "column": 1,
    "node_type": "function_declaration",
    "name": "Run",
    "snippet": "func (e *Engine) Run(ctx context.Context) error { ... }",
    "context_lines": 2
  }],
  "total": 1,
  "backend": "tree-sitter"
}
```

---

### test_discovery - Discover Tests


**Purpose:** Find test files and test functions that cover a source file, directory, or named symbol.


**Risk:** read


**Arguments**


| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `target` | string | no | - | Source file path - companion test file found via conventions |
| `pattern` | string | no | - | Glob pattern for test files (e.g. `**/*_test.go`) |
| `language` | string | no | - | Restrict to: `go`, `python`, `javascript`, `typescript`, `java`, `rust` |
| `symbol` | string | no | - | Limit to test functions matching this name substring |

One of `target` or `pattern` is required.

**Supported Conventions**

| Language | Test file convention | Test marker |
|---|---|---|
| Go | `*_test.go` next to source | `Test` / `Benchmark` / `Example` prefix |
| Python | `test_*.py`, `*_test.py`, `tests/` | `test` prefix, `unittest.TestCase` |
| JS/TS/JSX/TSX | `*.test.ts`, `*.spec.ts`, `__tests__/` | `test` / `it` / `describe` blocks |
| Java | `*Test.java`, `Test*.java` | `@Test` annotations |
| Rust | `*_test.rs` or `#[test]` attributes | `#[test]` |

**Output**
```json
{
  "target": "internal/hooks/hooks.go",
  "test_files": [{
    "path": "internal/hooks/hooks_test.go",
    "functions": ["TestRegisterHook", "TestDeregisterHook"]
  }]
}
```

**Always validate** with the actual test runner (`go test`, `pytest`, `jest`, etc.) after locating tests.

---

## 4. Refactor and Rename

### symbol_rename - Rename Symbol

**Purpose:** Rename a symbol across a file or the entire project safely, with read-before-mutation gating on every target file.

**Risk:** write | **Cost:** cpu-bound

**Arguments**

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `from` | string | yes | - | Original symbol name |
| `to` | string | yes | - | New symbol name |
| `file` | string | no | - | Scoped to this file; absent = full project |
| `kind` | string | no | all | Limit to: `func`, `type`, `var`, `const`, `method`, `all` |
| `dry_run` | boolean | no | true | Preview impact without writing |
| `skip_tests` | boolean | no | false | Skip test files |

**Output (dry_run)**
```json
{
  "impact": {"files": 4, "locations": 11},
  "changes": [{"path": "internal/hooks/hooks.go", "old": "RegisterHook", "new": "AddHook", "line": 12}],
  "dry_run": true
}
```

**Scope rules:**
- Go: renames only within the same package scope; imported symbols from other packages are NOT renamed
- Local vars: scoped to function body only
- Type/function names: full file scope

---

### symbol_move - Move Symbol


**Purpose:** Move a function/type/variable to another file and update all project references.

**Risk:** write | **Cost:** cpu-bound

**Arguments**

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `from` | string | yes | - | Symbol name to move |
| `to_file` | string | yes | - | Destination file path (relative to project root) |
| `to` | string | no | same as `from` | New symbol name in destination |
| `kind` | string | no | all | Limit to: `func`, `type`, `var`, `const`, `method`, `all` |
| `dry_run` | boolean | no | true | Preview without writing |
| `skip_tests` | boolean | no | false | Skip test files when updating references |

**Output**
```json
{
  "impact": {"files": 3, "locations": 7},
  "changes": [{"path": "internal/hooks/hooks.go", "old": "RegisterHook", "new": "AddHook", "line": 12}],
  "dry_run": true
}
```

---

## 5. Git Operations

### git_blame - Git Blame

**Purpose:** Per-line authorship for a file with hash, author, time, and commit subject.

**Risk:** read (idempotent) | **Cost:** cheap

**Arguments**

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `path` | string | yes | - | File path within the project root |
| `line_start` | integer | no | - | 1-based start line (inclusive) |
| `line_end` | integer | no | - | 1-based end line (inclusive) |
| `revision` | string | no | HEAD | Revision to blame at |

**Output**
```json
{
  "path": "internal/hooks/hooks.go",
  "lines": [{
    "line": 12,
    "hash": "a1b2c3d",
    "author": "Jane Doe",
    "author_time": "1704067200",
    "summary": "add RegisterHook function",
    "content": "func RegisterHook(name string, h Hook) error {"
  }],
  "count": 17,
  "exit_code": 0
}
```

`author_time` is a Unix timestamp string; format it on the consumer side.

---

### git_commit - Git Commit

**Purpose:** Stage exact paths and create a new commit. Safe by construction - refuses wildcards, staged all, and `--amend`.

**Risk:** write | **Cost:** io-bound

**Arguments**

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `paths` | string[] | yes* | - | Explicit file paths to stage (*required unless `path` is used) |
| `path` | string | yes* | - | Alias for single-file commit (*alternative to `paths`) |
| `message` | string | yes | - | Commit message (multiline supported) |
| `signoff` | boolean | no | false | Append Signed-off-by trailer |

**Rules**
- `paths` may NOT contain `-A`, `.`, or `*`. The user must name files explicitly.
- Pre-commit hooks run normally; do NOT use `run_command` with `--no-verify` to bypass a failing hook.
- `--amend`, `--no-verify`, `--no-gpg-sign` are blocked.

**Output**
```json
{"hash": "a1b2c3d", "paths": ["internal/hooks/hooks.go"], "exit_code": 0}
```

---

## 6. Execution and Validation

### run_command - Run Command

**Purpose:** Execute a binary in the project sandbox. **No shell** - `command` is argv[0], args are the rest. Use for build/test/lint/typecheck.

**Risk:** execute | **Cost:** network

**Arguments**

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `command` | string | yes | - | Binary to run (e.g. `go`, `git`, `python`) |
| `args` | string or string[] | no | - | Arguments; can be a string (split on whitespace) or array |
| `timeout_ms` | integer | no | tool config | Per-command timeout in ms |
| `cwd` | string | no | project root | Working directory |

**Example Shapes**
```json
{"command": "go", "args": ["build", "./..."]}
{"command": "go", "args": "version"}
{"command": "git", "args": ["status", "--short"]}
```

**Rules**
- **No shell features:** `&&`, `||`, `;`, `|`, `>`, redirects, and `cd` are NOT interpreted.
- **Prefer native tools:** `read_file` (not `cat`), `grep_codebase` (not `grep`), `glob` (not `find`), `edit_file` (not `sed`).
- **Dependent commands:** Issue as separate sequential `tool_call` invocations.
- **Git safety:** `--no-verify`, `--amend`, `-f` (force) are blocked.

**Output**
```json
{
  "stdout": "...",
  "stderr": "...",
  "exit_code": 0,
  "duration_ms": 1243
}
```

---

### patch_validation - Validate Patch

**Purpose:** Dry-run a unified-diff patch to confirm every hunk matches cleanly, then optionally run a build/test command to verify the patched code is valid.

**Risk:** execute (idempotent) | **Cost:** cpu-bound

**Arguments**

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `patch` | string | yes | - | Unified-diff patch string |
| `validation_command` | string | no | - | Optional shell command after dry-run (e.g. `go build ./...`); exit code 0 = passed |

**Output**
```json
{
  "files": [{
    "path": "internal/hooks/hooks.go",
    "hunks_applied": 3,
    "hunks_rejected": 0,
    "fuzzy_offsets": [0],
    "validation": {"exit_code": 0}
  }],
  "validation_passed": true
}
```

A patch is **clean** when all hunks apply without rejection (`hunks_rejected == 0`) and the validation command exits 0.

---

## 7. Web and Meta

### web_search - Web Search

**Purpose:** Search the web via DuckDuckGo HTML endpoint. No API key required.

**Risk:** execute | **Cost:** network

**Arguments**

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `query` | string | yes | - | Search query string |
| `limit` | integer | no | 8 | Max results (ceiling 25) |

**Output**
```json
{
  "query": "go 1.24 release notes",
  "count": 8,
  "results": [
    {"title": "Go 1.24 Release Notes", "url": "https://go.dev/doc/go1.24", "snippet": "..."}
  ]
}
```

**Rules:**
- Use `grep_codebase` / `glob` / `ast_query` for in-repo searches
- Refine the query instead of raising the limit for quality

---

### think - Think


**Purpose:** Record a reasoning step into the tool trace. No side effects. Low-cost scratch-pad.

**Risk:** read (idempotent) | **Cost:** cheap

**Arguments**

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `thought` | string | yes | - | Short reasoning note (max 2000 chars) |

**Output**
```json
{"thought": "Next step: grep for RegisterHook, then find_symbol, then read_file", "chars": 78}
```

**When to use:** Plans with 3+ steps or ambiguous decisions. Not for every turn.

---

## 8. Tool Discovery

### tool_search - Tool Search


**Purpose:** Discover backend tools by free-text query. Returns ranked short descriptions.


**Arguments**

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `query` | string | yes | - | Free-text search (name, tag, or topic) |
| `limit` | integer | no | 10 | Max results |

**Output**
```json
{
  "count": 3,
  "query": "patch",
  "results": [
    {"name": "patch_validation", "risk": "execute", "summary": "Dry-run a unified-diff patch...", "tags": ["patch", "validation", "dry-run"]},
    {"name": "apply_patch", "risk": "write", "summary": "Apply a unified diff...", "tags": ["filesystem", "write", "patch"]}
  ]
}
```

---

### tool_help - Tool Help

**Purpose:** Fetch the full schema and usage guide for a named backend tool.

**Arguments**

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `name` | string | yes | - | Exact tool name (from `tool_search` results) |

**Output**
Full tool schema including all arguments, their types, defaults, descriptions, and usage rules.

---

### tool_call - Call Tool

**Purpose:** Execute a single backend tool by name with its argument object.

**Arguments**

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `name` | string | yes | - | Backend tool name |
| `args` | object | yes | - | Argument object matching the tool schema |
| `_reason` | string | yes | - | Why this tool, why now, expected signal (max 140 chars) |

---

### tool_batch_call - Batch Call

**Purpose:** Execute several backend tool calls in parallel. Results returned in input order.

**Arguments**

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `calls` | {name, args}[] | yes | - | Array of tool calls to run in parallel |
| `_reason` | string | no | - | Wrapper-level reason (inherited by all calls if not overridden) |

**Rules**
- All calls in a batch must be **independent reads** (no call depends on another call's output).
- Dependent steps must be separate sequential `tool_call` invocations.
- Do NOT nest `tool_call` inside `tool_batch_call`.

---

## Read Stack Quick Reference

| Question | Tool |
|---|---|
| "Where does X appear?" | `grep_codebase` |
| "What is in the project?" (once, at start) | `codemap` |
| "Where is function/class NAME?" | `find_symbol` |
| "Show me lines N-M of file Y" | `read_file` |
| "What does this file define?" | `ast_query` |
| "Find test files matching X" | `glob` or `test_discovery` |
| "Run build/test/lint" | `run_command` |
| "Validate a patch before applying" | `patch_validation` |

---

## Risk Summary

| Risk level | Tools |
|---|---|
| read (idempotent) | `read_file`, `glob`, `list_dir`, `codemap`, `grep_codebase`, `find_symbol`, `ast_query`, `semantic_search`, `test_discovery`, `git_blame`, `think`, `tool_search`, `tool_help` |
| write | `write_file`, `edit_file`, `apply_patch`, `symbol_rename`, `symbol_move`, `git_commit` |
| execute | `run_command`, `patch_validation`, `web_search`, `tool_call`, `tool_batch_call` |
