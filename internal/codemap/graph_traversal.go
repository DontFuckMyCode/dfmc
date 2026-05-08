package codemap

// graph_traversal.go — graph traversal and analysis surfaces on top of
// the Graph CRUD primitives. HotSpots ranks nodes by degree
// (in+out edges) for "what's most connected"; Descendants/Ancestors
// run BFS from a start node up to maxDepth following outgoing or
// incoming edges; ShortestPathLength is a directed BFS that returns
// hop count; Orphans surfaces nodes with no incoming edges;
// WalkDepthFirst / WalkBreadthFirst expose visitor-driven walks for
// callers that need to observe each node exactly once. Sibling to
// graph.go which owns the Graph struct, types, and CRUD operations
// (AddNode/AddEdge/RemoveEdge/RemoveNode/Outgoing/Incoming/Nodes/
// Edges/Counts/Clear).

import "sort"

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
