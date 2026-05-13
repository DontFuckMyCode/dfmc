package supervisor

// coordinator_dispatch.go — task-dispatch + result-handling for the
// Supervisor's run loop. Companion siblings:
//
//   - coordinator.go        BudgetPool + RunResult / Status types +
//                           Supervisor lifecycle (NewSupervisor / Run /
//                           runImpl / Stop) + ExecuteTaskRequest /
//                           ExecuteTaskResponse worker contract
//   - coordinator_status.go counts / countsSafe / countSkipped /
//                           countSkippedFromState / Status / counts
//                           FieldLocked + findTaskByID accessors
//
// dispatchWorker spawns the goroutine that calls the registered
// ExecuteTaskFunc; it composes the prior-summary chain from upstream
// dependencies and assigns a per-task token slice from the shared
// budget. handleResult applies the failure-classification policy
// (retry-with-backoff vs waiting/external-review/blocked) and
// propagates skip status to dependents. nextReadyTask walks the
// plan layers picking the first pending task whose deps are all
// satisfied and is not currently in cooldown.

import (
	"context"
	"fmt"
	"time"
)

type workerResult struct {
	TaskID   string
	OK       bool
	Err      error
	Response ExecuteTaskResponse
}

func (s *Supervisor) dispatchWorker(ctx context.Context, task Task, attempt int, results chan<- workerResult) {
	// Panic shield: if the workerFn panics we must still decrement inFlight
	// and restore any allocated budget so the supervisor does not deadlock.
	var tokensUsed int
	defer func() {
		if r := recover(); r != nil {
			s.mu.Lock()
			s.inFlight--
			s.mu.Unlock()
			if tokensUsed > 0 {
				s.budget.RestoreTokens(tokensUsed)
			}
			results <- workerResult{
				TaskID: task.ID,
				OK:     false,
				Err:    fmt.Errorf("worker panic: %v", r),
			}
		}
	}()

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

	// Track tokens for deferred restoration on panic
	tokensUsed = allocBudget

	req := ExecuteTaskRequest{
		TaskID:       task.ID,
		ProviderTag:  string(task.ProviderTag),
		Title:        task.Title,
		Detail:       task.Detail,
		Brief:        brief,
		Skills:       task.Skills,
		Labels:       task.Labels,
		Verification: string(task.Verification),
		TokenBudget:  allocBudget,
		PriorSummary: priorSummary,
	}

	result, err := s.workerFn(ctx, req)

	// Defensively restore budget even in normal path to prevent any
	// double-counting if handleResult fails to be called.
	if tokensUsed > 0 {
		s.budget.RestoreTokens(tokensUsed)
		tokensUsed = 0
	}

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
			// Schedule retry with backoff. Do NOT mark depDone=true here —
			// dependents must wait for the retry to complete successfully.
			s.taskState[r.TaskID] = TaskPending
			(*backoffs)[r.TaskID] = time.Now().Add(policy.BackoffFor(attempts))
			depDone[r.TaskID] = false // invalidate any prior depDone signal
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
