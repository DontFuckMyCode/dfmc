package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

// sleepTool is a test-only tool that sleeps for the configured duration and
// reports when it ran. Used to prove tool_batch_call actually fans calls out
// concurrently (wall-clock under N*sleep) and to verify result ordering even
// when calls complete out of order.
type sleepTool struct {
	nameStr  string
	sleep    time.Duration
	inFlight *int32
	peak     *int32
	order    *int32
}

func (s *sleepTool) Name() string        { return s.nameStr }
func (s *sleepTool) Description() string { return "sleep for a fixed duration" }
func (s *sleepTool) Execute(ctx context.Context, _ Request) (Result, error) {
	if s.inFlight != nil {
		now := atomic.AddInt32(s.inFlight, 1)
		defer atomic.AddInt32(s.inFlight, -1)
		for {
			p := atomic.LoadInt32(s.peak)
			if now <= p || atomic.CompareAndSwapInt32(s.peak, p, now) {
				break
			}
		}
	}
	select {
	case <-time.After(s.sleep):
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
	finish := atomic.AddInt32(s.order, 1)
	return Result{Output: fmt.Sprintf("%s:done#%d", s.nameStr, finish)}, nil
}

func newTestEngine(t *testing.T) (*Engine, string) {
	t.Helper()
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "hello.go"), []byte("package main\n// marker TODO\nfunc main(){}\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	eng := New(*config.DefaultConfig())
	return eng, tmp
}

func TestEngineRegistersMetaTools(t *testing.T) {
	eng, _ := newTestEngine(t)
	for _, name := range []string{"tool_search", "tool_help", "tool_call", "tool_batch_call"} {
		if _, ok := eng.Get(name); !ok {
			t.Fatalf("meta tool not registered: %s", name)
		}
	}
	meta := eng.MetaSpecs()
	if len(meta) != 4 {
		t.Fatalf("MetaSpecs() count: want 4, got %d", len(meta))
	}
	backend := eng.BackendSpecs()
	for _, s := range backend {
		if isMetaTool(s.Name) {
			t.Errorf("BackendSpecs should exclude meta: %s", s.Name)
		}
	}
}

func TestToolSearchReturnsRankedResults(t *testing.T) {
	eng, tmp := newTestEngine(t)
	res, err := eng.Execute(context.Background(), "tool_search", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"query": "grep"},
	})
	if err != nil {
		t.Fatalf("tool_search: %v", err)
	}
	if !strings.Contains(res.Output, "grep_codebase") {
		t.Fatalf("expected grep_codebase in search output: %q", res.Output)
	}
	// Meta tools must not appear in search results even if query matches them.
	if strings.Contains(res.Output, "tool_search") || strings.Contains(res.Output, "tool_call") {
		t.Fatalf("meta tools leaked into search output: %q", res.Output)
	}
	data, _ := res.Data["results"].([]map[string]any)
	if len(data) == 0 {
		t.Fatalf("expected results array, got %v", res.Data)
	}
}

func TestToolSearchRequiresQuery(t *testing.T) {
	eng, tmp := newTestEngine(t)
	_, err := eng.Execute(context.Background(), "tool_search", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{},
	})
	if err == nil {
		t.Fatal("expected error for missing query")
	}
}

func TestToolHelpReturnsSchema(t *testing.T) {
	eng, tmp := newTestEngine(t)
	res, err := eng.Execute(context.Background(), "tool_help", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"name": "read_file"},
	})
	if err != nil {
		t.Fatalf("tool_help: %v", err)
	}
	if !strings.Contains(res.Output, "Args:") {
		t.Fatalf("expected Args: section in help, got %q", res.Output)
	}
	schema, _ := res.Data["schema"].(map[string]any)
	if schema == nil {
		t.Fatalf("expected schema in data, got %v", res.Data)
	}
	props, _ := schema["properties"].(map[string]any)
	if _, ok := props["path"]; !ok {
		t.Fatalf("expected path in schema properties, got %v", props)
	}
}

