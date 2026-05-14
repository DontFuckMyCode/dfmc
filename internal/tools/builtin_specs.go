package tools

// Spec() methods for the filesystem and code-aware builtin tools:
// read_file, write_file, edit_file, list_dir, grep_codebase, glob,
// ast_query, apply_patch. Meta / network / execution Spec()s (think,
// todo_write, web_fetch, web_search, delegate_task, run_command) live
// in builtin_specs_meta.go.
//
// The specs are the provider-facing contract — changing an Arg name
// or Required flag here is a public surface change. Keep Summary /
// Purpose short; the fat operational guidance lives in Prompt, which
// is only materialized when the model fetches tool_help.

func (t *ReadFileTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "read_file",
		Title:   "Read file",
		Summary: "Read a text file, optionally scoped to a line range.",
		Purpose: "Fetch file contents for analysis. Prefer narrow line ranges for large files.",
		Prompt: `Foundation reader for the context-gathering stack. Use this instead of ` + "`cat`/`head`/`tail`" + ` via run_command — it's cheaper, cached, and participates in the read-before-mutation guard.

# When to use which read tool

DFMC has four read-side tools, ordered by precision (and cost):

1. ` + "`grep_codebase`" + ` — discovery. "Where does string X appear?" Returns ` + "`file:line:text`" + ` lines. Cheapest first probe when you don't know where to look.
2. ` + "`codemap`" + ` — orientation. Project-level signatures-only outline. "What's the shape of this codebase?" before diving in.
3. ` + "`find_symbol`" + ` — semantic locate. "Where is function/class NAME and show me its body." AST-driven, returns the full scope. Use when you know WHAT but not WHERE.
4. ` + "`read_file`" + ` (this tool) — exact byte/line fetch. Use when you have a known path + line range, or need to see imports/headers/whole-file context that the other layers don't preserve.

The pattern is grep → find_symbol/codemap → read_file. Skipping straight to read_file on a guessed path costs more tokens than starting with discovery.

Rules:
- Prefer a tight line range (line_start/line_end). The default window is 200 lines; avoid reading the whole file when a 40-line slice is enough.
- Required before edit_file or write_file on an existing file — the engine rejects mutations that aren't preceded by a read of the current contents.
- The result includes ` + "`total_lines`" + `, ` + "`returned_lines`" + `, ` + "`truncated`" + `, and ` + "`language`" + ` — use ` + "`truncated`" + ` to decide whether to ask for the next slice instead of guessing.
- For structure-only questions ("what symbols does this file export?") prefer ast_query — it returns a dense outline without the full body.
- When you need many files at once, send multiple read_file calls in a single tool_batch_call rather than round-tripping.`,
		Risk: RiskRead,
		Tags: []string{"filesystem", "read", "code"},
		Args: []Arg{
			{Name: "path", Type: ArgString, Required: true, Description: "Relative path inside the project."},
			{Name: "line_start", Type: ArgInteger, Description: "1-indexed start line (default 1).", Default: 1},
			{Name: "line_end", Type: ArgInteger, Description: "1-indexed end line (inclusive).", Default: 200},
		},
		Returns:    "Text segment of the file plus {path, line_start, line_end, line_count, total_lines, returned_lines, truncated, language}. truncated=true means the file is longer than the returned slice — request more lines if you didn't already specify a tight range.",
		Examples:   []string{`{"path":"main.go","line_start":1,"line_end":80}`},
		Idempotent: true,
		CostHint:   "cheap",
	}
}

func (t *WriteFileTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "write_file",
		Title:   "Write file",
		Summary: "Create or overwrite a text file (requires prior read_file for existing files).",
		Purpose: "Materialize new files or rewrite existing ones. For small edits prefer edit_file.",
		Prompt: `Use for NEW files or full rewrites only. Any edit touching < ~50% of a file should use edit_file or apply_patch instead — write_file is destructive and hard to diff-review.

Rules:
- If the target already exists, you MUST have called read_file on it in this session. The engine refuses blind overwrites.
- Always include a trailing newline unless the file is deliberately binary-ish (lockfiles, etc.). Missing trailing newlines trigger noisy diffs.
- Never use this to "touch" a file just to trigger a build — prefer run_command with the explicit build step.
- Writing inside .git/, node_modules/, vendor/, .dfmc/ is blocked by root-escape checks. Redirect to the correct path.
- After writing a new file, verify with a follow-up read_file or the relevant test command. Do not declare done on a write alone.`,
		Risk: RiskWrite,
		Tags: []string{"filesystem", "write", "code"},
		Args: []Arg{
			{Name: "path", Type: ArgString, Required: true, Description: "Relative path inside the project."},
			{Name: "content", Type: ArgString, Required: true, Description: "Full file contents."},
			{Name: "create_dirs", Type: ArgBoolean, Default: true, Description: "Create parent directories if missing."},
			{Name: "overwrite", Type: ArgBoolean, Default: false, Description: "Allow overwriting existing files."},
		},
		Returns:  "{path, bytes} on success.",
		CostHint: "io-bound",
	}
}

