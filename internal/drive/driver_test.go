// Driver tests use a fake Runner — no real provider, no real engine.
// That keeps the tests fast and deterministic, and lets us pin the
// exact behavior the CLI/TUI depends on (event ordering, retry
// counts, brief stitching, deadline enforcement).

package drive

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRunner is the test double for drive.Runner. PlanFunc and
// ExecFunc are programmable per-test; both default to "succeed with
// trivial output" so tests only set what they care about.
type fakeRunner struct {
	mu sync.Mutex

	// PlanFunc returns a JSON envelope (the planner LLM "raw" text).
	// Tests override to inject malformed JSON, cycles, etc.
	PlanFunc func(req PlannerRequest) (string, error)

	// ExecFunc is called per TODO. Tests override to fail specific
	// TODOs or assert the brief seeded into req.
	ExecFunc func(req ExecuteTodoRequest) (ExecuteTodoResponse, error)

	// Calls log every ExecuteTodo invocation in order, so tests can
	// assert the scheduler walked TODOs in the expected sequence.
	Calls []ExecuteTodoRequest

	// AutoApprove* fields capture BeginAutoApprove invocations so
	// tests can assert the driver activated/released the scope as
	// expected. Started fires on the call; Released fires when the
	// returned closure runs.
	AutoApproveStarted  bool
	AutoApproveTools    []string
	AutoApproveReleased bool
}

func (f *fakeRunner) PlannerCall(_ context.Context, req PlannerRequest) (PlannerResponse, error) {
	if f.PlanFunc != nil {
		text, err := f.PlanFunc(req)
		return PlannerResponse{Text: text, Provider: "fake", Model: "fake-model"}, err
	}
	// Default: 2 trivial sequential TODOs.
	return PlannerResponse{
		Text: `{"todos":[
			{"id":"T1","title":"first","detail":"do first thing"},
			{"id":"T2","title":"second","detail":"do second thing","depends_on":["T1"]}
		]}`,
		Provider: "fake",
		Model:    "fake-model",
	}, nil
}

func (f *fakeRunner) ExecuteTodo(_ context.Context, req ExecuteTodoRequest) (ExecuteTodoResponse, error) {
	f.mu.Lock()
	f.Calls = append(f.Calls, req)
	f.mu.Unlock()
	if f.ExecFunc != nil {
		return f.ExecFunc(req)
	}
	return ExecuteTodoResponse{
		Summary:    "did " + req.TodoID,
		ToolCalls:  1,
		DurationMs: 10,
	}, nil
}

// BeginAutoApprove records the requested allow list and returns a
// release closure that flips the recorded state back. Tests inspect
// AutoApproveStarted / AutoApproveTools / AutoApproveReleased to
// verify the driver activated and released the scope correctly.
func (f *fakeRunner) BeginAutoApprove(tools []string) func() {
	f.mu.Lock()
	f.AutoApproveStarted = true
	f.AutoApproveTools = append([]string(nil), tools...)
	f.mu.Unlock()
	return func() {
		f.mu.Lock()
		f.AutoApproveReleased = true
		f.mu.Unlock()
	}
}

// captureEvents returns a Publisher that appends every event into the
// provided slice. Tests assert event ordering / count from this.
func captureEvents(events *[]string, mu *sync.Mutex) Publisher {
	return func(typ string, _ map[string]any) {
		mu.Lock()
		*events = append(*events, typ)
		mu.Unlock()
	}
}

