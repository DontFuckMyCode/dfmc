package tools

// find_symbol.go — locate a named symbol with its full lexical scope.
//
// The model needs this when it knows the NAME of something
// (function, class, method, variable, HTML id/class) but not where it
// lives or what its body looks like. Plain grep_codebase returns lines
// stripped of context — the model then has to issue read_file on each
// hit, guess line ranges, and stitch the body together. find_symbol
// collapses that whole loop into one call: AST-driven discovery,
// language-aware scope extraction, body trimmed to a configurable cap.
//
// Per-language behaviour:
//   - Go / JS / TS / Java / Rust / C / C++ / C# / Swift / Kotlin / Scala /
//     PHP : AST locates the start line, brace-balanced walk extracts
//     the scope. String + comment state is tracked best-effort to keep
//     literal `{`/`}` from breaking the count.
//   - Python / YAML / Ruby (best-effort): AST locates start line,
//     indent-based walk extracts the scope (stop at the first non-empty
//     line whose indent is ≤ header indent).
//   - HTML / XML / JSX (template parts) : line-scan for `id="NAME"`,
//     `class="NAME"`, or `<NAME` opening tags; extract the balanced
//     tag block via a tag-stack walk.
//
// Output is bounded: max_results cap (default 5, ceiling 20),
// body_max_lines per match (default 200, ceiling 1000), and a
// `truncated` flag per match so the model knows when to ask for more.

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/dontfuckmycode/dfmc/internal/ast"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// goReceiverRE pulls the receiver type out of a Go method signature like
// `func (s *Server) Start(...)` or `func (Server) Start(...)`. Group 1 is
// the type name with leading `*` stripped.
var goReceiverRE = regexp.MustCompile(`^\s*func\s*\(\s*(?:[A-Za-z_]\w*\s+)?\*?([A-Za-z_]\w*)`)

// FindSymbolTool implements the locate-by-name tool. Holds a lazily-init
// ast.Engine; the parse cache lets repeat lookups against the same files
// (model exploring a tree) avoid re-parsing.
type FindSymbolTool struct {
	once   sync.Once
	engine *ast.Engine
}

func NewFindSymbolTool() *FindSymbolTool      { return &FindSymbolTool{} }
func (t *FindSymbolTool) Name() string        { return "find_symbol" }
func (t *FindSymbolTool) Description() string { return "Locate a named symbol (function, class, HTML id, ...) and return its full scope." }

func (t *FindSymbolTool) getEngine() *ast.Engine {
	t.once.Do(func() { t.engine = ast.New() })
	return t.engine
}

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
			{Name: "name", Type: ArgString, Required: true, Description: "Symbol name to locate."},
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

