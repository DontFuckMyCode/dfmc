package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

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
	once   sync.Once
	engine *ast.Engine
}

func NewASTQueryTool() *ASTQueryTool        { return &ASTQueryTool{} }
func (t *ASTQueryTool) Name() string        { return "ast_query" }
func (t *ASTQueryTool) Description() string { return "Parse a file and return its symbols, imports, and language." }

func (t *ASTQueryTool) getEngine() *ast.Engine {
	t.once.Do(func() { t.engine = ast.New() })
	return t.engine
}

func (t *ASTQueryTool) Execute(ctx context.Context, req Request) (Result, error) {
	path := strings.TrimSpace(asString(req.Params, "path", ""))
	if path == "" {
		return Result{}, fmt.Errorf("path is required")
	}
	abs, err := EnsureWithinRoot(req.ProjectRoot, path)
	if err != nil {
		return Result{}, err
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
			"path":     abs,
			"language": result.Language,
			"symbols":  symbols,
			"imports":  imports,
			"errors":   result.Errors,
			"count":    len(symbols),
		},
	}, nil
}
