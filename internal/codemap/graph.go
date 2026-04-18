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
