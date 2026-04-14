package codemap

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/dontfuckmycode/dfmc/internal/ast"
)

type Engine struct {
	ast   *ast.Engine
	graph *Graph
	mu    sync.RWMutex
}

func New(astEngine *ast.Engine) *Engine {
	return &Engine{
		ast:   astEngine,
		graph: NewGraph(),
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

	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}

		result, err := e.ast.ParseFile(ctx, path)
		if err != nil {
			continue
		}

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

	return nil
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