func (t *FindSymbolTool) Execute(ctx context.Context, req Request) (Result, error) {
	name := strings.TrimSpace(asString(req.Params, "name", ""))
	if name == "" {
		return Result{}, missingParamError("find_symbol", "name", req.Params,
			`{"name":"aliveli"} or {"name":"render","kind":"method","max_results":20}`,
			`name is the symbol to locate. Optional filters: kind (function|method|class|html_id|html_class|tag), language, path (subdir), match (exact|prefix|contains), max_results, body_max_lines.`)
	}

	root := strings.TrimSpace(asString(req.Params, "path", ""))
	base := req.ProjectRoot
	if root != "" {
		p, err := EnsureWithinRoot(req.ProjectRoot, root)
		if err != nil {
			return Result{}, err
		}
		base = p
	}

	kind := strings.ToLower(strings.TrimSpace(asString(req.Params, "kind", "")))
	wantLang := strings.ToLower(strings.TrimSpace(asString(req.Params, "language", "")))
	wantParent := strings.TrimSpace(asString(req.Params, "parent", ""))
	matchMode := strings.ToLower(strings.TrimSpace(asString(req.Params, "match", "exact")))
	switch matchMode {
	case "exact", "prefix", "contains":
	case "":
		matchMode = "exact"
	default:
		return Result{}, fmt.Errorf("find_symbol: match must be exact|prefix|contains, got %q", matchMode)
	}

	maxResults := asInt(req.Params, "max_results", 5)
	if maxResults <= 0 {
		maxResults = 5
	}
	if maxResults > 20 {
		maxResults = 20
	}
	bodyMaxLines := asInt(req.Params, "body_max_lines", 200)
	if bodyMaxLines <= 0 {
		bodyMaxLines = 200
	}
	if bodyMaxLines > 1000 {
		bodyMaxLines = 1000
	}
	includeBody := asBool(req.Params, "include_body", true)

	// HTML mode trips when kind is one of the html_* values OR when
	// language is explicitly html/xml — tree-sitter doesn't expose HTML
	// tags as symbols, so we bypass the AST entirely.
	htmlKinds := map[string]bool{"html_id": true, "html_class": true, "tag": true}
	htmlOnly := htmlKinds[kind] || wantLang == "html" || wantLang == "xml"

	matches := []symbolMatch{}
	walkErr := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".dfmc", "node_modules", "vendor", "bin", "dist", ".venv", ".idea", "build", "target":
				return fs.SkipDir
			}
			return nil
		}
		if len(matches) >= maxResults {
			return fs.SkipAll
		}
		ext := strings.ToLower(filepath.Ext(path))
		switch ext {
		case ".html", ".htm", ".xml", ".vue", ".svelte":
			// Always eligible for HTML mode.
		default:
			if htmlOnly {
				return nil
			}
		}

		// Try HTML-mode extraction for HTML-shaped files. We don't rule it
		// out for non-HTML modes because templated files often hold IDs
		// (e.g. .vue / .svelte) and the model may search for them.
		if ext == ".html" || ext == ".htm" || ext == ".xml" || ext == ".vue" || ext == ".svelte" {
			if hms := findInHTML(path, name, kind, matchMode, bodyMaxLines, includeBody); len(hms) > 0 {
				matches = appendCapped(matches, hms, maxResults)
				return nil
			}
			if htmlOnly {
				return nil
			}
		}

		// AST mode for everything else. Skip files whose language we can't
		// detect (avoids parsing every binary or .lock file in the tree).
		parsed, perr := t.getEngine().ParseFile(ctx, path)
		if perr != nil || parsed == nil {
			return nil
		}
		if wantLang != "" && parsed.Language != wantLang {
			return nil
		}

		hits := filterSymbols(parsed.Symbols, name, kind, matchMode)
		if len(hits) == 0 {
			return nil
		}

		content, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		lines := strings.Split(string(content), "\n")
		fallback := parsed.Backend == "regex"
		for _, sym := range hits {
			m := buildScopeMatch(path, parsed.Language, sym, lines, bodyMaxLines, includeBody)
			m.Parent = detectParent(parsed.Language, sym, lines)
			if wantParent != "" && !parentMatches(m.Parent, wantParent) {
				continue
			}
			m.Fallback = fallback
			matches = append(matches, m)
			if len(matches) >= maxResults {
				return fs.SkipAll
			}
		}
		return nil
	})
	if walkErr != nil && walkErr != fs.SkipAll {
		return Result{}, walkErr
	}

	if len(matches) == 0 {
		// Be specific about what was searched so the model can broaden.
		filters := []string{fmt.Sprintf("name=%q", name)}
		if kind != "" {
			filters = append(filters, "kind="+kind)
		}
		if wantParent != "" {
			filters = append(filters, "parent="+wantParent)
		}
		if wantLang != "" {
			filters = append(filters, "language="+wantLang)
		}
		if matchMode != "exact" {
			filters = append(filters, "match="+matchMode)
		}
		if root != "" {
			filters = append(filters, "path="+root)
		}
		return Result{
			Output: fmt.Sprintf("(no symbols matched %s) — try match=contains, broaden language, or drop kind to widen the search.", strings.Join(filters, " ")),
			Data: map[string]any{
				"name":    name,
				"count":   0,
				"matches": []any{},
			},
		}, nil
	}

	output := renderSymbolMatches(name, matches, includeBody)
	dataMatches := make([]map[string]any, 0, len(matches))
	for _, m := range matches {
		entry := map[string]any{
			"path":       m.Path,
			"language":   m.Language,
			"name":       m.Name,
			"kind":       m.Kind,
			"start_line": m.StartLine,
			"end_line":   m.EndLine,
			"truncated":  m.Truncated,
		}
		if m.Parent != "" {
			entry["parent"] = m.Parent
		}
		if m.Signature != "" {
			entry["signature"] = m.Signature
		}
		if includeBody && m.Body != "" {
			entry["body"] = m.Body
		}
		if m.Fallback {
			entry["fallback"] = true
		}
		dataMatches = append(dataMatches, entry)
	}
	return Result{
		Output: output,
		Data: map[string]any{
			"name":    name,
			"count":   len(matches),
			"matches": dataMatches,
		},
	}, nil
}

