// semantic_search.go — Phase 7 structured semantic code search using AST.
// Searches AST nodes matching a pattern query across files or the full project.
// Returns structured matches with path, line, column, node type, and snippet context.
//
// Pattern language:
//   FunctionDecl:<name>  — function declaration
//   FunctionCall:<name>   — function call (treated same as FunctionDecl in regex fallback)
//   TypeDecl:<name>       — type declaration (class/interface/type)
//   MethodDecl:<name>    — method with receiver
//   IfStmt               — all if statements (maps to any symbol when queried)
//   ReturnStmt           — all return statements
//   AssignStmt           — all assignment statements
//   VarDecl:<name>       — variable declaration
//   ConstDecl:<name>      — constant declaration
//   :type=<typename>     — filter by result/var type (Go-style, e.g. error)
//   :context=N           — include N lines before/after in snippet (default 0)
package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/ast"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

type SemanticSearchTool struct{}

func NewSemanticSearchTool() *SemanticSearchTool { return &SemanticSearchTool{} }
func (t *SemanticSearchTool) Name() string    { return "semantic_search" }
func (t *SemanticSearchTool) Description() string {
	return "Structured semantic search for AST nodes by type and name pattern across files or the whole project."
}

func (t *SemanticSearchTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "semantic_search",
		Title:   "Semantic search",
		Summary: "Find AST nodes by type and name pattern across files or the whole project.",
		Purpose: `Use when you need to locate code constructs by their AST node kind and name — not just text matches. For example: all function declarations named "foo", all type declarations, all return statements. Returns structured results with file/line/column and optional context lines. Use instead of grep when you know the semantic type of what you're looking for.`,
		Prompt: `Finds AST nodes matching a pattern across files or the full project.
Pipeline:
1. Parse target files with the AST engine (tree-sitter when CGO enabled, regex fallback otherwise)
2. Match AST node kinds and names against the pattern query
3. Return structured matches with path, line, column, snippet

Pattern language:
- FunctionDecl:<name> — function declaration with matching name
- FunctionCall:<name>  — function call (same as FunctionDecl in regex fallback)
- TypeDecl:<name>      — type declaration (class/interface/type)
- MethodDecl:<name>    — method with receiver
- IfStmt              — if statements (matches any symbol when name is absent)
- ReturnStmt          — return statements
- AssignStmt          — assignment statements
- VarDecl:<name>      — variable declaration
- ConstDecl:<name>    — constant declaration
- :type=<typename>   — filter by result/parameter type
- :context=N          — include N lines before/after for snippet

Output is capped by max_results (default 100). Each match includes the snippet and optional surrounding context lines.`,
		Risk:  RiskRead,
		Tags:  []string{"search", "ast", "semantic", "structure", "find"},
		Args: []Arg{
			{Name: "query", Type: ArgString, Required: true, Description: `AST pattern (e.g. "FunctionDecl:name=foo", "TypeDecl:name=Bar", "IfStmt").`},
			{Name: "file", Type: ArgString, Description: `Scope to a single file. Absent = full project.`},
			{Name: "lang", Type: ArgString, Default: "go", Description: `"go" | "typescript" | "python" | "all". Default: go.`},
			{Name: "max_results", Type: ArgInteger, Default: 100, Description: `Cap results at N. Default: 100.`},
		},
		Returns:        "{query, matches: [{path, line, column, node_type, name, snippet, context_lines}], total, backend}",
		Idempotent:     true,
		CostHint:       "cpu-bound",
	}
}

type semanticMatch struct {
	Path         string   `json:"path"`
	Line         int      `json:"line"`
	Column       int      `json:"column"`
	NodeType     string   `json:"node_type"`
	Name         string   `json:"name,omitempty"`
	Snippet      string   `json:"snippet"`
	ContextLines []string `json:"context_lines,omitempty"`
}

// parsedQuery holds a parsed pattern query.
type parsedQuery struct {
	nodeType    string
	name        string
	typeFilter  string
	context     int
}

