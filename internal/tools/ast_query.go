package tools

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/ast"
)

// ASTQueryTool parses a file and returns its symbol outline — names, kinds,
// signatures, line numbers. Useful when the model wants structured code
// information without reading the whole file.
//
// Holds a lazily-initialized ast.Engine. It does not share cache with the
// main engine's ast.Engine, but the parse cache is per-instance and
// content-hashed, so repeated calls on the same file are cheap.
type ASTQueryTool struct {
	engine *ast.Engine
}

func NewASTQueryTool() *ASTQueryTool { return &ASTQueryTool{engine: ast.New()} }
func (t *ASTQueryTool) Name() string { return "ast_query" }
func (t *ASTQueryTool) Description() string {
	return "Parse a file and return its symbols, imports, and language."
}
func (t *ASTQueryTool) Close() error {
	if t == nil || t.engine == nil {
		return nil
	}
	return t.engine.Close()
}

func (t *ASTQueryTool) getEngine() *ast.Engine { return t.engine }

func (t *ASTQueryTool) Execute(ctx context.Context, req Request) (Result, error) {
	path := strings.TrimSpace(asString(req.Params, "path", ""))
	if path == "" {
		return Result{}, missingParamError("ast_query", "path", req.Params,
			`{"path":"internal/engine/engine.go"} or {"path":"main.go","kind":"function"}`,
			`ast_query parses ONE file at a time — name a single source file in "path".`)
	}
	abs, err := EnsureWithinRoot(req.ProjectRoot, path)
	if err != nil {
		return Result{}, err
	}
	// Reject directory paths up-front with a tool-shaped suggestion. The
	// 2026-04-18 screenshot caught the model passing
	// `internal/tools` here and getting Go's bare "read file ...
	// Access is denied." back — opaque, so it tried it again on a sibling
	// directory. ast_query parses ONE file; for a whole tree the model
	// should glob first then ast_query each match.
	if info, statErr := os.Stat(abs); statErr == nil && info.IsDir() {
		return Result{}, fmt.Errorf(
			"ast_query needs a FILE path, not a directory (%q is a folder). "+
				"Use glob first to discover files (e.g. {\"pattern\":\"%s/**/*.go\"}), "+
				"then call ast_query on each match. For a quick directory listing use list_dir.",
			path, strings.TrimRight(path, "/\\"))
	}
	result, err := t.getEngine().ParseFile(ctx, abs)
	if err != nil {
		return Result{}, fmt.Errorf("parse %s: %w", path, err)
	}

	kindFilter := strings.ToLower(strings.TrimSpace(asString(req.Params, "kind", "")))
	nameFilter := strings.ToLower(strings.TrimSpace(asString(req.Params, "name_contains", "")))

	symbols := make([]map[string]any, 0, len(result.Symbols))
	for _, s := range result.Symbols {
		if kindFilter != "" && !strings.EqualFold(string(s.Kind), kindFilter) {
			continue
		}
		if nameFilter != "" && !strings.Contains(strings.ToLower(s.Name), nameFilter) {
			continue
		}
		entry := map[string]any{
			"name":     s.Name,
			"kind":     string(s.Kind),
			"line":     s.Line,
			"language": s.Language,
		}
		if s.Signature != "" {
			entry["signature"] = s.Signature
		}
		if s.Visibility != "" {
			entry["visibility"] = s.Visibility
		}
		symbols = append(symbols, entry)
	}
	sort.SliceStable(symbols, func(i, j int) bool {
		li, _ := symbols[i]["line"].(int)
		lj, _ := symbols[j]["line"].(int)
		return li < lj
	})

	var lines []string
	for _, s := range symbols {
		sig := ""
		if v, ok := s["signature"].(string); ok && v != "" {
			sig = " " + v
		}
		lines = append(lines, fmt.Sprintf("%4d  %-12s %s%s", s["line"], s["kind"], s["name"], sig))
	}
	if len(lines) == 0 {
		lines = append(lines, "(no symbols matched)")
	}

	imports := append([]string(nil), result.Imports...)

	return Result{
		Output: strings.Join(lines, "\n"),
		Data: map[string]any{
			"path":     PathRelativeToRoot(req.ProjectRoot, abs),
			"language": result.Language,
			"symbols":  symbols,
			"imports":  imports,
			"errors":   result.Errors,
			"count":    len(symbols),
		},
	}, nil
}
