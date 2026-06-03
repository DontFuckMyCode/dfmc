package codemap

import (
	"fmt"
	"testing"
)

// bfsDistReference is an independent breadth-first shortest-path used to
// cross-check Graph.ShortestPathLength. Same contract: 0 when from==to,
// the hop count of the shortest directed path otherwise, -1 when
// unreachable.
func bfsDistReference(adj map[string][]string, from, to string) int {
	if from == to {
		return 0
	}
	visited := map[string]bool{from: true}
	dist := map[string]int{from: 0}
	queue := []string{from}
	for len(queue) > 0 {
		x := queue[0]
		queue = queue[1:]
		for _, w := range adj[x] {
			if visited[w] {
				continue
			}
			visited[w] = true
			dist[w] = dist[x] + 1
			if w == to {
				return dist[w]
			}
			queue = append(queue, w)
		}
	}
	return -1
}

// FuzzShortestPathMatchesBFS cross-checks Graph.ShortestPathLength against an
// independent BFS reference over the same directed graph. ShortestPathLength
// feeds dependency-distance metrics; an off-by-one or a wrong "unreachable"
// would mislead every consumer. Both must agree on every (from, to) pair for
// any graph shape, including self-loops and disconnected nodes.
func FuzzShortestPathMatchesBFS(f *testing.F) {
	f.Add([]byte{0, 1, 1, 2, 2, 3}, uint8(0), uint8(3)) // chain 0->1->2->3
	f.Add([]byte{0, 1, 0, 2, 2, 3}, uint8(0), uint8(3)) // diamond-ish
	f.Add([]byte{1, 0}, uint8(0), uint8(1))             // unreachable (edge goes the other way)
	f.Add([]byte{3, 3}, uint8(3), uint8(3))             // self-loop, from==to
	f.Add([]byte{}, uint8(0), uint8(5))                 // no edges

	f.Fuzz(func(t *testing.T, raw []byte, fromIdx, toIdx uint8) {
		const n = 6
		nodes := make([]string, n)
		g := NewGraph()
		for i := range n {
			nodes[i] = fmt.Sprintf("n%d", i)
			g.AddNode(Node{ID: nodes[i], Name: nodes[i]})
		}
		adj := make(map[string][]string, n)
		seen := make(map[[2]string]bool)
		for i := 0; i+1 < len(raw); i += 2 {
			from := nodes[int(raw[i])%n]
			to := nodes[int(raw[i+1])%n]
			key := [2]string{from, to}
			if seen[key] {
				continue
			}
			seen[key] = true
			g.AddEdge(Edge{From: from, To: to})
			adj[from] = append(adj[from], to)
		}

		from := nodes[int(fromIdx)%n]
		to := nodes[int(toIdx)%n]

		got := g.ShortestPathLength(from, to)
		want := bfsDistReference(adj, from, to)
		if got != want {
			t.Fatalf("ShortestPathLength(%s,%s)=%d, reference BFS=%d\n  edges=%v",
				from, to, got, want, seen)
		}
	})
}