func TestToolHelpUnknownTool(t *testing.T) {
	eng, tmp := newTestEngine(t)
	_, err := eng.Execute(context.Background(), "tool_help", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"name": "nope"},
	})
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
}

func TestToolCallDispatchesToBackend(t *testing.T) {
	eng, tmp := newTestEngine(t)
	res, err := eng.Execute(context.Background(), "tool_call", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"name": "read_file",
			"args": map[string]any{"path": "hello.go"},
		},
	})
	if err != nil {
		t.Fatalf("tool_call: %v", err)
	}
	if !strings.Contains(res.Output, "package main") {
		t.Fatalf("expected file contents, got %q", res.Output)
	}
}

func TestToolCallRefusesMetaTarget(t *testing.T) {
	eng, tmp := newTestEngine(t)
	_, err := eng.Execute(context.Background(), "tool_call", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"name": "tool_search",
			"args": map[string]any{"query": "read"},
		},
	})
	if err == nil {
		t.Fatal("expected error when calling meta via tool_call")
	}
	if !strings.Contains(err.Error(), "meta") {
		t.Fatalf("expected meta-tool refusal message, got %v", err)
	}
}

// TestToolCallAutoUnwrapsDoubleWrap pins the 2026-04-18 fix for the
// "tool_call cannot invoke meta tools (got tool_call)" loop. When the
// model emits {name:"tool_call", args:{name:"read_file", args:{...}}}
// — one canonical wrap too many — the dispatcher must peel one layer,
// dispatch the inner call, AND prepend a one-line hint so the model
// learns to drop the redundant wrapper next round. Without the unwrap
// the model just retries the same broken shape.
func TestToolCallAutoUnwrapsDoubleWrap(t *testing.T) {
	eng, tmp := newTestEngine(t)
	res, err := eng.Execute(context.Background(), "tool_call", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"name": "tool_call",
			"args": map[string]any{
				"name": "read_file",
				"args": map[string]any{"path": "hello.go"},
			},
		},
	})
	if err != nil {
		t.Fatalf("auto-unwrap should succeed, got error: %v", err)
	}
	if !strings.Contains(res.Output, "auto-unwrapped") {
		t.Fatalf("output must carry the unwrap hint so model learns, got: %s", res.Output)
	}
	if !strings.Contains(res.Output, "read_file") {
		t.Fatalf("hint must name the dispatched tool, got: %s", res.Output)
	}
	// The actual file contents must still be in the output (proves the
	// inner read_file ran end-to-end).
	if !strings.Contains(res.Output, "package main") {
		t.Fatalf("expected actual file contents after the hint, got: %s", res.Output)
	}
}

