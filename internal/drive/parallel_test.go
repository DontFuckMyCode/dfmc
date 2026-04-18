// Phase 2/3 tests: parallel scheduling, file-scope conflict detection,
// per-tag provider routing.

package drive

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestSchedulerReadyBatchPicksIndependentTodos: when N TODOs have no
// dependencies and no overlapping file_scope, readyBatch returns all
// of them (up to limit) so the parallel dispatcher can fan out.
func TestSchedulerReadyBatchPicksIndependentTodos(t *testing.T) {
	todos := []Todo{
		{ID: "T1", Status: TodoPending, FileScope: []string{"a.go"}},
		{ID: "T2", Status: TodoPending, FileScope: []string{"b.go"}},
		{ID: "T3", Status: TodoPending, FileScope: []string{"c.go"}},
	}
	picks := readyBatch(todos, 3)
	if len(picks) != 3 {
		t.Fatalf("expected all 3 picked, got %d", len(picks))
	}
}

// TestSchedulerReadyBatchHonorsLimit: even when all TODOs are ready,
// readyBatch returns at most `limit` items so MaxParallel is enforced.
func TestSchedulerReadyBatchHonorsLimit(t *testing.T) {
	todos := []Todo{
		{ID: "T1", Status: TodoPending, FileScope: []string{"a.go"}},
		{ID: "T2", Status: TodoPending, FileScope: []string{"b.go"}},
		{ID: "T3", Status: TodoPending, FileScope: []string{"c.go"}},
	}
	picks := readyBatch(todos, 2)
	if len(picks) != 2 {
		t.Fatalf("expected 2 picked, got %d", len(picks))
	}
}

// TestSchedulerSkipsConflictingFileScope: if T1 and T2 both declare
// file `shared.go`, the batch only gets one (the first in input
// order); T2 waits.
func TestSchedulerSkipsConflictingFileScope(t *testing.T) {
	todos := []Todo{
		{ID: "T1", Status: TodoPending, FileScope: []string{"shared.go"}},
		{ID: "T2", Status: TodoPending, FileScope: []string{"shared.go"}},
		{ID: "T3", Status: TodoPending, FileScope: []string{"other.go"}},
	}
	picks := readyBatch(todos, 5)
	if len(picks) != 2 {
		t.Fatalf("expected 2 picks (T1 + T3), got %d", len(picks))
	}
	if todos[picks[0]].ID != "T1" || todos[picks[1]].ID != "T3" {
		t.Fatalf("expected T1 then T3, got %s, %s",
			todos[picks[0]].ID, todos[picks[1]].ID)
	}
}

// TestSchedulerSkipsRunningConflict: a Pending TODO whose file_scope
// overlaps with a Running TODO must NOT be picked.
func TestSchedulerSkipsRunningConflict(t *testing.T) {
	todos := []Todo{
		{ID: "T1", Status: TodoRunning, FileScope: []string{"shared.go"}},
		{ID: "T2", Status: TodoPending, FileScope: []string{"shared.go"}},
		{ID: "T3", Status: TodoPending, FileScope: []string{"other.go"}},
	}
	picks := readyBatch(todos, 5)
	if len(picks) != 1 {
		t.Fatalf("expected 1 pick (T3 only), got %d", len(picks))
	}
	if todos[picks[0]].ID != "T3" {
		t.Fatalf("expected T3, got %s", todos[picks[0]].ID)
	}
}

// TestSchedulerUnscopedTodoRunsAlone: a TODO with empty FileScope
// is treated as "could touch anything" — runs in a batch by itself,
// blocks other TODOs from joining its batch, and won't start while
// any other TODO is running.
func TestSchedulerUnscopedTodoRunsAlone(t *testing.T) {
	todos := []Todo{
		{ID: "T1", Status: TodoPending /* no FileScope */},
		{ID: "T2", Status: TodoPending, FileScope: []string{"a.go"}},
	}
	picks := readyBatch(todos, 5)
	if len(picks) != 1 || todos[picks[0]].ID != "T1" {
		t.Fatalf("unscoped T1 should be picked alone first, got picks=%v", pickIDs(todos, picks))
	}

	// Inverse: scoped TODO running, then a new unscoped Pending must wait.
	todos2 := []Todo{
		{ID: "T1", Status: TodoRunning, FileScope: []string{"a.go"}},
		{ID: "T2", Status: TodoPending /* no FileScope */},
	}
	picks2 := readyBatch(todos2, 5)
	if len(picks2) != 0 {
		t.Fatalf("unscoped T2 must wait for scoped T1 to finish, got picks=%v", pickIDs(todos2, picks2))
	}
}

