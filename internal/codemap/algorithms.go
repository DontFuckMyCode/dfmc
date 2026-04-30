package codemap

func (g *Graph) StronglyConnectedComponents() [][]string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.sccLocked()
}

// sccLocked computes Tarjan's SCC over the current graph state. Caller
// must hold g.mu (read or write); the helper exists so Cycles() can run
// SCC and the per-component self-loop pass under a single RLock — the
// previous implementation released the lock between SCC and hasSelfLoop,
// leaving a mutation window where a node could disappear or grow a
// self-loop and the result would reflect neither state cleanly.
func (g *Graph) sccLocked() [][]string {
	var (
		index   int
		stack   []string
		onStack = map[string]bool{}
		indices = map[string]int{}
		lowlink = map[string]int{}
		sccs    [][]string
	)

	var strongConnect func(v string)
	strongConnect = func(v string) {
		indices[v] = index
		lowlink[v] = index
		index++

		stack = append(stack, v)
		onStack[v] = true

		for k := range g.outgoing[v] {
			w := k.Node
			if _, seen := indices[w]; !seen {
				strongConnect(w)
				if lowlink[w] < lowlink[v] {
					lowlink[v] = lowlink[w]
				}
			} else if onStack[w] && indices[w] < lowlink[v] {
				lowlink[v] = indices[w]
			}
		}

		if lowlink[v] == indices[v] {
			var comp []string
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				comp = append(comp, w)
				if w == v {
					break
				}
			}
			sccs = append(sccs, comp)
		}
	}

	for nodeID := range g.nodes {
		if _, seen := indices[nodeID]; !seen {
			strongConnect(nodeID)
		}
	}

	return sccs
}

func (g *Graph) Cycles() [][]string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	scc := g.sccLocked()
	out := make([][]string, 0)
	for _, comp := range scc {
		if len(comp) > 1 {
			out = append(out, comp)
			continue
		}
		id := comp[0]
		if g.hasSelfLoopLocked(id) {
			out = append(out, comp)
		}
	}
	return out
}

func (g *Graph) hasSelfLoop(id string) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.hasSelfLoopLocked(id)
}

// hasSelfLoopLocked is the lock-free body of hasSelfLoop. Caller must
// hold g.mu (read or write). Used by Cycles() to keep SCC + self-loop
// inspection inside a single RLock.
func (g *Graph) hasSelfLoopLocked(id string) bool {
	for k := range g.outgoing[id] {
		if k.Node == id {
			return true
		}
	}
	return false
}
