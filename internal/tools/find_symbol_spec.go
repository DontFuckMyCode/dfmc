package tools

// find_symbol_spec.go — long-form Spec() definition for FindSymbolTool.
// Sibling of find_symbol.go which keeps the tool struct, NewXxxTool /
// Name / Description / Close / getEngine plumbing, and the
// Execute pipeline. The Spec lives here because it is mostly prose
// (the model-facing Prompt is multi-paragraph guidance and accounts
// for ~80% of the byte count) and pulling it out lets the
// behavioural code stay scannable.

func (t *FindSymbolTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "find_symbol",
		Title:   "Find symbol with scope",
		Summary: "Locate a function/class/method/HTML id/class/tag by name and return its full lexical scope.",
		Purpose: "Use when you know WHAT you want but not WHERE. Returns code with brace/indent/tag-balanced bodies — no need to grep then read_file then guess line ranges.",
		Prompt: `Single-call symbol locator. Layer 3 of the read stack — sits between ` + "`grep_codebase`" + ` (cheapest discovery, line-stripped) and ` + "`read_file`" + ` (raw fetch, no semantics).

# When to pick find_symbol vs the neighbors

- "Where does the string X appear?" → ` + "`grep_codebase`" + `. Cheaper. Run this first when you only have a hunch.
- "What's the shape of this whole project?" → ` + "`codemap`" + `. Signatures-only outline, no bodies.
- "Show me the function/class NAMED X with its body" → this tool. AST-aware, returns the full scope.
- "Show me lines 200–260 of file Y" → ` + "`read_file`" + `. Cheaper when you already know the path and range.

Pipeline:
1. Walks the project tree (skips .git, node_modules, vendor, bin, dist, .dfmc, .venv).
2. For source files (Go, JS/TS/JSX/TSX, Python, Java, Rust, C/C++, C#, PHP, Swift, Kotlin, Scala, Ruby) — parses the AST, filters symbols by name (and kind if given), then extracts the full scope using brace balance (C-family) or indent (Python).
3. For HTML/XML — scans for ` + "`id=\"NAME\"`" + `, ` + "`class=\"NAME\"`" + `, or ` + "`<NAME`" + ` opening tags and extracts the balanced tag block.
4. Returns up to ` + "`max_results`" + ` matches (default 5, ceiling 20). Each body capped at ` + "`body_max_lines`" + ` (default 200) with a ` + "`truncated`" + ` flag so you know when to ask for more.

Args:
- name (required): symbol name. Use match=exact|prefix|contains to widen.
- kind (optional): function | method | class | interface | type | variable | constant | html_id | html_class | tag. Default: any AST symbol.
- parent (optional): disambiguate by enclosing scope. Receiver type for Go (` + "`(s *Server) Start`" + ` → parent="Server"); enclosing class for Python/JS/TS/Java/Rust. Drops matches whose parent doesn't equal this value — use it when several types share a method name.
- path (optional): restrict to a subdirectory of the project root.
- language (optional): filter to one language (e.g. "go", "python"). Default: auto.
- match (optional): exact (default) | prefix | contains.
- max_results (optional, default 5, ceiling 20).
- body_max_lines (optional, default 200, ceiling 1000).
- include_body (optional, default true). False → metadata only (path/line/kind/signature).

Output flags worth knowing:
- ` + "`fallback: true`" + ` on a match means tree-sitter could not parse that file and a regex extractor produced the symbols. Results are best-effort — broken syntax, partial code, or unsupported language stubs trip this. Verify with read_file before acting on it.

When to use:
- "Find the aliveli function" — exact symbol lookup, returns the body.
- "Where is the SettingsPanel class?" — class lookup with inheritance/method context.
- "Show me the HTML block with id='login'" — tag-balanced extract.
- "List every method named 'render'" — match=exact, kind=method, max_results=20.

When NOT to use:
- Pattern search across content (use grep_codebase).
- File listing (use glob or list_dir).
- Single-file outline (use ast_query).`,
		Risk: RiskRead,
		Tags: []string{"search", "read", "symbol", "ast", "scope"},
		Args: []Arg{

			{Name: "kind", Type: ArgString, Description: "function | method | class | interface | type | variable | constant | html_id | html_class | tag."},
			{Name: "parent", Type: ArgString, Description: `Disambiguate by enclosing scope: receiver type for Go ("Server"), enclosing class for Python/JS/TS/Java ("UserService"). Drops matches whose parent doesn't equal this value.`},
			{Name: "path", Type: ArgString, Description: "Restrict search to a subdirectory."},
			{Name: "language", Type: ArgString, Description: `Filter to one language (e.g. "go", "python", "html").`},
			{Name: "match", Type: ArgString, Default: "exact", Description: "exact | prefix | contains."},
			{Name: "max_results", Type: ArgInteger, Default: 5, Description: "Cap on returned matches (<=20)."},
			{Name: "body_max_lines", Type: ArgInteger, Default: 200, Description: "Cap on lines per match (<=1000)."},
			{Name: "include_body", Type: ArgBoolean, Default: true, Description: "When false, omit the code body — return metadata only."},
		},
		Returns: "{name, count, matches:[{path, language, name, kind, start_line, end_line, parent?, signature?, body?, truncated, fallback?}]}. fallback=true means tree-sitter couldn't parse the file and a regex extractor was used — results are best-effort.",
		Examples: []string{
			`{"name":"aliveli"}`,
			`{"name":"render","kind":"method","max_results":20}`,
			`{"name":"login","kind":"html_id","language":"html"}`,
			`{"name":"SettingsPanel","kind":"class","path":"frontend","include_body":false}`,
		},
		Idempotent: true,
		CostHint:   "io-bound",
	}
}