func TestDriverRunsHappyPathSequentially(t *testing.T) {
	runner := &fakeRunner{}
	var events []string
	var mu sync.Mutex
	d := NewDriver(runner, nil, captureEvents(&events, &mu), Config{})

	run, err := d.Run(context.Background(), "build a thing")
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if run.Status != RunDone {
		t.Fatalf("expected RunDone, got %s (reason=%q)", run.Status, run.Reason)
	}
	done, blocked, skipped, _ := run.Counts()
	if done != 2 || blocked != 0 || skipped != 0 {
		t.Fatalf("expected 2 done, got done=%d blocked=%d skipped=%d", done, blocked, skipped)
	}

	// Scheduler must walk T1 before T2 (T2 depends on T1).
	if len(runner.Calls) != 2 {
		t.Fatalf("expected 2 ExecuteTodo calls, got %d", len(runner.Calls))
	}
	if runner.Calls[0].TodoID != "T1" || runner.Calls[1].TodoID != "T2" {
		t.Fatalf("expected T1 then T2, got %v", []string{runner.Calls[0].TodoID, runner.Calls[1].TodoID})
	}

	// T2 must see T1's brief in its prompt context.
	if !strings.Contains(runner.Calls[1].Brief, "T1") || !strings.Contains(runner.Calls[1].Brief, "did T1") {
		t.Fatalf("T2 brief missing T1 summary: %q", runner.Calls[1].Brief)
	}

	// Events must include start, plan, two todo:start/done, run:done.
	wantSubstr := []string{
		EventRunStart,
		EventPlanStart,
		EventPlanDone,
		EventTodoStart, EventTodoDone,
		EventTodoStart, EventTodoDone,
		EventRunDone,
	}
	for i, w := range wantSubstr {
		if i >= len(events) || events[i] != w {
			t.Fatalf("event[%d]: want %q, got %q (full=%v)", i, w, safeIdx(events, i), events)
		}
	}
}

func TestDriverRetryThenBlocks(t *testing.T) {
	runner := &fakeRunner{
		PlanFunc: func(_ PlannerRequest) (string, error) {
			return `{"todos":[{"id":"T1","title":"only","detail":"do it"}]}`, nil
		},
		ExecFunc: func(_ ExecuteTodoRequest) (ExecuteTodoResponse, error) {
			return ExecuteTodoResponse{}, errors.New("simulated failure")
		},
	}
	var events []string
	var mu sync.Mutex
	d := NewDriver(runner, nil, captureEvents(&events, &mu), Config{Retries: 1})

	run, _ := d.Run(context.Background(), "task")
	// One retry => 2 attempts, then blocked.
	if len(runner.Calls) != 2 {
		t.Fatalf("expected 2 attempts (initial + 1 retry), got %d", len(runner.Calls))
	}
	if run.Todos[0].Status != TodoBlocked {
		t.Fatalf("expected blocked, got %s", run.Todos[0].Status)
	}
	if run.Todos[0].Attempts != 2 {
		t.Fatalf("expected 2 attempts logged, got %d", run.Todos[0].Attempts)
	}
	// Run terminates with RunDone (single TODO blocked, no consecutive
	// blocks limit hit because MaxFailedTodos default is 3 and we only
	// blocked 1).
	if run.Status != RunDone {
		t.Fatalf("with 1 blocked TODO and MaxFailedTodos=3, expected RunDone, got %s", run.Status)
	}

	// Must have exactly one EventTodoRetry between the two attempts.
	retries := 0
	for _, e := range events {
		if e == EventTodoRetry {
			retries++
		}
	}
	if retries != 1 {
		t.Fatalf("expected 1 retry event, got %d (events=%v)", retries, events)
	}
}

func TestDriverMaxFailedTodosFails(t *testing.T) {
	runner := &fakeRunner{
		PlanFunc: func(_ PlannerRequest) (string, error) {
			return `{"todos":[
				{"id":"T1","title":"a","detail":"a"},
				{"id":"T2","title":"b","detail":"b"},
				{"id":"T3","title":"c","detail":"c"}
			]}`, nil
		},
		ExecFunc: func(_ ExecuteTodoRequest) (ExecuteTodoResponse, error) {
			return ExecuteTodoResponse{}, errors.New("always fail")
		},
	}
	d := NewDriver(runner, nil, nil, Config{Retries: 0, MaxFailedTodos: 2})
	run, _ := d.Run(context.Background(), "doomed task")
	if run.Status != RunFailed {
		t.Fatalf("expected RunFailed after 2 consecutive blocks, got %s (reason=%q)", run.Status, run.Reason)
	}
	if !strings.Contains(run.Reason, "max_failed_todos") {
		t.Fatalf("expected reason to cite max_failed_todos, got %q", run.Reason)
	}
}

