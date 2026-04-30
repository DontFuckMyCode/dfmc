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
	"fmt"
	"sort"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/provider"
)

// readRangeEntry remembers a successful read_file response so a later
// call asking for a sub-range of the same file can be served from
// memory instead of dispatching a fresh tool call. Stored on
// parkedAgentState alongside the exact-key LoopFileCache; survives
// park→resume cycles.
//
// Only entries with truncated=false are recorded — when the cached
// read returned everything between [start,end], slicing a sub-range
// out of the content is lossless. Truncated entries get the existing
// exact-key cache only because the slicing math doesn't account for
// the trailing truncation marker.
type readRangeEntry struct {
	start   int    // inclusive, 1-based
	end     int    // inclusive, 1-based
	content string // raw segment text — split by "\n" yields exactly (end-start+1) lines
}

// readRangeIndexKey turns a project-relative path into the bucket key
// used by the per-path range index. Lower-case + forward-slash
// normalization so the same file referred to by different casings or
// path separators (Windows) collapses to one bucket.
func readRangeIndexKey(path string) string {
	p := strings.TrimSpace(path)
	if p == "" {
		return ""
	}
	p = strings.ReplaceAll(p, "\\", "/")
	return strings.ToLower(p)
}

// extractReadRangeRequest pulls (path, start, end) from a tool_call
// envelope when the call is a read_file with a normalized line range.
// Returns ok=false for any other tool, missing path, or non-numeric
// range values. The engine's normalizeToolParams ensures line_start /
// line_end are always populated for read_file by the time we reach
// the cache layer, so this should rarely fall through for live calls.
func extractReadRangeRequest(call provider.ToolCall) (path string, start int, end int, ok bool) {
	if call.Name != "tool_call" {
		return "", 0, 0, false
	}
	name, _ := call.Input["name"].(string)
	if strings.TrimSpace(name) != "read_file" {
		return "", 0, 0, false
	}
	args, _ := call.Input["args"].(map[string]any)
	if args == nil {
		return "", 0, 0, false
	}
	rawPath, _ := args["path"].(string)
	path = strings.TrimSpace(rawPath)
	if path == "" {
		return "", 0, 0, false
	}
	start = pickIntFromArgs(args, "line_start")
	end = pickIntFromArgs(args, "line_end")
	if start < 1 || end < start {
		return "", 0, 0, false
	}
	return path, start, end, true
}

// pickIntFromArgs accepts the loose JSON-decoded shapes models emit
// (int / int64 / float64 / numeric string). Returns 0 on miss or
// unrecognized type — the caller treats 0 as "not present".
func pickIntFromArgs(m map[string]any, key string) int {
	if m == nil {
		return 0
	}
	switch v := m[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case string:
		// Defensive: some providers stringify numbers in tool_use.
		// strconv.Atoi would be cleaner but we want zero-on-fail.
		var n int
		_, err := fmt.Sscanf(v, "%d", &n)
		if err != nil {
			return 0
		}
		return n
	}
	return 0
}

// sliceReadEntry returns the segment of `entry` covering the
// inclusive line range [reqStart, reqEnd], or "" if the entry
// doesn't fully cover the requested range. The caller has already
// confirmed it's a read_file call against the matching path.
//
// Slicing splits by "\n" and rejoins so trailing newline behaviour
// matches what builtin_read.go produces directly. We never invent
// a truncation marker here — by construction, every line in the
// requested range exists inside the entry's recorded content.
func sliceReadEntry(entry readRangeEntry, reqStart, reqEnd int) (string, bool) {
	if reqStart < entry.start || reqEnd > entry.end || reqStart > reqEnd {
		return "", false
	}
	if entry.content == "" {
		// Possible if the original read returned an empty file. The
		// exact-key cache already serves that path; range-merge is
		// only meaningful for non-empty payloads.
		return "", false
	}
	lines := strings.Split(entry.content, "\n")
	want := reqEnd - reqStart + 1
	offset := reqStart - entry.start
	if offset < 0 || offset+want > len(lines) {
		// Defensive: line count mismatch (e.g. trailing newline edge
		// cases). Refuse the slice rather than ship wrong content.
		return "", false
	}
	return strings.Join(lines[offset:offset+want], "\n"), true
}

// cacheableBackendTools lists the backend tool names whose results are
// safe to memoize across a single agent loop. Whitelist (not blacklist)
// because adding a side-effecting tool here would silently break loops
// that rely on freshness — we'd rather forget to cache a new read tool
// than accidentally cache a write.
var cacheableBackendTools = map[string]struct{}{
	"read_file":     {},
	"list_dir":      {},
	"grep_codebase": {},
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