// TestToolCallTripleWrapStillRejected guards the safety bound: a
// {name:"tool_call",args:{name:"tool_call",args:{...}}} chain must NOT
// recurse forever — one unwrap is the limit. Otherwise the bug would
// shift from "loops on the wrong call" to "stack-overflows on a really
// confused call".
func TestToolCallTripleWrapAutoUnwraps(t *testing.T) {
	eng, tmp := newTestEngine(t)
	res, err := eng.Execute(context.Background(), "tool_call", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"name": "tool_call",
			"args": map[string]any{
				"name": "tool_call",
				"args": map[string]any{"name": "read_file", "args": map[string]any{"path": "hello.go"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("triple-wrap should auto-unwrap successfully, got %v", err)
	}
	if !strings.Contains(res.Output, "auto-unwrapped 2 redundant tool_call layer") {
		t.Fatalf("expected unwrap hint to mention depth, got %q", res.Output)
	}
	if !strings.Contains(res.Output, "package main") {
		t.Fatalf("expected actual file contents after triple unwrap, got %q", res.Output)
	}
}

func TestToolCallAcceptsStringifiedArgs(t *testing.T) {
	eng, tmp := newTestEngine(t)
	argsJSON, _ := json.Marshal(map[string]any{"path": "hello.go"})
	res, err := eng.Execute(context.Background(), "tool_call", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"name": "read_file",
			"args": string(argsJSON),
		},
	})
	if err != nil {
		t.Fatalf("tool_call with stringified args: %v", err)
	}
	if !strings.Contains(res.Output, "package main") {
		t.Fatalf("expected file contents from stringified args, got %q", res.Output)
	}
}

func TestToolBatchCallCollectsResults(t *testing.T) {
	eng, tmp := newTestEngine(t)
	if err := os.WriteFile(filepath.Join(tmp, "second.go"), []byte("package second\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	res, err := eng.Execute(context.Background(), "tool_batch_call", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"calls": []any{
				map[string]any{"name": "read_file", "args": map[string]any{"path": "hello.go"}},
				map[string]any{"name": "read_file", "args": map[string]any{"path": "second.go"}},
				map[string]any{"name": "read_file", "args": map[string]any{"path": "missing.go"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("tool_batch_call: %v", err)
	}
	arr, _ := res.Data["results"].([]map[string]any)
	if len(arr) != 3 {
		t.Fatalf("expected 3 result entries, got %d", len(arr))
	}
	// First two succeed.
	for _, i := range []int{0, 1} {
		if ok, _ := arr[i]["success"].(bool); !ok {
			t.Errorf("results[%d] should succeed, got %v", i, arr[i])
		}
	}
	// Third fails but the batch continues.
	if ok, _ := arr[2]["success"].(bool); ok {
		t.Errorf("results[2] should fail, got %v", arr[2])
	}
	if _, ok := arr[2]["error"]; !ok {
		t.Errorf("results[2] should expose error message, got %v", arr[2])
	}
}

func TestToolBatchCallRefusesMetaTarget(t *testing.T) {
	eng, tmp := newTestEngine(t)
	res, err := eng.Execute(context.Background(), "tool_batch_call", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"calls": []any{
				map[string]any{"name": "tool_search", "args": map[string]any{"query": "x"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("tool_batch_call: %v", err)
	}
	arr, _ := res.Data["results"].([]map[string]any)
	if len(arr) != 1 {
		t.Fatalf("expected 1 result, got %d", len(arr))
	}
	if ok, _ := arr[0]["success"].(bool); ok {
		t.Errorf("meta-target call should be refused, got %v", arr[0])
	}
}

func TestToolBatchCallRequiresCalls(t *testing.T) {
	eng, tmp := newTestEngine(t)
	_, err := eng.Execute(context.Background(), "tool_batch_call", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{},
	})
	if err == nil {
		t.Fatal("expected error for missing calls")
	}
}

// TestToolCallAcceptsToolAlias verifies that when a model emits `tool`
// instead of the schema-correct `name`, the single-dispatch path still
// executes the requested backend tool. Some third-party OpenAI-compatible
// endpoints do this because their fine-tune reproduces training-data
// shapes rather than our tool schema.
func TestToolCallAcceptsToolAlias(t *testing.T) {
	eng, tmp := newTestEngine(t)
	res, err := eng.Execute(context.Background(), "tool_call", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"tool":  "read_file",
			"input": map[string]any{"path": "hello.go"},
		},
	})
	if err != nil {
		t.Fatalf("tool_call with tool/input aliases: %v", err)
	}
	if !strings.Contains(res.Output, "package main") {
		t.Fatalf("expected file contents via alias dispatch, got %q", res.Output)
	}
}

// TestToolBatchCallAcceptsToolAlias covers the same fallback on the batch
// path: inner entries keyed `tool` still execute when `name` is absent.
func TestToolBatchCallAcceptsToolAlias(t *testing.T) {
	eng, tmp := newTestEngine(t)
	res, err := eng.Execute(context.Background(), "tool_batch_call", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"calls": []any{
				map[string]any{"tool": "read_file", "args": map[string]any{"path": "hello.go"}},
				map[string]any{"name": "read_file", "arguments": map[string]any{"path": "hello.go"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("tool_batch_call with aliases: %v", err)
	}
	arr, _ := res.Data["results"].([]map[string]any)
	if len(arr) != 2 {
		t.Fatalf("expected 2 results, got %d", len(arr))
	}
	for i, entry := range arr {
		if ok, _ := entry["success"].(bool); !ok {
			t.Fatalf("results[%d] should succeed via alias, got %v", i, entry)
		}
	}
}

// registerSleepers installs N sleepTool instances on the engine sharing the
// same in-flight counters so tests can assert concurrency without timing
// heuristics. Returns (peak concurrency observed pointer, completion-order
// counter pointer) plus the tool names in registration order.
func registerSleepers(eng *Engine, n int, sleep time.Duration) (*int32, *int32, []string) {
	var inFlight, peak, order int32
	names := make([]string, n)
	for i := range n {
		name := fmt.Sprintf("sleep_%d", i)
		names[i] = name
		eng.Register(&sleepTool{
			nameStr:  name,
			sleep:    sleep,
			inFlight: &inFlight,
			peak:     &peak,
			order:    &order,
		})
	}
	return &peak, &order, names
}

func buildBatchCallsForSleepers(names []string) []any {
	out := make([]any, len(names))
	for i, n := range names {
		out[i] = map[string]any{"name": n, "args": map[string]any{}}
	}
	return out
}

func TestToolBatchCallRunsInParallel(t *testing.T) {
	eng, tmp := newTestEngine(t)
	peak, _, names := registerSleepers(eng, 4, 40*time.Millisecond)

	start := time.Now()
	res, err := eng.Execute(context.Background(), "tool_batch_call", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"calls": buildBatchCallsForSleepers(names)},
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("tool_batch_call: %v", err)
	}
	if got := atomic.LoadInt32(peak); got < 2 {
		t.Fatalf("expected peak concurrency >= 2 with ParallelBatchSize=4, got %d", got)
	}
	// Sequential would take >= 4*40ms = 160ms. Parallel should finish well
	// under that. Leave generous slack for loaded CI.
	if elapsed >= 140*time.Millisecond {
		t.Fatalf("batch wall-clock %v suggests serial execution (expected well under 160ms)", elapsed)
	}
	arr, _ := res.Data["results"].([]map[string]any)
	if len(arr) != 4 {
		t.Fatalf("expected 4 results, got %d", len(arr))
	}
	// parallel metadata should surface the effective concurrency.
	if n, _ := res.Data["parallel"].(int); n < 2 {
		t.Fatalf("expected parallel >= 2 in Data, got %v", res.Data["parallel"])
	}
}

func TestToolBatchCallPreservesInputOrder(t *testing.T) {
	eng, tmp := newTestEngine(t)
	// Register three sleepers with staggered sleeps so completion order does
	// not match input order when run concurrently.
	_, _, names := registerSleepers(eng, 3, 0)
	// Replace with per-index durations.
	eng.Register(&sleepTool{nameStr: names[0], sleep: 30 * time.Millisecond, inFlight: new(int32), peak: new(int32), order: new(int32)})
	eng.Register(&sleepTool{nameStr: names[1], sleep: 10 * time.Millisecond, inFlight: new(int32), peak: new(int32), order: new(int32)})
	eng.Register(&sleepTool{nameStr: names[2], sleep: 20 * time.Millisecond, inFlight: new(int32), peak: new(int32), order: new(int32)})

	res, err := eng.Execute(context.Background(), "tool_batch_call", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"calls": buildBatchCallsForSleepers(names)},
	})
	if err != nil {
		t.Fatalf("tool_batch_call: %v", err)
	}
	arr, _ := res.Data["results"].([]map[string]any)
	if len(arr) != 3 {
		t.Fatalf("expected 3 results, got %d", len(arr))
	}
	for i, entry := range arr {
		if got, _ := entry["name"].(string); got != names[i] {
			t.Fatalf("results[%d].name = %q, want %q — output order must match input order", i, got, names[i])
		}
	}
	// The Output lines must also reflect input order (#1 is names[0], etc.)
	outLines := strings.Split(strings.TrimSpace(res.Output), "\n")
	for i, line := range outLines {
		prefix := fmt.Sprintf("#%d %s:", i+1, names[i])
		if !strings.HasPrefix(line, prefix) {
			t.Fatalf("Output line %d = %q, want prefix %q", i, line, prefix)
		}
	}
}

func TestToolBatchCallSequentialWhenLimitOne(t *testing.T) {
	tmp := t.TempDir()
	cfg := *config.DefaultConfig()
	cfg.Agent.ParallelBatchSize = 1
	eng := New(cfg)
	_, _, names := registerSleepers(eng, 3, 20*time.Millisecond)

	res, err := eng.Execute(context.Background(), "tool_batch_call", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"calls": buildBatchCallsForSleepers(names)},
	})
	if err != nil {
		t.Fatalf("tool_batch_call: %v", err)
	}
	if n, _ := res.Data["parallel"].(int); n != 1 {
		t.Fatalf("expected parallel=1 with ParallelBatchSize=1, got %v", res.Data["parallel"])
	}
	arr, _ := res.Data["results"].([]map[string]any)
	if len(arr) != 3 {
		t.Fatalf("expected 3 results, got %d", len(arr))
	}
	for i, entry := range arr {
		if ok, _ := entry["success"].(bool); !ok {
			t.Fatalf("results[%d] should succeed, got %v", i, entry)
		}
	}
}

// concurrentRunner counts peak parallelism across RunSubagent invocations so
// we can assert that tool_batch_call actually fans delegate_task out rather
// than serialising it through the tools engine.
type concurrentRunner struct {
	sleep    time.Duration
	inFlight int32
	peak     int32
}

func (r *concurrentRunner) RunSubagent(ctx context.Context, req SubagentRequest) (SubagentResult, error) {
	now := atomic.AddInt32(&r.inFlight, 1)
	defer atomic.AddInt32(&r.inFlight, -1)
	for {
		p := atomic.LoadInt32(&r.peak)
		if now <= p || atomic.CompareAndSwapInt32(&r.peak, p, now) {
			break
		}
	}
	select {
	case <-time.After(r.sleep):
	case <-ctx.Done():
		return SubagentResult{}, ctx.Err()
	}
	return SubagentResult{Summary: "ran: " + req.Task, ToolCalls: 1, DurationMs: int64(r.sleep / time.Millisecond)}, nil
}

// TestToolBatchCallFansOutDelegateTask proves the orchestration promise:
// tool_batch_call(delegate_task, delegate_task, ...) runs N sub-agents in
// parallel bounded by ParallelBatchSize. Without this fan-out the "multiple
// providers/models as subagents in parallel" design collapses into N
// sequential round-trips.
func TestToolBatchCallFansOutDelegateTask(t *testing.T) {
	cfg := *config.DefaultConfig()
	cfg.Agent.ParallelBatchSize = 4
	eng := New(cfg)
	runner := &concurrentRunner{sleep: 40 * time.Millisecond}
	eng.SetSubagentRunner(runner)

	calls := []any{}
	for i := range 4 {
		calls = append(calls, map[string]any{
			"name": "delegate_task",
			"args": map[string]any{"task": fmt.Sprintf("task-%d", i)},
		})
	}

	start := time.Now()
	res, err := eng.Execute(context.Background(), "tool_batch_call", Request{
		Params: map[string]any{"calls": calls},
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("tool_batch_call: %v", err)
	}
	if got := atomic.LoadInt32(&runner.peak); got < 2 {
		t.Fatalf("sub-agents did not run concurrently (peak=%d); expected at least 2", got)
	}
	// 4 calls × 40ms each = 160ms serial. With parallelism ≥2 we should beat
	// 100ms comfortably on any dev machine.
	if elapsed >= 120*time.Millisecond {
		t.Fatalf("fan-out too slow (%s); batch is likely running serially", elapsed)
	}
	arr, _ := res.Data["results"].([]map[string]any)
	if len(arr) != 4 {
		t.Fatalf("expected 4 delegate_task results, got %d", len(arr))
	}
	for i, entry := range arr {
		if ok, _ := entry["success"].(bool); !ok {
			t.Fatalf("results[%d] should succeed, got %v", i, entry)
		}
	}
}

// TestToolBatchCallFailureIsolation proves a per-call error does not cancel
// its siblings when they run concurrently.
func TestToolBatchCallFailureIsolation(t *testing.T) {
	eng, tmp := newTestEngine(t)
	_, _, names := registerSleepers(eng, 2, 20*time.Millisecond)

	res, err := eng.Execute(context.Background(), "tool_batch_call", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"calls": []any{
				map[string]any{"name": names[0], "args": map[string]any{}},
				map[string]any{"name": "does_not_exist", "args": map[string]any{}},
				map[string]any{"name": names[1], "args": map[string]any{}},
			},
		},
	})
	if err != nil {
		t.Fatalf("tool_batch_call: %v", err)
	}
	arr, _ := res.Data["results"].([]map[string]any)
	if len(arr) != 3 {
		t.Fatalf("expected 3 results, got %d", len(arr))
	}
	if ok, _ := arr[0]["success"].(bool); !ok {
		t.Fatalf("results[0] should succeed despite sibling failure, got %v", arr[0])
	}
	if ok, _ := arr[1]["success"].(bool); ok {
		t.Fatalf("results[1] should fail, got %v", arr[1])
	}
	if ok, _ := arr[2]["success"].(bool); !ok {
		t.Fatalf("results[2] should succeed despite sibling failure, got %v", arr[2])
	}
}

// Real-world failure (TUI 2026-04-18 session): the model called
// `tool_call` with no `name` field — typically because it nested the
// args wrong (e.g. {"tool":"...","args":{...}} with `tool` mistyped, or
// passed only an `args` blob). The pre-fix error was the bare string
// "name is required" which gave the model nothing to recover from, so
// it looped with the same bug. Post-fix the error names the keys it
// actually received and shows the canonical example so the next call
// self-corrects in a single round.
func TestToolCall_MissingNameReturnsActionableError(t *testing.T) {
	eng := New(*config.DefaultConfig())

	cases := []struct {
		label  string
		params map[string]any
	}{
		{"empty params", map[string]any{}},
		{"only args, no name", map[string]any{"args": map[string]any{"path": "main.go"}}},
		{"mistyped key", map[string]any{"toool": "read_file", "args": map[string]any{}}},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			_, err := eng.Execute(context.Background(), "tool_call", Request{Params: c.params})
			if err == nil {
				t.Fatal("expected an error when name is missing")
			}
			msg := err.Error()
			// Must call out the offending tool name so the model knows which
			// meta-tool failed (vs. the underlying backend tool).
			if !strings.Contains(msg, "tool_call") {
				t.Fatalf("error should mention tool_call, got %q", msg)
			}
			// Must include the canonical example so the model can copy
			// the right shape for the next call.
			if !strings.Contains(msg, `"name":"read_file"`) {
				t.Fatalf("error should show the canonical example, got %q", msg)
			}
			// Must show the keys the model ACTUALLY sent so it can
			// see the mismatch.
			if len(c.params) == 0 {
				if !strings.Contains(msg, "(empty)") {
					t.Fatalf("empty params should be reported as (empty), got %q", msg)
				}
			} else {
				for k := range c.params {
					if !strings.Contains(msg, k) {
						t.Fatalf("error should list received key %q, got %q", k, msg)
					}
				}
			}
		})
	}
}

