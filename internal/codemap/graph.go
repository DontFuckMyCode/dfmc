package codemap

import (
	"sort"
	"sync"
)

type Node struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Kind     string            `json:"kind"`
	Path     string            `json:"path"`
	Language string            `json:"language,omitempty"`
	Meta     map[string]string `json:"meta,omitempty"`
}

type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Type string `json:"type"`
}

// edgeKey is the composite (other-endpoint, type) key used inside the
// adjacency maps. Pre-fix the inner key was just the other endpoint, so
// AddEdge silently overwrote prior entries when two edges shared the
// From/To pair but differed in Type (e.g. "calls" and "imports" between
// the same two nodes). With the composite key both edges coexist.
// REPORT.md #4. For outgoing[from], `Node` holds the To id; for
// incoming[to], `Node` holds the From id.
type edgeKey struct {
	Node string
	Type string
}

type Graph struct {
	mu       sync.RWMutex
	nodes    map[string]Node
	outgoing map[string]map[edgeKey]Edge
	incoming map[string]map[edgeKey]Edge
}

type Counts struct {
	Nodes int `json:"nodes"`
	Edges int `json:"edges"`
}

func NewGraph() *Graph {
	return &Graph{
		nodes:    map[string]Node{},
		outgoing: map[string]map[edgeKey]Edge{},
		incoming: map[string]map[edgeKey]Edge{},
	}
}

func (g *Graph) AddNode(node Node) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.nodes[node.ID] = node
}

func (g *Graph) AddEdge(edge Edge) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.outgoing[edge.From] == nil {
		g.outgoing[edge.From] = map[edgeKey]Edge{}
	}
	g.outgoing[edge.From][edgeKey{Node: edge.To, Type: edge.Type}] = edge

	if g.incoming[edge.To] == nil {
		g.incoming[edge.To] = map[edgeKey]Edge{}
	}
	g.incoming[edge.To][edgeKey{Node: edge.From, Type: edge.Type}] = edge
}

// RemoveEdge deletes one specific (from, to, type) triple from both
// the outgoing and incoming adjacency maps. Returns true when at least
// one side held the edge. No-op when neither side does (idempotent —
// safe to call repeatedly during a partial-rebuild sweep). Empty
// adjacency sub-maps are dropped so Counts() and HotSpots() don't keep
// counting a node's degree based on stale empty buckets.
func (g *Graph) RemoveEdge(edge Edge) bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	removed := false
	outKey := edgeKey{Node: edge.To, Type: edge.Type}
	if outs, ok := g.outgoing[edge.From]; ok {
		if _, has := outs[outKey]; has {
			delete(outs, outKey)
			removed = true
			if len(outs) == 0 {
				delete(g.outgoing, edge.From)
			}
		}
	}
	inKey := edgeKey{Node: edge.From, Type: edge.Type}
	if ins, ok := g.incoming[edge.To]; ok {
		if _, has := ins[inKey]; has {
			delete(ins, inKey)
			removed = true
			if len(ins) == 0 {
				delete(g.incoming, edge.To)
			}
		}
	}
	return removed
}

// RemoveNode deletes a node and every edge that touches it. Returns
// true when the node existed. Used by incremental rebuilds: when a
// file is deleted from the project, the codemap layer prunes the file
// node and all symbol nodes that lived in it; without this method the
// graph kept growing across rebuilds until process restart, polluting
// HotSpots() and Orphans() with phantom IDs (REPORT.md L2).
func (g *Graph) RemoveNode(id string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	if _, ok := g.nodes[id]; !ok {
		return false
	}
	// Drop reverse pointers first: every outgoing edge from `id` has
	// an incoming entry on the other side that needs to go too, and
	// vice versa. Doing it before deleting g.outgoing[id] /
	// g.incoming[id] keeps the loop reading from the same map snapshot.
	for k := range g.outgoing[id] {
		if ins, ok := g.incoming[k.Node]; ok {
			delete(ins, edgeKey{Node: id, Type: k.Type})
			if len(ins) == 0 {
				delete(g.incoming, k.Node)
			}
		}
	}
	for k := range g.incoming[id] {
		if outs, ok := g.outgoing[k.Node]; ok {
			delete(outs, edgeKey{Node: id, Type: k.Type})
			if len(outs) == 0 {
				delete(g.outgoing, k.Node)
			}
		}
	}
	delete(g.outgoing, id)
	delete(g.incoming, id)
	delete(g.nodes, id)
	return true
}