func TestDriverDependencyChainSkipsDescendants(t *testing.T) {
	runner := &fakeRunner{
		PlanFunc: func(_ PlannerRequest) (string, error) {
			// T1 -> T2 -> T3, plus an independent T4 that should still run.
			return `{"todos":[
				{"id":"T1","title":"root","detail":"do root"},
				{"id":"T2","title":"mid","detail":"do mid","depends_on":["T1"]},
				{"id":"T3","title":"leaf","detail":"do leaf","depends_on":["T2"]},
				{"id":"T4","title":"indep","detail":"unrelated"}
			]}`, nil
		},
		ExecFunc: func(req ExecuteTodoRequest) (ExecuteTodoResponse, error) {
			if req.TodoID == "T1" {
				return ExecuteTodoResponse{}, errors.New("T1 fails hard")
			}
			return ExecuteTodoResponse{Summary: "ok"}, nil
		},
	}
	d := NewDriver(runner, nil, nil, Config{Retries: 0, MaxFailedTodos: 5})
	run, _ := d.Run(context.Background(), "chain task")

	statusOf := func(id string) TodoStatus {
		for _, t := range run.Todos {
			if t.ID == id {
				return t.Status
			}
		}
		return ""
	}
	if statusOf("T1") != TodoBlocked {
		t.Fatalf("T1 must be blocked, got %s", statusOf("T1"))
	}
	if statusOf("T2") != TodoSkipped {
		t.Fatalf("T2 must be skipped (T1 blocked), got %s", statusOf("T2"))
	}
	if statusOf("T3") != TodoSkipped {
		t.Fatalf("T3 must be skipped (T2 skipped), got %s", statusOf("T3"))
	}
	if statusOf("T4") != TodoDone {
		t.Fatalf("T4 must run independently, got %s", statusOf("T4"))
	}
}

func TestDriverPlannerErrorFailsRun(t *testing.T) {
	runner := &fakeRunner{
		PlanFunc: func(_ PlannerRequest) (string, error) {
			return "", errors.New("upstream model timeout")
		},
	}
	d := NewDriver(runner, nil, nil, Config{})
	run, err := d.Run(context.Background(), "task")
	if err == nil {
		t.Fatal("expected error on planner failure")
	}
	if run.Status != RunFailed {
		t.Fatalf("expected RunFailed, got %s", run.Status)
	}
	if !strings.Contains(run.Reason, "plan failed") {
		t.Fatalf("reason should mention plan failure, got %q", run.Reason)
	}
}