func (t *EditFileTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "edit_file",
		Title:   "Edit file",
		Summary: "Apply an exact string replacement in a text file.",
		Purpose: "Surgical edits. old_string must be unique unless replace_all is true.",
		Prompt: `Default tool for targeted changes. The old_string→new_string pair is applied literally — no regex, no whitespace-fuzzing.

Rules:
- old_string must be UNIQUE in the file, or the call fails. Grow context (add surrounding lines) until it is, rather than using replace_all blindly.
- Preserve the exact indentation and trailing whitespace of the original. The engine compares bytes, not tokens.
- For renaming a symbol everywhere, set replace_all=true AND make old_string long enough to avoid collateral hits on unrelated code.
- If you need to apply several hunks to the same file, prefer apply_patch with a single unified diff — fewer round-trips and the diff is self-documenting.
- Always read_file first: the engine rejects edits to files whose content changed since the last read.
- After edit, run the smallest validation that proves correctness (targeted test, go vet, tsc on the changed file).

Failure hints (read the error message carefully, don't blindly retry):
- "not found — trimmed form matches" → drop surrounding whitespace from old_string.
- "not found — indentation may be off" → re-read the region and copy bytes verbatim (tabs vs spaces).
- "not unique: N matches (line A, line B)" → extend old_string with a neighbouring unique line, or pass replace_all=true.
- "identical" → you did not actually change anything; re-plan the edit.

Line endings are auto-normalized for matching (CRLF files match LF old_string and vice-versa); the tool re-applies the file's original newline style when writing, so you never need to think about this.`,
		Risk: RiskWrite,
		Tags: []string{"filesystem", "write", "edit", "code"},
		Args: []Arg{
			{Name: "path", Type: ArgString, Required: true, Description: "Relative path inside the project."},
			{Name: "old_string", Type: ArgString, Required: true, Description: "Exact text to find."},
			{Name: "new_string", Type: ArgString, Required: true, Description: "Replacement text."},
			{Name: "replace_all", Type: ArgBoolean, Default: false, Description: "Replace every occurrence instead of exactly one."},
		},
		Returns:  "{path, replacements}.",
		CostHint: "io-bound",
	}
}

func (t *ListDirTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "list_dir",
		Title:   "List directory",
		Summary: "List files and directories under a path.",
		Purpose: "Discover project layout. Use recursive=true for whole-subtree walks.",
		Prompt: `Use for shape-of-repo questions; for "find files that match X" reach for glob instead.

Rules:
- recursive=true on a large repo can flood the context window. Cap max_entries (default 200, ceiling 500) and prefer scoped paths.
- Skips .git/, node_modules/, vendor/, bin/, dist/ automatically. Don't fight this — if you actually need those dirs, navigate with read_file against a known path.
- For "does this file exist?" prefer glob with an exact pattern — cheaper than a full listing.`,
		Risk: RiskRead,
		Tags: []string{"filesystem", "read", "list"},
		Args: []Arg{
			{Name: "path", Type: ArgString, Required: true, Description: "Relative directory path (use \".\" for project root)."},
			{Name: "recursive", Type: ArgBoolean, Default: false, Description: "Walk subdirectories."},
			{Name: "max_entries", Type: ArgInteger, Default: 200, Description: "Cap on returned entries (<=500)."},
		},
		Returns:    "{path, entries[], count}.",
		Idempotent: true,
		CostHint:   "cheap",
	}
}

