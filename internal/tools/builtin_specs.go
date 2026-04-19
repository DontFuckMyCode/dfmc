package tools

// Spec() methods for the builtin tools. The specs are the provider-facing
// contract — changing an Arg name or Required flag here is a public surface
// change. Keep Summary/Purpose short; the fat operational guidance lives in
// Prompt, which is only materialized when the model fetches tool_help.

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

func (t *ThinkTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "think",
		Title:   "Think",
		Summary: "Record a reasoning step into the tool trace. No side effects.",
		Purpose: "Use when a plan has several stages and you want the next step visible before acting.",
		Prompt: `Low-cost scratch-pad. Writes a reasoning note to the trace so the user (and later turns) can see WHY you took the next action.

Rules:
- Reach for it when a plan has 3+ steps or when ambiguity is high and you want to narrate the decision point before acting.
- Do NOT use for every turn — it adds trace noise for linear tasks where the next call is obvious.
- Keep each thought compact (<300 chars ideal, 2000 max). Dense bullets beat paragraphs.
- Not a substitute for todo_write: think is episodic reasoning; todo_write is durable state.`,
		Risk: RiskRead,
		Tags: []string{"meta", "reasoning"},
		Args: []Arg{
			{Name: "thought", Type: ArgString, Required: true, Description: "Short reasoning note (<= 2000 chars)."},
		},
		Returns:    `{"thought":"...","chars":N}`,
		Idempotent: true,
		CostHint:   "cheap",
	}
}

func (t *TodoWriteTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "todo_write",
		Title:   "Write todos",
		Summary: "Maintain a session-scoped todo list (plan multi-step work).",
		Purpose: "Set the full todo list at once with `action=set`. The state is ephemeral — cleared per process.",
		Prompt: `Use for multi-step work (3+ distinct tasks). Renders a checklist in the TUI so the user can follow progress.

Rules:
- Always call ` + "`action=set`" + ` with the FULL list — individual status updates overwrite the slot. Partial updates that omit earlier items will drop them.
- Exactly ONE item should be status=in_progress at a time. Mark completed immediately when a step finishes; don't batch completions.
- Skip this tool for trivial single-step tasks — overhead outweighs the benefit.
- State does not persist across sessions. For durable tracking, ask the user to use external issue tracking.`,
		Risk: RiskRead,
		Tags: []string{"meta", "planning"},
		Args: []Arg{
			{Name: "action", Type: ArgString, Description: "set | list | clear. Default set.", Enum: []any{"set", "list", "clear"}},
			{
				Name:        "todos",
				Type:        ArgArray,
				Description: "Array of {content,status[pending|in_progress|completed],active_form?}.",
				Items:       &Arg{Type: ArgObject, Description: "{content,status,active_form?}"},
			},
		},
		Returns:  "Rendered list + {count, pending, in_progress, completed, items[]}.",
		Examples: []string{`{"action":"set","todos":[{"content":"wire router","status":"in_progress"}]}`, `{"action":"list"}`},
		CostHint: "cheap",
	}
}

func (t *WebFetchTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "web_fetch",
		Title:   "Web fetch",
		Summary: "GET a URL and return its body (HTML stripped to text by default).",
		Purpose: "Pull documentation / release notes / API specs. Follows up to 5 redirects.",
		Prompt: `Fetches external HTTP(S) content. HTML is stripped to plain text by default; set raw=true only when you specifically need tags.

Rules:
- http(s) only — file://, ftp://, and other schemes are rejected to block SSRF-adjacent abuse.
- Cap max_bytes aggressively (default 128 KiB, ceiling 1 MiB). Most docs pages compress under 50 KiB once HTML is stripped.
- For search-style questions ("what's the latest Go release?") call web_search first, then web_fetch the best result — don't guess URLs.
- The response includes {status, content_type, truncated}. If truncated=true, try a more specific URL rather than raising max_bytes blindly.
- Do not fetch the same URL twice in a session unless content is known to change (build status pages, etc.).`,
		Risk: RiskExecute,
		Tags: []string{"network", "web", "read"},
		Args: []Arg{
			{Name: "url", Type: ArgString, Required: true, Description: "http(s) URL."},
			{Name: "max_bytes", Type: ArgInteger, Default: 131072, Description: "Body size cap in bytes (<=1 MiB)."},
			{Name: "raw", Type: ArgBoolean, Default: false, Description: "Return body as-is instead of stripping HTML."},
		},
		Returns:  "Text content plus {url, status, content_type, bytes, truncated}.",
		Examples: []string{`{"url":"https://pkg.go.dev/net/http"}`},
		CostHint: "network",
	}
}

