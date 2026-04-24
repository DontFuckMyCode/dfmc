// Parallel dispatch loop for the Drive runner. Extracted from
// driver.go. Owns the executor side of the plan->execute->persist
// pipeline: ready-batch dispatch under MaxParallel, goroutine-per-
// TODO with a buffered results channel, per-result state updates,
// and the two-phase drain that stamps a terminal status without
// losing in-flight worker outcomes.

package drive

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// executeLoop is the parallel dispatcher: pick ready TODOs (up to
// cfg.MaxParallel), spawn each in a goroutine, wait for any to
// finish, apply the result, repeat. Sequential execution is just
// MaxParallel=1; the same code path handles both.
//
// Concurrency model:
//   - The main loop is the SOLE writer of run.Todos. Workers only
//     read the TODO they were dispatched with (passed by value into
//     the goroutine) and never touch run.Todos directly.
//   - Workers communicate back via a buffered results channel. The
//     main loop drains one result per iteration and updates state.
//   - When the result channel is empty AND no TODOs are inFlight AND
//     no TODOs are ready, the run is over (done or skipped tail).
//
// Why not a sync.WaitGroup: the loop has to be reactive to *any*
// worker completing so it can dispatch the next batch as soon as a
// slot frees up. A WaitGroup only signals "all done"; we need
// "first done", which is the channel idiom.
func (d *Driver) executeLoop(ctx context.Context, run *Run) {
	deadline := run.CreatedAt.Add(d.cfg.MaxWallTime)
	if !run.EndedAt.IsZero() {
		// Resume path may have cleared EndedAt; deadline is from
		// resume time when the original deadline already passed.
		deadline = time.Now().Add(d.cfg.MaxWallTime)
	}
	policy := schedulerPolicyForRun(run, d.cfg.MaxParallel)
	consecutiveBlocked := 0
	results := make(chan todoOutcome, policy.MaxParallel)
	inFlight := 0

	for {
		// Termination checks before scheduling. Any in-flight workers
		// will still publish their outcome; we drain those before
		// finalizing so the run record reflects every TODO that
		// actually executed.
		if err := ctx.Err(); err != nil {
			d.drainAndFinalize(ctx, run, results, inFlight, RunStopped,
				fmt.Sprintf("ctx cancelled: %v", err))
			return
		}
		if time.Now().After(deadline) {
			d.drainAndFinalize(ctx, run, results, inFlight, RunStopped,
				fmt.Sprintf("max_wall_time exceeded (%s)", d.cfg.MaxWallTime))
			return
		}
		if consecutiveBlocked >= d.cfg.MaxFailedTodos {
			d.drainAndFinalize(ctx, run, results, inFlight, RunFailed,
				fmt.Sprintf("max_failed_todos exceeded: %d consecutive blocks", consecutiveBlocked))
			return
		}

		// Dispatch as many ready TODOs as fit under MaxParallel.
		available := policy.MaxParallel - inFlight
		if available > 0 {
			picks := readyBatchWithPolicy(run.Todos, policy, available)
			for _, idx := range picks {
				d.dispatchTodo(ctx, run, idx, results)
				inFlight++
			}
		}

		if inFlight == 0 {
			// No workers running and nothing dispatched this pass.
			// Either we're done, or we're stuck behind Blocked deps.
			skipped := skipBlockedDescendants(run.Todos)
			for _, id := range skipped {
				d.publish(EventTodoSkipped, map[string]any{
					"run_id":  run.ID,
					"todo_id": id,
					"reason":  reasonByID(run.Todos, id),
				})
			}
			d.persist(run)
			if runFinished(run.Todos) {
				d.finalize(run, RunDone, "")
				return
			}
			// Re-scan after the skip pass — a previously-blocked
			// chain may now have ready siblings that don't depend on
			// the blocked branch.
			if len(readyBatchWithPolicy(run.Todos, policy, 1)) > 0 {
				continue
			}
			d.finalize(run, RunFailed, "scheduler deadlock — no ready TODO and no progress possible")
			return
		}

		// Wait for the next worker to finish. Block here is fine —
		// at least one goroutine is in flight, so a result will
		// arrive (or ctx will cancel through it). Honor ctx.Done in
		// the select so a parent cancel doesn't deadlock us.
		select {
		case <-ctx.Done():
			d.drainAndFinalize(ctx, run, results, inFlight, RunStopped,
				fmt.Sprintf("ctx cancelled: %v", ctx.Err()))
			return
		case res := <-results:
			inFlight--
			d.applyOutcome(run, res, &consecutiveBlocked)
		}
	}
}