// Real-world UX gap (TUI 2026-04-18): batched calls only showed
// "5 calls · 4 parallel · 5 ok" — opaque about WHAT each call did.
// previewBatchTarget pulls the most identifying arg out so downstream
// renderers can show "✓ read_file foo.go" instead.
func TestPreviewBatchTarget_PicksMostIdentifyingArg(t *testing.T) {
	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{"read_file uses path", map[string]any{"path": "foo.go", "line_start": 1}, "foo.go"},
		{"grep_codebase uses pattern", map[string]any{"pattern": "TODO", "glob": "*.go"}, "TODO"},
		{"glob uses pattern", map[string]any{"pattern": "**/*.ts"}, "**/*.ts"},
		{"run_command joins command + args (slice)", map[string]any{"command": "go", "args": []any{"build", "./..."}}, "go build ./..."},
		{"run_command joins command + args (string)", map[string]any{"command": "git", "args": "status --short"}, "git status --short"},
		{"empty args yields empty target", map[string]any{}, ""},
		{"unknown args fall through to empty", map[string]any{"some_random": "thing"}, ""},
		{"long path truncates", map[string]any{"path": strings.Repeat("a/", 50) + "deep.go"}, ""}, // sentinel — checked below
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := previewBatchTarget(c.args)
			if c.name == "long path truncates" {
				if !strings.HasSuffix(got, "...") {
					t.Fatalf("expected truncation suffix on overlong target, got %q", got)
				}
				if len(got) > 64 {
					t.Fatalf("truncated target should fit in 64 chars, got %d (%q)", len(got), got)
				}
				return
			}
			if got != c.want {
				t.Fatalf("want %q, got %q", c.want, got)
			}
		})
	}
}

