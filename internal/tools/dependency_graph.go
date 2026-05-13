// dependency_graph_tool.go — Phase 7 tool surface expansion.
// Exposes the codemap's import/call graph as a structured tool so the
// model can reason about impact, fan-out, and coupling without
// running grep over the entire codebase.

package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// DependencyGraphTool surfaces module and symbol dependency edges from
// the codemap. It is NOT a codemap replacement — it focuses purely on
// WHO imports/whom and WHAT calls what, using the edges already built
// during the codemap's BuildFromFiles pass.
type DependencyGraphTool struct {
	engine *Engine // tools Engine, set at registration; codemap read at Execute time
}

func NewDependencyGraphTool() *DependencyGraphTool {
	return &DependencyGraphTool{}
}

// SetEngine stores the tools Engine reference. The codemap field is
// re-read from engine.codemap at Execute time (after SetCodemap has
// been called by engine.Init), avoiding a circular import.
func (t *DependencyGraphTool) SetEngine(eng *Engine) {
	t.engine = eng
}

func (t *DependencyGraphTool) Name() string { return "dependency_graph" }
func (t *DependencyGraphTool) Description() string {
	return "Query the project's import and call dependency graph — find all files that import a module, all callers of a symbol, or the full fan-out from a given file or package."
}

func (t *DependencyGraphTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "dependency_graph",
		Title:   "Dependency graph",
		Summary: "Structured import/call dependency graph for impact analysis.",
		Purpose: `Use when you need to know "what will break if I change X?" or "who depends on this module?" Answers impact and coupling questions without scanning the whole codebase.`,
		Prompt: `Dependency graph query tool. Works on the graph already built by the codemap pass — it does not scan the filesystem.

Query types (specify exactly one):
- "importers": find all files that import a given module path
- "callers": find all symbols that call a given symbol (method/function name)
- "imports": list all modules imported by a given file
- "fan_out": all symbols called by a given file's symbols
- "fan_in": all symbols that call into a given file's symbols
- "path": path of edges from file A to file B (cyclic graphs return the cycle)

Filter args:
- file (optional): scope to a specific file path
- module (optional): scope to a specific module path (imports only)
- symbol (optional): limit to a specific symbol name
- edge_type (optional): "imports" | "calls" | "defines" | "method_of"
- max_depth (optional, default 3): traversal depth for fan_out/fan_in/path
- max_results (optional, default 50): cap on results per query

Output is structured JSON with edges, nodes, and a summary line.`,
		Risk: RiskRead,
		Tags: []string{"dependencies", "impact", "coupling", "imports", "analysis"},
		Args: []Arg{
			{Name: "query", Type: ArgString, Description: `Query type: "importers" | "callers" | "imports" | "fan_out" | "fan_in" | "path". Exactly one required.`},
			{Name: "file", Type: ArgString, Description: `File path to query (e.g. "internal/engine/engine.go").`},
			{Name: "module", Type: ArgString, Description: `Module path to query (e.g. "github.com/foo/bar"). Used as destination for "path" query.`},
			{Name: "symbol", Type: ArgString, Description: `Symbol name to query (e.g. "Authenticate").`},
			{Name: "edge_type", Type: ArgString, Description: `Filter by edge type: "imports" | "calls" | "defines" | "method_of".`},
			{Name: "max_depth", Type: ArgInteger, Default: 3, Description: `Traversal depth for fan_out/fan_in/path queries.`},
			{Name: "max_results", Type: ArgInteger, Default: 50, Description: `Cap on results (ceiling 200).`},
		},
		Returns: "Structured JSON: {nodes: [...], edges: [...], summary: string}",
		Examples: []string{
			`{"query":"importers","module":"github.com/foo/bar"}`,
			`{"query":"fan_out","file":"internal/engine/engine.go","max_depth":2}`,
			`{"query":"callers","symbol":"Authenticate"}`,
		},
		Idempotent: true,
		CostHint:   "cpu-bound",
	}
}

