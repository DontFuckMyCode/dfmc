package codemap

import (
	"sync"
)

// Node represents a code entity (function, type, file, etc.) in the graph.
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

// Graph stores a directed graph using single-slice edges with external indexes.
// Memory optimization: edges stored once with out/in indexes instead of dual maps.
// This reduces memory overhead from 2x edge storage to 1x + index overhead.
type Graph struct {
	mu      sync.RWMutex
	nodes   map[string]Node
	edges   []Edge           // single source of truth
	outIdx  map[string][]int // outgoing: nodeID -> edge indices
	inIdx   map[string][]int // incoming: nodeID -> edge indices
	edgeIdx map[edgeSlotKey]int
}

type edgeSlotKey struct {
	From string
	To   string
	Type string
}

type Counts struct {
	Nodes int `json:"nodes"`
	Edges int `json:"edges"`
}

func NewGraph() *Graph {
	return &Graph{
		nodes:   map[string]Node{},
		edges:   []Edge{},
		outIdx:  map[string][]int{},
		inIdx:   map[string][]int{},
		edgeIdx: map[edgeSlotKey]int{},
	}
}

func (g *Graph) AddNode(node Node) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.nodes[node.ID] = node
}

// AddEdge records a directed edge exactly as given. Cycles are allowed by
// design: real import/call graphs can be cyclic, and downstream consumers are
// expected to consult Cycles()/StronglyConnectedComponents() rather than
// assume the graph is a DAG.
func (g *Graph) AddEdge(edge Edge) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.addEdgeLocked(edge)
}

func (g *Graph) addEdgeLocked(e Edge) {
	key := edgeSlotKey(e)
	if idx, ok := g.edgeIdx[key]; ok {
		g.edges[idx] = e
		return
	}
	idx := len(g.edges)
	g.edges = append(g.edges, e)
	g.outIdx[e.From] = append(g.outIdx[e.From], idx)
	g.inIdx[e.To] = append(g.inIdx[e.To], idx)
	g.edgeIdx[key] = idx
}

// AddNodeWithEdges adds a node and multiple edges in a single lock
// scope. This prevents intermediate state visibility when adding
// related node+edges atomically (e.g. file node + its symbol children).
// Thread-safe for concurrent callers.
func (g *Graph) AddNodeWithEdges(node Node, edges []Edge) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.nodes[node.ID] = node
	for _, e := range edges {
		g.addEdgeLocked(e)
	}
}

// AddNodesWithEdges adds a batch of nodes and edges in one lock scope.
func (g *Graph) AddNodesWithEdges(nodes []Node, edges []Edge) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, node := range nodes {
		g.nodes[node.ID] = node
	}
	for _, e := range edges {
		g.addEdgeLocked(e)
	}
}

// RemoveEdge deletes one specific (from, to, type) triple.
func (g *Graph) RemoveEdge(edge Edge) bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	key := edgeSlotKey(edge)
	idx, exists := g.edgeIdx[key]
	if !exists {
		return false
	}
	// Tombstone the edge slot so existing index slices (outIdx/inIdx) remain stable.
	// Index 0 is ambiguous: map returns 0 for both "not found" and "first edge".
	// We work around this by ensuring edgeIdx is never empty when we delete.
	g.edges[idx] = Edge{}
	// Update indexes to remove this index from in/out lists.
	g.outIdx[edge.From] = removeInt(g.outIdx[edge.From], idx)
	g.inIdx[edge.To] = removeInt(g.inIdx[edge.To], idx)
	// Clean up empty index slices to prevent unbounded growth.
	if len(g.outIdx[edge.From]) == 0 {
		delete(g.outIdx, edge.From)
	}
	if len(g.inIdx[edge.To]) == 0 {
		delete(g.inIdx, edge.To)
	}
	delete(g.edgeIdx, key)
	return true
}

// RemoveNode deletes a node and every edge that touches it.
func (g *Graph) RemoveNode(id string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	if _, ok := g.nodes[id]; !ok {
		return false
	}
	delete(g.nodes, id)

	var outRemove, inRemove []int
	for _, idx := range g.outIdx[id] {
		if g.edges[idx].To != "" {
			outRemove = append(outRemove, idx)
			g.inIdx[g.edges[idx].To] = removeInt(g.inIdx[g.edges[idx].To], idx)
			delete(g.edgeIdx, edgeSlotKey{From: g.edges[idx].From, To: g.edges[idx].To, Type: g.edges[idx].Type})
		}
	}
	for _, idx := range g.inIdx[id] {
		if g.edges[idx].From != "" {
			inRemove = append(inRemove, idx)
			g.outIdx[g.edges[idx].From] = removeInt(g.outIdx[g.edges[idx].From], idx)
			delete(g.edgeIdx, edgeSlotKey{From: g.edges[idx].From, To: g.edges[idx].To, Type: g.edges[idx].Type})
		}
	}

	for _, idx := range append(outRemove, inRemove...) {
		g.edges[idx] = Edge{}
	}

	delete(g.outIdx, id)
	delete(g.inIdx, id)
	return true
}

func removeInt(slice []int, val int) []int {
	for i, v := range slice {
		if v == val {
			return append(slice[:i], slice[i+1:]...)
		}
	}
	return slice
}

func activeEdgeIndexCount(edges []Edge, indices []int) int {
	count := 0
	for _, idx := range indices {
		if idx < 0 || idx >= len(edges) {
			continue
		}
		if e := edges[idx]; e.From != "" && e.To != "" {
			count++
		}
	}
	return count
}

func (g *Graph) Node(id string) (Node, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	n, ok := g.nodes[id]
	return n, ok
}

func (g *Graph) HasNode(id string) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	_, ok := g.nodes[id]
	return ok
}

func (g *Graph) GetNode(id string) (Node, bool) {
	return g.Node(id)
}

func (g *Graph) Counts() Counts {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return Counts{Nodes: len(g.nodes), Edges: len(g.edgeIdx)}
}

func (g *Graph) Outgoing(fromID string) []Edge {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.copyOutgoing(fromID)
}

func (g *Graph) copyOutgoing(fromID string) []Edge {
	indices := g.outIdx[fromID]
	result := make([]Edge, 0, len(indices))
	for _, idx := range indices {
		if e := g.edges[idx]; e.From != "" {
			result = append(result, e)
		}
	}
	return result
}

func (g *Graph) Incoming(toID string) []Edge {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.copyIncoming(toID)
}

func (g *Graph) copyIncoming(toID string) []Edge {
	indices := g.inIdx[toID]
	result := make([]Edge, 0, len(indices))
	for _, idx := range indices {
		if e := g.edges[idx]; e.To != "" {
			result = append(result, e)
		}
	}
	return result
}

func (g *Graph) Nodes() []Node {
	g.mu.RLock()
	defer g.mu.RUnlock()
	result := make([]Node, 0, len(g.nodes))
	for _, n := range g.nodes {
		result = append(result, n)
	}
	return result
}

func (g *Graph) Edges() []Edge {
	g.mu.RLock()
	defer g.mu.RUnlock()
	result := make([]Edge, 0, len(g.edges))
	for _, e := range g.edges {
		if e.From != "" && e.To != "" {
			result = append(result, e)
		}
	}
	return result
}

func (g *Graph) Clear() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.nodes = map[string]Node{}
	g.edges = []Edge{}
	g.outIdx = map[string][]int{}
	g.inIdx = map[string][]int{}
	g.edgeIdx = map[edgeSlotKey]int{}
}

