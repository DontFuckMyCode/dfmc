package tools

// Spec() methods for the meta / network / execution tools: think,
// todo_write, web_fetch, web_search, delegate_task, run_command.
// Filesystem and code Spec()s (read_file, write_file, edit_file,
// list_dir, grep_codebase, glob, ast_query, apply_patch) live in
// builtin_specs.go.

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