func parseQuery(q string) parsedQuery {
	q = strings.TrimSpace(q)
	var pq parsedQuery
	pq.context = 0

	parts := strings.Split(q, ":")
	pq.nodeType = strings.TrimSpace(parts[0])
	if len(parts) > 1 {
		for i := 1; i < len(parts); i++ {
			part := strings.TrimSpace(parts[i])
			if strings.HasPrefix(part, "type=") {
				pq.typeFilter = strings.TrimPrefix(part, "type=")
			} else if strings.HasPrefix(part, "context=") {
				fmt.Sscanf(part, "context=%d", &pq.context)
			} else if strings.HasPrefix(part, "name=") {
				pq.name = strings.TrimPrefix(part, "name=")
			} else if pq.name == "" {
				// First non-flag :part is the bare name filter
				pq.name = part
			}
		}
	}
	return pq
}

func (t *SemanticSearchTool) Execute(ctx context.Context, req Request) (Result, error) {
	queryStr := strings.TrimSpace(asString(req.Params, "query", ""))
	if queryStr == "" {
		return Result{}, missingParamError("semantic_search", "query", req.Params,
			`{"query":"FunctionDecl:name=foo","file":"main.go"}`,
			`query is required — an AST pattern like "FunctionDecl:name=foo" or "TypeDecl:name=Bar".`)
	}

	pq := parseQuery(queryStr)
	if pq.nodeType == "" {
		return Result{}, fmt.Errorf("semantic_search: query %q has no node type", queryStr)
	}

	file := strings.TrimSpace(asString(req.Params, "file", ""))
	langFilter := strings.ToLower(strings.TrimSpace(asString(req.Params, "lang", "go")))
	maxResults := asInt(req.Params, "max_results", 100)
	if maxResults <= 0 {
		maxResults = 100
	}

	projectRoot := req.ProjectRoot
	if projectRoot == "" {
		projectRoot = "."
	}

	var targetFiles []string
	if file != "" {
		// EnsureWithinRoot rejects `../`-relative escapes — the
		// returned snippets and ContextLines surface file content, so
		// a path outside the project would leak `~/.ssh/config` etc.
		abs, err := EnsureWithinRoot(projectRoot, file)
		if err != nil {
			return Result{}, fmt.Errorf("semantic_search: file outside project root: %w", err)
		}
		targetFiles = []string{abs}
	} else {
		targetFiles = collectSearchFiles(projectRoot, langFilter)
	}

	if len(targetFiles) == 0 {
		return Result{
			Output: fmt.Sprintf("semantic_search: no files found matching lang=%q", langFilter),
			Data: map[string]any{
				"query":   queryStr,
				"matches": []semanticMatch{},
				"total":   0,
				"backend": "n/a",
			},
		}, nil
	}

	astEngine := ast.New()
	defer astEngine.Close()

	var matches []semanticMatch
	backend := "unknown"

	for _, fpath := range targetFiles {
		if len(matches) >= maxResults {
			break
		}
		fileMatches, fileBackend := searchFileWithEngine(astEngine, fpath, pq)
		if len(fileMatches) > 0 {
			if backend == "unknown" {
				backend = fileBackend
			}
			for _, m := range fileMatches {
				if len(matches) >= maxResults {
					break
				}
				matches = append(matches, m)
			}
		}
	}

	return Result{
		Output: fmt.Sprintf("semantic_search: found %d matches for %q", len(matches), queryStr),
		Data: map[string]any{
			"query":   queryStr,
			"matches": matches,
			"total":   len(matches),
			"backend": backend,
		},
	}, nil
}

func searchFileWithEngine(engine *ast.Engine, fpath string, pq parsedQuery) ([]semanticMatch, string) {
	content, err := os.ReadFile(fpath)
	if err != nil {
		return nil, ""
	}

	res, err := engine.ParseContent(context.Background(), fpath, content)
	if err != nil {
		return nil, ""
	}

	backend := res.Backend
	if backend == "" {
		backend = "regex"
	}

	var matches []semanticMatch
	lines := strings.Split(string(content), "\n")

	for _, sym := range res.Symbols {
		if !symKindMatchesQuery(sym, pq) {
			continue
		}
		snippet := sym.Signature
		if snippet == "" {
			if sym.Line >= 1 && sym.Line <= len(lines) {
				snippet = strings.TrimSpace(lines[sym.Line-1])
			}
		}

		var contextLines []string
		if pq.context > 0 && sym.Line >= 1 {
			start := sym.Line - 1 - pq.context
			if start < 0 {
				start = 0
			}
			end := sym.Line - 1 + pq.context
			if end > len(lines) {
				end = len(lines)
			}
			for i := start; i < end; i++ {
				if i >= 0 && i < len(lines) {
					contextLines = append(contextLines, lines[i])
				}
			}
		}

		matches = append(matches, semanticMatch{
			Path:         fpath,
			Line:         sym.Line,
			Column:       sym.Column,
			NodeType:     pq.nodeType,
			Name:         sym.Name,
			Snippet:      snippet,
			ContextLines: contextLines,
		})
	}

	return matches, backend
}

