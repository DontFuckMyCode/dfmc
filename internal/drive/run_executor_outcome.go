// run_executor_outcome.go — per-TODO worker dispatch + outcome
// application. Sibling of run_executor.go which keeps the parallel
// dispatcher loop (executeLoop) plus the driveContextStopReason
// helper used by the drainer.
//
// Splitting dispatch + outcome out keeps run_executor.go scoped to
// "the scheduler tick" while this file owns "what does one worker do
// and what happens when it returns." dispatchTodo spawns the
// goroutine and is the only call site that mutates run.Todos under
// dispatch; applyOutcome is the only call site that mutates
// run.Todos when a worker returns. Both maintain the
// "main-loop-is-sole-writer-of-run.Todos" invariant; pulling them
// into one file makes that invariant easier to audit.

package drive

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// dispatchTodo marks the TODO at idx as Running, publishes the start
// event, and spawns a goroutine that calls runner.ExecuteTodo. The
// goroutine writes its result to the results channel. The buffered
// channel size matches MaxParallel so workers never block on send.
func (d *Driver) dispatchTodo(ctx context.Context, run *Run, idx int, results chan<- todoOutcome) {
	t := &run.Todos[idx]
	t.Status = TodoRunning
	t.StartedAt = time.Now()
	t.Attempts++
	// Note: we do NOT persist here. On restart Running TODOs are reset
	// to Pending anyway (Resume path), so the snapshot at dispatch time
	// is redundant and would cost an extra SQLite write per TODO.

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
		MaxSteps:          adaptiveBudgetFor(*t, run, d.cfg.AdaptiveStepBudget),
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
	// Spawned-TODO insertion shifts indices when applySpawnedTodos inserts
	// somewhere other than the slice tail. Today the planner contract pins
	// verification to the end so the captured Idx always still resolves to
	// the same TodoID, but verifying it before mutating costs one comparison
	// and prevents a future planner change from silently mis-routing
	// outcomes (e.g. stamping worker B's result onto worker A's slot).
	if res.TodoID != "" && t.ID != res.TodoID {
		recovered := -1
		for i := range run.Todos {
			if run.Todos[i].ID == res.TodoID {
				recovered = i
				break
			}
		}
		if recovered < 0 {
			return // TODO disappeared (shouldn't happen — TODOs are append-only)
		}
		t = &run.Todos[recovered]
	}
	t.EndedAt = res.Ended
	dur := t.EndedAt.Sub(t.StartedAt).Milliseconds()

	if res.Err != nil {
		class := FailureClassify(res.Err)
		// If max retries are exhausted, transition to Blocked.
		if t.Attempts > d.cfg.Retries {
			t.Status = TodoBlocked
			t.BlockedReason = BlockReasonRetriesExhausted
			t.Error = res.Err.Error()
			t.RetryScheduledAt = time.Time{}
			*consecutiveBlocked++
			// A retries-exhausted Blocked is a genuine executor-stage
			// failure — feed it to the breaker so a persistently sick
			// executor (across this run and future runs sharing this
			// Driver) trips the circuit, mirroring the spawn-invalid
			// path below and the planner stage.
			d.executorBreaker.Record(false)
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
		t.Status = TodoRetrying
		// Retries fire immediately. The scheduler picks zero-time Retrying
		// TODOs on the next pass via the !time.Now().Before check; the
		// executor loop has no sleep-until-due gate, so a non-zero
		// RetryScheduledAt with no other in-flight workers would trip the
		// "scheduler deadlock" path. Configurable backoff remains a future
		// feature.
		t.RetryScheduledAt = time.Time{}
		d.publish(EventTodoRetry, map[string]any{
			"run_id":     run.ID,
			"todo_id":    t.ID,
			"attempt":    t.Attempts,
			"last_error": res.Err.Error(),
			"class":      class.String(),
			"retry_at":   t.RetryScheduledAt.Format(time.RFC3339),
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
		d.executorBreaker.Record(false)
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
	d.executorBreaker.Record(true)
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
