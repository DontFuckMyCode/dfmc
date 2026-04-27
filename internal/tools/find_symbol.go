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
	"slices"
	"strings"

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
	engine *ast.Engine
}

func NewFindSymbolTool() *FindSymbolTool { return &FindSymbolTool{engine: ast.New()} }
func (t *FindSymbolTool) Name() string   { return "find_symbol" }
func (t *FindSymbolTool) Description() string {
	return "Locate a named symbol (function, class, HTML id, ...) and return its full scope."
}
func (t *FindSymbolTool) Close() error {
	if t == nil || t.engine == nil {
		return nil
	}
	return t.engine.Close()
}

func (t *FindSymbolTool) getEngine() *ast.Engine { return t.engine }

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

	output := renderSymbolMatches(matches, includeBody)
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
	return slices.Contains(aliases[want], have)
}

// buildScopeMatch turns one symbol hit into a symbolMatch with its
// extracted body. Picks the scope strategy from the language.
func buildScopeMatch(path, language string, sym types.Symbol, lines []string, bodyMax int, includeBody bool) symbolMatch {
	startLine := min(max(sym.Line, 1), len(lines))
	endLine := extractScopeEnd(language, lines, startLine)
	endLine = max(endLine, startLine)
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


// renderSymbolMatches produces the human-readable Output. Each match
// gets a header "N. PATH:START-END  KIND  NAME" then (when bodies are
// included) a fenced code block. Without bodies it's a compact one-line
// list.
func renderSymbolMatches(matches []symbolMatch, includeBody bool) string {
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