// dispatchTodo marks the TODO at idx as Running, publishes the start
// event, and spawns a goroutine that calls runner.ExecuteTodo. The
// goroutine writes its result to the results channel. The buffered
// channel size matches MaxParallel so workers never block on send.
func (d *Driver) dispatchTodo(ctx context.Context, run *Run, idx int, results chan<- todoOutcome) {
	t := &run.Todos[idx]
	t.Status = TodoRunning
	t.StartedAt = time.Now()
	t.Attempts++
	d.persist(run)

	// Snapshot fields the worker needs so the goroutine doesn't
	// touch run.Todos. The brief is built from the *current* state
	// of completed TODOs (may include parallel-completed siblings
	// when the worker wakes up — that's fine; the brief is best-
	// effort context, not a strict ordering invariant).
	// The main loop is the sole writer of run.Todos; workers only see
	// this copied request payload and never mutate the shared slice.
	req := ExecuteTodoRequest{
		TodoID:            t.ID,
		ProviderTag:       t.ProviderTag,
		Title:             t.Title,
		Detail:            t.Detail,
		Brief:             briefSoFar(run.Todos, idx),
		Role:              executorRoleFor(t.WorkerClass),
		Skills:            append([]string(nil), t.Skills...),
		Labels:            append([]string(nil), t.Labels...),
		Verification:      t.Verification,
		Model:             d.cfg.providerForTag(t.ProviderTag),
		ProfileCandidates: nil,
		AllowedTools:      append([]string(nil), t.AllowedTools...),
		MaxSteps:          executorStepBudgetFor(*t),
	}
	d.publish(EventTodoStart, map[string]any{
		"run_id":           run.ID,
		"todo_id":          t.ID,
		"title":            t.Title,
		"attempt":          t.Attempts,
		"origin":           t.Origin,
		"kind":             t.Kind,
		"worker_class":     t.WorkerClass,
		"provider_tag":     t.ProviderTag,
		"routed":           t.ProviderTag != "" && d.cfg.Routing != nil,
		"profile_selected": req.Model,
		"max_steps":        req.MaxSteps,
		"providers":        req.ProfileCandidates,
		"skills":           t.Skills,
		"file_scope":       t.FileScope,
	})
	go func(idx int, req ExecuteTodoRequest, started time.Time, attempt int) {
		defer func() {
			if r := recover(); r != nil {
				results <- todoOutcome{
					Idx:     idx,
					TodoID:  req.TodoID,
					Err:     fmt.Errorf("todo panic: %v", r),
					Started: started,
					Ended:   time.Now(),
					Attempt: attempt,
				}
			}
		}()
		resp, err := d.runner.ExecuteTodo(ctx, req)
		results <- todoOutcome{
			Idx:     idx,
			TodoID:  req.TodoID,
			Resp:    resp,
			Err:     err,
			Started: started,
			Ended:   time.Now(),
			Attempt: attempt,
		}
	}(idx, req, t.StartedAt, t.Attempts)
}

// todoOutcome is the message a worker sends back to the main loop
// when its ExecuteTodo call returns. Carries enough state for the
// main loop to attribute the result to the right TODO without
// holding a pointer (which would dance with the slice's address
// stability across re-allocations — currently safe, but the
// channel-of-values pattern keeps it that way regardless).
type todoOutcome struct {
	Idx     int
	TodoID  string
	Resp    ExecuteTodoResponse
	Err     error
	Started time.Time
	Ended   time.Time
	Attempt int
}