// symbolMatch is the per-result struct used internally before flattening
// to the JSON-shaped Data map. Body is the extracted scope (already
// truncated to body_max_lines when needed); Truncated is true when the
// real scope ran longer.
type symbolMatch struct {
	Path      string
	Language  string
	Name      string
	Kind      string
	Parent    string
	StartLine int
	EndLine   int
	Signature string
	Body      string
	Truncated bool
	// Fallback is true when the file's symbols came from the regex
	// extractor instead of tree-sitter — broken syntax, CGO-disabled
	// build, or a stub language. The model treats these results as
	// best-effort and verifies before acting.
	Fallback bool
}

func appendCapped(dst, src []symbolMatch, cap int) []symbolMatch {
	for _, m := range src {
		if len(dst) >= cap {
			break
		}
		dst = append(dst, m)
	}
	return dst
}

// filterSymbols applies the name/kind/match filters to an AST symbol
// list. Kind aliases ("function"↔"method", "class"↔"struct"↔"type")
// are accepted so the model doesn't need to know the exact AST term
// per language.
func filterSymbols(symbols []types.Symbol, name, kind, mode string) []types.Symbol {
	out := []types.Symbol{}
	for _, s := range symbols {
		if !nameMatches(s.Name, name, mode) {
			continue
		}
		if kind != "" && !kindMatches(string(s.Kind), kind) {
			continue
		}
		out = append(out, s)
	}
	return out
}

func nameMatches(have, want, mode string) bool {
	if want == "" {
		return false
	}
	switch mode {
	case "prefix":
		return strings.HasPrefix(have, want)
	case "contains":
		return strings.Contains(strings.ToLower(have), strings.ToLower(want))
	default:
		return have == want
	}
}

func kindMatches(have, want string) bool {
	have = strings.ToLower(have)
	want = strings.ToLower(want)
	if have == want {
		return true
	}
	// Common aliases — the model often guesses one term per language.
	aliases := map[string][]string{
		"function":  {"method", "func"},
		"method":    {"function", "func"},
		"class":     {"type", "struct", "interface"},
		"type":      {"class", "struct", "interface"},
		"struct":    {"type", "class"},
		"interface": {"type", "class"},
		"variable":  {"var", "field"},
		"constant":  {"const", "enum"},
	}
	for _, alt := range aliases[want] {
		if have == alt {
			return true
		}
	}
	return false
}

// buildScopeMatch turns one symbol hit into a symbolMatch with its
// extracted body. Picks the scope strategy from the language.
func buildScopeMatch(path, language string, sym types.Symbol, lines []string, bodyMax int, includeBody bool) symbolMatch {
	startLine := sym.Line
	if startLine < 1 {
		startLine = 1
	}
	if startLine > len(lines) {
		startLine = len(lines)
	}
	endLine := extractScopeEnd(language, lines, startLine)
	if endLine < startLine {
		endLine = startLine
	}
	m := symbolMatch{
		Path:      path,
		Language:  language,
		Name:      sym.Name,
		Kind:      string(sym.Kind),
		StartLine: startLine,
		EndLine:   endLine,
		Signature: sym.Signature,
	}
	if includeBody {
		m.Body, m.Truncated = sliceBody(lines, startLine, endLine, bodyMax)
	} else if endLine-startLine+1 > bodyMax {
		m.Truncated = true
	}
	return m
}

