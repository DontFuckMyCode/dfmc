package supervisor

// coordinator.go — supervisor lifecycle: the Supervisor struct + the
// Run / runImpl / Stop run-loop dispatcher. Pure data carriers
// (BudgetPool, RunResult, SupervisorStatus, ExecuteTaskFunc /
// ExecuteTaskRequest / ExecuteTaskResponse) live in
// coordinator_types.go.
//
// Companion siblings (extracted to keep this file scannable):
//
//   - coordinator_dispatch.go dispatchWorker + handleResult +
//                             propagateSkip + nextReadyTask
//   - coordinator_status.go   counts / countsSafe / countSkipped /
//                             countSkippedFromState / Status / counts
//                             FieldLocked + findTaskByID accessors

import (
	"context"
	"sync"
	"time"
)

// Supervisor owns the execution of a run's task graph. It receives an
// already-planned run (from BuildExecutionPlan) and a shared budget pool,
// then manages worker goroutines within those constraints.
//
// Supervisor does NOT import internal/engine or internal/drive — it is
// called back via ExecuteTaskFunc set by the engine-side adapter.
type Supervisor struct {
	run    *Run
	plan   *ExecutionPlan
	budget *BudgetPool

	workerFn ExecuteTaskFunc

	mu       sync.RWMutex
	inFlight int
	status   SupervisorStatus
	stopped  bool

	// per-task state guarded by mu
	taskState     map[string]TaskState
	taskAtts      map[string]int    // attempt count
	taskSummaries map[string]string // taskID → cumulative trajectory summary for dependent chains
}

var ErrNoWorker = "supervisor: no worker function registered — call SetWorkerFunc before Run"
var ErrStopped = "supervisor: stopped"
var ErrBudgetExhausted = "supervisor: budget exhausted"

// NewSupervisor builds a supervisor for the given run and plan with the
// given shared budget pool.
func NewSupervisor(run *Run, plan *ExecutionPlan, budget *BudgetPool) *Supervisor {
	s := &Supervisor{
		run:           run,
		plan:          plan,
		budget:        budget,
		taskState:     make(map[string]TaskState, len(plan.Tasks)),
		taskAtts:      make(map[string]int, len(plan.Tasks)),
		taskSummaries: make(map[string]string, len(plan.Tasks)),
		status: SupervisorStatus{
			Active: true,
			RunID:  run.ID,
		},
	}
	// Seed task states from the plan
	for _, pt := range plan.Tasks {
		s.taskState[pt.ID] = pt.State
		if pt.State == "" {
			s.taskState[pt.ID] = TaskPending
		}
		s.taskAtts[pt.ID] = 0
	}
	return s
}

// SetWorkerFunc registers the callback used to execute individual tasks.
func (s *Supervisor) SetWorkerFunc(fn ExecuteTaskFunc) {
	s.workerFn = fn
}

// Run blocks until all tasks in the plan complete, ctx is cancelled, or
// the budget is exhausted. It returns a RunResult summarizing the outcome.
func (s *Supervisor) Run(ctx context.Context) *RunResult {
	if s.workerFn == nil {
		return &RunResult{RunID: s.run.ID, Status: "failed", Reason: ErrNoWorker}
	}
	return s.runImpl(ctx)
}

