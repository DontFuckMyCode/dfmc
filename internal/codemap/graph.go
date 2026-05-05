package codemap

import (
	"sort"
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
	key := edgeSlotKey{From: e.From, To: e.To, Type: e.Type}
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

	key := edgeSlotKey{From: edge.From, To: edge.To, Type: edge.Type}
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

func (g *Graph) HotSpots(k int) []Node {
	g.mu.RLock()
	defer g.mu.RUnlock()

	type nodeDegree struct {
		node   Node
		degree int
	}
	degrees := make([]nodeDegree, 0, len(g.nodes))
	for id, node := range g.nodes {
		degree := activeEdgeIndexCount(g.edges, g.outIdx[id]) + activeEdgeIndexCount(g.edges, g.inIdx[id])
		degrees = append(degrees, nodeDegree{node, degree})
	}

	sort.Slice(degrees, func(i, j int) bool {
		return degrees[i].degree > degrees[j].degree
	})

	limit := k
	if limit <= 0 || limit > len(degrees) {
		limit = len(degrees)
	}
	result := make([]Node, 0, limit)
	for i := 0; i < limit; i++ {
		result = append(result, degrees[i].node)
	}
	return result
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

func (g *Graph) Descendants(startID string, maxDepth int) []Node {
	return g.relatedNodes(startID, maxDepth, true)
}

func (g *Graph) Ancestors(startID string, maxDepth int) []Node {
	return g.relatedNodes(startID, maxDepth, false)
}

func (g *Graph) relatedNodes(startID string, maxDepth int, outgoing bool) []Node {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if maxDepth < 0 {
		maxDepth = len(g.nodes)
	}
	type queueItem struct {
		id    string
		depth int
	}
	visited := map[string]bool{startID: true}
	queue := []queueItem{{id: startID, depth: 0}}
	out := []Node{}
	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]
		if item.depth >= maxDepth {
			continue
		}
		indices := g.outIdx[item.id]
		if !outgoing {
			indices = g.inIdx[item.id]
		}
		for _, idx := range indices {
			edge := g.edges[idx]
			nextID := edge.To
			if !outgoing {
				nextID = edge.From
			}
			if edge.From == "" || nextID == "" || visited[nextID] {
				continue
			}
			visited[nextID] = true
			if node, ok := g.nodes[nextID]; ok {
				out = append(out, node)
			}
			queue = append(queue, queueItem{id: nextID, depth: item.depth + 1})
		}
	}
	return out
}

func (g *Graph) ShortestPathLength(fromID, toID string) int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if fromID == toID {
		if _, ok := g.nodes[fromID]; ok {
			return 0
		}
	}
	type queueItem struct {
		id   string
		dist int
	}
	visited := map[string]bool{fromID: true}
	queue := []queueItem{{id: fromID, dist: 0}}
	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]
		for _, idx := range g.outIdx[item.id] {
			edge := g.edges[idx]
			if edge.From == "" || edge.To == "" || visited[edge.To] {
				continue
			}
			if edge.To == toID {
				return item.dist + 1
			}
			visited[edge.To] = true
			queue = append(queue, queueItem{id: edge.To, dist: item.dist + 1})
		}
	}
	return -1
}

func (g *Graph) Orphans() []Node {
	g.mu.RLock()
	defer g.mu.RUnlock()
	var orphans []Node
	for id, node := range g.nodes {
		if activeEdgeIndexCount(g.edges, g.inIdx[id]) == 0 {
			orphans = append(orphans, node)
		}
	}
	return orphans
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

func (g *Graph) WalkDepthFirst(startID string, visitor func(Node)) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	visited := make(map[string]bool)
	var walk func(id string)
	walk = func(id string) {
		if visited[id] {
			return
		}
		visited[id] = true
		if node, ok := g.nodes[id]; ok {
			visitor(node)
		}
		for _, idx := range g.outIdx[id] {
			if to := g.edges[idx].To; to != "" {
				walk(to)
			}
		}
	}
	walk(startID)
}

func (g *Graph) WalkBreadthFirst(startID string, visitor func(Node)) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	visited := make(map[string]bool)
	queue := []string{startID}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if visited[id] {
			continue
		}
		visited[id] = true
		if node, ok := g.nodes[id]; ok {
			visitor(node)
		}
		for _, idx := range g.outIdx[id] {
			if to := g.edges[idx].To; to != "" && !visited[to] {
				queue = append(queue, to)
			}
		}
	}
}