func (t *GrepCodebaseTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "grep_codebase",
		Title:   "Grep codebase",
		Summary: "Regex search across project files (skips .git, node_modules, vendor, bin, dist).",
		Purpose: "Locate symbols, patterns, or call sites. Always prefer a tight regex over broad queries.",
		Prompt: `Use this instead of shelling out to grep/rg via run_command. It respects project ignore rules and returns file:line:text directly.

# When to pick grep_codebase vs the neighbors

This is the cheapest discovery layer in the read stack. Use it FIRST when you don't know where to look.

- "Where does string X live?" → grep_codebase (this tool). Cheap, pattern-based, returns file:line:text.
- "What's the shape of the project?" → ` + "`codemap`" + `. Signatures-only outline.
- "Show me the function/class NAMED X with its body" → ` + "`find_symbol`" + `. Semantic; returns the full scope.
- "Show me file Y around line N" → ` + "`read_file`" + `. Use this once grep tells you where.

A common pattern is: ` + "`grep_codebase`" + ` to locate hits → ` + "`find_symbol`" + ` for the named scope → ` + "`read_file`" + ` for surrounding context. Don't read whole files speculatively when grep can pinpoint the relevant 3 lines.

# Regex syntax — Go RE2, NOT PCRE/Perl

DO NOT use these (they will reject with "invalid Perl syntax"):
- Lookbehind / lookahead: ` + "`(?<=...)`" + `, ` + "`(?<!...)`" + `, ` + "`(?=...)`" + `, ` + "`(?!...)`" + `
- Backreferences: ` + "`\\1`" + `, ` + "`\\2`" + ` — match candidates and check equality in a follow-up call
- Possessive quantifiers: ` + "`*+`" + `, ` + "`++`" + `, ` + "`?+`" + `

DO use:
- Standard char classes: ` + "`\\d \\w \\s \\b`" + `
- Case-insensitive flag: ` + "`(?i)pattern`" + `
- Non-capturing group: ` + "`(?:foo|bar)`" + `
- Named capture: ` + "`(?P<name>...)`" + ` (Python-style; this IS supported)

# Anchor your query

- Anchor the query as tightly as you can — ` + "`func FooBar`" + ` or ` + "`import \"pkg/foo\"`" + ` rather than just ` + "`FooBar`" + `. Broad patterns waste tokens and miss the actual call site.
- If you need file listings not content, use glob instead — cheaper and returns paths directly.
- For symbol lookup inside a known file, ast_query is better: it returns structured symbols with kinds.
- If a result is truncated, narrow the regex or raise max_results — don't retry the same call expecting different output.

# Filtering and shaping output

- ` + "`include`" + ` / ` + "`exclude`" + ` accept globs (array or comma-string). Use ` + "`include:[\"**/*.go\"]`" + ` to confine the search rather than walking every file in the tree, then matching, then dropping non-Go hits — much cheaper.
- ` + "`case_sensitive: false`" + ` is equivalent to ` + "`(?i)`" + ` at the start of the pattern; pick one, not both.
- ` + "`context: 3`" + ` (or ` + "`before` + `after`" + ` per side, capped at 50) wraps each hit in surrounding lines. Output switches to ripgrep-style blocks separated by ` + "`--`" + `: match lines use ` + "`path:line:text`" + `, context lines use ` + "`path-line-text`" + `. Use this when you need to see what calls a symbol or what an error message neighbors — saves a follow-up ` + "`view`" + ` round-trip.
- ` + "`respect_gitignore: false`" + ` searches inside generated/vendored dirs the project would normally hide. Default is true.`,
		Risk: RiskRead,
		Tags: []string{"search", "read", "code", "grep"},
		Args: []Arg{
			{Name: "pattern", Type: ArgString, Required: true, Description: "Go regexp (RE2) pattern."},
			{Name: "query", Type: ArgString, Description: "Backward-compatible alias for pattern. If both are present, pattern wins."},
			{Name: "path", Type: ArgString, Description: "Restrict search to a subdirectory of the project root."},
			{Name: "case_sensitive", Type: ArgBoolean, Default: true, Description: "When false, prefixes the pattern with (?i) for a case-insensitive match."},
			{Name: "context", Type: ArgInteger, Default: 0, Description: "Lines of context to include before AND after each match (cap 50). Override per-side with `before` / `after`."},
			{Name: "before", Type: ArgInteger, Description: "Override of `context` for lines before the match."},
			{Name: "after", Type: ArgInteger, Description: "Override of `context` for lines after the match."},
			{Name: "include", Type: ArgString, Description: `Glob(s) to keep. Array ["**/*.go","internal/**"] or comma-string "**/*.go,**/*.md". Doublestar supported.`},
			{Name: "exclude", Type: ArgString, Description: `Glob(s) to skip in addition to the hardcoded ignore set. Same shape as include.`},
			{Name: "respect_gitignore", Type: ArgBoolean, Default: true, Description: "When true (default), reads top-level .gitignore and skips matching paths. Negation patterns (!) and per-subdir .gitignore are NOT honoured."},
			{Name: "max_results", Type: ArgInteger, Default: 80, Description: "Cap on matches (<=500)."},
		},
		Returns:    "{pattern, matches[] (file:line:text or context blocks separated by `--`), count, case_sensitive, context_before?, context_after?, include?, exclude?}.",
		Idempotent: true,
		CostHint:   "io-bound",
	}
}