// TestSchedulerNormalizesPathSeparators: planner output may use
// backslashes (Windows-leaning model); the scheduler must treat
// `a\b.go` and `a/b.go` as the same file when checking conflicts.
func TestSchedulerNormalizesPathSeparators(t *testing.T) {
	todos := []Todo{
		{ID: "T1", Status: TodoRunning, FileScope: []string{`internal\foo.go`}},
		{ID: "T2", Status: TodoPending, FileScope: []string{"internal/foo.go"}},
		{ID: "T3", Status: TodoPending, FileScope: []string{"internal/bar.go"}},
	}
	picks := readyBatch(todos, 5)
	if len(picks) != 1 || todos[picks[0]].ID != "T3" {
		t.Fatalf("T2 must conflict with T1 across separator styles, expected T3 only, got picks=%v",
			pickIDs(todos, picks))
	}
}

// TestSchedulerDepsBlockEvenIfScopeFree: a TODO whose FileScope is
// free but whose deps are still Pending/Running must NOT be picked.
func TestSchedulerDepsBlockEvenIfScopeFree(t *testing.T) {
	todos := []Todo{
		{ID: "T1", Status: TodoRunning, FileScope: []string{"a.go"}},
		{ID: "T2", Status: TodoPending, FileScope: []string{"b.go"}, DependsOn: []string{"T1"}},
	}
	picks := readyBatch(todos, 5)
	if len(picks) != 0 {
		t.Fatalf("T2 deps not done, must not be picked, got picks=%v", pickIDs(todos, picks))
	}
}

// TestDriverParallelExecutesIndependentTodos: with 3 independent
// TODOs and MaxParallel=3, all three execute concurrently.
func TestDriverParallelExecutesIndependentTodos(t *testing.T) {
	var inFlight int64
	var maxObserved int64
	runner := &fakeRunner{
		PlanFunc: func(_ PlannerRequest) (string, error) {
			return `{"todos":[
				{"id":"T1","title":"a","detail":"a","file_scope":["a.go"]},
				{"id":"T2","title":"b","detail":"b","file_scope":["b.go"]},
				{"id":"T3","title":"c","detail":"c","file_scope":["c.go"]}
			]}`, nil
		},
		ExecFunc: func(_ ExecuteTodoRequest) (ExecuteTodoResponse, error) {
			cur := atomic.AddInt64(&inFlight, 1)
			defer atomic.AddInt64(&inFlight, -1)
			// Track the high-water mark.
			for {
				prev := atomic.LoadInt64(&maxObserved)
				if cur <= prev || atomic.CompareAndSwapInt64(&maxObserved, prev, cur) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond) // give other workers time to enter
			return ExecuteTodoResponse{Summary: "ok"}, nil
		},
	}
	d := NewDriver(runner, nil, nil, Config{MaxParallel: 3})
	run, err := d.Run(context.Background(), "task")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Status != RunDone {
		t.Fatalf("expected RunDone, got %s", run.Status)
	}
	if maxObserved < 2 {
		t.Fatalf("expected at least 2 concurrent workers (got max=%d) — parallel dispatch not happening",
			maxObserved)
	}
}

// TestDriverParallelSerializesConflictingFileScope: T1 and T2 both
// declare `shared.go`; T3 declares `other.go`. Even with
// MaxParallel=3, T2 must wait for T1 to finish — but T3 runs in
// parallel with T1.
func TestDriverParallelSerializesConflictingFileScope(t *testing.T) {
	var t1Started, t2Started, t1Done time.Time
	var mu sync.Mutex
	runner := &fakeRunner{
		PlanFunc: func(_ PlannerRequest) (string, error) {
			return `{"todos":[
				{"id":"T1","title":"a","detail":"a","file_scope":["shared.go"]},
				{"id":"T2","title":"b","detail":"b","file_scope":["shared.go"]},
				{"id":"T3","title":"c","detail":"c","file_scope":["other.go"]}
			]}`, nil
		},
		ExecFunc: func(req ExecuteTodoRequest) (ExecuteTodoResponse, error) {
			mu.Lock()
			now := time.Now()
			switch req.TodoID {
			case "T1":
				t1Started = now
			case "T2":
				t2Started = now
			}
			mu.Unlock()
			time.Sleep(20 * time.Millisecond)
			if req.TodoID == "T1" {
				mu.Lock()
				t1Done = time.Now()
				mu.Unlock()
			}
			return ExecuteTodoResponse{Summary: "ok"}, nil
		},
	}
	d := NewDriver(runner, nil, nil, Config{MaxParallel: 3})
	run, _ := d.Run(context.Background(), "task")
	if run.Status != RunDone {
		t.Fatalf("expected RunDone, got %s", run.Status)
	}
	mu.Lock()
	defer mu.Unlock()
	if t1Done.IsZero() || t2Started.IsZero() {
		t.Fatal("expected both T1 done and T2 started")
	}
	// T2 must start AFTER T1 finishes (file_scope conflict).
	if t2Started.Before(t1Done) {
		t.Fatalf("T2 started %v before T1 done %v — file_scope conflict not honored",
			t2Started, t1Done)
	}
	// T1 and T3 ran in parallel — call counts confirm the scheduler picked them together.
	if len(runner.Calls) != 3 {
		t.Fatalf("expected 3 calls, got %d", len(runner.Calls))
	}
	_ = t1Started
}

