package context

// ranking_heuristics.go — codemap-driven score boosts that run during
// StrategyRefactor retrieval. refactorBoost flags two refactoring
// signals: orphan symbols (never called/referenced) and symbols
// participating in import cycles. Both surface their files to the
// LLM so the user gets refactor candidates without asking explicitly.
//
// Pure functions over codemap.Graph — no Manager state, no I/O.

import (
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/codemap"
)

// refactorBoost applies bonus scores to files containing refactoring
// opportunity indicators: orphaned functions (never called/referenced) and
// symbols involved in import/call cycles. These files get surfaced in
// context so the LLM can review them during a StrategyRefactor task.
func refactorBoost(graph *codemap.Graph, scores map[string]float64, sources map[string]string) {
	if graph == nil {
		return
	}

	// 1. Orphan detection: nodes with no incoming edges that are not
	// top-level entry points (main, init). A function/type that has
	// zero callers or importers is a dead code candidate.
	for _, n := range graph.Orphans() {
		// Skip entry points and modules — we care about callables.
		// Also skip test files as orphans there are expected.
		if n.Kind == "file" || n.Kind == "module" {
			continue
		}
		if isLikelyEntryPoint(n.Name) {
			continue
		}
		if strings.Contains(n.Path, "_test.go") {
			continue
		}
		if n.Path != "" {
			scores[n.Path] += 3.0
			if sources[n.Path] == "" {
				sources[n.Path] = "orphan_candidate"
			}
		}
	}

	// 2. Cycle detection: files containing symbols that participate in
	// import/call cycles may need interface extraction or refactoring.
	cycles := findImportCycles(graph)
	for from := range cycles {
		if from == "" {
			continue
		}
		// 'from' is a module node ID like "module:fmt"; we want the file.
		// The edge is from file → module, so the file is the From side.
		// We need to walk outgoing edges to find file nodes.
		edges := graph.Edges()
		for _, e := range edges {
			if e.Type == "imports" {
				// Check if this edge is part of a cycle involving 'from'.
				if e.To == from || e.From == from {
					// The other end is the file.
					nodeID := e.From
					if nodeID == from {
						nodeID = e.To
					}
					if n, ok := graph.GetNode(nodeID); ok && n.Kind == "file" && n.Path != "" {
						scores[n.Path] += 2.0
						if sources[n.Path] == "" {
							sources[n.Path] = "cycle_candidate"
						}
					}
				}
			}
		}
	}
}

// isLikelyEntryPoint returns true for names that are common entry points
// and should not be flagged as orphans.
func isLikelyEntryPoint(name string) bool {
	lower := strings.ToLower(name)
	return lower == "main" || lower == "init" || lower == "test" ||
		strings.HasPrefix(lower, "test_") || strings.HasSuffix(lower, "_test")
}

// findImportCycles returns the set of module IDs that are part of import
// cycles using Tarjan's strongly connected components algorithm.
func findImportCycles(graph *codemap.Graph) map[string]bool {
	if graph == nil {
		return nil
	}
	indices := make(map[string]int)
	var stack []string
	onStack := make(map[string]bool)
	varStrong := make(map[string]bool)
	index := 0

	var scc func(string) // forward declare for recursion

	scc = func(v string) {
		indices[v] = index
		index++
		stack = append(stack, v)
		onStack[v] = true

		edges := graph.Edges()
		for _, e := range edges {
			if e.Type != "imports" {
				continue
			}
			w := e.To
			if e.From == v && w != v {
				if _, ok := indices[w]; !ok {
					scc(w)
					if onStack[w] {
						indices[v] = min(indices[v], indices[w])
					}
				} else if onStack[w] {
					indices[v] = min(indices[v], indices[w])
				}
			}
		}

		if indices[v] == index-1 && len(stack) > 0 && stack[len(stack)-1] == v {
			// Root of an SCC
			var component []string
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				component = append(component, w)
				if w == v {
					break
				}
			}
			// Only flag as cyclic if the SCC has more than one member.
			if len(component) > 1 {
				for _, n := range component {
					varStrong[n] = true
				}
			}
		}
	}

	for _, n := range graph.Nodes() {
		if n.Kind != "module" {
			continue
		}
		if _, ok := indices[n.ID]; !ok {
			scc(n.ID)
		}
	}

	return varStrong
}
