package tools

// dependency_graph_queries.go — per-query implementations dispatched
// by DependencyGraphTool.Execute. Each query returns a depResult with
// nodes, edges, and a one-line summary; fan_out/fan_in walk the graph
// BFS up to max_depth, path runs a directed BFS with parent links and
// reconstructs the route. Sibling to dependency_graph.go which owns
// the tool surface (Spec, Execute, types) and the mapValues sort.

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/codemap"
)

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