func (t *WebSearchTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "web_search",
		Title:   "Web search",
		Summary: "Search the web (DuckDuckGo HTML endpoint). Returns title/url/snippet triples.",
		Purpose: "Find canonical documentation or external references. No API key required.",
		Prompt: `Discovery tool for external references. Returns ranked title/url/snippet triples you can then web_fetch.

Rules:
- Uses DuckDuckGo's HTML endpoint — no API key, but also no guaranteed stability. Expect occasional empty results under rate-limiting.
- Query like you would a search engine, not a chatbot. ` + "`go 1.24 release notes site:go.dev`" + ` beats ` + "`what is new in go 1.24`" + `.
- Limit defaults to 8, cap 25. Don't raise the cap hoping for quality — refine the query instead.
- Do not use for in-repo searches. Use grep_codebase / glob / ast_query for anything inside the project.`,
		Risk: RiskExecute,
		Tags: []string{"network", "web", "search"},
		Args: []Arg{
			{Name: "query", Type: ArgString, Required: true, Description: "Search query string."},
			{Name: "limit", Type: ArgInteger, Default: 8, Description: "Max results (<=25)."},
		},
		Returns:  "Formatted list plus {query, count, results:[{title,url,snippet}]}.",
		Examples: []string{`{"query":"go 1.24 release notes"}`},
		CostHint: "network",
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

func (t *DelegateTaskTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "delegate_task",
		Title:   "Delegate to sub-agent",
		Summary: "Spawn a bounded sub-agent to handle a focused task with its own fresh context.",
		Purpose: "Move a token-heavy survey or research off the main loop. The sub-agent returns a summary only.",
		Prompt: `Spawns a fresh agent with its own context window. Use when the main loop would be overwhelmed by the token cost of exploration.

When to delegate:
- "Survey all call sites of X and summarize" — a research task the main thread doesn't need blow-by-blow.
- "Analyze these 12 files and pick the 2 most likely to contain the bug" — triage with high read volume.
- Anything where the VALUE is a summary, not the intermediate tool output.

When NOT to delegate:
- Single-step lookups — round-trip cost > benefit.
- Anything that edits code — the sub-agent's edits land in the same repo but the summary won't show you the diff line-by-line.
- Questions the user asked YOU directly — don't hand off the main conversation.

Rules:
- The sub-agent sees NO prior context. The ` + "`task`" + ` string must be self-contained: include paths, symbols, and what form the answer should take.
- ` + "`allowed_tools`" + ` restricts the sub-agent to a subset. For read-only surveys pass ` + "`[\"read_file\",\"grep_codebase\",\"ast_query\",\"glob\"]`" + ` — keeps it cheap and safe.
- ` + "`max_steps`" + ` caps tool calls (default 10, ceiling 40). The sub-agent also gets half the parent's token budget (floor 10000).
- The return is ` + "`{summary, tool_calls, duration_ms}`" + `. Treat ` + "`summary`" + ` as authoritative; do not re-do the work unless the sub-agent clearly failed.`,
		Risk: RiskExecute,
		Tags: []string{"meta", "agent", "planning"},
		Args: []Arg{
			{Name: "task", Type: ArgString, Required: true, Description: "Self-contained prompt for the sub-agent (it sees no prior context)."},
			{Name: "role", Type: ArgString, Description: "Optional role hint (researcher, reviewer, planner)."},
			{Name: "allowed_tools", Type: ArgArray, Description: "Restrict sub-agent to a tool subset.", Items: &Arg{Type: ArgString}},
			{Name: "max_steps", Type: ArgInteger, Default: 10, Description: "Tool-call budget for the sub-agent (<=40)."},
			{Name: "model", Type: ArgString, Description: "Provider profile override (defaults to parent's)."},
		},
		Returns:  "Final summary plus {summary, tool_calls, duration_ms}.",
		Examples: []string{`{"task":"Find all call sites of OpenProject and report their files","role":"researcher","max_steps":8}`},
		CostHint: "network",
	}
}

