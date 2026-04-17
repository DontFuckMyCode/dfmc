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
// to sequential execution (still via this function so callers have one
// code path), and very large values are capped at len(calls). Each tool
// runs through executeToolWithLifecycle so approval gating and hooks
// behave identically to the sequential path.
func (e *Engine) executeToolCallsParallel(ctx context.Context, calls []provider.ToolCall, batchSize int) []parallelToolResult {
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

	if batchSize == 1 {
		for i, call := range calls {
			if ctx.Err() != nil {
				out[i] = parallelToolResult{Index: i, Err: ctx.Err()}
				continue
			}
			res, err := e.executeToolWithLifecycle(ctx, call.Name, call.Input, "agent")
			out[i] = parallelToolResult{Index: i, Result: res, Err: err}
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
			res, err := e.executeToolWithLifecycle(ctx, c.Name, c.Input, "agent")
			out[idx] = parallelToolResult{Index: idx, Result: res, Err: err}
		}(i, call)
	}
	wg.Wait()
	return out
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