// sliceBody returns lines[start-1:end] joined, clamped to maxLines.
// When clamped, leaves a "// … (NN lines elided)" marker at the cut so
// the model knows the body was bigger.
func sliceBody(lines []string, start, end, maxLines int) (string, bool) {
	if start < 1 {
		start = 1
	}
	if end > len(lines) {
		end = len(lines)
	}
	if end < start {
		return "", false
	}
	span := lines[start-1 : end]
	if len(span) <= maxLines {
		return strings.Join(span, "\n"), false
	}
	// Keep the head — the function/class signature + opening — and trim
	// the tail. The model usually wants to see HOW it begins more than
	// HOW it ends; a tail elision marker tells it how much was cut.
	keep := maxLines - 1
	elided := len(span) - keep
	head := span[:keep]
	return strings.Join(head, "\n") + fmt.Sprintf("\n// … (%d lines elided — raise body_max_lines to see the rest)", elided), true
}

// detectParent returns the enclosing-scope name for a symbol — receiver
// type for Go methods, enclosing class for class-shaped languages
// (Python/JS/TS/Java/C#/Kotlin/Scala/Swift/PHP/Ruby/Rust impl). Returns
// "" when the symbol is top-level or the language doesn't carry a parent
// notion that we recognise. Best-effort: regex-based, no AST traversal.
func detectParent(language string, sym types.Symbol, lines []string) string {
	if sym.Kind != types.SymbolMethod && sym.Kind != types.SymbolFunction {
		return ""
	}
	switch language {
	case "go":
		if m := goReceiverRE.FindStringSubmatch(sym.Signature); len(m) > 1 {
			return m[1]
		}
		// Signature may be empty (regex fallback) — peek at the symbol's
		// header line directly.
		if sym.Line >= 1 && sym.Line <= len(lines) {
			if m := goReceiverRE.FindStringSubmatch(lines[sym.Line-1]); len(m) > 1 {
				return m[1]
			}
		}
		return ""
	case "python":
		return enclosingByIndent(lines, sym.Line, `^\s*class\s+([A-Za-z_]\w*)`)
	case "javascript", "typescript", "tsx", "jsx":
		return enclosingByBraces(lines, sym.Line, `^\s*(?:export\s+(?:default\s+)?)?(?:abstract\s+)?class\s+([A-Za-z_$][\w$]*)`)
	case "java", "csharp", "kotlin", "scala", "swift":
		return enclosingByBraces(lines, sym.Line, `^\s*(?:public\s+|private\s+|protected\s+|internal\s+|abstract\s+|final\s+|static\s+|sealed\s+|open\s+)*(?:class|interface|object|trait|struct|enum)\s+([A-Za-z_]\w*)`)
	case "rust":
		// Rust methods live in `impl X { ... }` or `impl Trait for X { ... }`.
		// Pick the type after `for` if present, otherwise the type after `impl`.
		return enclosingByBraces(lines, sym.Line, `^\s*impl(?:<[^>]*>)?\s+(?:[\w:<>,\s']+for\s+)?([A-Za-z_]\w*)`)
	case "php", "ruby":
		return enclosingByIndent(lines, sym.Line, `^\s*class\s+([A-Za-z_]\w*)`)
	case "cpp", "c++", "c":
		return enclosingByBraces(lines, sym.Line, `^\s*(?:class|struct)\s+([A-Za-z_]\w*)`)
	}
	return ""
}

// parentMatches reports whether `have` is the enclosing scope the caller
// asked for. Match is exact and case-sensitive — receiver/class names are
// canonical identifiers, not free text.
func parentMatches(have, want string) bool {
	if want == "" {
		return true
	}
	return have == want
}

// enclosingByIndent walks backward from line `at` to find the most recent
// header that matches `pattern` AND has a smaller leading indent than the
// symbol itself. Used for indent-scoped languages (Python, Ruby).
func enclosingByIndent(lines []string, at int, pattern string) string {
	if at < 1 || at > len(lines) {
		return ""
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return ""
	}
	symIndent := leadingIndent(lines[at-1])
	for i := at - 2; i >= 0; i-- {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := leadingIndent(line)
		if indent >= symIndent {
			continue
		}
		if m := re.FindStringSubmatch(line); len(m) > 1 {
			return m[1]
		}
	}
	return ""
}

