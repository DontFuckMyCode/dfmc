// Scheduler: picks the next ready TODO from a Run.
//
// Phase 1 is sequential — at most one TODO runs at a time, so the
// scheduler is a simple "find the first Pending TODO whose deps are
// all Done". Phase 2 will extend ready() to return a *set* of TODOs
// for parallel dispatch with file_scope conflict checks; the picker
// surface here is shaped to support that without a rewrite.

package drive

import "time"

type SchedulerPolicy struct {
	MaxParallel int
	LaneCaps    map[string]int
	LaneOrder   []string
}

// readyNext returns the next TODO eligible to run, plus its index in
// the Todos slice. Returns (nil, -1) when nothing is ready (run is
// done OR everything pending is blocked behind a Blocked dep).
//
// Eligibility rules:
//  1. Status must be TodoPending or TodoRetrying (if scheduled time has passed).
//  2. Every depends_on id must point at a Done TODO. Pending deps
//     mean "not ready yet"; Blocked deps mean "will never be ready
//     via this TODO" (caller marks it Skipped).
//
// Ordering: input order is preserved (we don't reorder by priority).
// The planner controls execution sequence by emitting TODOs in the
// intended order.
// depsState is the tri-state outcome of dependency inspection. Used
// both by readyNext (to skip not-ready TODOs) and by skipBlocked (to
// mark TODOs Skipped when a dep is Blocked).
type depsState int

const (
	depsAllDone    depsState = iota // every dep is Done — ready to run
	depsHasPending                  // at least one dep is still Pending or Running
	depsHasBlocked                  // at least one dep is Blocked (caller should skip)
)

// depsReady classifies a single TODO's dependency state. When any dep
// is Blocked we report depsHasBlocked even if other deps are also
// pending — Blocked is sticky.
func depsReady(t *Todo, status map[string]TodoStatus) depsState {
	hasPending := false
	for _, dep := range t.DependsOn {
		switch status[dep] {
		case TodoBlocked, TodoSkipped:
			return depsHasBlocked
		case TodoPending, TodoRunning:
			hasPending = true
		case TodoDone:
			// keep scanning — another dep might be Blocked
		default:
			// Unknown status (shouldn't happen — validateTodos rejected
			// unknown ids — but treat conservatively as pending).
			hasPending = true
		}
	}
	if hasPending {
		return depsHasPending
	}
	return depsAllDone
}

// skipBlockedDescendants marks every Pending TODO whose deps include
// a Blocked or Skipped TODO as Skipped, with a reason naming the
// blocking dep. Run after every TODO transition so the run terminates
// promptly when a critical TODO blocks. Returns the list of newly-
// skipped TODO ids for event emission.
func skipBlockedDescendants(todos []Todo) []string {
	statusByID := make(map[string]TodoStatus, len(todos))
	for _, t := range todos {
		statusByID[t.ID] = t.Status
	}
	var skipped []string
	// Loop until a fixed point — skipping a TODO can newly-block its
	// own descendants. Bounded by len(todos) iterations since each
	// pass marks at least one TODO or terminates.
	for range todos {
		changed := false
		for i := range todos {
			t := &todos[i]
			if t.Status != TodoPending {
				if t.Status == TodoRetrying && !time.Now().Before(t.RetryScheduledAt) {
					// Retry scheduled time has passed — act on it.
				} else {
					continue
				}
			}
			for _, dep := range t.DependsOn {
				if s := statusByID[dep]; s == TodoBlocked || s == TodoSkipped {
					t.Status = TodoSkipped
					t.Error = "dependency " + dep + " was " + string(s)
					t.BlockedReason = BlockReasonDependencyBlocked
					statusByID[t.ID] = TodoSkipped
					skipped = append(skipped, t.ID)
					changed = true
					break
				}
			}
		}
		if !changed {
			return skipped
		}
	}
	return skipped
}

// runFinished reports whether every TODO has reached a terminal state
// (Done, Blocked, or Skipped). Used to break the driver loop and emit
// the final summary.
func runFinished(todos []Todo) bool {
	for _, t := range todos {
		// Retrying is not terminal — RetryScheduledAt controls when to act.
		switch t.Status {
		case TodoDone, TodoBlocked, TodoSkipped:
			// terminal — keep scanning
		case TodoRetrying:
			// not terminal, but will become Pending once scheduled time passes
			return false
		default:
			// Pending, Running, etc. — not terminal
			return false
		}
	}
	return true
}

