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
//
// Per-loop cache helpers (lookupToolCache, storeToolCache,
// invalidateCacheForFiles, extractModifiedPath) live in
// agent_loop_parallel_cache.go.

package engine

import (
	"context"
	"strings"
	"sync"

	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/internal/tools"
)

// parallelSafeTools maps tool names that are guaranteed to have no
// observable side effects on the filesystem or process state. Keep this
// tight: when in doubt, leave a tool out. A false positive here (tool
// claimed safe but actually mutates) produces subtle race bugs that are
// nearly impossible to reproduce.
//
// Access is protected by mu so the map can be extended at runtime if
// needed (e.g. via plugin registration). Read-heavy paths use RLock.
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
	// NOTE: todo_write is intentionally excluded. It can trigger nested
	// CallTool invocations (e.g. through invalidateContextForTool) that
	// mutate engine state from concurrent goroutines. While e.mu
	// protects seenFiles/modifiedFiles, todo_write's own execution path
	// is not re-entrant safe across parallel calls.
}

var parallelSafeToolsMu sync.RWMutex

// isParallelSafeToolCall reports whether the named tool may run
// concurrently with other tool calls in the same batch. The check is
// case-folded since providers occasionally emit SHOUTY names.
func isParallelSafeToolCall(name string) bool {
	key := strings.ToLower(strings.TrimSpace(name))
	parallelSafeToolsMu.RLock()
	_, ok := parallelSafeTools[key]
	parallelSafeToolsMu.RUnlock()
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
	// Seq is the per-call event-sequence value allocated by the
	// dispatcher BEFORE invoking executeToolWithLifecycle. The agent
	// loop copies it onto the trace so the eventual tool:result fired
	// from publishNativeToolResultWithPayload carries the same Seq as
	// the matching tool:call / tool:error / tool:timeout from the
	// same execution.
	Seq uint64
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
func (e *Engine) executeToolCallsParallel(ctx context.Context, calls []provider.ToolCall, batchSize int, source Source, cache map[string]string, cacheMu *sync.Mutex, rangeIndex map[string][]readRangeEntry, seqs []uint64) []parallelToolResult {
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

	// seqFor returns the pre-allocated sequence for index idx; falls
	// back to an inline allocation when the caller passed a shorter
	// slice (defensive — keeps the dispatcher usable from older
	// call sites during incremental refactors).
	seqFor := func(idx int) uint64 {
		if idx < len(seqs) && seqs[idx] != 0 {
			return seqs[idx]
		}
		return e.allocToolEventSeq()
	}

	dispatch := func(idx int, c provider.ToolCall) {
		seq := seqFor(idx)
		if hit, ok := lookupToolCache(c, cache, cacheMu, rangeIndex); ok {
			// Cache hits don't go through the lifecycle, so no
			// tool:* lifecycle events fire — but the matching
			// tool:call has ALREADY been published by the batch
			// loop with this Seq, so we carry it forward to the
			// eventual tool:result emit. Subscribers that dedupe on
			// Seq still see a coherent (call, result) pair.
			out[idx] = parallelToolResult{Index: idx, Result: hit, Seq: seq}
			e.publishAgentLoopEvent("agent:tool:cache_hit", map[string]any{
				"name": c.Name,
			})
			return
		}
		callCtx := withToolEventSeq(ctx, seq)
		res, err := e.executeToolWithLifecycle(callCtx, c.Name, c.Input, source)
		out[idx] = parallelToolResult{Index: idx, Result: res, Err: err, Seq: seq}
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
		// Acquire-or-cancel: bare `sem <- struct{}{}` would block
		// the dispatch loop until an in-flight worker freed a slot,
		// even after ctx was cancelled. With cancellation-aware
		// acquire, the loop drains immediately when the caller
		// gives up — important on Ctrl+C with one slow tool still
		// running (its goroutine sees ctx.Err() via its own select
		// and returns), so the user doesn't wait for the slow tool
		// to finish just to get their prompt back.
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			out[i] = parallelToolResult{Index: i, Err: ctx.Err()}
			continue
		}
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