// enclosingByBraces walks backward from line `at` looking for a header
// line that matches `pattern` whose `{` block still encloses the symbol.
// The brace count is computed from the candidate line through `at` — if
// it ends > 0 the candidate's block is still open at `at`. Best-effort:
// strings/comments are not stripped, so weird literal braces in between
// can mislead the count, but the common case (one class per file, methods
// inside it) is handled correctly.
func enclosingByBraces(lines []string, at int, pattern string) string {
	if at < 1 || at > len(lines) {
		return ""
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return ""
	}
	for i := at - 2; i >= 0; i-- {
		m := re.FindStringSubmatch(lines[i])
		if len(m) <= 1 {
			continue
		}
		// Count braces from this header through the symbol's line.
		depth := 0
		seenOpen := false
		for j := i; j < at && j < len(lines); j++ {
			for _, c := range lines[j] {
				switch c {
				case '{':
					depth++
					seenOpen = true
				case '}':
					depth--
				}
			}
		}
		if seenOpen && depth > 0 {
			return m[1]
		}
	}
	return ""
}

// extractScopeEnd picks a per-language scope strategy. Falls back to
// brace-balanced (most C-family languages) when the language is unknown.
func extractScopeEnd(language string, lines []string, startLine int) int {
	switch language {
	case "python", "yaml":
		return extractByIndent(lines, startLine)
	case "ruby":
		return extractRubyScope(lines, startLine)
	case "bash", "shell":
		return extractByIndent(lines, startLine)
	default:
		return extractByBraces(lines, startLine)
	}
}

// extractByBraces walks forward from startLine counting `{` vs `}`,
// best-effort skipping over chars inside string literals and comments.
// Returns the line that closes the first scope opened at-or-after
// startLine; returns startLine when no `{` is found within a reasonable
// look-ahead (3000 lines) so we don't run away on a malformed file.
func extractByBraces(lines []string, startLine int) int {
	depth := 0
	opened := false
	const maxLookahead = 3000
	limit := startLine + maxLookahead
	if limit > len(lines) {
		limit = len(lines)
	}
	inBlockComment := false
	for i := startLine - 1; i < limit; i++ {
		line := lines[i]
		j := 0
		inString := byte(0) // 0 = not in string, '"' / '\'' / '`' = in string of that quote
		for j < len(line) {
			c := line[j]
			if inBlockComment {
				if c == '*' && j+1 < len(line) && line[j+1] == '/' {
					inBlockComment = false
					j += 2
					continue
				}
				j++
				continue
			}
			if inString != 0 {
				if c == '\\' && j+1 < len(line) {
					j += 2
					continue
				}
				if c == inString {
					inString = 0
				}
				j++
				continue
			}
			// Not in a string or block comment.
			if c == '/' && j+1 < len(line) {
				if line[j+1] == '/' {
					break // line comment — skip rest of line
				}
				if line[j+1] == '*' {
					inBlockComment = true
					j += 2
					continue
				}
			}
			if c == '"' || c == '\'' || c == '`' {
				inString = c
				j++
				continue
			}
			if c == '{' {
				depth++
				opened = true
			} else if c == '}' {
				depth--
				if opened && depth <= 0 {
					return i + 1
				}
			}
			j++
		}
	}
	// No closing brace found within look-ahead — return the start line so
	// the caller doesn't dump 3000 lines of unrelated code.
	if !opened {
		return startLine
	}
	return limit
}

// extractByIndent walks forward from startLine; the scope ends at the
// first non-empty line whose indent is ≤ the header's indent. Used for
// Python / YAML / shell heredocs / similar.
func extractByIndent(lines []string, startLine int) int {
	if startLine < 1 || startLine > len(lines) {
		return startLine
	}
	headerIndent := leadingIndent(lines[startLine-1])
	const maxLookahead = 3000
	limit := startLine + maxLookahead
	if limit > len(lines) {
		limit = len(lines)
	}
	end := startLine
	for i := startLine; i < limit; i++ { // i is 0-based line just AFTER the header
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			end = i + 1
			continue
		}
		if leadingIndent(line) <= headerIndent {
			break
		}
		end = i + 1
	}
	return end
}

func leadingIndent(s string) int {
	n := 0
	for _, r := range s {
		switch r {
		case ' ':
			n++
		case '\t':
			n += 4 // count a tab as 4 columns for comparison purposes
		default:
			return n
		}
	}
	return n
}

