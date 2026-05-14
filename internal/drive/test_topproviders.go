//go:build ignore

package drive

// TopProviders returns the top N providers by total error count.
func (c *ErrorCategorizer) TopProviders(n int) []string {
	type kv struct {
		k string
		v int
	}
	var sorted []kv
	for k, v := range c.ByProvider {
		sorted = append(sorted, kv{k, v.Total})
	}
	// simple bubble sort for small N
	for i := 0; i < len(sorted)-1; i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].v > sorted[i].v {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	if n > len(sorted) {
		n = len(sorted)
	}
	out := make([]string, n)
	for i := 0; i < n; i++ {
		out[i] = sorted[i].k
	}
	return out
}