func (t *RunCommandTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "run_command",
		Title:   "Run command",
		Summary: "Execute one binary in the project sandbox. NO shell — pass the binary in `command`, the rest in `args`.",
		Purpose: "Run build/test/lint commands. Blocked commands, timeouts, and output caps are enforced by config.",
		Prompt: `Direct binary execution inside the project sandbox. There is **no shell**: ` + "`command`" + ` is argv[0] (the binary), ` + "`args`" + ` is the rest. ` + "`&&`" + `, ` + "`||`" + `, ` + "`;`" + `, ` + "`|`" + `, ` + "`>`" + `, redirects, and ` + "`cd `" + ` chains are NOT interpreted — pass them in ` + "`command`" + ` and you'll get a "shell syntax not supported" error.

# Shape

` + "```json" + `
{"command": "go", "args": ["build", "./..."]}
{"command": "go", "args": "version"}             // string also accepted, split on whitespace
{"command": "git", "args": ["status", "--short"]}
` + "```" + `

# Prefer dedicated tools over the shell

DFMC has native tools that are cheaper, cached, and reviewable. Only reach for run_command when none apply:

- Read a file → ` + "`read_file`" + ` (NOT ` + "`cat`/`head`/`tail`" + `).
- Search content → ` + "`grep_codebase`" + ` (NOT ` + "`grep`/`rg`" + `).
- Find files → ` + "`glob`" + ` (NOT ` + "`find`/`ls`" + `).
- Edit a file → ` + "`edit_file`" + ` / ` + "`apply_patch`" + ` (NOT ` + "`sed`/`awk`" + `).
- Write a file → ` + "`write_file`" + ` (NOT ` + "`echo >`" + ` / heredocs).
- Fetch a URL → ` + "`web_fetch`" + ` (NOT ` + "`curl`/`wget`" + `).

Use run_command for: build, test, lint, typecheck, dependency management, git operations, anything without a DFMC-native equivalent.

# Sequencing

- Independent commands → send multiple tool_call invocations in ONE tool_batch_call; they run in the order given but you save round-trips.
- Dependent commands (build before test) → issue them as separate sequential tool_calls. The engine runs them in order and surfaces the failure if the first one breaks. Do NOT try to chain with ` + "`&&`" + ` — there is no shell to interpret it.
- Want to keep going after a failure? Just send the next tool_call regardless of the previous result.

# Git safety

- NEVER use ` + "`--no-verify`" + `, ` + "`--no-gpg-sign`" + `, or any flag that bypasses hooks/signing unless the user explicitly asked for it.
- NEVER ` + "`git push --force`" + ` against main/master. Against feature branches, confirm with the user first.
- NEVER ` + "`git reset --hard`" + `, ` + "`git checkout .`" + `, ` + "`git clean -f`" + `, or ` + "`git branch -D`" + ` without explicit user authorization.
- Prefer specific file staging (` + "`git add path/to/file.go`" + `) over ` + "`git add -A`" + ` / ` + "`git add .`" + ` — the latter can sweep in .env and secrets.
- Never use ` + "`-i`" + ` flags (` + "`git rebase -i`" + `, ` + "`git add -i`" + `) — interactive mode is not supported.
- Pre-commit hook failed? Fix the issue and create a NEW commit. Do not ` + "`--amend`" + ` — the failed commit doesn't exist, amending rewrites the previous one.
- Multiline commit messages: use a HEREDOC via ` + "`git commit -m \"$(cat <<'EOF' ... EOF)\"`" + ` — inline ` + "`-m`" + ` mangles quotes.

# Paths and working directory

- Use absolute paths or paths relative to the project root. Don't rely on ` + "`cd`" + ` persisting across calls (it doesn't — each command starts at the project root).
- Quote paths that contain spaces: ` + "`cd \"path with spaces/...\"`" + `.

# Timeouts and long runs

- Default timeout is from config (30s typical). Override with ` + "`timeout_ms`" + ` when you know a command takes longer, up to 120000ms.
- Don't poll with ` + "`sleep`" + ` loops. If a command is genuinely long-running, tell the user and consider running a shorter check instead.
- Failing commands should be diagnosed, not retried. A ` + "`go test`" + ` failure won't fix itself on the second run.

# Output

- stdout and stderr are combined. Large outputs get truncated with a notice; narrow the command (` + "`go test ./internal/foo/...`" + ` instead of ` + "`./...`" + `) rather than raising the cap.
- Exit code is in ` + "`data.exit_code`" + `; non-zero is a failure even if stdout looks fine.`,
		Risk: RiskExecute,
		Tags: []string{"shell", "execute", "build", "test"},
		Args: []Arg{
			{Name: "command", Type: ArgString, Required: true, Description: `argv[0] only — a single binary name like "go", "git", "npm". NO shell syntax (&&, ||, ;, |, >, cd ...): the executor calls the binary directly and rejects shell-line packing with a clear error.`},
			{Name: "args", Type: ArgString, Description: `Arguments for the binary. Either a JSON array (preferred: ["build","./..."]) or a single whitespace-separated string ("build ./...") — both are accepted.`},
			{Name: "dir", Type: ArgString, Description: "Working directory relative to the project root. Defaults to the project root. Use this instead of `cd` (which is not interpreted)."},
			{Name: "timeout_ms", Type: ArgInteger, Description: "Optional per-call timeout override in ms (<=120000)."},
		},
		Returns:  "stdout/stderr combined Output plus {exit_code, duration_ms}.",
		CostHint: "network",
	}
}