// extractRubyScope walks until the matching `end` keyword at the
// header's indent. Best-effort — doesn't track strings/heredocs.
func extractRubyScope(lines []string, startLine int) int {
	if startLine < 1 || startLine > len(lines) {
		return startLine
	}
	headerIndent := leadingIndent(lines[startLine-1])
	const maxLookahead = 3000
	limit := startLine + maxLookahead
	if limit > len(lines) {
		limit = len(lines)
	}
	for i := startLine; i < limit; i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "end" && leadingIndent(line) == headerIndent {
			return i + 1
		}
	}
	return startLine
}

// findInHTML scans an HTML/XML/template file for the named id, class,
// or tag and returns the balanced tag block(s). Multiple matches are
// possible (e.g. multiple elements with the same class).
func findInHTML(path, name, kind, mode string, bodyMax int, includeBody bool) []symbolMatch {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(string(data), "\n")

	wantTag := kind == "tag"
	wantID := kind == "html_id" || kind == ""
	wantClass := kind == "html_class" || kind == ""

	out := []symbolMatch{}
	for i, line := range lines {
		startLine := i + 1
		var hit struct {
			matched bool
			tagName string
			kind    string
		}
		if wantTag && containsHTMLTagOpener(line, name) {
			hit.matched = true
			hit.tagName = name
			hit.kind = "tag"
		} else if wantID {
			if attrValueMatches(line, "id", name, mode) {
				hit.matched = true
				hit.tagName = htmlOpeningTagName(line)
				hit.kind = "html_id"
			}
		}
		if !hit.matched && wantClass {
			if attrValueMatches(line, "class", name, mode) {
				hit.matched = true
				hit.tagName = htmlOpeningTagName(line)
				hit.kind = "html_class"
			}
		}
		if !hit.matched {
			continue
		}
		endLine := extractHTMLTagBlock(lines, startLine, hit.tagName)
		body := ""
		truncated := false
		if includeBody {
			body, truncated = sliceBody(lines, startLine, endLine, bodyMax)
		} else if endLine-startLine+1 > bodyMax {
			truncated = true
		}
		out = append(out, symbolMatch{
			Path:      path,
			Language:  "html",
			Name:      name,
			Kind:      hit.kind,
			StartLine: startLine,
			EndLine:   endLine,
			Body:      body,
			Truncated: truncated,
		})
	}
	return out
}

// containsHTMLTagOpener reports whether `line` opens a `<tag` (not the
// closing `</tag` and not a substring of an unrelated word).
func containsHTMLTagOpener(line, tag string) bool {
	if tag == "" {
		return false
	}
	needle := "<" + strings.ToLower(tag)
	low := strings.ToLower(line)
	idx := 0
	for {
		pos := strings.Index(low[idx:], needle)
		if pos < 0 {
			return false
		}
		pos += idx
		end := pos + len(needle)
		if end >= len(low) {
			return false
		}
		next := low[end]
		// Followed by whitespace, '>', or '/' → real opener. Followed
		// by another letter (e.g. `<header` for tag=`head`) → keep
		// looking.
		if next == ' ' || next == '\t' || next == '>' || next == '/' || next == '\n' || next == '\r' {
			return true
		}
		idx = end
	}
}

// attrValueMatches checks whether `line` carries `attr="value"` (or
// single-quoted) where value matches `name` per `mode`. For class
// attrs the value is split on whitespace before comparison so
// class="foo bar" matches name="bar".
func attrValueMatches(line, attr, name, mode string) bool {
	low := strings.ToLower(line)
	wantAttr := strings.ToLower(attr) + "="
	pos := strings.Index(low, wantAttr)
	if pos < 0 {
		return false
	}
	rest := line[pos+len(wantAttr):]
	if len(rest) == 0 {
		return false
	}
	quote := rest[0]
	if quote != '"' && quote != '\'' {
		return false
	}
	end := strings.IndexByte(rest[1:], quote)
	if end < 0 {
		return false
	}
	value := rest[1 : 1+end]
	if attr == "class" {
		for _, tok := range strings.Fields(value) {
			if nameMatches(tok, name, mode) {
				return true
			}
		}
		return false
	}
	return nameMatches(value, name, mode)
}

