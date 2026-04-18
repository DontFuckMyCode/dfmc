// Per-loop tool-result cache for sustained orchestration. Long agent
// loops (refactor: read 30 files, edit 12, verify, edit again) re-read
// the same files repeatedly — each read costs disk I/O, tool dispatch,
// approval-gate eval, and a network round-trip with the model. Caching
// the output keyed by (tool name, canonical args) lets the loop serve
// repeats from memory while still feeding the model the same payload
// it would have received from a fresh read.
//
// Strictly READ-class tools. Anything with side effects (writes,
// commands, network) MUST always execute — caching their results would
// hide bug fixes or external state changes from the model.
//
// Cache lives on parkedAgentState so it survives park→resume cycles:
// a 60-step loop that parks at step 30 and resumes for another 30 will
// keep its file cache, avoiding cold-start re-reads after every park.

package engine

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/provider"
)

// cacheableBackendTools lists the backend tool names whose results are
// safe to memoize across a single agent loop. Whitelist (not blacklist)
// because adding a side-effecting tool here would silently break loops
// that rely on freshness — we'd rather forget to cache a new read tool
// than accidentally cache a write.
var cacheableBackendTools = map[string]struct{}{
	"read_file":      {},
	"list_dir":       {},
	"grep_codebase":  {},
}

// cacheableToolCallKey returns a stable key for the call when it's a
// cacheable read AND ok=true. Returns ("", false) for write/exec tools,
// for malformed input, or for the meta-tools that don't unwrap to a
// single backend call (tool_search, tool_help, tool_batch_call — the
// last one's nested calls are still cached individually inside the
// batch handler, but the batch envelope itself is not).
//
// Convention: backend tools are invoked through the meta-tool
// `tool_call` with input shape {"name": <backend>, "args": {...}}. The
// key is `<backend>|<canonical-json-of-args>` so two calls with the
// same logical query collapse to one cache slot regardless of map
// iteration order in the args.
func cacheableToolCallKey(call provider.ToolCall) (string, bool) {
	if call.Name != "tool_call" {
		return "", false
	}
	rawName, _ := call.Input["name"].(string)
	backend := strings.TrimSpace(rawName)
	if backend == "" {
		return "", false
	}
	if _, ok := cacheableBackendTools[backend]; !ok {
		return "", false
	}
	args, _ := call.Input["args"].(map[string]any)
	canonical, err := canonicalJSON(args)
	if err != nil {
		return "", false
	}
	return backend + "|" + canonical, true
}

// canonicalJSON marshals m with deterministic key ordering so two
// arg maps that differ only in iteration order map to the same string.
// Standard json.Marshal already sorts keys for top-level maps, but
// nested maps need the same treatment for the cache key to be stable
// — handle this by walking the value tree once.
func canonicalJSON(v any) (string, error) {
	normalized := normalizeForJSON(v)
	b, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// normalizeForJSON walks v and rebuilds maps with sorted keys so the
// resulting json.Marshal output is deterministic regardless of source
// map iteration order. Primitives pass through unchanged.
func normalizeForJSON(v any) any {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make(map[string]any, len(x))
		for _, k := range keys {
			out[k] = normalizeForJSON(x[k])
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = normalizeForJSON(item)
		}
		return out
	default:
		return v
	}
}
