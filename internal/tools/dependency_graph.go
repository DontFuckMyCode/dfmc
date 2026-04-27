// dependency_graph_tool.go — Phase 7 tool surface expansion.
// Exposes the codemap's import/call graph as a structured tool so the
// model can reason about impact, fan-out, and coupling without
// running grep over the entire codebase.

package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/codemap"
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

func (t *DependencyGraphTool) Name() string    { return "dependency_graph" }
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
	Nodes    []depNode  `json:"nodes"`
	Edges    []depEdge  `json:"edges"`
	Summary  string     `json:"summary"`
	Cycles   [][]string `json:"cycles,omitempty"`
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

func (t *DependencyGraphTool) queryImporters(g *codemap.Graph, module, edgeType string, max int) depResult {
	if module == "" {
		return depResult{Summary: "query requires module"}
	}
	modID := "module:" + module
	var edges []depEdge
	nodes := make(map[string]depNode)

	for _, e := range g.Edges() {
		if e.Type != "imports" {
			continue
		}
		if edgeType != "" && e.Type != edgeType {
			continue
		}
		if e.To == modID || strings.HasSuffix(e.To, ":"+module) {
			nodes[e.From] = depNode{ID: e.From, Name: filepath.Base(e.From), Kind: "file", Path: e.From}
			edges = append(edges, depEdge{From: e.From, To: e.To, Type: e.Type})
		}
	}

	if len(edges) > max {
		edges = edges[:max]
	}
	return depResult{
		Nodes:   mapValues(nodes),
		Edges:   edges,
		Summary: fmt.Sprintf("importers of %s: %d files", module, len(edges)),
	}
}

func (t *DependencyGraphTool) queryCallers(g *codemap.Graph, symbol string, max int) depResult {
	if symbol == "" {
		return depResult{Summary: "query requires symbol"}
	}
	symLower := strings.ToLower(symbol)
	var edges []depEdge
	nodes := make(map[string]depNode)

	for _, e := range g.Edges() {
		if e.Type != "calls" {
			continue
		}
		if strings.Contains(strings.ToLower(e.To), symLower) {
			nodes[e.From] = depNode{ID: e.From, Name: e.From, Kind: "symbol"}
			edges = append(edges, depEdge{From: e.From, To: e.To, Type: e.Type})
		}
	}

	if len(edges) > max {
		edges = edges[:max]
	}
	return depResult{
		Nodes:   mapValues(nodes),
		Edges:   edges,
		Summary: fmt.Sprintf("callers of %s: %d edges", symbol, len(edges)),
	}
}

func (t *DependencyGraphTool) queryImports(g *codemap.Graph, file, edgeType string, max int) depResult {
	if file == "" {
		return depResult{Summary: "query requires file"}
	}
	p := "file:" + filepath.ToSlash(file)
	var edges []depEdge
	nodes := make(map[string]depNode)
	nodes[p] = depNode{ID: p, Name: filepath.Base(file), Kind: "file", Path: file}

	for _, e := range g.Edges() {
		if e.From != p {
			continue
		}
		if edgeType != "" && e.Type != edgeType {
			continue
		}
		if e.Type == "imports" || e.Type == "calls" || e.Type == "defines" {
			edges = append(edges, depEdge{From: e.From, To: e.To, Type: e.Type})
			nodes[e.To] = depNode{ID: e.To, Name: e.To, Kind: e.Type}
		}
	}

	if len(edges) > max {
		edges = edges[:max]
	}
	return depResult{
		Nodes:   mapValues(nodes),
		Edges:   edges,
		Summary: fmt.Sprintf("%s imports %d modules/symbols", file, len(edges)),
	}
}

func (t *DependencyGraphTool) queryFanOut(g *codemap.Graph, file string, depth, max int) depResult {
	if file == "" {
		return depResult{Summary: "query requires file"}
	}
	start := "file:" + filepath.ToSlash(file)
	visited := map[string]bool{}
	nodes := map[string]depNode{}
	var edges []depEdge

	// BFS traversal up to depth levels, collecting "calls" and "imports" edges
	type queueItem struct {
		id     string
		level  int
	}
	queue := []queueItem{{id: start, level: 0}}
	visited[start] = true
	nodes[start] = depNode{ID: start, Name: filepath.Base(file), Kind: "file", Path: file}

	for len(queue) > 0 && len(edges) < max {
		item := queue[0]
		queue = queue[1:]
		if item.level >= depth {
			continue
		}
		for _, e := range g.Edges() {
			if e.From != item.id {
				continue
			}
			if e.Type != "calls" && e.Type != "imports" {
				continue
			}
			edges = append(edges, depEdge{From: e.From, To: e.To, Type: e.Type})
			if !visited[e.To] {
				visited[e.To] = true
				nodes[e.To] = depNode{ID: e.To, Name: e.To, Kind: e.Type}
				if item.level+1 < depth {
					queue = append(queue, queueItem{id: e.To, level: item.level + 1})
				}
			}
		}
	}

	return depResult{
		Nodes:   mapValues(nodes),
		Edges:   edges,
		Summary: fmt.Sprintf("fan_out from %s: %d nodes, %d edges", file, len(nodes), len(edges)),
	}
}