func (t *GlobTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "glob",
		Title:   "Glob files",
		Summary: "Match project files against a glob pattern (supports ** for recursive).",
		Purpose: "Discover files by pattern. Use before list_dir when you know what shape of file you want.",
		Prompt: `Fastest way to ask "what files match this shape?". Prefer over run_command with find.

Rules:
- Supports ` + "`**`" + ` for recursive traversal (e.g. ` + "`internal/**/*_test.go`" + `). Patterns without ` + "`**`" + ` are matched against basename, so ` + "`*.go`" + ` will find every .go file in the repo.
- Skips .git/, node_modules/, vendor/, bin/, dist/, .venv/ — same ignore set as the rest of DFMC.
- For finding code that CONTAINS a pattern, combine with grep_codebase; glob alone only returns paths.
- Prefer narrow subpaths (` + "`path: \"internal\"`" + `) when you already know the area — cheaper than a root-wide walk.`,
		Risk: RiskRead,
		Tags: []string{"filesystem", "read", "search", "glob"},
		Args: []Arg{
			{Name: "pattern", Type: ArgString, Required: true, Description: `Pattern like "**/*.go" or "internal/**/*_test.go".`},
			{Name: "path", Type: ArgString, Description: "Restrict search to a subdirectory (defaults to project root)."},
			{Name: "max_results", Type: ArgInteger, Default: 200, Description: "Cap on returned paths (<=2000)."},
		},
		Returns:    "{pattern, count, matches[]}.",
		Examples:   []string{`{"pattern":"**/*.go"}`, `{"pattern":"*.md","path":"docs"}`},
		Idempotent: true,
		CostHint:   "io-bound",
	}
}

func (t *ASTQueryTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "ast_query",
		Title:   "Query AST",
		Summary: "Parse a source file and return its symbols, imports, and language.",
		Purpose: "Get the outline of a file without reading the whole body. Filter by kind or name substring.",
		Prompt: `Structured view of a source file: functions, types, classes, imports, with file:line anchors.

Rules:
- Reach for this before read_file when the question is "what does this file define?". Cheaper than reading, and the outline is denser than code.
- Supports Go, JavaScript, TypeScript, Python via tree-sitter (CGO build). Without CGO, falls back to a regex extractor — less accurate but still useful.
- Filter with ` + "`kind`" + ` ("function", "struct", "class", ...) or ` + "`name_contains`" + ` (case-insensitive substring) to focus on one piece.
- For "where is Foo called?" this is the wrong tool — use grep_codebase on the identifier.
- If the returned outline has ` + "`errors[]`" + `, the file failed to parse cleanly; fall back to read_file.`,
		Risk: RiskRead,
		Tags: []string{"code", "ast", "read", "symbols"},
		Args: []Arg{
			{Name: "path", Type: ArgString, Required: true, Description: "Relative path inside the project."},
			{Name: "kind", Type: ArgString, Description: "Filter by symbol kind (function, class, struct, ...)."},
			{Name: "name_contains", Type: ArgString, Description: "Filter to symbols whose name contains this substring (case-insensitive)."},
		},
		Returns:    "Outline text plus {path, language, symbols[], imports[], errors[], count}.",
		Examples:   []string{`{"path":"internal/engine/engine.go","kind":"function"}`},
		Idempotent: true,
		CostHint:   "io-bound",
	}
}

func (t *ApplyPatchTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "apply_patch",
		Title:   "Apply patch",
		Summary: "Apply a unified diff (one or more files) to the project.",
		Purpose: "Preferred for multi-hunk edits the model expresses as a unified diff. Use dry_run to preview.",
		Prompt: `Best tool for multi-hunk edits or touching several files in one go. The diff IS the review artifact — no follow-up explanation needed.

Rules:
- Format is standard unified diff: ` + "`--- a/path`" + ` / ` + "`+++ b/path`" + ` / ` + "`@@ -old,count +new,count @@`" + ` headers, ` + "` `" + `/` + "`+`" + `/` + "`-`" + ` line prefixes.
- New file: use ` + "`--- /dev/null`" + ` with ` + "`+++ b/newpath`" + `. Deleted file: use ` + "`+++ /dev/null`" + `.
- Context lines must match the CURRENT file — a stale read will produce rejected hunks. If you see ` + "`hunks_rejected > 0`" + `, re-read the file and regenerate the patch, don't retry.
- Run with ` + "`dry_run: true`" + ` first when the patch is non-trivial; the output tells you exactly which hunks would land.
- For a single one-line swap, edit_file is simpler and shows the change inline.
- Do NOT use for renaming files — the current parser treats rename as delete+add and loses history.`,
		Risk: RiskWrite,
		Tags: []string{"filesystem", "write", "patch", "diff"},
		Args: []Arg{
			{Name: "patch", Type: ArgString, Required: true, Description: "Unified diff (git-style `---`/`+++`/`@@` format)."},
			{Name: "dry_run", Type: ArgBoolean, Default: false, Description: "Parse and match but do not write files."},
		},
		Returns:  "Per-file summary plus {files[], count, dry_run}.",
		CostHint: "io-bound",
	}
}
