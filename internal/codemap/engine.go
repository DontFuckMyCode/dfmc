package codemap

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/ast"
)

type Engine struct {
	ast   *ast.Engine
	graph *Graph
	stats *buildMetricsTracker
	mu    sync.RWMutex
}

func New(astEngine *ast.Engine) *Engine {
	return &Engine{
		ast:   astEngine,
		graph: NewGraph(),
		stats: newBuildMetricsTracker(),
	}
}

func (e *Engine) Graph() *Graph {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.graph
}

func (e *Engine) BuildFromFiles(ctx context.Context, paths []string) error {
	if e.ast == nil {
		return fmt.Errorf("ast engine is nil")
	}

	startedAt := time.Now()
	before := e.graph.Counts()
	processed := 0
	skipped := 0
	parseErrors := 0
	languageCounts := map[string]int64{}
	directoryCounts := map[string]int64{}

	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			skipped++
			continue
		}

		result, err := e.ast.ParseFile(ctx, path)
		if err != nil {
			parseErrors++
			continue
		}
		processed++
		if lang := strings.TrimSpace(result.Language); lang != "" {
			languageCounts[lang]++
		}
		directoryCounts[filepath.ToSlash(filepath.Dir(path))]++

		fileNodeID := "file:" + filepath.ToSlash(path)
		e.graph.AddNode(Node{
			ID:       fileNodeID,
			Name:     filepath.Base(path),
			Path:     filepath.ToSlash(path),
			Kind:     "file",
			Language: result.Language,
		})

		for _, imp := range result.Imports {
			impID := "module:" + imp
			e.graph.AddNode(Node{
				ID:   impID,
				Name: imp,
				Kind: "module",
			})
			e.graph.AddEdge(Edge{
				From: fileNodeID,
				To:   impID,
				Type: "imports",
			})
		}

		for _, sym := range result.Symbols {
			symID := fmt.Sprintf("sym:%s:%s:%d", filepath.ToSlash(path), sym.Name, sym.Line)
			e.graph.AddNode(Node{
				ID:       symID,
				Name:     sym.Name,
				Path:     filepath.ToSlash(path),
				Kind:     string(sym.Kind),
				Language: sym.Language,
			})
			e.graph.AddEdge(Edge{
				From: fileNodeID,
				To:   symID,
				Type: "defines",
			})
		}
	}

	after := e.graph.Counts()
	e.stats.recordBuild(BuildSample{
		StartedAt:      startedAt.UTC(),
		DurationMs:     time.Since(startedAt).Milliseconds(),
		FilesRequested: int64(len(paths)),
		FilesProcessed: int64(processed),
		FilesSkipped:   int64(skipped),
		ParseErrors:    int64(parseErrors),
		GraphNodes:     int64(after.Nodes),
		GraphEdges:     int64(after.Edges),
		NodesAdded:     int64(after.Nodes - before.Nodes),
		EdgesAdded:     int64(after.Edges - before.Edges),
		Languages:      languageCounts,
		Directories:    directoryCounts,
	})

	return nil
}

func (e *Engine) Metrics() BuildMetrics {
	if e == nil || e.stats == nil {
		return BuildMetrics{}
	}
	return e.stats.snapshot()
}

func (e *Engine) FindSymbol(name string) []Node {
	target := strings.TrimSpace(strings.ToLower(name))
	if target == "" {
		return nil
	}

	var out []Node
	for _, n := range e.graph.Nodes() {
		if strings.ToLower(n.Name) == target {
			out = append(out, n)
		}
	}
	return out
}