func (g *Graph) GetNode(id string) (Node, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	n, ok := g.nodes[id]
	return n, ok
}

func (g *Graph) Nodes() []Node {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]Node, 0, len(g.nodes))
	for _, n := range g.nodes {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (g *Graph) Edges() []Edge {
	g.mu.RLock()
	defer g.mu.RUnlock()
	var out []Edge
	for _, m := range g.outgoing {
		for _, e := range m {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].From == out[j].From {
			return out[i].To < out[j].To
		}
		return out[i].From < out[j].From
	})
	return out
}

func (g *Graph) Counts() Counts {
	g.mu.RLock()
	defer g.mu.RUnlock()

	edges := 0
	for _, m := range g.outgoing {
		edges += len(m)
	}

	return Counts{
		Nodes: len(g.nodes),
		Edges: edges,
	}
}

func (g *Graph) Descendants(start string, depth int) []Node {
	if depth <= 0 {
		return nil
	}
	g.mu.RLock()
	defer g.mu.RUnlock()

	type item struct {
		id    string
		level int
	}
	seen := map[string]struct{}{start: {}}
	queue := []item{{id: start, level: 0}}
	var out []Node

	for len(queue) > 0 {
		cur := queue[0]
		queue[0] = item{} // clear ref for GC
		queue = queue[1:]
		if cur.level >= depth {
			continue
		}
		for k := range g.outgoing[cur.id] {
			to := k.Node
			if _, ok := seen[to]; ok {
				continue
			}
			seen[to] = struct{}{}
			if n, ok := g.nodes[to]; ok {
				out = append(out, n)
			}
			queue = append(queue, item{id: to, level: cur.level + 1})
		}
	}
	return out
}

func (g *Graph) Ancestors(start string, depth int) []Node {
	if depth <= 0 {
		return nil
	}
	g.mu.RLock()
	defer g.mu.RUnlock()

	type item struct {
		id    string
		level int
	}
	seen := map[string]struct{}{start: {}}
	queue := []item{{id: start, level: 0}}
	var out []Node

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.level >= depth {
			continue
		}
		for k := range g.incoming[cur.id] {
			from := k.Node
			if _, ok := seen[from]; ok {
				continue
			}
			seen[from] = struct{}{}
			if n, ok := g.nodes[from]; ok {
				out = append(out, n)
			}
			queue = append(queue, item{id: from, level: cur.level + 1})
		}
	}
	return out
}

func (g *Graph) ShortestPathLength(fromID, toID string) int {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if fromID == toID {
		return 0
	}

	type item struct {
		id    string
		steps int
	}
	seen := map[string]struct{}{fromID: {}}
	queue := []item{{id: fromID, steps: 0}}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for k := range g.outgoing[cur.id] {
			to := k.Node
			if to == toID {
				return cur.steps + 1
			}
			if _, ok := seen[to]; ok {
				continue
			}
			seen[to] = struct{}{}
			queue = append(queue, item{id: to, steps: cur.steps + 1})
		}
	}
	return -1
}

func (g *Graph) Orphans() []Node {
	g.mu.RLock()
	defer g.mu.RUnlock()

	var out []Node
	for id, n := range g.nodes {
		if len(g.incoming[id]) == 0 {
			out = append(out, n)
		}
	}
	return out
}

func (g *Graph) HotSpots(limit int) []Node {
	g.mu.RLock()
	defer g.mu.RUnlock()

	type score struct {
		node  Node
		score int
	}
	all := make([]score, 0, len(g.nodes))
	for id, n := range g.nodes {
		out := len(g.outgoing[id])
		in := len(g.incoming[id])
		all = append(all, score{node: n, score: in + out})
	}

	sort.Slice(all, func(i, j int) bool { return all[i].score > all[j].score })
	if limit > 0 && limit < len(all) {
		all = all[:limit]
	}

	out := make([]Node, 0, len(all))
	for _, s := range all {
		out = append(out, s.node)
	}
	return out
}