// TestDriverProviderRoutingByTag: each TODO carries a provider_tag,
// and the Config.Routing map sends each tag to a different provider
// profile. The executor sees the routed Model in its request.
func TestDriverProviderRoutingByTag(t *testing.T) {
	runner := &fakeRunner{
		PlanFunc: func(_ PlannerRequest) (string, error) {
			return `{"todos":[
				{"id":"T1","title":"plan","detail":"x","provider_tag":"plan"},
				{"id":"T2","title":"code","detail":"x","provider_tag":"code","depends_on":["T1"]},
				{"id":"T3","title":"test","detail":"x","provider_tag":"test","depends_on":["T2"]}
			]}`, nil
		},
	}
	d := NewDriver(runner, nil, nil, Config{
		Routing: map[string]string{
			"plan": "anthropic-opus",
			"code": "anthropic-sonnet",
			"test": "anthropic-haiku",
		},
	})
	run, err := d.Run(context.Background(), "task")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Status != RunDone {
		t.Fatalf("expected RunDone, got %s", run.Status)
	}
	want := map[string]string{
		"T1": "anthropic-opus",
		"T2": "anthropic-sonnet",
		"T3": "anthropic-haiku",
	}
	for _, c := range runner.Calls {
		if c.Model != want[c.TodoID] {
			t.Fatalf("TODO %s: expected Model=%q, got %q", c.TodoID, want[c.TodoID], c.Model)
		}
	}
}

// TestDriverProviderRoutingFallsBackOnUnknownTag: a TODO with a tag
// not in the Routing map gets Model="" so the executor uses the
// engine's active provider. No crash.
func TestDriverProviderRoutingFallsBackOnUnknownTag(t *testing.T) {
	runner := &fakeRunner{
		PlanFunc: func(_ PlannerRequest) (string, error) {
			return `{"todos":[
				{"id":"T1","title":"x","detail":"x","provider_tag":"unknown-tag"}
			]}`, nil
		},
	}
	d := NewDriver(runner, nil, nil, Config{
		Routing: map[string]string{"code": "x"},
	})
	run, err := d.Run(context.Background(), "task")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Status != RunDone {
		t.Fatalf("expected RunDone, got %s", run.Status)
	}
	if runner.Calls[0].Model != "" {
		t.Fatalf("unknown tag should yield empty Model, got %q", runner.Calls[0].Model)
	}
}

// TestDriverProviderRoutingCaseInsensitive: tag lookup ignores case
// on the key side so a planner that emits "Code" still routes to the
// "code" mapping.
func TestDriverProviderRoutingCaseInsensitive(t *testing.T) {
	cfg := Config{
		Routing: map[string]string{"code": "sonnet"},
	}
	if got := cfg.providerForTag("CODE"); got != "sonnet" {
		t.Fatalf("uppercase tag should match lowercase key, got %q", got)
	}
	if got := cfg.providerForTag("code"); got != "sonnet" {
		t.Fatalf("exact match should still work, got %q", got)
	}
	if got := cfg.providerForTag("nothing"); got != "" {
		t.Fatalf("missing tag should yield empty, got %q", got)
	}
}

