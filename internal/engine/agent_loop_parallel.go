// Parallel tool-call dispatch inside one agent round.
//
// Claude / OpenAI frequently emit several tool_use blocks in a single
// assistant turn — "read A, read B, grep C". Executing them sequentially
// multiplies wall-clock latency without buying anything. This file adds
// a conservative parallelizer: when every call in the batch targets a
// read-only, side-effect-free tool, we fan them out under a bounded
// worker pool and rejoin the results in issue order. Any mutating tool
// (write_file, edit_file, run_command, apply_patch, …) forces the
// sequential fallback — out-of-order writes would race on the same file
// or the git snapshot that run_command captures.
//
// The returned slice matches the order of input calls exactly so the
// message-append loop in the caller doesn't need to re-sort anything.

package engine

import (
	"context"
	"strconv"
	"strings"
	"sync"

	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/internal/tools"
)

// parallelSafeTools lists the tool names that are guaranteed to have no
// observable side effects on the filesystem or process state. Keep this
// tight: when in doubt, leave a tool out. A false positive here (tool
// claimed safe but actually mutates) produces subtle race bugs that are
// nearly impossible to reproduce.
var parallelSafeTools = map[string]struct{}{
	"read_file":     {},
	"list_dir":      {},
	"grep_codebase": {},
	"glob":          {},
	"find_symbol":   {}, // pure read walker, same shape as grep_codebase
	"ast_query":     {}, // ParseFile + cache, no fs writes
	"web_fetch":     {},
	"web_search":    {},
	"think":         {},
	"todo_write":    {}, // mutates engine state only, not fs
}

// isParallelSafeToolCall reports whether the named tool may run
// concurrently with other tool calls in the same batch. The check is
// case-folded since providers occasionally emit SHOUTY names.
func isParallelSafeToolCall(name string) bool {
	key := strings.ToLower(strings.TrimSpace(name))
	_, ok := parallelSafeTools[key]
	return ok
}

// allParallelSafe reports whether every call in the batch is safe to
// run concurrently. Single-call batches always return false — there's
// nothing to parallelize and the sequential path is simpler.
func allParallelSafe(calls []provider.ToolCall) bool {
	if len(calls) < 2 {
		return false
	}
	for _, c := range calls {
		if !isParallelSafeToolCall(c.Name) {
			return false
		}
	}
	return true
}

// parallelToolResult pairs an executed tool with its outcome. Kept as a
// private type so callers don't accidentally depend on the internal
// ordering contract.
type parallelToolResult struct {
	Index  int
	Result tools.Result
	Err    error
}

// executeToolCallsParallel fans out a batch of parallel-safe tool calls
// under a bounded worker pool and returns the results in the same order
// as the input. The batchSize argument is clamped: values <=1 collapse
// to sequential dispatch (still via this function so callers have one
// code path), and very large values are capped at len(calls). Each tool
// runs through executeToolWithLifecycle so approval gating and hooks
// behave identically to the sequential path.
//
// When cache is non-nil, calls that match cacheableToolCallKey are
// served from the cache on hit (no tool dispatch, no approval gate, no
// network) and stored on miss. Cache writes are guarded by cacheMu so
// the parallel branch is safe under fan-out. Cache lookups are O(1)
// and can save tens of seconds per repeated read in long loops.
func (e *Engine) executeToolCallsParallel(ctx context.Context, calls []provider.ToolCall, batchSize int, source string, cache map[string]string, cacheMu *sync.Mutex, rangeIndex map[string][]readRangeEntry) []parallelToolResult {
	if len(calls) == 0 {
		return nil
	}
	if batchSize <= 0 {
		batchSize = 1
	}
	if batchSize > len(calls) {
		batchSize = len(calls)
	}

	out := make([]parallelToolResult, len(calls))

	dispatch := func(idx int, c provider.ToolCall) {
		if hit, ok := lookupToolCache(c, cache, cacheMu, rangeIndex); ok {
			out[idx] = parallelToolResult{Index: idx, Result: hit}
			e.publishAgentLoopEvent("agent:tool:cache_hit", map[string]any{
				"name": c.Name,
			})
			return
		}
		res, err := e.executeToolWithLifecycle(ctx, c.Name, c.Input, source)
		out[idx] = parallelToolResult{Index: idx, Result: res, Err: err}
		if err == nil {
			storeToolCache(c, res, cache, cacheMu, rangeIndex, e.maxRangeEntriesPerPath())
		}
	}

	if batchSize == 1 {
		for i, call := range calls {
			if ctx.Err() != nil {
				out[i] = parallelToolResult{Index: i, Err: ctx.Err()}
				continue
			}
			dispatch(i, call)
		}
		return out
	}

	sem := make(chan struct{}, batchSize)
	var wg sync.WaitGroup
	for i, call := range calls {
		if ctx.Err() != nil {
			out[i] = parallelToolResult{Index: i, Err: ctx.Err()}
			continue
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(idx int, c provider.ToolCall) {
			defer func() {
				<-sem
				wg.Done()
			}()
			select {
			case <-ctx.Done():
				out[idx] = parallelToolResult{Index: idx, Err: ctx.Err()}
				return
			default:
			}
			dispatch(idx, c)
		}(i, call)
	}
	wg.Wait()
	return out
}

// lookupToolCache returns the cached result for a cacheable read call,
// or zero+false on miss / non-cacheable. Nil cache short-circuits to
// miss so callers can always pass a cache pointer regardless of
// whether per-loop caching is enabled.
//
// On exact-key miss, this also tries a range-merge lookup against
// rangeIndex (when non-nil): if any cached read_file entry covers the
// requested [line_start, line_end], the stored content is sliced and
// returned. This is the single source of "cache hit" for the parallel
// dispatcher — adding a separate range-merge call site would risk
// double-storing or violating the mutex invariant.
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
			if rangeIndex != nil {
				for k := range rangeIndex {
					delete(rangeIndex, k)
				}
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

// parallelBatchSize returns the cfg-driven concurrency ceiling for a
// parallel tool-call batch. Defaults to 4 when unset (matches the
// agent.parallel_batch_size default in config/defaults.go). Values <=1
// disable parallelism entirely so operators can pin execution back to
// sequential if an unsafe tool sneaks onto the safe list.
func (e *Engine) parallelBatchSize() int {
	if e == nil || e.Config == nil {
		return 4
	}
	n := e.Config.Agent.ParallelBatchSize
	if n <= 0 {
		return 4
	}
	return n
}