// readyBatch returns up to `limit` TODOs that are ready to run RIGHT
// NOW under the parallel scheduler. Two filters apply on top of
// readyNext's "deps all Done" rule:
//
//  1. File-scope conflict with currently-running TODOs:
//     If a candidate's FileScope intersects any TODO already Running,
//     skip it — running both at the same time would race on the same
//     file (write_file vs edit_file from concurrent goroutines, plus
//     the read-before-mutate snapshot guard would invalidate one of
//     them mid-flight).
//
//  2. File-scope conflict within the picked batch:
//     A batch may contain at most one TODO per file. If T2 and T3
//     both declare `internal/foo.go` and both are ready, the batch
//     gets only the first (in input order) and T3 waits.
//
// Conservative when FileScope is empty: a TODO with no declared scope
// is treated as "could touch anything", so it runs alone (no other
// TODO joins its batch, no other TODO starts while it's running).
// That matches the planner contract — declare your scope to unlock
// parallelism — and means the worst case is sequential, never racy.
//
// Returns the indices of selected TODOs in run.Todos so the caller
// can mark them Running and dispatch under their original positions.
func readyBatch(todos []Todo, limit int) []int {
	return readyBatchWithPolicy(todos, SchedulerPolicy{}, limit)
}

func readyBatchWithPolicy(todos []Todo, policy SchedulerPolicy, limit int) []int {
	if limit <= 0 {
		return nil
	}
	if hasRunningExclusiveTodo(todos) {
		return nil
	}
	statusByID := make(map[string]TodoStatus, len(todos))
	for _, t := range todos {
		statusByID[t.ID] = t.Status
	}
	// Files held by currently-running TODOs. Empty FileScope normally
	// means "unknown — assume could touch anything", which we represent
	// as the sentinel scopeAny in busyScopes. Read-only TODOs are the
	// exception: when they carry no file scope they don't claim scopeAny
	// because they only inspect state and should not serialize unrelated
	// writers.
	busyScopes := collectScopes(todos, func(t Todo) bool { return t.Status == TodoRunning })
	runningLanes := countRunningLanes(todos)

	picked := make([]int, 0, limit)
	pickedScopes := scopeSet{}
	pickedLanes := map[string]int{}
	for _, i := range orderedCandidateIndices(todos, policy) {
		t := &todos[i]
		if t.Status != TodoPending {
			if t.Status == TodoRetrying && !time.Now().Before(t.RetryScheduledAt) {
				// Retry scheduled time has passed — treat as pickable.
			} else {
				continue
			}
		}
		if depsReady(t, statusByID) != depsAllDone {
			continue
		}
		if scopeConflicts(*t, busyScopes) {
			continue
		}
		lane := todoLane(*t)
		if cap := laneCap(policy, lane); cap > 0 && runningLanes[lane]+pickedLanes[lane] >= cap {
			continue
		}
		if todoNeedsExclusiveSlot(*t) {
			if len(picked) > 0 || hasAnyRunningTodo(todos) {
				continue
			}
		}
		if scopeConflicts(*t, pickedScopes) {
			continue
		}
		// A TODO with no declared scope (scopeAny) is treated as
		// "exclusive" — it runs alone in its batch. The same logic
		// already prevented joining a batch that has any picked TODO
		// with scope, so all we need here is: if our candidate has
		// no scope but the batch already has anyone, skip.
		if len(t.FileScope) == 0 && !todoReadOnly(*t) && len(picked) > 0 {
			continue
		}
		picked = append(picked, i)
		pickedScopes = mergeScopes(pickedScopes, *t)
		pickedLanes[lane]++
		if todoNeedsExclusiveSlot(*t) {
			break
		}
		// Same reverse rule: if the picked-just-now is unscoped, no
		// further TODOs can join — break early.
		if len(t.FileScope) == 0 && !todoReadOnly(*t) {
			break
		}
		if len(picked) >= limit {
			break
		}
	}
	return picked
}

func schedulerPolicyForRun(run *Run, fallbackMaxParallel int) SchedulerPolicy {
	policy := SchedulerPolicy{MaxParallel: fallbackMaxParallel}
	if policy.MaxParallel <= 0 {
		policy.MaxParallel = 1
	}
	if run == nil || run.Plan == nil {
		return policy
	}
	if run.Plan.MaxParallel > 0 {
		policy.MaxParallel = run.Plan.MaxParallel
	}
	if len(run.Plan.LaneCaps) > 0 {
		policy.LaneCaps = cloneWorkerCounts(run.Plan.LaneCaps)
	}
	if len(run.Plan.LaneOrder) > 0 {
		policy.LaneOrder = append([]string(nil), run.Plan.LaneOrder...)
	}
	return policy
}

func orderedCandidateIndices(todos []Todo, policy SchedulerPolicy) []int {
	out := make([]int, 0, len(todos))
	if len(policy.LaneOrder) == 0 {
		for i := range todos {
			out = append(out, i)
		}
		return out
	}
	buckets := map[string][]int{}
	var unknown []int
	for i := range todos {
		lane := todoLane(todos[i])
		if containsLane(policy.LaneOrder, lane) {
			buckets[lane] = append(buckets[lane], i)
			continue
		}
		unknown = append(unknown, i)
	}
	for _, lane := range policy.LaneOrder {
		out = append(out, buckets[lane]...)
	}
	out = append(out, unknown...)
	return out
}

