package engine

// agent_loop_parallel_cache.go — per-loop tool-result cache helpers
// (lookup / store / invalidate) and the path extractor that decides
// what to wipe after a successful mutation. Sibling of
// agent_loop_parallel.go which keeps parallelSafeTools, the parallel-
// safety predicates (isParallelSafeToolCall, allParallelSafe), the
// parallelToolResult shape, executeToolCallsParallel itself, and the
// parallelBatchSize cfg knob.
//
// The cache + range index live on the parked seed (LoopFileCache,
// LoopReadRangeIndex) so they survive park/resume; this sibling owns
// the read/write/invalidate paths against those two maps. Locking is
// the caller's responsibility — every helper takes the *sync.Mutex
// rather than reaching into the seed for it, so a single Lock/Unlock
// in the dispatcher covers an exact-key + range-merge pair without
// double-acquiring.

import (
	"strconv"
	"strings"
	"sync"

	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/internal/tools"
)

func lookupToolCache(call provider.ToolCall, cache map[string]string, mu *sync.Mutex, rangeIndex map[string][]readRangeEntry) (tools.Result, bool) {
	if cache == nil {
		return tools.Result{}, false
	}
	key, ok := cacheableToolCallKey(call)
	if !ok {
		return tools.Result{}, false
	}
	mu.Lock()
	output, hit := cache[key]
	if hit {
		mu.Unlock()
		return tools.Result{Output: output}, true
	}
	// Range-merge fallback: a previous read_file may have grabbed a
	// larger window of the same file. Slice that to satisfy the
	// current request without dispatching the tool.
	if rangeIndex != nil {
		if path, start, end, ok := extractReadRangeRequest(call); ok {
			bucket := rangeIndex[readRangeIndexKey(path)]
			for i := range bucket {
				if sliced, ok := sliceReadEntry(bucket[i], start, end); ok {
					mu.Unlock()
					return tools.Result{Output: sliced}, true
				}
			}
		}
	}
	mu.Unlock()
	return tools.Result{}, false
}

// storeToolCache writes a successful tool result back into the cache.
// Skips when the call isn't cacheable, when the result is empty, or
// when the cache is nil. The map is created once per loop in the seed;
// callers must pass a non-nil map to actually retain anything (this
// helper does not lazy-init the slot).
//
// When rangeIndex is non-nil and the call is a non-truncated read_file
// with a known range, this also appends a readRangeEntry to the index
// so a later sub-range request can be served from the same memory by
// slicing rather than re-dispatching the tool. Truncated reads skip
// the index because their content has a trailing marker that would
// corrupt slice math.
func storeToolCache(call provider.ToolCall, res tools.Result, cache map[string]string, mu *sync.Mutex, rangeIndex map[string][]readRangeEntry, perPathCap int) {
	if cache == nil {
		return
	}
	key, ok := cacheableToolCallKey(call)
	if !ok || strings.TrimSpace(res.Output) == "" {
		return
	}
	mu.Lock()
	cache[key] = res.Output
	if rangeIndex != nil && !res.Truncated {
		if path, start, end, ok := extractReadRangeRequest(call); ok {
			bucketKey := readRangeIndexKey(path)
			bucket := rangeIndex[bucketKey]
			// FIFO eviction — drop the oldest entry once the bucket is at
			// cap so a long loop reading many overlapping windows of the
			// same file doesn't grow this slice unboundedly. The newest
			// entry usually has the best hit rate for the next request.
			cap := perPathCap
			if cap <= 0 {
				cap = defaultMaxRangeEntriesPerPath
			}
			if len(bucket) >= cap {
				bucket = bucket[1:]
			}
			rangeIndex[bucketKey] = append(bucket, readRangeEntry{
				start:   start,
				end:     end,
				content: res.Output,
			})
		}
	}
	mu.Unlock()
}

// invalidateCacheForFiles drops cache entries whose canonical args
// contain any of the supplied file paths. Called from the tool-batch
// post-processor after edit_file/write_file/apply_patch succeeds so the
// next read on the same path sees fresh content instead of a stale
// cached body.
//
// The "*" sentinel triggers a full cache wipe — used for apply_patch
// where the affected paths live inside a diff blob and would cost more
// to parse than the wipe saves.
//
// Cache key shape (set by storeToolCache): "<backend>|<canonical-json>".
// We substring-match the JSON-encoded path so any args field that
// embeds the path (read_file's "path", list_dir's "path", grep's
// include glob) invalidates correctly. This is conservative — it will
// over-invalidate when a path string accidentally matches inside an
// unrelated arg — but the cost of an unnecessary re-read is tiny
// compared to serving stale content.
func invalidateCacheForFiles(cache map[string]string, mu *sync.Mutex, paths []string, rangeIndex map[string][]readRangeEntry) {
	if cache == nil || len(paths) == 0 {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	// Wildcard wipe — used after apply_patch since we can't cheaply
	// enumerate the touched files from a unified diff.
	for _, p := range paths {
		if p == "*" {
			for k := range cache {
				delete(cache, k)
			}
			for k := range rangeIndex {
				delete(rangeIndex, k)
			}
			return
		}
	}
	for key := range cache {
		sep := strings.Index(key, "|")
		if sep < 0 {
			continue
		}
		args := key[sep+1:]
		for _, p := range paths {
			if p == "" {
				continue
			}
			// Match the path token inside the canonical JSON. We compare
			// against the JSON-quoted form so a path like "foo.go" doesn't
			// collide with a substring of an unrelated arg.
			needle := strconv.Quote(p)
			if strings.Contains(args, needle) {
				delete(cache, key)
				break
			}
		}
	}
	// Range-index invalidation runs in parallel: any path the writer
	// touched must drop ALL its sub-range entries because we can't tell
	// which lines of the cached content the edit changed.
	if rangeIndex != nil {
		for _, p := range paths {
			if p == "" {
				continue
			}
			delete(rangeIndex, readRangeIndexKey(p))
		}
	}
}

// extractModifiedPath returns the file path a write-class call touched
// (edit_file, write_file). Returns "*" for apply_patch — the diff may
// touch multiple files and parsing it is more expensive than wiping
// the cache. Empty string for non-mutating calls or args we can't
// inspect (the invalidator treats that as "no-op").
func extractModifiedPath(call provider.ToolCall) string {
	if call.Name != "tool_call" {
		return ""
	}
	name, _ := call.Input["name"].(string)
	switch name {
	case "edit_file", "write_file":
		args, _ := call.Input["args"].(map[string]any)
		if args == nil {
			return ""
		}
		path, _ := args["path"].(string)
		return strings.TrimSpace(path)
	case "apply_patch":
		return "*"
	}
	return ""
}