func TestToolHelp_MissingNameReturnsActionableError(t *testing.T) {
	eng := New(*config.DefaultConfig())
	_, err := eng.Execute(context.Background(), "tool_help", Request{Params: map[string]any{}})
	if err == nil {
		t.Fatal("expected error when name is missing for tool_help")
	}
	msg := err.Error()
	if !strings.Contains(msg, "tool_help") {
		t.Fatalf("error should mention tool_help, got %q", msg)
	}
	if !strings.Contains(msg, `"name":"grep_codebase"`) {
		t.Fatalf("error should show the tool_help example, got %q", msg)
	}
}

func TestToolBatchCall_UsesCumulativeMetaBudgetPerTurn(t *testing.T) {
	eng, tmp := newTestEngine(t)
	ctx := SeedMetaToolBudget(context.Background())

	makeCalls := func(n int) []any {
		out := make([]any, 0, n)
		for range n {
			out = append(out, map[string]any{
				"name": "read_file",
				"args": map[string]any{"path": "hello.go"},
			})
		}
		return out
	}

	for i := 0; i < 2; i++ {
		if _, err := eng.Execute(ctx, "tool_batch_call", Request{
			ProjectRoot: tmp,
			Params:      map[string]any{"calls": makeCalls(32)},
		}); err != nil {
			t.Fatalf("batch %d should fit within the shared meta budget: %v", i+1, err)
		}
	}
	if _, err := eng.Execute(ctx, "tool_call", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"name": "read_file",
			"args": map[string]any{"path": "hello.go"},
		},
	}); err == nil || !strings.Contains(err.Error(), "meta tool budget exhausted") {
		t.Fatalf("expected cumulative budget exhaustion on the 65th planned call, got %v", err)
	}
}

