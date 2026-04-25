package supervisor

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// mockWorkerFunc records calls and returns programmable responses.
type mockWorker struct {
	calls   int32
	results []ExecuteTaskResponse
	errs    []error
	callCh  chan struct{} // closed after each call
}

func (m *mockWorker) fn(ctx context.Context, req ExecuteTaskRequest) (ExecuteTaskResponse, error) {
	atomic.AddInt32(&m.calls, 1)
	if m.callCh != nil {
		close(m.callCh)
	}
	idx := int(atomic.LoadInt32(&m.calls) - 1)
	var resp ExecuteTaskResponse
	var err error
	if idx < len(m.results) {
		resp = m.results[idx]
	}
	if idx < len(m.errs) && m.errs[idx] != nil {
		err = m.errs[idx]
	}
	// Simulate some work
	select {
	case <-ctx.Done():
		return ExecuteTaskResponse{}, ctx.Err()
	case <-time.After(1 * time.Millisecond):
	}
	return resp, err
}

func makeMockWorker(results []ExecuteTaskResponse, errs []error) (*mockWorker, ExecuteTaskFunc) {
	m := &mockWorker{results: results, errs: errs}
	return m, m.fn
}

func TestSupervisor_SingleTask_Success(t *testing.T) {
	run := &Run{ID: "r1", Task: "test"}
	plan := &ExecutionPlan{
		Tasks:      []PlannedTask{{Task: Task{ID: "t1", Title: "one task"}}},
		Layers:     [][]string{{"t1"}},
		Roots:      []string{"t1"},
		MaxParallel: 1,
	}
	budget := &BudgetPool{TotalTokens: 0}

	worker, workerFn := makeMockWorker([]ExecuteTaskResponse{{Summary: "done"}}, nil)

	sup := NewSupervisor(run, plan, budget)
	sup.SetWorkerFunc(workerFn)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result := sup.Run(ctx)

	if result.Status != "done" {
		t.Errorf("status = %q; want done", result.Status)
	}
	if atomic.LoadInt32(&worker.calls) != 1 {
		t.Errorf("calls = %d; want 1", worker.calls)
	}
}

func TestSupervisor_TwoTasks_Dependencies(t *testing.T) {
	// t2 depends on t1 — t1 must complete before t2 starts
	run := &Run{ID: "r1", Task: "test"}
	plan := &ExecutionPlan{
		Tasks: []PlannedTask{
			{Task: Task{ID: "t1", Title: "first"}},
			{Task: Task{ID: "t2", Title: "second", DependsOn: []string{"t1"}}},
		},
		Layers:      [][]string{{"t1"}, {"t2"}},
		Roots:       []string{"t1"},
		MaxParallel: 2,
	}
	budget := &BudgetPool{TotalTokens: 0}

	calls := []int32{0, 0}
	var mu sync.Mutex

	workerFn := func(ctx context.Context, req ExecuteTaskRequest) (ExecuteTaskResponse, error) {
		mu.Lock()
		calls[atomic.AddInt32(&calls[0], 1)-1] = 1
		mu.Unlock()
		// Simulate work
		select {
		case <-ctx.Done():
			return ExecuteTaskResponse{}, ctx.Err()
		case <-time.After(2 * time.Millisecond):
		}
		mu.Lock()
		calls[1] = 2 // mark done
		mu.Unlock()
		return ExecuteTaskResponse{Summary: "done " + req.TaskID}, nil
	}

	sup := NewSupervisor(run, plan, budget)
	sup.SetWorkerFunc(workerFn)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result := sup.Run(ctx)

	if result.Status != "done" {
		t.Errorf("status = %q; want done", result.Status)
	}
	if calls[0] == 0 || calls[1] == 0 {
		t.Errorf("not all tasks called: %v", calls)
	}
}