// htmlOpeningTagName returns the tag name from the first `<TAG` opener
// on the line. Returns "" when no opener is found (rare for a hit; the
// scope walker falls back to a minimal 1-line span).
func htmlOpeningTagName(line string) string {
	idx := strings.Index(line, "<")
	if idx < 0 {
		return ""
	}
	rest := line[idx+1:]
	if len(rest) == 0 || rest[0] == '/' || rest[0] == '!' {
		return ""
	}
	end := 0
	for end < len(rest) {
		c := rest[end]
		if c == ' ' || c == '\t' || c == '>' || c == '/' || c == '\n' || c == '\r' {
			break
		}
		end++
	}
	return strings.ToLower(rest[:end])
}

// extractHTMLTagBlock walks forward from startLine maintaining a tag
// stack; returns the line that closes the opening tag. Self-closing
// tags (`<br/>`, `<img ... />`) and void elements (`<input ...>`)
// resolve to startLine. Best-effort — doesn't parse CDATA or scripts.
func extractHTMLTagBlock(lines []string, startLine int, tag string) int {
	if tag == "" || startLine < 1 || startLine > len(lines) {
		return startLine
	}
	const maxLookahead = 3000
	limit := startLine + maxLookahead
	if limit > len(lines) {
		limit = len(lines)
	}
	voidTags := map[string]bool{
		"area": true, "base": true, "br": true, "col": true, "embed": true,
		"hr": true, "img": true, "input": true, "link": true, "meta": true,
		"param": true, "source": true, "track": true, "wbr": true,
	}
	if voidTags[tag] {
		return startLine
	}
	opener := "<" + tag
	closer := "</" + tag
	depth := 0
	openedSeen := false
	for i := startLine - 1; i < limit; i++ {
		low := strings.ToLower(lines[i])
		for {
			oIdx := strings.Index(low, opener)
			cIdx := strings.Index(low, closer)
			if oIdx < 0 && cIdx < 0 {
				break
			}
			// Check if this is a self-closing opener — in that case it
			// doesn't increment the stack.
			if oIdx >= 0 && (cIdx < 0 || oIdx < cIdx) {
				end := oIdx + len(opener)
				gt := strings.Index(low[end:], ">")
				if gt < 0 {
					// Tag opener spans lines; treat as a real open and
					// move on.
					depth++
					openedSeen = true
					low = low[end:]
					continue
				}
				selfClose := gt > 0 && low[end+gt-1] == '/'
				if !selfClose {
					depth++
					openedSeen = true
				}
				low = low[end+gt+1:]
				continue
			}
			// Closer comes first.
			depth--
			if openedSeen && depth <= 0 {
				return i + 1
			}
			low = low[cIdx+len(closer):]
		}
	}
	if !openedSeen {
		return startLine
	}
	return limit
}

// renderSymbolMatches produces the human-readable Output. Each match
// gets a header "N. PATH:START-END  KIND  NAME" then (when bodies are
// included) a fenced code block. Without bodies it's a compact one-line
// list.
func renderSymbolMatches(query string, matches []symbolMatch, includeBody bool) string {
	// Stable sort by path then line so repeated calls render the same
	// shape, even though the walker order is filesystem-dependent.
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Path != matches[j].Path {
			return matches[i].Path < matches[j].Path
		}
		return matches[i].StartLine < matches[j].StartLine
	})

	var b strings.Builder
	for i, m := range matches {
		if i > 0 {
			b.WriteString("\n\n")
		}
		display := m.Name
		if m.Parent != "" {
			display = m.Parent + "." + m.Name
		}
		header := fmt.Sprintf("%d. %s:%d-%d  %s  %s", i+1, m.Path, m.StartLine, m.EndLine, m.Kind, display)
		if m.Signature != "" && m.Signature != m.Name {
			header += "  " + m.Signature
		}
		if m.Truncated {
			header += "  [truncated]"
		}
		if m.Fallback {
			header += "  [regex-fallback]"
		}
		b.WriteString(header)
		if includeBody && m.Body != "" {
			b.WriteString("\n```")
			if m.Language != "" {
				b.WriteString(m.Language)
			}
			b.WriteString("\n")
			b.WriteString(m.Body)
			b.WriteString("\n```")
		}
	}
	return b.String()
}