// applyOutcome updates run.Todos with one worker's result and
// publishes the matching event. consecutiveBlocked is updated by
// pointer because Done resets it but Blocked increments it — the
// caller's loop reads it at the top of each iteration to decide
// whether to abort.
func (d *Driver) applyOutcome(run *Run, res todoOutcome, consecutiveBlocked *int) {
	if res.Idx < 0 || res.Idx >= len(run.Todos) {
		return // shouldn't happen; defensive
	}
	t := &run.Todos[res.Idx]
	t.EndedAt = res.Ended
	dur := t.EndedAt.Sub(t.StartedAt).Milliseconds()

	if res.Err != nil {
		class := FailureClassify(res.Err)
		if class == Fatal || t.Attempts > d.cfg.Retries {
			t.Status = TodoBlocked
			t.Error = res.Err.Error()
			if class == Fatal {
				t.BlockedReason = BlockReasonFatal
			} else {
				t.BlockedReason = BlockReasonRetriesExhausted
			}
			*consecutiveBlocked++
			d.persist(run)
			d.publish(EventTodoBlocked, map[string]any{
				"run_id":         run.ID,
				"todo_id":        t.ID,
				"error":          t.Error,
				"attempts":       t.Attempts,
				"class":          class.String(),
				"blocked_reason": string(t.BlockedReason),
			})
			return
		}
		t.Status = TodoPending
		d.publish(EventTodoRetry, map[string]any{
			"run_id":     run.ID,
			"todo_id":    t.ID,
			"attempt":    t.Attempts,
			"last_error": res.Err.Error(),
			"class":      class.String(),
		})
		d.persist(run)
		return
	}
	t.Status = TodoDone
	// Attach the retrieval snapshot so resume can reuse the same chunks.
	t.LastContext = res.Resp.LastContext
	brief, spawned, spawnErr := parseSpawnedTodos(res.Resp.Summary, *t, run.Todos)
	if spawnErr != nil {
		t.Status = TodoBlocked
		t.Error = "spawn_todos invalid: " + spawnErr.Error()
		t.BlockedReason = BlockReasonSpawnInvalid
		*consecutiveBlocked++
		d.persist(run)
		d.publish(EventTodoBlocked, map[string]any{
			"run_id":         run.ID,
			"todo_id":        t.ID,
			"error":          t.Error,
			"attempts":       t.Attempts,
			"blocked_reason": string(t.BlockedReason),
		})
		return
	}
	t.Brief = strings.TrimSpace(brief)
	var added []Todo
	if len(spawned) > 0 {
		added = applySpawnedTodos(run, *t, spawned, d.cfg.MaxTodos, d.cfg.MaxParallel)
	}
	*consecutiveBlocked = 0
	d.persist(run)
	d.publish(EventTodoDone, map[string]any{
		"run_id":           run.ID,
		"todo_id":          t.ID,
		"brief":            t.Brief,
		"duration_ms":      dur,
		"tool_calls":       res.Resp.ToolCalls,
		"parked":           res.Resp.Parked,
		"origin":           t.Origin,
		"kind":             t.Kind,
		"worker_class":     t.WorkerClass,
		"skills":           t.Skills,
		"spawned":          len(added),
		"provider":         res.Resp.Provider,
		"model":            res.Resp.Model,
		"attempts":         res.Resp.Attempts,
		"fallback":         res.Resp.FallbackUsed,
		"fallback_from":    res.Resp.FallbackFrom,
		"fallback_reasons": res.Resp.FallbackReasons,
		"profiles_tried":   res.Resp.FallbackChain,
	})
	if len(added) > 0 {
		d.publish(EventPlanAugment, map[string]any{
			"run_id":      run.ID,
			"added":       len(added),
			"source":      "worker",
			"parent_todo": t.ID,
			"todos":       planSummary(added),
		})
	}
}

// drainAndFinalize collects the outcomes of in-flight workers before
// stamping the final run status. Without this, a deadline trip or a
// ctx cancel while workers are still running would lose their results
// entirely — the goroutines would complete and write to a channel
// nobody reads. The channel is buffered to MaxParallel so workers
// never block on send.
//
// Two-phase drain: first non-blocking (pulls anything already queued
// up), then a bounded grace period (give in-flight workers a chance
// to flush). After the grace expires, any TODOs still Running stay
// Running in the persisted record — that's accurate, they WERE
// running when we stopped, and Resume will reset Running -> Pending
// so they get re-attempted on the next run.
//
// Why we can't just block until all workers return: workers depend
// on ctx through ExecuteTodo, but a malicious or buggy provider
// could ignore cancellation. We bound our wait so a single stuck
// worker can't keep the run from terminating.
func (d *Driver) drainAndFinalize(ctx context.Context, run *Run, results <-chan todoOutcome, inFlight int, status RunStatus, reason string) {
	consecutiveBlocked := 0 // applyOutcome takes a pointer; value is discarded after finalize

	// Phase 1: non-blocking drain of anything already queued. This
	// catches the common case where a worker finished concurrently
	// with the cancel/deadline trip and its result is already on the
	// channel. No grace period needed for these.
	for inFlight > 0 {
		select {
		case res := <-results:
			inFlight--
			d.applyOutcome(run, res, &consecutiveBlocked)
		default:
			goto graceDrain
		}
	}

graceDrain:
	// Phase 2: give the rest of the workers a bounded grace period
	// to return. We honor ctx.Done in case the parent has already
	// fully torn down, but only after the grace window — otherwise
	// a cancel that's already fired would short-circuit us before
	// any worker had a chance to flush.
	if inFlight > 0 {
		grace := time.NewTimer(d.cfg.DrainGraceWindow)
		defer grace.Stop()
		for inFlight > 0 {
			select {
			case res := <-results:
				inFlight--
				d.applyOutcome(run, res, &consecutiveBlocked)
			case <-grace.C:
				// Time's up — leave any still-running TODOs as Running
				// in the persisted record. Resume will reset them.
				d.finalize(run, status, reason)
				_ = ctx // ctx kept in signature for symmetry; future
				// extension may want to differentiate ctx-cancel vs
				// deadline drains.
				return
			}
		}
	}
	d.finalize(run, status, reason)
}
