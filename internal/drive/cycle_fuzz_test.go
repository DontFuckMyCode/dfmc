package drive

import (
	"fmt"
	"testing"
)

// kahnAcyclic is an INDEPENDENT reference cycle detector (Kahn's algorithm:
// repeatedly remove in-degree-zero nodes; the graph is acyclic iff every
// node is removed). It exists only to cross-check detectCycle's 3-color DFS
// — two unrelated algorithms must always agree on whether a graph has a
// cycle, so a disagreement pinpoints a bug in one of them.
func kahnAcyclic(todos []Todo) bool {
	indeg := make(map[string]int, len(todos))
	adj := make(map[string][]string, len(todos))
	for _, t := range todos {
		if _, ok := indeg[t.ID]; !ok {
			indeg[t.ID] = 0
		}
	}
	for _, t := range todos {
		for _, dep := range t.DependsOn {
			adj[t.ID] = append(adj[t.ID], dep)
			indeg[dep]++
		}
	}
	queue := make([]string, 0, len(todos))
	for id, d := range indeg {
		if d == 0 {
			queue = append(queue, id)
		}
	}
	removed := 0
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		removed++
		for _, dep := range adj[id] {
			indeg[dep]--
			if indeg[dep] == 0 {
				queue = append(queue, dep)
			}
		}
	}
	return removed == len(indeg)
}

// FuzzDetectCycleMatchesKahn drives an arbitrary dependency graph (edges
// packed into a fuzzed byte string) through both detectCycle and the Kahn
// reference and asserts they agree on acyclicity. detectCycle gates every
// plan before the scheduler runs: a missed cycle deadlocks the run, a false
// positive rejects a valid plan.
func FuzzDetectCycleMatchesKahn(f *testing.F) {
	f.Add([]byte{0, 1, 1, 2, 2, 0}) // 0->1->2->0 cycle
	f.Add([]byte{0, 1, 1, 2, 2, 3}) // chain, acyclic
	f.Add([]byte{0, 1, 0, 2, 0, 3}) // fan-out, acyclic
	f.Add([]byte{1, 0, 2, 1, 0, 2}) // 0->2->1->0 cycle
	f.Add([]byte{})                 // no edges

	f.Fuzz(func(t *testing.T, edges []byte) {
		const n = 6
		todos := make([]Todo, n)
		idSet := make(map[string]int, n)
		for i := range n {
			todos[i] = Todo{ID: fmt.Sprintf("n%d", i)}
			idSet[todos[i].ID] = i
		}
		// Pack byte pairs into directed edges from%n -> to%n, skipping
		// self-loops (validateTodos rejects those on a separate path) and
		// duplicates (detectCycle tolerates them but the reference graph
		// should stay simple).
		seen := make(map[[2]int]bool)
		for i := 0; i+1 < len(edges); i += 2 {
			from := int(edges[i]) % n
			to := int(edges[i+1]) % n
			if from == to {
				continue
			}
			key := [2]int{from, to}
			if seen[key] {
				continue
			}
			seen[key] = true
			todos[from].DependsOn = append(todos[from].DependsOn, todos[to].ID)
		}

		dfsCycle := detectCycle(todos, idSet) != ""
		kahnCycle := !kahnAcyclic(todos)
		if dfsCycle != kahnCycle {
			t.Fatalf("cycle detectors disagree: detectCycle=%v kahn=%v\n  graph=%+v",
				dfsCycle, kahnCycle, edgeDump(todos))
		}
	})
}

func edgeDump(todos []Todo) string {
	out := ""
	for _, t := range todos {
		if len(t.DependsOn) > 0 {
			out += fmt.Sprintf("%s->%v ", t.ID, t.DependsOn)
		}
	}
	return out
}
