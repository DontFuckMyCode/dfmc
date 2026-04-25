package supervisor

import (
	"context"
	"sync"
	"time"
)

// BudgetPool is the shared token/step budget all workers draw from.
// Each worker is allocated a lease when it starts; the pool is restored
// when the worker reports actual spend on completion.
type BudgetPool struct {
	TotalTokens int // 0 means unlimited
	UsedTokens  int
	TotalSteps  int // 0 means unlimited
	UsedSteps   int
}

// Remaining returns the currently available token budget.
func (b *BudgetPool) Remaining() int {
	if b.TotalTokens <= 0 {
		return -1 // unlimited
	}
	remaining := b.TotalTokens - b.UsedTokens
	if remaining < 0 {
		return 0
	}
	return remaining
}

// AllocTokens attempts to reserve tokens for a worker's lease.
// Returns the allocated amount (may be less than requested if pool is low).
func (b *BudgetPool) AllocTokens(tokens int) int {
	if b.TotalTokens <= 0 {
		return tokens // unlimited
	}
	avail := b.TotalTokens - b.UsedTokens
	if avail <= 0 {
		return 0
	}
	alloc := tokens
	if alloc > avail {
		alloc = avail
	}
	b.UsedTokens += alloc
	return alloc
}

// RestoredTokens returns tokens to the pool (called on worker completion).
func (b *BudgetPool) RestoreTokens(tokens int) {
	b.UsedTokens -= tokens
	if b.UsedTokens < 0 {
		b.UsedTokens = 0
	}
}

// RunResult is the final outcome of a supervisor run.
type RunResult struct {
	RunID       string
	Status      string // "done", "failed", "stopped"
	Reason      string
	TasksDone   int
	TasksFailed int
	TasksSkipped int
	TotalSteps  int
	TotalTokens int
	Duration    time.Duration
}

// SupervisorStatus describes the current state of a running supervisor.
type SupervisorStatus struct {
	Active   bool
	RunID    string
	InFlight int
	Done     int
	Failed   int
	Skipped  int
}

// ExecuteTaskFunc is the callback the supervisor invokes to execute one task.
// It is registered via SetWorkerFunc, typically by the engine-side adapter
// that bridges to the drive executor.
type ExecuteTaskFunc func(ctx context.Context, req ExecuteTaskRequest) (ExecuteTaskResponse, error)

// ExecuteTaskRequest is the input passed to a worker's ExecuteTaskFunc.
type ExecuteTaskRequest struct {
	TaskID           string
	ProviderTag      string
	Title            string
	Detail           string
	Brief            string
	Role             string
	Skills           []string
	Labels           []string
	Verification     string
	Model            string
	ProfileCandidates []string
	AllowedTools     []string
	MaxSteps         int
	TokenBudget      int // allocated from BudgetPool for this task
	// PriorSummary chains trajectory summaries from completed dependency tasks
	// into this task's prompt so the next worker knows what prior work occurred.
	PriorSummary string
}

// ExecuteTaskResponse is the output from a worker's ExecuteTaskFunc.
type ExecuteTaskResponse struct {
	Summary         string
	ToolCalls       int
	DurationMs      int64
	Provider        string
	Model           string
	Attempts        int
	TokensUsed      int
	FallbackUsed    bool
	FallbackReasons []string
}

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
	taskState    map[string]TaskState
	taskAtts     map[string]int  // attempt count
	taskSummaries map[string]string // taskID → cumulative trajectory summary for dependent chains
}

var ErrNoWorker = "supervisor: no worker function registered — call SetWorkerFunc before Run"
var ErrStopped = "supervisor: stopped"
var ErrBudgetExhausted = "supervisor: budget exhausted"

