package tools

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/dontfuckmycode/dfmc/internal/codemap"
)

// DeadCodeFinder scans for unused exported functions, types, and variables.
type DeadCodeFinder struct {
	engine *Engine
}

func NewDeadCodeTool() *DeadCodeFinder        { return &DeadCodeFinder{} }
func (t *DeadCodeFinder) Name() string        { return "dead_code" }
func (t *DeadCodeFinder) SetEngine(e *Engine) { t.engine = e }

func (t *DeadCodeFinder) Description() string {
	return "Find unused exported symbols (functions, types, variables) using call-graph analysis."
}

func (t *DeadCodeFinder) Spec() ToolSpec {
	return ToolSpec{
		Name:    "dead_code",
		Title:   "Dead code finder",
		Summary: "Detect unused exported symbols to reduce technical debt.",
		Purpose: "Use when you want to identify code that can be safely removed. It finds exported symbols with zero callers.",
		Risk:    RiskRead,
		Tags:    []string{"quality", "refactor", "dead-code", "cleanup"},
		Args: []Arg{
			{Name: "path", Type: ArgString, Description: "Directory or file to scan (default: project root)."},
			{Name: "kind", Type: ArgString, Description: "Filter by kind: function, type, var, const, or all.", Default: "all"},
		},
		Idempotent: true,
	}
}

type deadCodeResult struct {
	Path  string `json:"path"`
	Line  int    `json:"line"`
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Type  string `json:"type,omitempty"`
	Users int    `json:"users"` // how many places use this
}

// Execute walks the project tree collecting exported symbols,
// then cross-references them against the call graph to find orphans.
func (t *DeadCodeFinder) Execute(ctx context.Context, req Request) (Result, error) {
	targetPath := asString(req.Params, "path", ".")
	kindFilter := strings.ToLower(asString(req.Params, "kind", "all"))
	displayRoot := req.ProjectRoot

	// Load codemap if available
	var callers map[string]map[string]bool // symbolName -> set of callers
	if t.engine != nil {
		if cm := t.engine.codemap; cm != nil {
			callers = t.buildCallerMap(cm.Graph())
		}
	}

	var allFindings []deadCodeResult
	var mu sync.Mutex
	var wg sync.WaitGroup

	walkFn := func(path string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		// Skip generated / test / vendor
		base := filepath.Base(path)
		if strings.HasSuffix(base, "_test.go") || strings.HasPrefix(base, "gen_") || strings.HasPrefix(base, "zzz_") {
			return nil
		}

		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			findings := t.scanFile(p, callers, kindFilter)
			if len(findings) > 0 {
				mu.Lock()
				allFindings = append(allFindings, findings...)
				mu.Unlock()
			}
		}(path)
		return nil
	}

	if err := filepath.Walk(targetPath, walkFn); err != nil && !os.IsNotExist(err) {
		return Result{}, fmt.Errorf("dead_code: walk failed: %w", err)
	}
	wg.Wait()

	// Sort: most users (most dead) first
	sort.Slice(allFindings, func(i, j int) bool {
		if allFindings[i].Users != allFindings[j].Users {
			return allFindings[i].Users < allFindings[j].Users
		}
		return allFindings[i].Path < allFindings[j].Path
	})

	// Build markdown output
	var sb strings.Builder
	if len(allFindings) == 0 {
		sb.WriteString("✅ No unused exported symbols found.\n")
	} else {
		fmt.Fprintf(&sb, "Found %d unused exported symbol(s):\n\n", len(allFindings))
		sb.WriteString("| File | Line | Symbol | Kind | Type | Callers |\n")
		sb.WriteString("|------|------|--------|------|------|--------|\n")
		for _, f := range allFindings {
			fmt.Fprintf(&sb, "| %s | %d | `%s` | %s | %s | %d |\n",
				relPath(displayRoot, f.Path), f.Line, f.Name, f.Kind, f.Type, f.Users)
		}
		sb.WriteString("\n**Recommendation:** Review each symbol. If no external usage exists, remove or unexport (use lowercase).\n")
	}

	// Attach structured data
	meta := map[string]any{
		"total_found": len(allFindings),
	}
	if len(allFindings) > 0 {
		meta["items"] = allFindings
	}

	return Result{Output: sb.String(), Data: meta}, nil
}

