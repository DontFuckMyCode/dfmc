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
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/ast"
)

// maxFindSymbolFileSize keeps symbol search from reading huge generated files.
const maxFindSymbolFileSize = 10 << 20

// goReceiverRE pulls the receiver type out of a Go method signature like
// `func (s *Server) Start(...)` or `func (Server) Start(...)`. Group 1 is
// the type name with leading `*` stripped.
var goReceiverRE = regexp.MustCompile(`^\s*func\s*\(\s*(?:[A-Za-z_]\w*\s+)?\*?([A-Za-z_]\w*)`)

// FindSymbolTool implements the locate-by-name tool. Uses the process-wide
// shared ast.Engine (see ast_shared.go) so repeat lookups across tools
// (model exploring a tree) reuse the parse cache instead of re-parsing.
type FindSymbolTool struct {
	engine *ast.Engine
}

func NewFindSymbolTool() *FindSymbolTool { return &FindSymbolTool{engine: astSharedEngine()} }
func (t *FindSymbolTool) Name() string   { return "find_symbol" }
func (t *FindSymbolTool) Description() string {
	return "Locate a named symbol (function, class, HTML id, ...) and return its full scope."
}

// Close is a no-op. See ast_shared.go for the rationale.
func (t *FindSymbolTool) Close() error { return nil }

func (t *FindSymbolTool) getEngine() *ast.Engine { return t.engine }

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
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if defaultWalkSkipDirs[d.Name()] {
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
			if hms := findInHTML(req.ProjectRoot, path, name, kind, matchMode, bodyMaxLines, includeBody); len(hms) > 0 {
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

		info, _ := d.Info()
		if info != nil && info.Size() > maxFindSymbolFileSize {
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

// Helpers (appendCapped, filterSymbols, nameMatches, kindMatches,
// buildScopeMatch, sliceBody, renderSymbolMatches) live in
// find_symbol_helpers.go.