func TestDriverContextCancelStopsRun(t *testing.T) {
	runner := &fakeRunner{
		PlanFunc: func(_ PlannerRequest) (string, error) {
			return `{"todos":[
				{"id":"T1","title":"a","detail":"a"},
				{"id":"T2","title":"b","detail":"b"}
			]}`, nil
		},
		ExecFunc: func(req ExecuteTodoRequest) (ExecuteTodoResponse, error) {
			if req.TodoID == "T1" {
				return ExecuteTodoResponse{Summary: "ok"}, nil
			}
			return ExecuteTodoResponse{}, errors.New("never reached")
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	d := NewDriver(runner, nil, nil, Config{})
	// Cancel after the first TODO completes by hooking into ExecFunc.
	runner.ExecFunc = func(req ExecuteTodoRequest) (ExecuteTodoResponse, error) {
		if req.TodoID == "T1" {
			cancel()
			return ExecuteTodoResponse{Summary: "ok"}, nil
		}
		return ExecuteTodoResponse{Summary: "shouldnt"}, nil
	}
	run, _ := d.Run(ctx, "task")
	if run.Status != RunStopped {
		t.Fatalf("expected RunStopped on ctx cancel, got %s", run.Status)
	}
	// T1 done, T2 should remain Pending (not Running).
	statusByID := map[string]TodoStatus{}
	for _, t := range run.Todos {
		statusByID[t.ID] = t.Status
	}
	if statusByID["T1"] != TodoDone {
		t.Fatalf("T1 must be Done after cancel, got %s", statusByID["T1"])
	}
	if statusByID["T2"] != TodoPending {
		t.Fatalf("T2 must remain Pending after cancel, got %s", statusByID["T2"])
	}
}

func TestDriverDeadlineStopsRun(t *testing.T) {
	runner := &fakeRunner{
		PlanFunc: func(_ PlannerRequest) (string, error) {
			return `{"todos":[
				{"id":"T1","title":"slow","detail":"slow"},
				{"id":"T2","title":"never","detail":"unreached"}
			]}`, nil
		},
		ExecFunc: func(req ExecuteTodoRequest) (ExecuteTodoResponse, error) {
			if req.TodoID == "T1" {
				time.Sleep(50 * time.Millisecond) // pushes us past the deadline
				return ExecuteTodoResponse{Summary: "ok"}, nil
			}
			return ExecuteTodoResponse{}, fmt.Errorf("should not reach T2")
		},
	}
	d := NewDriver(runner, nil, nil, Config{MaxWallTime: 10 * time.Millisecond})
	run, _ := d.Run(context.Background(), "task")
	if run.Status != RunStopped {
		t.Fatalf("expected RunStopped on deadline, got %s (reason=%q)", run.Status, run.Reason)
	}
	if !strings.Contains(run.Reason, "max_wall_time") {
		t.Fatalf("expected reason to cite max_wall_time, got %q", run.Reason)
	}
}

func TestPlannerRejectsCycles(t *testing.T) {
	runner := &fakeRunner{
		PlanFunc: func(_ PlannerRequest) (string, error) {
			return `{"todos":[
				{"id":"A","title":"a","detail":"a","depends_on":["B"]},
				{"id":"B","title":"b","detail":"b","depends_on":["A"]}
			]}`, nil
		},
	}
	d := NewDriver(runner, nil, nil, Config{})
	run, err := d.Run(context.Background(), "task")
	if err == nil {
		t.Fatal("expected error on cyclic plan")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle error, got: %v", err)
	}
	if run.Status != RunFailed {
		t.Fatalf("expected RunFailed, got %s", run.Status)
	}
}

func TestPlannerRejectsUnknownDep(t *testing.T) {
	runner := &fakeRunner{
		PlanFunc: func(_ PlannerRequest) (string, error) {
			return `{"todos":[
				{"id":"T1","title":"a","detail":"a","depends_on":["GHOST"]}
			]}`, nil
		},
	}
	d := NewDriver(runner, nil, nil, Config{})
	_, err := d.Run(context.Background(), "task")
	if err == nil || !strings.Contains(err.Error(), "unknown id") {
		t.Fatalf("expected unknown-dep error, got: %v", err)
	}
}

func TestPlannerStripsCodeFences(t *testing.T) {
	runner := &fakeRunner{
		PlanFunc: func(_ PlannerRequest) (string, error) {
			return "```json\n{\"todos\":[{\"id\":\"T1\",\"title\":\"a\",\"detail\":\"a\"}]}\n```", nil
		},
	}
	d := NewDriver(runner, nil, nil, Config{})
	run, err := d.Run(context.Background(), "task")
	if err != nil {
		t.Fatalf("fenced JSON should parse, got: %v", err)
	}
	if run.Status != RunDone {
		t.Fatalf("expected RunDone, got %s", run.Status)
	}
}

func safeIdx(s []string, i int) string {
	if i < 0 || i >= len(s) {
		return "<missing>"
	}
	return s[i]
}