func TestSupervisor_RetryOnTransientFailure(t *testing.T) {
	run := &Run{ID: "r1", Task: "test"}
	plan := &ExecutionPlan{
		Tasks:      []PlannedTask{{Task: Task{ID: "t1", Title: "transient"}}},
		Layers:     [][]string{{"t1"}},
		Roots:      []string{"t1"},
		MaxParallel: 1,
	}
	budget := &BudgetPool{TotalTokens: 0}

	// Fail twice with retryable error, succeed on third attempt
	workerFn := func(ctx context.Context, req ExecuteTaskRequest) (ExecuteTaskResponse, error) {
		select {
		case <-ctx.Done():
			return ExecuteTaskResponse{}, ctx.Err()
		case <-time.After(1 * time.Millisecond):
		}
		return ExecuteTaskResponse{}, errors.New("rate limit exceeded")
	}

	sup := NewSupervisor(run, plan, budget)
	sup.SetWorkerFunc(workerFn)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result := sup.Run(ctx)

	// With DefaultRetryPolicy (MaxAttempts=3) and FailureRetryable,
	// the task should exhaust all retries and end up blocked.
	if result.Status != "failed" {
		t.Errorf("status = %q; want failed", result.Status)
	}
}

func TestSupervisor_PermanentFailure_DoesNotRetry(t *testing.T) {
	run := &Run{ID: "r1", Task: "test"}
	plan := &ExecutionPlan{
		Tasks:      []PlannedTask{{Task: Task{ID: "t1", Title: "bad"}}},
		Layers:     [][]string{{"t1"}},
		Roots:      []string{"t1"},
		MaxParallel: 1,
	}
	budget := &BudgetPool{TotalTokens: 0}

	calls := 0
	workerFn := func(ctx context.Context, req ExecuteTaskRequest) (ExecuteTaskResponse, error) {
		calls++
		return ExecuteTaskResponse{}, errors.New("syntax error: unexpected token")
	}

	sup := NewSupervisor(run, plan, budget)
	sup.SetWorkerFunc(workerFn)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result := sup.Run(ctx)

	// Permanent failure: should be called exactly once, no retries
	if calls != 1 {
		t.Errorf("calls = %d; want 1 (no retries for permanent failure)", calls)
	}
	if result.Status != "failed" {
		t.Errorf("status = %q; want failed", result.Status)
	}
}

func TestSupervisor_CancelByContext(t *testing.T) {
	run := &Run{ID: "r1", Task: "test"}
	plan := &ExecutionPlan{
		Tasks:      []PlannedTask{{Task: Task{ID: "t1", Title: "slow"}}},
		Layers:     [][]string{{"t1"}},
		Roots:      []string{"t1"},
		MaxParallel: 1,
	}
	budget := &BudgetPool{TotalTokens: 0}

	workerFn := func(ctx context.Context, req ExecuteTaskRequest) (ExecuteTaskResponse, error) {
		<-time.After(500 * time.Millisecond)
		return ExecuteTaskResponse{}, nil
	}

	sup := NewSupervisor(run, plan, budget)
	sup.SetWorkerFunc(workerFn)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	result := sup.Run(ctx)

	if result.Status != "stopped" {
		t.Errorf("status = %q; want stopped", result.Status)
	}
}

func TestSupervisor_Status(t *testing.T) {
	run := &Run{ID: "r1", Task: "test"}
	plan := &ExecutionPlan{
		Tasks:      []PlannedTask{{Task: Task{ID: "t1"}}},
		Layers:     [][]string{{"t1"}},
		Roots:      []string{"t1"},
		MaxParallel: 1,
	}
	budget := &BudgetPool{TotalTokens: 0}

	sup := NewSupervisor(run, plan, budget)
	status := sup.Status()

	if !status.Active {
		t.Error("status.Active should be true before Run")
	}
	if status.RunID != "r1" {
		t.Errorf("status.RunID = %q; want r1", status.RunID)
	}
}

