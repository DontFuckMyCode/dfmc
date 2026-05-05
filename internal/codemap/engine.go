package codemap

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/ast"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// Engine wires the AST parser into the codemap graph. The graph itself
// is the synchronization boundary — *Graph carries its own RWMutex and
// is the only mutable state here. Engine fields (ast, graph, stats) are
// set once in New and never reassigned, so Engine itself needs no lock.
type Engine struct {
	ast   *ast.Engine
	graph *Graph
	stats *buildMetricsTracker
}

func New(astEngine *ast.Engine) *Engine {
	return &Engine{
		ast:   astEngine,
		graph: NewGraph(),
		stats: newBuildMetricsTracker(),
	}
}

func (e *Engine) Graph() *Graph {
	return e.graph
}

// BuildFromFiles parses each path with the AST engine and populates
// the codemap graph. OnProgress (if provided) is called every ~50 files
// so the caller can publish progress events and check for context
// cancellation — without this hook, cancellation is only detected between
// files, causing multi-second delay on large projects.
func (e *Engine) BuildFromFiles(ctx context.Context, paths []string, onProgress ...func(processed, total int)) error {
	callback := func(processed, total int) {}
	if len(onProgress) > 0 && onProgress[0] != nil {
		callback = onProgress[0]
	}
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
	total := len(paths)

	for i, path := range paths {
		// Cancellation must be checked every iteration, not only on the
		// progress-callback boundary below — a run that's overwhelmingly
		// parse-failures (or all empty paths) never increments
		// `processed`, so the old `processed%50 == 0` gate could leave
		// ctx unchecked for an entire batch. ctx.Err() is a single
		// atomic load; the cost is negligible compared to ParseFile.
		if err := ctx.Err(); err != nil {
			return err
		}
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

		filePath := filepath.ToSlash(path)
		fileNodeID := "file:" + filePath
		nodes := []Node{{
			ID:       fileNodeID,
			Name:     filepath.Base(path),
			Path:     filePath,
			Kind:     "file",
			Language: result.Language,
		}}
		edges := make([]Edge, 0, len(result.Imports)+len(result.Symbols)*2)

		for _, imp := range result.Imports {
			impID := "module:" + imp
			nodes = append(nodes, Node{
				ID:   impID,
				Name: imp,
				Kind: "module",
			})
			edges = append(edges, Edge{
				From: fileNodeID,
				To:   impID,
				Type: "imports",
			})
		}

		typeNodes := make(map[string]string)
		for _, sym := range result.Symbols {
			switch sym.Kind {
			case types.SymbolClass, types.SymbolInterface, types.SymbolType:
				if name := strings.ToLower(strings.TrimSpace(sym.Name)); name != "" {
					typeNodes[name] = fmt.Sprintf("sym:%s:%s:%d", filePath, sym.Name, sym.Line)
				}
			}
		}

		for _, sym := range result.Symbols {
			symID := fmt.Sprintf("sym:%s:%s:%d", filePath, sym.Name, sym.Line)
			node := Node{
				ID:       symID,
				Name:     sym.Name,
				Path:     filePath,
				Kind:     string(sym.Kind),
				Language: sym.Language,
				Meta:     sym.Metadata,
			}
			nodes = append(nodes, node)
			edges = append(edges, Edge{
				From: fileNodeID,
				To:   symID,
				Type: "defines",
			})
			// Method→type ownership edge: a method declared on a receiver type
			// gets a "method_of" edge to the type symbol found in the same file.
			if sym.Kind == types.SymbolMethod && sym.Metadata != nil {
				if receiver := sym.Metadata["receiver"]; receiver != "" {
					if typeNode := typeNodes[receiverTypeName(receiver)]; typeNode != "" {
						edges = append(edges, Edge{
							From: symID,
							To:   typeNode,
							Type: "method_of",
						})
					}
				}
			}
		}
		e.graph.AddNodesWithEdges(nodes, edges)

		// Progress callback every 50 processed files, plus the final
		// iteration. Cancellation is already polled at the top of the
		// loop so we don't need to re-check here.
		if processed%50 == 0 || i == total-1 {
			callback(processed, total)
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

// InvalidateFile removes a file and all its symbol nodes from the codemap.
// Subsequent context builds for this file will re-parse it from scratch.
func (e *Engine) InvalidateFile(path string) {
	if e == nil || e.graph == nil {
		return
	}
	p := filepath.ToSlash(path)
	e.graph.RemoveNode("file:" + p)

	prefix := fmt.Sprintf("sym:%s:", p)
	for _, n := range e.graph.Nodes() {
		if strings.HasPrefix(n.ID, prefix) {
			e.graph.RemoveNode(n.ID)
		}
	}
}

// findTypeNodeForReceiver locates the type symbol (class/interface/type)
// whose name matches the receiver string within the same file. This wires
// method→type ownership edges ("method_of") so that codemap queries like
// "what methods does *Server own?" can traverse the graph directly.
func findTypeNodeForReceiver(graph *Graph, receiver, filePath string) string {
	rec := receiverTypeName(receiver)
	if rec == "" {
		return ""
	}
	var candidates []string
	for _, n := range graph.Nodes() {
		if n.Path != filePath {
			continue
		}
		switch n.Kind {
		case "class", "interface", "type":
			if strings.ToLower(n.Name) == rec {
				candidates = append(candidates, n.ID)
			}
		}
	}
	if len(candidates) == 0 {
		return ""
	}
	if len(candidates) == 1 {
		return candidates[0]
	}
	for _, id := range candidates {
		if id == rec {
			return id
		}
	}
	return candidates[0]
}

func receiverTypeName(receiver string) string {
	rec := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(receiver), "*"), "&")
	rec = strings.TrimLeft(rec, "() ")
	return strings.ToLower(strings.TrimSpace(rec))
}
