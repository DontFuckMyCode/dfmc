package codemap

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/ast"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// parseTask represents a single file parsing job.
type parseTask struct {
	path     string
	result   *ast.ParseResult
	parseErr error
}

// buildResult holds the aggregated output from all workers.
type buildResult struct {
	nodes           []Node
	edges           []Edge
	parseErrors     int
	languageCounts  map[string]int64
	directoryCounts map[string]int64
	mu              sync.Mutex // protects aggregated fields
}

// BuildFromFilesParallel parses files using a worker pool for improved
// performance on large projects. The workers parameter controls the number
// of concurrent parsers; if <= 0, it defaults to NumCPU().
func (e *Engine) BuildFromFilesParallel(ctx context.Context, paths []string, workers int, onProgress ...func(processed, total int)) error {
	callback := func(processed, total int) {}
	if len(onProgress) > 0 && onProgress[0] != nil {
		callback = onProgress[0]
	}
	if e.ast == nil {
		return fmt.Errorf("ast engine is nil")
	}

	if workers <= 0 {
		workers = runtime.NumCPU()
	}

	startedAt := time.Now()
	before := e.graph.Counts()
	total := len(paths)

	// Filter empty paths upfront (serial, fast)
	var validPaths []string
	for _, path := range paths {
		if strings.TrimSpace(path) != "" {
			validPaths = append(validPaths, path)
		}
	}
	skipped := total - len(validPaths)

	// Channel-based task distribution
	taskCh := make(chan string, len(validPaths))
	resultCh := make(chan parseTask, workers*2)
	doneCh := make(chan struct{})

	// result aggregates from all workers
	result := &buildResult{
		languageCounts:  make(map[string]int64),
		directoryCounts: make(map[string]int64),
	}

	// Start worker pool
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range taskCh {
				result, err := e.ast.ParseFile(ctx, path)
				resultCh <- parseTask{path: path, result: result, parseErr: err}
			}
		}()
	}

	// Close result channel when all workers are done
	go func() {
		wg.Wait()
		close(resultCh)
		close(doneCh)
	}()

	// Feed paths to workers
	go func() {
		for _, path := range validPaths {
			select {
			case <-ctx.Done():
				return
			case taskCh <- path:
			}
		}
		close(taskCh)
	}()

	// Collect results and progress reporting
	processed := 0
	progressTick := 50 // report every 50 files

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case task, ok := <-resultCh:
			if !ok {
				// All workers finished
				goto aggregate
			}

			// Aggregate result
			result.mu.Lock()
			if task.parseErr != nil {
				result.parseErrors++
			} else {
				processed++
				if lang := strings.TrimSpace(task.result.Language); lang != "" {
					result.languageCounts[lang]++
				}
				dir := filepath.ToSlash(filepath.Dir(task.path))
				result.directoryCounts[dir]++

				// Build graph nodes and edges (same logic as sequential version)
				filePath := filepath.ToSlash(task.path)
				fileNodeID := "file:" + filePath
				nodes := []Node{{
					ID:       fileNodeID,
					Name:     filepath.Base(task.path),
					Path:     filePath,
					Kind:     "file",
					Language: task.result.Language,
				}}
				edges := make([]Edge, 0, len(task.result.Imports)+len(task.result.Symbols)*2)

				for _, imp := range task.result.Imports {
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
				for _, sym := range task.result.Symbols {
					switch sym.Kind {
					case types.SymbolClass, types.SymbolInterface, types.SymbolType:
						if name := strings.ToLower(strings.TrimSpace(sym.Name)); name != "" {
							typeNodes[name] = fmt.Sprintf("sym:%s:%s:%d", filePath, sym.Name, sym.Line)
						}
					}
				}

				for _, sym := range task.result.Symbols {
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

				result.nodes = append(result.nodes, nodes...)
				result.edges = append(result.edges, edges...)
			}
			result.mu.Unlock()

			// Progress callback (throttled)
			if processed%progressTick == 0 {
				callback(processed, total)
			}

		case <-doneCh:
			goto aggregate
		}
	}

aggregate:
	// Add all accumulated nodes/edges to graph in one batch
	// Graph.AddNodesWithEdges is thread-safe via RWMutex
	e.graph.AddNodesWithEdges(result.nodes, result.edges)

	// Record metrics
	after := e.graph.Counts()
	e.stats.recordBuild(BuildSample{
		StartedAt:      startedAt.UTC(),
		DurationMs:     time.Since(startedAt).Milliseconds(),
		FilesRequested: int64(len(paths)),
		FilesProcessed: int64(processed),
		FilesSkipped:   int64(skipped),
		ParseErrors:    int64(result.parseErrors),
		GraphNodes:     int64(after.Nodes),
		GraphEdges:     int64(after.Edges),
		NodesAdded:     int64(after.Nodes - before.Nodes),
		EdgesAdded:     int64(after.Edges - before.Edges),
		Languages:      result.languageCounts,
		Directories:    result.directoryCounts,
	})

	// Final progress callback
	callback(processed, total)

	return nil
}