type depResult struct {
	Nodes   []depNode  `json:"nodes"`
	Edges   []depEdge  `json:"edges"`
	Summary string     `json:"summary"`
	Cycles  [][]string `json:"cycles,omitempty"`
}

type depNode struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Kind     string            `json:"kind"`
	Path     string            `json:"path,omitempty"`
	Language string            `json:"language,omitempty"`
	Meta     map[string]string `json:"meta,omitempty"`
}

type depEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Type string `json:"type"`
}

func (t *DependencyGraphTool) Execute(ctx context.Context, req Request) (Result, error) {
	if t.engine == nil || t.engine.codemap == nil {
		return Result{}, fmt.Errorf("codemap engine is not initialized")
	}

	query := strings.TrimSpace(asString(req.Params, "query", ""))
	file := strings.TrimSpace(asString(req.Params, "file", ""))
	module := strings.TrimSpace(asString(req.Params, "module", ""))
	symbol := strings.TrimSpace(asString(req.Params, "symbol", ""))
	edgeType := strings.TrimSpace(asString(req.Params, "edge_type", ""))
	maxDepth := asInt(req.Params, "max_depth", 3)
	maxResults := asInt(req.Params, "max_results", 50)
	if maxResults <= 0 {
		maxResults = 50
	}
	if maxResults > 200 {
		maxResults = 200
	}
	if maxDepth <= 0 {
		maxDepth = 3
	}

	switch query {
	case "importers", "callers", "imports", "fan_out", "fan_in", "path":
		// valid
	default:
		return Result{}, fmt.Errorf(`query must be one of: "importers" | "callers" | "imports" | "fan_out" | "fan_in" | "path"`)
	}

	// Validate required params per query type.
	switch query {
	case "importers", "imports":
		if module == "" {
			return Result{}, missingParamError("dependency_graph", "module", req.Params,
				`{"query":"importers","module":"github.com/foo/bar"}`,
				`module is required for query type "`+query+`".`)
		}
	case "callers":
		if symbol == "" {
			return Result{}, missingParamError("dependency_graph", "symbol", req.Params,
				`{"query":"callers","symbol":"Authenticate"}`,
				`symbol is required for query type "callers".`)
		}
	case "fan_out", "fan_in", "path":
		if file == "" {
			return Result{}, missingParamError("dependency_graph", "file", req.Params,
				`{"query":"fan_out","file":"internal/engine/engine.go"}`,
				`file is required for query type "`+query+`".`)
		}
	}

	graph := t.engine.codemap.Graph()
	if graph == nil {
		return Result{}, fmt.Errorf("codemap graph is not built yet — run codemap first")
	}

	var r depResult
	switch query {
	case "importers":
		r = t.queryImporters(graph, module, edgeType, maxResults)
	case "callers":
		r = t.queryCallers(graph, symbol, maxResults)
	case "imports":
		r = t.queryImports(graph, file, edgeType, maxResults)
	case "fan_out":
		r = t.queryFanOut(graph, file, maxDepth, maxResults)
	case "fan_in":
		r = t.queryFanIn(graph, file, maxDepth, maxResults)
	case "path":
		r = t.queryPath(graph, file, module, edgeType, maxResults)
	}

	if len(r.Nodes) > maxResults {
		r.Nodes = r.Nodes[:maxResults]
	}
	if len(r.Edges) > maxResults {
		r.Edges = r.Edges[:maxResults]
	}

	summary := fmt.Sprintf("%s: %d nodes, %d edges", query, len(r.Nodes), len(r.Edges))
	if len(r.Cycles) > 0 {
		summary += fmt.Sprintf(", %d cycle(s)", len(r.Cycles))
	}

	return Result{
		Output: summary,
		Data: map[string]any{
			"nodes":   r.Nodes,
			"edges":   r.Edges,
			"summary": summary,
			"cycles":  r.Cycles,
		},
	}, nil
}

func mapValues(m map[string]depNode) []depNode {
	out := make([]depNode, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