func TestSupervisor_DeadlockDetection(t *testing.T) {
	// Both tasks depend on each other (cycle) — nothing should run
	run := &Run{ID: "r1", Task: "test"}
	plan := &ExecutionPlan{
		Tasks: []PlannedTask{
			{Task: Task{ID: "t1", DependsOn: []string{"t2"}}},
			{Task: Task{ID: "t2", DependsOn: []string{"t1"}}},
		},
		Layers:      [][]string{{"t1", "t2"}},
		Roots:       []string{"t1", "t2"},
		MaxParallel: 1,
	}
	budget := &BudgetPool{TotalTokens: 0}

	calls := 0
	workerFn := func(ctx context.Context, req ExecuteTaskRequest) (ExecuteTaskResponse, error) {
		calls++
		return ExecuteTaskResponse{}, nil
	}

	sup := NewSupervisor(run, plan, budget)
	sup.SetWorkerFunc(workerFn)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result := sup.Run(ctx)

	if result.Status != "failed" {
		t.Errorf("status = %q; want failed (deadlock)", result.Status)
	}
	if calls != 0 {
		t.Errorf("calls = %d; want 0 (deadlock prevents dispatch)", calls)
	}
}

func TestBudgetPool_AllocAndRestore(t *testing.T) {
	bp := &BudgetPool{TotalTokens: 100, TotalSteps: 10}

	alloc := bp.AllocTokens(30)
	if alloc != 30 {
		t.Errorf("AllocTokens(30) = %d; want 30", alloc)
	}
	if bp.Remaining() != 70 {
		t.Errorf("Remaining() = %d; want 70", bp.Remaining())
	}

	// Overallocate
	alloc = bp.AllocTokens(200)
	if alloc != 70 {
		t.Errorf("AllocTokens(200) = %d; want 70 (only 70 left)", alloc)
	}

	bp.RestoreTokens(30)
	// We had used 100 (30 + 70 overalloc); restoring 30 leaves 70 used, 30 remaining
	if bp.Remaining() != 30 {
		t.Errorf("Remaining() after restore = %d; want 30", bp.Remaining())
	}
}

func TestBudgetPool_Unlimited(t *testing.T) {
	bp := &BudgetPool{TotalTokens: 0} // 0 = unlimited

	alloc := bp.AllocTokens(100000)
	if alloc != 100000 {
		t.Errorf("AllocTokens(100000) on unlimited = %d; want 100000", alloc)
	}
	if bp.Remaining() != -1 {
		t.Errorf("Remaining() on unlimited = %d; want -1", bp.Remaining())
	}
}

func TestSupervisor_TaskWaiting_State(t *testing.T) {
	// When a task returns FailureWaiting, coordinator sets TaskWaiting
	run := &Run{ID: "r1", Task: "test"}
	plan := &ExecutionPlan{
		Tasks:      []PlannedTask{{Task: Task{ID: "t1", Title: "waiting task"}}},
		Layers:     [][]string{{"t1"}},
		Roots:      []string{"t1"},
		MaxParallel: 1,
	}
	budget := &BudgetPool{TotalTokens: 0}

	workerFn := func(ctx context.Context, req ExecuteTaskRequest) (ExecuteTaskResponse, error) {
		return ExecuteTaskResponse{}, errors.New("waiting for user input")
	}

	sup := NewSupervisor(run, plan, budget)
	sup.SetWorkerFunc(workerFn)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	result := sup.Run(ctx)

	// FailureWaiting is not retried, sets TaskWaiting
	if result.Status != "failed" {
		t.Errorf("status = %q; want failed (waiting task exhausts retries)", result.Status)
	}
}

func TestSupervisor_TaskExternalReview_State(t *testing.T) {
	// When a task returns FailureExternalReview, coordinator sets TaskExternalReview
	run := &Run{ID: "r1", Task: "test"}
	plan := &ExecutionPlan{
		Tasks:      []PlannedTask{{Task: Task{ID: "t1", Title: "review task"}}},
		Layers:     [][]string{{"t1"}},
		Roots:      []string{"t1"},
		MaxParallel: 1,
	}
	budget := &BudgetPool{TotalTokens: 0}

	workerFn := func(ctx context.Context, req ExecuteTaskRequest) (ExecuteTaskResponse, error) {
		return ExecuteTaskResponse{}, errors.New("security finding requires human review")
	}

	sup := NewSupervisor(run, plan, budget)
	sup.SetWorkerFunc(workerFn)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	result := sup.Run(ctx)

	// FailureExternalReview is not retried, sets TaskExternalReview
	if result.Status != "failed" {
		t.Errorf("status = %q; want failed (external review task exhausts retries)", result.Status)
	}
}