func (t *DependencyGraphTool) queryFanIn(g *codemap.Graph, file string, depth, max int) depResult {
	if file == "" {
		return depResult{Summary: "query requires file"}
	}
	start := "file:" + filepath.ToSlash(file)
	visited := map[string]bool{}
	nodes := map[string]depNode{}
	var edges []depEdge

	type queueItem struct {
		id    string
		level int
	}
	queue := []queueItem{{id: start, level: 0}}
	visited[start] = true
	nodes[start] = depNode{ID: start, Name: filepath.Base(file), Kind: "file", Path: file}

	for len(queue) > 0 && len(edges) < max {
		item := queue[0]
		queue = queue[1:]
		if item.level >= depth {
			continue
		}
		// Incoming edges to item.id
		for _, e := range g.Edges() {
			if e.To != item.id {
				continue
			}
			if e.Type != "calls" && e.Type != "imports" {
				continue
			}
			edges = append(edges, depEdge{From: e.From, To: e.To, Type: e.Type})
			if !visited[e.From] {
				visited[e.From] = true
				nodes[e.From] = depNode{ID: e.From, Name: e.From, Kind: e.Type}
				if item.level+1 < depth {
					queue = append(queue, queueItem{id: e.From, level: item.level + 1})
				}
			}
		}
	}

	return depResult{
		Nodes:   mapValues(nodes),
		Edges:   edges,
		Summary: fmt.Sprintf("fan_in to %s: %d nodes, %d edges", file, len(nodes), len(edges)),
	}
}

func (t *DependencyGraphTool) queryPath(g *codemap.Graph, fileA, fileB, edgeType string, max int) depResult {
	_ = max // safety cap on BFS is hard-coded to 10 levels
	if fileA == "" || fileB == "" {
		return depResult{Summary: "path query requires file and module or fileA/fileB"}
	}
	start := "file:" + filepath.ToSlash(fileA)
	end := "file:" + filepath.ToSlash(fileB)
	if fileB != "" && !strings.Contains(fileB, ":") && !strings.HasPrefix(fileB, "module:") {
		end = "file:" + filepath.ToSlash(fileB)
	} else if fileB != "" {
		end = fileB
	}

	visited := map[string]bool{}
	parent := map[string]string{}
	var pathEdges []depEdge
	found := false

	type queueItem struct {
		id    string
		level int
	}
	queue := []queueItem{{id: start, level: 0}}
	visited[start] = true

	for len(queue) > 0 && !found {
		item := queue[0]
		queue = queue[1:]
		if item.level > 10 { // safety cap
			continue
		}
		for _, e := range g.Edges() {
			if e.From != item.id {
				continue
			}
			if edgeType != "" && e.Type != edgeType {
				continue
			}
			pathEdges = append(pathEdges, depEdge{From: e.From, To: e.To, Type: e.Type})
			if !visited[e.To] {
				visited[e.To] = true
				parent[e.To] = e.From
				if e.To == end {
					found = true
					break
				}
				queue = append(queue, queueItem{id: e.To, level: item.level + 1})
			}
		}
	}

	nodes := make(map[string]depNode)
	for _, n := range g.Nodes() {
		if visited[n.ID] {
			nodes[n.ID] = depNode{ID: n.ID, Name: n.Name, Kind: n.Kind, Path: n.Path, Language: n.Language}
		}
	}

	var cycles [][]string
	if found {
		// reconstruct path
		path := []string{}
		for cur := end; cur != ""; cur = parent[cur] {
			path = append([]string{cur}, path...)
			if cur == start {
				break
			}
		}
		if len(path) > 0 {
			cycles = append(cycles, path)
		}
	}

	return depResult{
		Nodes:   mapValues(nodes),
		Edges:   pathEdges,
		Cycles:  cycles,
		Summary: fmt.Sprintf("path from %s to %s: %s", fileA, fileB, map[bool]string{found: "found", false: "not found"}[found]),
	}
}

func mapValues(m map[string]depNode) []depNode {
	out := make([]depNode, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}