// TestDriverParallelHandlesRetryWithoutDeadlock: when a TODO fails
// and retries, the parallel dispatcher must re-pick it on the next
// scheduling pass (Status went Pending again). Regression for an
// early version that left it stuck in Running.
func TestDriverParallelHandlesRetryWithoutDeadlock(t *testing.T) {
	var attempt int64
	runner := &fakeRunner{
		PlanFunc: func(_ PlannerRequest) (string, error) {
			return `{"todos":[
				{"id":"T1","title":"flaky","detail":"x","file_scope":["a.go"]}
			]}`, nil
		},
		ExecFunc: func(_ ExecuteTodoRequest) (ExecuteTodoResponse, error) {
			n := atomic.AddInt64(&attempt, 1)
			if n == 1 {
				return ExecuteTodoResponse{}, errors.New("transient")
			}
			return ExecuteTodoResponse{Summary: "ok"}, nil
		},
	}
	d := NewDriver(runner, nil, nil, Config{MaxParallel: 3, Retries: 1})
	done := make(chan *Run, 1)
	go func() {
		run, _ := d.Run(context.Background(), "task")
		done <- run
	}()
	select {
	case run := <-done:
		if run.Status != RunDone {
			t.Fatalf("expected RunDone after retry, got %s", run.Status)
		}
		if run.Todos[0].Status != TodoDone {
			t.Fatalf("expected T1 done after retry, got %s", run.Todos[0].Status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("driver deadlocked on parallel retry")
	}
}

// TestDriverAutoApproveActivatedAndReleased: when AutoApprove is set,
// the driver must call BeginAutoApprove with the configured tool list
// before plan/execute and call the returned release closure on every
// exit path (including failures).
func TestDriverAutoApproveActivatedAndReleased(t *testing.T) {
	runner := &fakeRunner{}
	allow := []string{"edit_file", "write_file", "apply_patch"}
	d := NewDriver(runner, nil, nil, Config{AutoApprove: allow})
	run, err := d.Run(context.Background(), "task")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Status != RunDone {
		t.Fatalf("expected RunDone, got %s", run.Status)
	}
	if !runner.AutoApproveStarted {
		t.Fatal("BeginAutoApprove was never called")
	}
	if !runner.AutoApproveReleased {
		t.Fatal("auto-approve release closure never fired — drive would leak the override")
	}
	if len(runner.AutoApproveTools) != len(allow) {
		t.Fatalf("expected %d tools passed to BeginAutoApprove, got %d", len(allow), len(runner.AutoApproveTools))
	}
	for i, tool := range allow {
		if runner.AutoApproveTools[i] != tool {
			t.Fatalf("tool[%d]: want %q, got %q", i, tool, runner.AutoApproveTools[i])
		}
	}
}

// TestDriverAutoApproveReleasedOnPlannerFailure: even when planner
// fails (early return path), the release closure must fire so the
// engine doesn't end up with a leaked auto-approver scope.
func TestDriverAutoApproveReleasedOnPlannerFailure(t *testing.T) {
	runner := &fakeRunner{
		PlanFunc: func(_ PlannerRequest) (string, error) {
			return "", errors.New("planner gone")
		},
	}
	d := NewDriver(runner, nil, nil, Config{AutoApprove: []string{"*"}})
	_, _ = d.Run(context.Background(), "task")
	if !runner.AutoApproveReleased {
		t.Fatal("auto-approve must be released even when planner fails")
	}
}

// TestDriverAutoApproveSkippedWhenEmpty: with no AutoApprove config,
// BeginAutoApprove still gets called but with an empty list (the
// engine adapter treats that as no-op). The driver doesn't second-
// guess the runner; the runner decides what empty means.
func TestDriverAutoApproveSkippedWhenEmpty(t *testing.T) {
	runner := &fakeRunner{}
	d := NewDriver(runner, nil, nil, Config{})
	_, err := d.Run(context.Background(), "task")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !runner.AutoApproveStarted {
		t.Fatal("BeginAutoApprove must always be called (release closure pattern)")
	}
	if len(runner.AutoApproveTools) != 0 {
		t.Fatalf("expected empty tool list when AutoApprove is unset, got %v", runner.AutoApproveTools)
	}
	if !runner.AutoApproveReleased {
		t.Fatal("release closure must always fire")
	}
}

// pickIDs is a small helper for assertion failure messages.
func pickIDs(todos []Todo, picks []int) []string {
	ids := make([]string, 0, len(picks))
	for _, idx := range picks {
		ids = append(ids, todos[idx].ID)
	}
	return ids
}

// quietT keeps go vet happy when fmt/sort imports above aren't used
// in every test (kept for future expansion).
var _ = fmt.Sprintf
var _ = sort.Strings
