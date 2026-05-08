package tools

// failure_tracker.go — bounded recent-failure counter used by Execute
// to bail out after 3 consecutive same-shape failures. Same shape is
// defined as toolFailureKey: the tool name + canonical sorted-key
// params. A successful Execute clears the entry.
//
// The map is bounded by maxRecentFailures; eviction follows insertion
// order (FIFO) rather than touch order so the retry gate stays
// deterministic across identical runs. Map iteration order is
// randomized in Go, so a touch-based LRU here would make the gate
// nondeterministic across runs with the same trace.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const maxRecentFailures = 256

func (e *Engine) trackFailure(key string) int {
	e.failureMu.Lock()
	defer e.failureMu.Unlock()
	if e.failureOrderIdx == nil {
		e.failureOrderIdx = map[string]int{}
	}
	if _, ok := e.recentFailures[key]; !ok {
		idx := len(e.recentFailOrder)
		e.recentFailOrder = append(e.recentFailOrder, key)
		e.failureOrderIdx[key] = idx
	}
	e.recentFailures[key]++
	// M3: evict oldest entries when the map grows too large. Map
	// iteration order is randomized, so deleting arbitrary keys made the
	// retry gate nondeterministic across identical runs.
	cap := e.recentFailureCap
	if cap <= 0 {
		cap = maxRecentFailures
	}
	if len(e.recentFailures) > cap {
		target := cap / 2
		for len(e.recentFailures) > target && len(e.recentFailOrder) > 0 {
			oldest := e.recentFailOrder[0]
			e.recentFailOrder = e.recentFailOrder[1:]
			delete(e.recentFailures, oldest)
			delete(e.failureOrderIdx, oldest)
			// Re-index remaining entries (rare, only on eviction path).
			for i, k := range e.recentFailOrder {
				e.failureOrderIdx[k] = i
			}
		}
	}
	return e.recentFailures[key]
}

func (e *Engine) clearFailure(key string) {
	e.failureMu.Lock()
	defer e.failureMu.Unlock()
	delete(e.recentFailures, key)
	// O(1) reverse lookup via map instead of O(n) slice scan.
	if e.failureOrderIdx == nil {
		e.failureOrderIdx = map[string]int{}
	}
	delete(e.failureOrderIdx, key)
}

func toolFailureKey(name string, params map[string]any) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(strings.ToLower(strings.TrimSpace(name)))
	for _, k := range keys {
		b.WriteString("|")
		b.WriteString(strings.TrimSpace(k))
		b.WriteString("=")
		b.WriteString(canonicalToolFailureValue(params[k]))
	}
	return b.String()
}

func canonicalToolFailureValue(v any) string {
	switch typed := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	}
	raw, err := json.Marshal(v)
	if err == nil {
		return strings.TrimSpace(string(raw))
	}
	return strings.TrimSpace(fmt.Sprint(v))
}