func (s *Supervisor) runImpl(ctx context.Context) *RunResult {
	start := time.Now()
	maxParallel := s.plan.MaxParallel
	if maxParallel <= 0 {
		maxParallel = 3
	}

	// Build a task-index for quick state lookups
	taskIndex := make(map[string]Task, len(s.plan.Tasks))
	for _, pt := range s.plan.Tasks {
		taskIndex[pt.ID] = pt.Task
	}

	// Track deps satisfaction
	depDone := make(map[string]bool, len(s.plan.Tasks))
	roots := make(map[string]bool, len(s.plan.Roots))
	for _, r := range s.plan.Roots {
		roots[r] = true
		depDone[r] = s.taskState[r] == TaskDone
	}

	results := make(chan workerResult, maxParallel)
	backoffs := make(map[string]time.Time, len(s.plan.Tasks))

	// Main loop
	for {
		// Check stop conditions
		select {
		case <-ctx.Done():
			return &RunResult{
				RunID:    s.run.ID,
				Status:   "stopped",
				Reason:   ctx.Err().Error(),
				Duration: time.Since(start),
			}
		default:
		}

		// Drain any available results
		for s.inFlight > 0 {
			var result workerResult
			var ok bool
			select {
			case result, ok = <-results:
				if !ok {
					return &RunResult{
						RunID:    s.run.ID,
						Status:   "stopped",
						Reason:   ErrStopped,
						Duration: time.Since(start),
					}
				}
				s.handleResult(result, depDone, &backoffs)
				s.inFlight--
			default:
				goto dispatch
			}
		}

	dispatch:
		// Check terminal conditions
		done, failed, skipped := s.counts()
		if done+failed+skipped == len(s.plan.Tasks) {
			status := "done"
			reason := ""
			if failed > 0 {
				status = "failed"
				reason = "one or more tasks failed"
			}
			return &RunResult{
				RunID:        s.run.ID,
				Status:       status,
				Reason:       reason,
				TasksDone:    done,
				TasksFailed:  failed,
				TasksSkipped: skipped,
				Duration:     time.Since(start),
			}
		}

		// Budget check
		if s.budget.Remaining() == 0 {
			return &RunResult{
				RunID:        s.run.ID,
				Status:       "failed",
				Reason:       ErrBudgetExhausted,
				TasksDone:    done,
				TasksFailed:  failed,
				TasksSkipped: s.countSkipped(depDone),
				Duration:     time.Since(start),
			}
		}

		// Dispatch up to available slots
		available := maxParallel - s.inFlight
		for available > 0 && s.inFlight < maxParallel {
			taskID := s.nextReadyTask(depDone, backoffs, taskIndex)
			if taskID == "" {
				break
			}
			task := taskIndex[taskID]
			s.mu.Lock()
			s.taskState[taskID] = TaskRunning
			s.taskAtts[taskID]++
			attempts := s.taskAtts[taskID]
			s.inFlight++
			s.mu.Unlock()

			// Apply backoff if this is a retry
			bo := backoffs[taskID]
			if !bo.IsZero() && time.Now().Before(bo) {
				// Re-queue: push the backoff task back as pending and wait
				s.mu.Lock()
				s.taskState[taskID] = TaskPending
				s.inFlight--
				s.mu.Unlock()
				time.Sleep(time.Until(bo))
				continue
			}

			go s.dispatchWorker(ctx, task, attempts, results)
			available--
		}

		// If nothing can be dispatched and nothing is in flight, we're deadlocked
		if s.inFlight == 0 && available == maxParallel {
			return &RunResult{
				RunID:        s.run.ID,
				Status:       "failed",
				Reason:       "deadlock: no tasks ready and no tasks in flight",
				TasksDone:    s.countsSafe().Done,
				TasksSkipped: s.countSkippedFromState(),
				Duration:     time.Since(start),
			}
		}

		// Block waiting for next result if at capacity
		if s.inFlight >= maxParallel {
			select {
			case <-ctx.Done():
				return &RunResult{RunID: s.run.ID, Status: "stopped", Reason: ctx.Err().Error(), Duration: time.Since(start)}
			case result, ok := <-results:
				if !ok {
					return &RunResult{RunID: s.run.ID, Status: "stopped", Reason: ErrStopped, Duration: time.Since(start)}
				}
				s.handleResult(result, depDone, &backoffs)
				s.inFlight--
			}
		}
	}
}

// Stop signals all workers to cancel and marks the supervisor as stopped.
func (s *Supervisor) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = true
}
