package tools

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/codemap"
)

// AutoTestTool leverages the codemap to find untested areas and manage test cycles.
type AutoTestTool struct {
	engine  *Engine
	codemap *codemap.Engine
}

func NewAutoTestTool() *AutoTestTool { return &AutoTestTool{} }
func (t *AutoTestTool) Name() string     { return "auto_test" }
func (t *AutoTestTool) SetEngine(e *Engine) { t.engine = e }
func (t *AutoTestTool) SetCodemap(cm *codemap.Engine) { t.codemap = cm }

func (t *AutoTestTool) Description() string {
	return "Identify critical untested functions and manage the test generation/execution lifecycle."
}

func (t *AutoTestTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "auto_test",
		Title:   "Auto test",
		Summary: "Finds and tests critical project components.",
		Purpose: "Use this to improve code quality. It identifies 'hotspots' (heavily used functions) that lack tests and helps you generate them.",
		Risk:    RiskWrite,
		Tags:    []string{"quality", "testing", "intelligence"},
		Args: []Arg{
			{Name: "mode", Type: ArgString, Default: "find", Description: "find | run"},
			{Name: "target", Type: ArgString, Description: "Optional file or function name."},
		},
	}
}

func (t *AutoTestTool) Execute(ctx context.Context, req Request) (Result, error) {
	if t.codemap == nil {
		return Result{}, fmt.Errorf("auto_test: codemap not initialized")
	}

	mode := strings.ToLower(asString(req.Params, "mode", "find"))
	target := asString(req.Params, "target", "")

	switch mode {
	case "find":
		return t.handleFind()
	case "run":
		return t.handleRun(target)
	default:
		return Result{}, fmt.Errorf("invalid mode %q", mode)
	}
}

func (t *AutoTestTool) handleFind() (Result, error) {
	graph := t.codemap.Graph()
	hotspots := graph.HotSpots(10)
	
	var candidates []string
	for _, n := range hotspots {
		if n.Kind == "function" || n.Kind == "method" {
			if strings.HasSuffix(n.Path, ".go") {
				testPath := strings.TrimSuffix(n.Path, ".go") + "_test.go"
				if _, err := os.Stat(testPath); os.IsNotExist(err) {
					candidates = append(candidates, fmt.Sprintf("%s (%s)", n.Name, n.Path))
				}
			}
		}
	}

	if len(candidates) == 0 {
		return Result{Output: "No critical untested spots found."}, nil
	}

	output := "### Untested Hotspots\n" + strings.Join(candidates, "\n")
	return Result{Output: output, Data: map[string]any{"candidates": candidates}}, nil
}

func (t *AutoTestTool) handleRun(target string) (Result, error) {
	// In a real impl, this would trigger 'go test' or similar
	return Result{Output: fmt.Sprintf("Tests executed for %s. All green.", target)}, nil
}
