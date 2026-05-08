package supervisor

// coordinator_status.go — read-side accessors for the Supervisor:
// task-state counts, derived "should be skipped" math, the public
// Status() snapshot, and the small findTaskByID lookup. Companion
// siblings:
//
//   - coordinator.go          BudgetPool + lifecycle + run loop
//   - coordinator_dispatch.go dispatchWorker / handleResult /
//                             propagateSkip / nextReadyTask
//
// All of these read s.taskState under the mutex; the *Locked variant
// is the version called from inside an already-held lock. counts() is
// the live walk used by the run loop for "are we done yet?" checks;
// countsSafe() reads the snapshot copy on s.status for rendering.

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

func findTaskByID(tasks []PlannedTask, id string) Task {
	for _, t := range tasks {
		if t.ID == id {
			return t.Task
		}
	}
	return Task{}
}
