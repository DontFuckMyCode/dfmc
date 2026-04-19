package engine

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/internal/tools"
)

// sleepTool is a test-only Tool that sleeps for a fixed duration so
// tests can assert real concurrency (not just "all ran to completion
// super fast"). When counter is non-nil it's incremented atomically on
// each invocation.
type sleepTool struct {
	name    string
	delay   time.Duration
	counter *int32
}

func (s *sleepTool) Name() string        { return s.name }
func (s *sleepTool) Description() string { return "test sleep tool" }
func (s *sleepTool) Execute(ctx context.Context, _ tools.Request) (tools.Result, error) {
	if s.counter != nil {
		atomic.AddInt32(s.counter, 1)
	}
	if s.delay > 0 {
		select {
		case <-ctx.Done():
			return tools.Result{}, ctx.Err()
		case <-time.After(s.delay):
		}
	}
	return tools.Result{Output: "slept " + s.delay.String()}, nil
}

func registerTestSleepTool(t *testing.T, eng *Engine, name string, delay time.Duration, counter *int32) {
	t.Helper()
	if eng.Tools == nil {
		t.Fatal("engine has no Tools registry — cannot register test tool")
	}
	// Parallel safety: route the test tool through parallelSafeTools so
	// the dispatcher will actually parallelize it. Revert on cleanup so
	// unrelated tests don't see the override.
	prev, had := parallelSafeTools[name]
	parallelSafeTools[name] = struct{}{}
	t.Cleanup(func() {
		if had {
			parallelSafeTools[name] = prev
		} else {
			delete(parallelSafeTools, name)
		}
	})
	eng.Tools.Register(&sleepTool{name: name, delay: delay, counter: counter})
}

func randomID(i int) string { return fmt.Sprintf("call-%d", i) }

func TestAllParallelSafe_ReadsOnly(t *testing.T) {
	calls := []provider.ToolCall{
		{Name: "read_file"},
		{Name: "list_dir"},
		{Name: "grep_codebase"},
	}
	if !allParallelSafe(calls) {
		t.Fatalf("batch of reads should be parallel-safe")
	}
}

func TestAllParallelSafe_MixedIsUnsafe(t *testing.T) {
	cases := [][]provider.ToolCall{
		{{Name: "read_file"}, {Name: "write_file"}},
		{{Name: "read_file"}, {Name: "edit_file"}},
		{{Name: "read_file"}, {Name: "run_command"}},
		{{Name: "read_file"}, {Name: "apply_patch"}},
	}
	for _, calls := range cases {
		if allParallelSafe(calls) {
			t.Fatalf("mixed batch must NOT be parallel-safe: %v", calls)
		}
	}
}

func TestAllParallelSafe_SingleCallIsSequential(t *testing.T) {
	// With one call there's nothing to parallelize and the caller
	// should take the simpler sequential path.
	if allParallelSafe([]provider.ToolCall{{Name: "read_file"}}) {
		t.Fatal("single-call batch should not claim parallel-safety")
	}
}

func TestAllParallelSafe_CaseInsensitive(t *testing.T) {
	calls := []provider.ToolCall{
		{Name: "Read_File"},
		{Name: "LIST_DIR"},
	}
	if !allParallelSafe(calls) {
		t.Fatal("tool name casing should not block parallel dispatch")
	}
}

func TestParallelBatchSize_DefaultsTo4(t *testing.T) {
	eng := &Engine{}
	if got := eng.parallelBatchSize(); got != 4 {
		t.Fatalf("bare engine should default to 4, got %d", got)
	}
}

// TestExecuteToolCallsParallel_RunsConcurrently verifies that a batch
// of safe calls actually runs in parallel — measured by running the
// test-only "slow_read" stub and checking that total wall time is less
// than the sum of individual delays. Without parallelism the same batch
// would serialize.
func TestExecuteToolCallsParallel_RunsConcurrently(t *testing.T) {
	eng := newTestEngine(t)

	// Register a test-only tool that sleeps to simulate IO-bound work.
	// Using read_file directly would be too fast to measure reliably.
	var counter int32
	registerTestSleepTool(t, eng, "__test_sleep_read", 40*time.Millisecond, &counter)

	calls := make([]provider.ToolCall, 4)
	for i := range calls {
		calls[i] = provider.ToolCall{Name: "__test_sleep_read", ID: randomID(i), Input: map[string]any{}}
	}

	start := time.Now()
	results := eng.executeToolCallsParallel(context.Background(), calls, 4, "agent", nil, nil)
	elapsed := time.Since(start)

	if len(results) != 4 {
		t.Fatalf("expected 4 results, got %d", len(results))
	}
	for i, r := range results {
		if r.Err != nil {
			t.Fatalf("result %d had error: %v", i, r.Err)
		}
		if r.Index != i {
			t.Fatalf("result order violated: slot %d has Index=%d", i, r.Index)
		}
	}
	// Sequential would be ~160ms (4 × 40ms). Parallel with size=4
	// should be roughly the slowest single call (~40ms) plus overhead.
	// Allow generous headroom on slow CI runners but assert a real win.
	if elapsed > 120*time.Millisecond {
		t.Fatalf("parallel dispatch was not concurrent: %s (expected ≤120ms for 4×40ms)", elapsed)
	}
	if atomic.LoadInt32(&counter) != 4 {
		t.Fatalf("every tool should have fired, got %d", atomic.LoadInt32(&counter))
	}
}

func TestExecuteToolCallsParallel_PreservesOrder(t *testing.T) {
	eng := newTestEngine(t)

	// Use a mix of instant and slow calls — the slow one finishes last
	// but the result slot for it must still match its issue index.
	registerTestSleepTool(t, eng, "__test_slow", 30*time.Millisecond, nil)
	registerTestSleepTool(t, eng, "__test_fast", 0, nil)

	calls := []provider.ToolCall{
		{Name: "__test_slow", ID: "a", Input: map[string]any{"tag": "first-slow"}},
		{Name: "__test_fast", ID: "b", Input: map[string]any{"tag": "second-fast"}},
		{Name: "__test_slow", ID: "c", Input: map[string]any{"tag": "third-slow"}},
	}

	results := eng.executeToolCallsParallel(context.Background(), calls, 3, "agent", nil, nil)
	for i, r := range results {
		if r.Index != i {
			t.Fatalf("result[%d].Index = %d, expected %d", i, r.Index, i)
		}
	}
}

func TestExecuteToolCallsParallel_BatchSizeZeroFallsBackToSequential(t *testing.T) {
	eng := newTestEngine(t)
	registerTestSleepTool(t, eng, "__test_sleep_seq", 0, nil)
	calls := []provider.ToolCall{
		{Name: "__test_sleep_seq", ID: "a"},
		{Name: "__test_sleep_seq", ID: "b"},
	}
	results := eng.executeToolCallsParallel(context.Background(), calls, 0, "agent", nil, nil)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for _, r := range results {
		if r.Err != nil {
			t.Fatalf("sequential fallback failed: %v", r.Err)
		}
	}
}