func TestEnterMetaBudget_ConcurrentCallsStayWithinBudget(t *testing.T) {
	ctx := SeedMetaToolBudget(context.Background())
	state, _ := ctx.Value(metaBudgetKey{}).(*metaBudgetState)
	if state == nil {
		t.Fatal("expected seeded meta budget state")
	}

	start := make(chan struct{})
	successes := int32(0)
	errs := make(chan error, 20)

	for range 20 {
		go func() {
			<-start
			_, release, err := enterMetaBudget(ctx, 8)
			if err != nil {
				errs <- err
				return
			}
			atomic.AddInt32(&successes, 1)
			release()
			errs <- nil
		}()
	}

	close(start)
	for range 20 {
		<-errs
	}
	remainingSuccesses := atomic.LoadInt32(&successes)
	if remainingSuccesses != 8 {
		t.Fatalf("expected exactly 8 successful 8-call reservations within a 64-call budget, got %d", remainingSuccesses)
	}

	state.mu.Lock()
	if state.used != defaultMetaCallBudget {
		state.mu.Unlock()
		t.Fatalf("expected used budget to stop at %d before releases, got %d", defaultMetaCallBudget, state.used)
	}
	if state.depth != 0 {
		state.mu.Unlock()
		t.Fatalf("expected meta depth to return to 0 after releases, got %d", state.depth)
	}
	state.mu.Unlock()
}
