package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/codemap"
)

type CallGraphTool struct {
	codemap *codemap.Engine
}

func NewCallGraphTool() *CallGraphTool { return &CallGraphTool{} }
func (t *CallGraphTool) Name() string   { return "call_graph" }

func (t *CallGraphTool) SetCodemap(cm *codemap.Engine) {
	t.codemap = cm
}

func (t *CallGraphTool) Description() string {
	return "Explore function/method call relationships (callers and callees) using the project codemap."
}

func (t *CallGraphTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "call_graph",
		Title:   "Call graph",
		Summary: "Find who calls a function and what a function calls.",
		Purpose: "Use when you need to understand the impact of changing a function, or to find where a specific logic is triggered. It helps navigate the codebase by following the actual execution flow.",
		Prompt: `Navigates the project's call graph.
Args:
- symbol (required): The name of the function or method to inspect.
- direction (optional, default="both"): "callers" (who calls this), "callees" (what does this call), or "both".
- depth (optional, default=1): How many levels to traverse.

Returns a tree-like view of the relationships.`,
		Risk: RiskRead,
		Tags: []string{"navigation", "intelligence", "call-graph", "impact-analysis"},
		Args: []Arg{
			{Name: "symbol", Type: ArgString, Required: true, Description: "Function or method name."},
			{Name: "direction", Type: ArgString, Default: "both", Description: "callers | callees | both"},
			{Name: "depth", Type: ArgInteger, Default: 1, Description: "Traversals depth (1-3)."},
		},
		Idempotent: true,
	}
}

func (t *CallGraphTool) Execute(ctx context.Context, req Request) (Result, error) {
	if t.codemap == nil {
		return Result{}, fmt.Errorf("call_graph: codemap engine not initialized")
	}

	symbol := strings.TrimSpace(asString(req.Params, "symbol", ""))
	if symbol == "" {
		return Result{}, fmt.Errorf("call_graph: symbol is required")
	}

	direction := strings.ToLower(asString(req.Params, "direction", "both"))
	depth := asInt(req.Params, "depth", 1)
	if depth < 1 {
		depth = 1
	}
	if depth > 3 {
		depth = 3
	}

	graph := t.codemap.Graph()
	// Find nodes matching the symbol name
	var targets []codemap.Node
	for _, n := range graph.Nodes() {
		if strings.EqualFold(n.Name, symbol) && (n.Kind == "function" || n.Kind == "method") {
			targets = append(targets, n)
		}
	}

	if len(targets) == 0 {
		return Result{
			Output: fmt.Sprintf("No function or method found named %q", symbol),
		}, nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Call graph for %q:\n", symbol)

	for _, target := range targets {
		fmt.Fprintf(&sb, "\n### Symbol: %s (%s) in %s\n", target.Name, target.Kind, target.Path)

		if direction == "callers" || direction == "both" {
			sb.WriteString("  Callers (who calls this):\n")
			t.renderEdges(&sb, graph, target.ID, "calls", true, depth, 0)
		}

		if direction == "callees" || direction == "both" {
			sb.WriteString("  Callees (what this calls):\n")
			t.renderEdges(&sb, graph, target.ID, "calls", false, depth, 0)
		}
	}

	return Result{
		Output: sb.String(),
	}, nil
}

func (t *CallGraphTool) renderEdges(sb *strings.Builder, graph *codemap.Graph, nodeID string, edgeType string, incoming bool, maxDepth, currentDepth int) {
	if currentDepth >= maxDepth {
		return
	}

	var edges []codemap.Edge
	allEdges := graph.Edges()
	for _, e := range allEdges {
		if e.Type != edgeType {
			continue
		}
		if incoming && e.To == nodeID {
			edges = append(edges, e)
		} else if !incoming && e.From == nodeID {
			edges = append(edges, e)
		}
	}

	if len(edges) == 0 && currentDepth == 0 {
		sb.WriteString("    (none found)\n")
		return
	}

	// Sort for stability
	sort.Slice(edges, func(i, j int) bool {
		if incoming {
			return edges[i].From < edges[j].From
		}
		return edges[i].To < edges[j].To
	})

	indent := strings.Repeat("  ", currentDepth+2)
	for _, e := range edges {
		otherID := e.From
		if !incoming {
			otherID = e.To
		}

		other, ok := graph.GetNode(otherID)
		if !ok {
			continue
		}

		fmt.Fprintf(sb, "%s- %s (%s) [%s]\n", indent, other.Name, other.Kind, other.Path)
		t.renderEdges(sb, graph, otherID, edgeType, incoming, maxDepth, currentDepth+1)
	}
}
