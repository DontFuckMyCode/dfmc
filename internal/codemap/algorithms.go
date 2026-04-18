package codemap

func (g *Graph) StronglyConnectedComponents() [][]string {
	g.mu.RLock()
	defer g.mu.RUnlock()

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
	scc := g.StronglyConnectedComponents()
	out := make([][]string, 0)
	for _, comp := range scc {
		if len(comp) > 1 {
			out = append(out, comp)
			continue
		}
		id := comp[0]
		if g.hasSelfLoop(id) {
			out = append(out, comp)
		}
	}
	return out
}

func (g *Graph) hasSelfLoop(id string) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	for k := range g.outgoing[id] {
		if k.Node == id {
			return true
		}
	}
	return false
}