func (t *DeadCodeFinder) scanFile(path string, callers map[string]map[string]bool, kindFilter string) []deadCodeResult {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		return nil
	}

	var results []deadCodeResult

	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if !d.Name.IsExported() {
				continue
			}
			if kindFilter != "all" && kindFilter != "function" {
				continue
			}
			sig := typeStringFunc(d.Type)
			users := countCallers(d.Name.Name, callers)
			if users == 0 {
				results = append(results, deadCodeResult{
					Path:  path,
					Line:  fset.Position(d.Pos()).Line,
					Name:  d.Name.Name,
					Kind:  "function",
					Type:  sig,
					Users: users,
				})
			}

		case *ast.GenDecl:
			if d.Tok != token.VAR && d.Tok != token.CONST {
				continue
			}
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.ValueSpec:
					for i, name := range s.Names {
						if !name.IsExported() {
							continue
						}
						if kindFilter != "all" && kindFilter != strings.ToLower(d.Tok.String()) {
							continue
						}
						users := countCallers(name.Name, callers)
						typeStr := ""
						if i == 0 && s.Type != nil {
							typeStr = deadCodeTypeString(s.Type)
						}
						if users == 0 {
							results = append(results, deadCodeResult{
								Path:  path,
								Line:  fset.Position(s.Pos()).Line,
								Name:  name.Name,
								Kind:  strings.ToLower(d.Tok.String()),
								Type:  typeStr,
								Users: users,
							})
						}
					}
				}
			}
		}
	}

	// Scan type declarations
	for _, decl := range f.Decls {
		if ts, ok := decl.(*ast.GenDecl); ok && ts.Tok == token.TYPE {
			for _, spec := range ts.Specs {
				if tspec, ok := spec.(*ast.TypeSpec); ok {
					if !tspec.Name.IsExported() {
						continue
					}
					if kindFilter != "all" && kindFilter != "type" {
						continue
					}
					users := countCallers(tspec.Name.Name, callers)
					typeStr := typeString(tspec.Type)
					if users == 0 {
						results = append(results, deadCodeResult{
							Path:  path,
							Line:  fset.Position(tspec.Pos()).Line,
							Name:  tspec.Name.Name,
							Kind:  "type",
							Type:  typeStr,
							Users: users,
						})
					}
				}
			}
		}
	}

	return results
}

// buildCallerMap creates a map from symbol name → callers from codemap graph.
func (t *DeadCodeFinder) buildCallerMap(g *codemap.Graph) map[string]map[string]bool {
	result := map[string]map[string]bool{}
	if g == nil {
		return result
	}
	for _, e := range g.Edges() {
		if e.Type == "calls" {
			callee := e.To
			if n, ok := g.GetNode(e.To); ok && n.Name != "" {
				callee = n.Name
			}
			if result[callee] == nil {
				result[callee] = map[string]bool{}
			}
			result[callee][e.From] = true
		}
	}
	return result
}

// countCallers returns how many callers reference the given symbol.
func countCallers(name string, callers map[string]map[string]bool) int {
	if callers == nil {
		return -1 // unknown
	}
	return len(callers[name])
}

func deadCodeTypeString(n ast.Node) string {
	if n == nil {
		return ""
	}
	switch t := n.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		if x, ok := t.X.(*ast.Ident); ok {
			return x.Name + "." + t.Sel.Name
		}
	case *ast.StarExpr:
		return "*" + deadCodeTypeString(t.X)
	case *ast.ArrayType:
		return "[]" + deadCodeTypeString(t.Elt)
	case *ast.MapType:
		return "map[" + deadCodeTypeString(t.Key) + "]" + deadCodeTypeString(t.Value)
	}
	return "any"
}

func typeStringFunc(fn *ast.FuncType) string {
	if fn == nil {
		return "()"
	}
	var parts []string
	for _, p := range fn.Params.List {
		parts = append(parts, deadCodeTypeString(p.Type))
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// relPath returns p relative to root using forward slashes for stable
// markdown output. Falls back to filepath.Base(p) if Rel fails.
func relPath(root, p string) string {
	if root == "" {
		return filepath.Base(p)
	}
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return filepath.Base(p)
	}
	return filepath.ToSlash(rel)
}