func symKindMatchesQuery(sym types.Symbol, pq parsedQuery) bool {
	nodeKind := strings.ToLower(pq.nodeType)
	switch nodeKind {
	case "functiondecl", "functioncall":
		if sym.Kind != types.SymbolFunction {
			return false
		}
		return patternNameMatches(sym.Name, pq.name)
	case "typedecl":
		if sym.Kind != types.SymbolType && sym.Kind != types.SymbolInterface && sym.Kind != types.SymbolClass {
			return false
		}
		return patternNameMatches(sym.Name, pq.name)
	case "methoddecl":
		if sym.Kind != types.SymbolFunction {
			return false
		}
		if !strings.Contains(sym.Signature, "(") {
			return false
		}
		return patternNameMatches(sym.Name, pq.name)
	case "ifstmt", "returnstmt", "assignstmt":
		return pq.name == "" || patternNameMatches(sym.Name, pq.name)
	case "vardecl":
		if sym.Kind != types.SymbolVariable {
			return false
		}
		return patternNameMatches(sym.Name, pq.name)
	case "constdecl":
		if sym.Kind != types.SymbolConstant {
			return false
		}
		return patternNameMatches(sym.Name, pq.name)
	default:
		return pq.name == "" || patternNameMatches(sym.Name, pq.name)
	}
}

// patternNameMatches checks if symName matches the given pattern.
// pattern may be "", a plain name, or a name with * at start/end only.
// Supports: "foo" exact, "foo*" prefix, "*foo" suffix, "*Bar*" infix, "*" any.
// * in the middle of the pattern is treated as a literal character.
func patternNameMatches(symName, pattern string) bool {
	if pattern == "" {
		return true
	}
	pattern = strings.TrimPrefix(pattern, "name=")
	// Exact match (no wildcards)
	if !strings.Contains(pattern, "*") {
		return symName == pattern
	}
	// Bare * matches everything
	if pattern == "*" {
		return true
	}
	// Infix * (e.g. "foo*bar") — * at both start AND end with non-empty sides
	if strings.HasPrefix(pattern, "*") && strings.HasSuffix(pattern, "*") {
		infix := pattern[1 : len(pattern)-1]
		return infix == "" || strings.Contains(symName, infix)
	}
	// Trailing-only * wildcard (e.g. "foo*")
	if strings.HasSuffix(pattern, "*") && !strings.Contains(pattern[:len(pattern)-1], "*") {
		prefix := pattern[:len(pattern)-1]
		return prefix == "" || strings.HasPrefix(symName, prefix)
	}
	// Leading-only * wildcard (e.g. "*Bar")
	if strings.HasPrefix(pattern, "*") && !strings.Contains(pattern[1:], "*") {
		suffix := pattern[1:]
		return suffix == "" || strings.HasSuffix(symName, suffix)
	}
	// Multiple wildcards in inconsistent positions — literal match
	return symName == pattern
}

var searchSkipDirs = []string{".git", "node_modules", "vendor", "bin", "dist", ".dfmc", "__pycache__", ".venv", ".idea", ".vscode"}

func collectSearchFiles(projectRoot, langFilter string) []string {
	exts := extensionsForLang(langFilter)
	var files []string
	filepath.Walk(projectRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		if info.IsDir() {
			for _, d := range searchSkipDirs {
				if info.Name() == d {
					return filepath.SkipDir
				}
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if !slices.Contains(exts, ext) {
			return nil
		}
		files = append(files, path)
		return nil
	})
	return files
}

func extensionsForLang(lang string) []string {
	switch lang {
	case "go":
		return []string{".go"}
	case "typescript", "ts":
		return []string{".ts", ".tsx"}
	case "javascript", "js":
		return []string{".js", ".jsx", ".mjs", ".cjs"}
	case "python", "py":
		return []string{".py"}
	case "rust", "rs":
		return []string{".rs"}
	case "java":
		return []string{".java"}
	case "all":
		return []string{".go", ".ts", ".tsx", ".js", ".jsx", ".py", ".rs", ".java"}
	default:
		return []string{".go"}
	}
}