// NewSupervisor builds a supervisor for the given run and plan with the
// given shared budget pool.
func NewSupervisor(run *Run, plan *ExecutionPlan, budget *BudgetPool) *Supervisor {
	s := &Supervisor{
		run:          run,
		plan:         plan,
		budget:       budget,
		taskState:    make(map[string]TaskState, len(plan.Tasks)),
		taskAtts:     make(map[string]int, len(plan.Tasks)),
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

type workerResult struct {
	TaskID   string
	OK       bool
	Err      error
	Response ExecuteTaskResponse
}

func (s *Supervisor) dispatchWorker(ctx context.Context, task Task, attempt int, results chan<- workerResult) {
	// Build the task brief from Summary if present
	brief := task.Summary
	if brief == "" {
		brief = task.Title
	}

	// Collect trajectory summaries from completed dependencies
	var priorSummary string
	for _, dep := range task.DependsOn {
		s.mu.RLock()
		depState := s.taskState[dep]
		depSummary := s.taskSummaries[dep]
		s.mu.RUnlock()
		if depState == TaskDone && depSummary != "" {
			priorSummary += depSummary + "\n---\n"
		}
	}

	// Token budget for this task: estimate as 1/N of remaining, min 10k
	allocBudget := 0
	if s.budget.TotalTokens > 0 {
		n := len(s.plan.Tasks)
		if n > 0 {
			est := (s.budget.Remaining()+n-1)/n + s.budget.UsedTokens
			if est > s.budget.UsedTokens {
				allocBudget = est - s.budget.UsedTokens
			}
		}
		if allocBudget < 10000 {
			allocBudget = 10000
		}
	}

	req := ExecuteTaskRequest{
		TaskID:       task.ID,
		ProviderTag:  string(task.ProviderTag),
		Title:        task.Title,
		Detail:       task.Detail,
		Brief:        brief,
		Skills:       task.Skills,
		Labels:       task.Labels,
		Verification:  string(task.Verification),
		TokenBudget:  allocBudget,
		PriorSummary: priorSummary,
	}

	result, err := s.workerFn(ctx, req)
	results <- workerResult{
		TaskID:   task.ID,
		OK:       err == nil,
		Err:      err,
		Response: result,
	}
}

func (s *Supervisor) handleResult(r workerResult, depDone map[string]bool, backoffs *map[string]time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if r.OK {
		s.taskState[r.TaskID] = TaskDone
		depDone[r.TaskID] = true
		// Capture trajectory summary for dependent tasks
		if r.Response.Summary != "" {
			s.taskSummaries[r.TaskID] = r.Response.Summary
		}
	} else {
		// Classify failure
		fc := ClassifyFailure(r.Err, "")
		policy := DefaultRetryPolicy
		attempts := s.taskAtts[r.TaskID]

		if policy.ShouldRetry(fc, attempts) {
			// Schedule retry with backoff
			s.taskState[r.TaskID] = TaskPending
			(*backoffs)[r.TaskID] = time.Now().Add(policy.BackoffFor(attempts))
		} else if fc == FailureWaiting {
			s.taskState[r.TaskID] = TaskWaiting
		} else if fc == FailureExternalReview {
			s.taskState[r.TaskID] = TaskExternalReview
			s.propagateSkip(r.TaskID, TaskExternalReview)
		} else if fc == FailureEscalate {
			s.taskState[r.TaskID] = TaskBlocked
			s.propagateSkip(r.TaskID, TaskBlocked)
		} else {
			s.taskState[r.TaskID] = TaskBlocked
		}
	}

	// Propagate blocked state to dependents
	if s.taskState[r.TaskID] == TaskBlocked || s.taskState[r.TaskID] == TaskSkipped {
		s.propagateSkip(r.TaskID, s.taskState[r.TaskID])
	}
}

// propagateSkip marks all tasks that depend on blockedTask as Skipped.
func (s *Supervisor) propagateSkip(blockedTaskID string, _ TaskState) {
	for _, pt := range s.plan.Tasks {
		for _, dep := range pt.DependsOn {
			if dep == blockedTaskID {
				if s.taskState[pt.ID] == TaskPending || s.taskState[pt.ID] == TaskWaiting {
					s.taskState[pt.ID] = TaskSkipped
				}
			}
		}
	}
}

// nextReadyTask returns the ID of the next task that is pending, has all
// dependencies satisfied, and is not currently in flight or backed off.
// Returns "" if none are ready.
func (s *Supervisor) nextReadyTask(depDone map[string]bool, backoffs map[string]time.Time, taskIndex map[string]Task) string {
	// Iterate through plan layers in order
	for _, layerIDs := range s.plan.Layers {
		for _, id := range layerIDs {
			state := s.taskState[id]
			if state != TaskPending {
				continue
			}
			// Check deps satisfied
			task := taskIndex[id]
			allDone := true
			for _, dep := range task.DependsOn {
				if !depDone[dep] {
					allDone = false
					break
				}
			}
			if !allDone {
				continue
			}
			// Check backoff
			if bo, ok := backoffs[id]; ok && time.Now().Before(bo) {
				continue
			}
			return id
		}
	}
	return ""
}

type counts struct {
	Done    int
	Failed  int
	Skipped int
}

func (s *Supervisor) counts() (done, failed, skipped int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, state := range s.taskState {
		switch state {
		case TaskDone:
			done++
		case TaskBlocked:
			failed++
		case TaskSkipped:
			skipped++
		}
	}
	return
}

func (s *Supervisor) countsSafe() counts {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return counts{
		Done:    s.status.Done,
		Failed:  s.status.Failed,
		Skipped: s.status.Skipped,
	}
}

func (s *Supervisor) countSkipped(depDone map[string]bool) int {
	skipped := 0
	for id, state := range s.taskState {
		if state == TaskSkipped {
			skipped++
			continue
		}
		// Also count tasks whose deps are all done/failed but are still pending
		if state == TaskPending {
			task := findTaskByID(s.plan.Tasks, id)
			if task.ID != "" {
				allDone := true
				for _, dep := range task.DependsOn {
					if !depDone[dep] {
						allDone = false
						break
					}
				}
				if allDone && len(task.DependsOn) > 0 {
					skipped++
				}
			}
		}
	}
	return skipped
}

func (s *Supervisor) countSkippedFromState() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for _, state := range s.taskState {
		if state == TaskSkipped {
			n++
		}
	}
	return n
}

// Status returns the current supervisor state.
func (s *Supervisor) Status() SupervisorStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := s.countsFieldLocked()
	return SupervisorStatus{
		Active:   !s.stopped,
		RunID:    s.run.ID,
		InFlight: s.inFlight,
		Done:     n.Done,
		Failed:   n.Failed,
		Skipped:  n.Skipped,
	}
}

func (s *Supervisor) countsFieldLocked() counts {
	n := counts{}
	for _, state := range s.taskState {
		switch state {
		case TaskDone:
			n.Done++
		case TaskBlocked:
			n.Failed++
		case TaskSkipped:
			n.Skipped++
		}
	}
	return n
}

// Stop signals all workers to cancel and marks the supervisor as stopped.
func (s *Supervisor) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = true
}

func findTaskByID(tasks []PlannedTask, id string) Task {
	for _, t := range tasks {
		if t.ID == id {
			return t.Task
		}
	}
	return Task{}
}
