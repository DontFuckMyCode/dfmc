// Driver: the main plan -> execute -> persist loop.
//
// Run() is the only entry point. It is synchronous on purpose — the
// caller (CLI / TUI / web) decides whether to block or fire-and-forget
// in a goroutine. Events fire through the supplied Publisher (typically
// engine.EventBus.Publish) so the caller's UI can render progress
// without polling. Persistence is automatic: every state transition
// writes the Run blob back, so a crash or restart loses at most one
// in-flight transition.
//
// The loop deliberately does not call ctx.Done() in the hot path more
// often than necessary — checking only at the top of each TODO is
// enough because per-TODO ExecuteTodo calls already honor ctx
// cancellation through the engine sub-agent runner. That keeps the
// loop simple and the cancellation latency bounded by the longest
// TODO (which is also the longest sub-agent call).

package drive

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Publisher matches engine.EventBus.Publish without importing the
// engine package. The driver pushes drive:* events through it; nil
// is a valid value (events are silently dropped).
type Publisher func(eventType string, payload map[string]any)

// Driver wires a runner + store + publisher into one object so the
// Run method doesn't need a 6-parameter signature. Construct via
// NewDriver. All fields are required — nil runner panics on Run, nil
// store skips persistence (useful in tests).
type Driver struct {
	runner    Runner
	store     *Store
	publisher Publisher
	cfg       Config
}

const emptyTaskError = "drive task is empty; pass a non-empty task description"

// NewDriver wires the dependencies. cfg.Apply() runs internally so
// callers can pass a zero Config and get the defaults. Publisher may
// be nil; store may be nil for in-memory test runs.
func NewDriver(runner Runner, store *Store, publisher Publisher, cfg Config) *Driver {
	return &Driver{
		runner:    runner,
		store:     store,
		publisher: publisher,
		cfg:       cfg.Apply(),
	}
}

// NewRun allocates the persisted record for a brand-new drive invocation.
// Callers that need to hand a run ID back immediately can create and save
// the run first, then pass it to RunPrepared for execution.
func NewRun(task string) (*Run, error) {
	task = strings.TrimSpace(task)
	if task == "" {
		return nil, fmt.Errorf(emptyTaskError)
	}
	return &Run{
		ID:        newRunID(),
		Task:      task,
		Status:    RunPlanning,
		CreatedAt: time.Now(),
		Todos:     []Todo{},
	}, nil
}

// Run executes a complete drive: plan, then execute every TODO until
// the run reaches a terminal state. Returns the final Run record.
//
// Termination conditions:
//   - All TODOs reached a terminal state (Done/Blocked/Skipped) -> RunDone.
//   - cfg.MaxFailedTodos consecutive Blocked TODOs -> RunFailed.
//   - cfg.MaxWallTime exceeded -> RunStopped (resumable).
//   - ctx cancelled -> RunStopped (resumable).
//   - Planner returned no TODOs / failed -> RunFailed.
//
// The returned Run is always non-nil even on error; check Run.Status
// for the outcome and Run.Reason for the human-readable cause.
func (d *Driver) Run(ctx context.Context, task string) (*Run, error) {
	run, err := NewRun(task)
	if err != nil {
		return nil, err
	}
	return d.RunPrepared(ctx, run)
}

// RunPrepared executes a run record that has already been created (and
// optionally saved) by the caller. This lets HTTP/MCP surfaces return a
// stable run ID before the planner starts.
func (d *Driver) RunPrepared(ctx context.Context, run *Run) (retRun *Run, retErr error) {
	if ctx == nil {
		return nil, fmt.Errorf("drive.Driver: context must not be nil")
	}
	if d.runner == nil {
		return nil, fmt.Errorf("drive.Driver: runner is nil")
	}
	if run == nil {
		return nil, fmt.Errorf("drive.Driver: run is nil")
	}
	retRun = run
	run.Task = strings.TrimSpace(run.Task)
	if run.Task == "" {
		return nil, fmt.Errorf(emptyTaskError)
	}
	if strings.TrimSpace(run.ID) == "" {
		run.ID = newRunID()
	}
	if IsActive(run.ID) {
		return run, fmt.Errorf("run %q already active in this process", run.ID)
	}
	if run.CreatedAt.IsZero() {
		run.CreatedAt = time.Now()
	}
	run.Status = RunPlanning
	run.Reason = ""
	run.EndedAt = time.Time{}
	if run.Todos == nil {
		run.Todos = []Todo{}
	}
	defer func() {
		if r := recover(); r != nil {
			run.Status = RunFailed
			run.Reason = fmt.Sprintf("panic: %v", r)
			run.EndedAt = time.Now()
			d.persist(run)
			d.publish(EventRunFailed, map[string]any{
				"run_id": run.ID,
				"reason": run.Reason,
			})
			switch v := r.(type) {
			case error:
				retErr = fmt.Errorf("drive run panic: %w", v)
			default:
				retErr = fmt.Errorf("drive run panic: %v", v)
			}
			retRun = run
		}
	}()
	d.persist(run)

	// Wrap the caller's ctx so external Cancel(runID) calls can
	// interrupt the loop. register() stashes the cancel func keyed
	// by run.ID; defer unregister keeps the registry clean even
	// when the run finishes via a panic. The cancel itself is
	// deferred too — without it the wrapper goroutine would leak
	// on every successful run.
	cancelCtx, cancel := context.WithCancel(ctx)
	register(run.ID, run.Task, cancel)
	defer unregister(run.ID)
	defer cancel()
	ctx = cancelCtx

	d.publish(EventRunStart, map[string]any{"run_id": run.ID, "task": run.Task})

	// Activate the per-run auto-approve scope. Released on every
	// return path via defer so a panic in plan/execute doesn't leave
	// a wide-open approver for subsequent /chat or /ask calls.
	release := d.runner.BeginAutoApprove(d.cfg.AutoApprove)
	defer release()

	// --- Plan stage ---------------------------------------------------
	d.publish(EventPlanStart, map[string]any{"run_id": run.ID, "model": d.cfg.PlannerModel})
	todos, err := runPlanner(ctx, d.runner, run.Task, d.cfg.PlannerModel)
	if err != nil {
		run.Status = RunFailed
		run.Reason = fmt.Sprintf("plan failed: %v", err)
		run.EndedAt = time.Now()
		d.persist(run)
		d.publish(EventPlanFailed, map[string]any{"run_id": run.ID, "error": err.Error()})
		d.publish(EventRunFailed, map[string]any{"run_id": run.ID, "reason": run.Reason})
		return run, fmt.Errorf("plan failed: %w", err)
	}
	if len(todos) > d.cfg.MaxTodos {
		// Truncate noisily — surface the cap so the user sees why later
		// TODOs are missing, instead of silently dropping work.
		truncated := todos[d.cfg.MaxTodos:]
		todos = todos[:d.cfg.MaxTodos]
		d.publish(EventPlanFailed, map[string]any{
			"run_id":           run.ID,
			"warning":          "MaxTodos exceeded; truncated",
			"kept":             d.cfg.MaxTodos,
			"dropped":          len(truncated),
			"dropped_ids_head": collectIDsHead(truncated, 5),
		})
	}
	run.Todos = todos
	beforePlanCount := len(run.Todos)
	applySupervisorPlan(run, d.cfg.AutoSurvey, d.cfg.AutoVerify, d.cfg.MaxParallel)
	if len(run.Todos) > beforePlanCount {
		added := append([]Todo(nil), run.Todos[beforePlanCount:]...)
		d.publish(EventPlanAugment, map[string]any{
			"run_id": run.ID,
			"added":  len(added),
			"todos":  planSummary(added),
		})
	}
	run.Status = RunRunning
	d.persist(run)
	d.publish(EventPlanDone, map[string]any{
		"run_id":     run.ID,
		"todo_count": len(run.Todos),
		"todos":      planSummary(run.Todos),
		"plan":       run.Plan,
	})

	d.executeLoop(ctx, run)
	return run, nil
}

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
		"run_id":       run.ID,
		"todo_id":      t.ID,
		"title":        t.Title,
		"attempt":      t.Attempts,
		"origin":       t.Origin,
		"kind":         t.Kind,
		"worker_class": t.WorkerClass,
		"max_steps":    req.MaxSteps,
		"providers":    req.ProfileCandidates,
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
		if t.Attempts <= d.cfg.Retries {
			t.Status = TodoPending
			d.publish(EventTodoRetry, map[string]any{
				"run_id":     run.ID,
				"todo_id":    t.ID,
				"attempt":    t.Attempts,
				"last_error": res.Err.Error(),
			})
			d.persist(run)
			return
		}
		t.Status = TodoBlocked
		t.Error = res.Err.Error()
		*consecutiveBlocked++
		d.persist(run)
		d.publish(EventTodoBlocked, map[string]any{
			"run_id":   run.ID,
			"todo_id":  t.ID,
			"error":    res.Err.Error(),
			"attempts": t.Attempts,
		})
		return
	}
	t.Status = TodoDone
	brief, spawned, spawnErr := parseSpawnedTodos(res.Resp.Summary, *t, run.Todos)
	if spawnErr != nil {
		t.Status = TodoBlocked
		t.Error = "spawn_todos invalid: " + spawnErr.Error()
		*consecutiveBlocked++
		d.persist(run)
		d.publish(EventTodoBlocked, map[string]any{
			"run_id":   run.ID,
			"todo_id":  t.ID,
			"error":    t.Error,
			"attempts": t.Attempts,
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
		"run_id":         run.ID,
		"todo_id":        t.ID,
		"brief":          t.Brief,
		"duration_ms":    dur,
		"tool_calls":     res.Resp.ToolCalls,
		"parked":         res.Resp.Parked,
		"origin":         t.Origin,
		"kind":           t.Kind,
		"worker_class":   t.WorkerClass,
		"spawned":        len(added),
		"provider":       res.Resp.Provider,
		"model":          res.Resp.Model,
		"attempts":       res.Resp.Attempts,
		"fallback":       res.Resp.FallbackUsed,
		"fallback_from":  res.Resp.FallbackFrom,
		"fallback_reasons": res.Resp.FallbackReasons,
		"profiles_tried": res.Resp.FallbackChain,
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

// Resume re-enters a previously-stopped or in-progress run. Pending
// and Running TODOs are reset to Pending (Running ones got interrupted
// mid-execution, safest to retry) and the loop picks up from there.
// Done/Blocked/Skipped status is preserved.
//
// Returns ErrRunFinished if the run is already in a terminal state —
// callers should distinguish that from a real load failure.
func (d *Driver) Resume(ctx context.Context, runID string) (*Run, error) {
	if ctx == nil {
		return nil, fmt.Errorf("drive.Driver: context must not be nil")
	}
	if d.store == nil {
		return nil, fmt.Errorf("drive.Resume: persistence is disabled (no store)")
	}
	run, err := d.store.Load(runID)
	if err != nil {
		return nil, fmt.Errorf("load run %q: %w", runID, err)
	}
	if run == nil {
		return nil, fmt.Errorf("run %q not found", runID)
	}
	if IsActive(runID) {
		return run, fmt.Errorf("run %q already active in this process", runID)
	}
	switch run.Status {
	case RunDone, RunFailed:
		return run, fmt.Errorf("run %q already terminal (status=%s)", runID, run.Status)
	}
	for i := range run.Todos {
		if run.Todos[i].Status == TodoRunning {
			run.Todos[i].Status = TodoPending
		}
	}
	run.Status = RunRunning
	run.EndedAt = time.Time{}
	run.Reason = ""
	d.persist(run)
	// Same registry hook as Run(). Use the original run ID so
	// `dfmc drive stop <id>` works on a resumed run too.
	cancelCtx, cancel := context.WithCancel(ctx)
	register(run.ID, run.Task, cancel)
	defer unregister(run.ID)
	defer cancel()
	ctx = cancelCtx

	d.publish(EventRunStart, map[string]any{
		"run_id":  run.ID,
		"task":    run.Task,
		"resumed": true,
	})

	// Same auto-approve scope as Run() — released on return.
	release := d.runner.BeginAutoApprove(d.cfg.AutoApprove)
	defer release()

	// Phase 1 used to copy the loop body here verbatim. Now that
	// executeLoop reads its deadline from `run.CreatedAt + MaxWallTime`
	// (resetting to time.Now() when EndedAt was already cleared by
	// the resume path above), Resume can call straight into it.
	d.executeLoop(ctx, run)
	return run, nil
}

// finalize stamps EndedAt + status, persists, and publishes the
// matching done/stopped/failed event. Single funnel so the run record
// always reaches a consistent terminal state regardless of which
// branch decided the run is over.
func (d *Driver) finalize(run *Run, status RunStatus, reason string) {
	run.Status = status
	run.Reason = reason
	run.EndedAt = time.Now()
	d.persist(run)
	done, blocked, skipped, _ := run.Counts()
	payload := map[string]any{
		"run_id":      run.ID,
		"status":      string(status),
		"done":        done,
		"blocked":     blocked,
		"skipped":     skipped,
		"duration_ms": run.EndedAt.Sub(run.CreatedAt).Milliseconds(),
	}
	if reason != "" {
		payload["reason"] = reason
	}
	switch status {
	case RunDone:
		d.publish(EventRunDone, payload)
	case RunStopped:
		d.publish(EventRunStopped, payload)
	case RunFailed:
		d.publish(EventRunFailed, payload)
	}
}

// persist is a no-op when store is nil (tests). Any persistence error
// is swallowed deliberately — failing to write the run blob mid-loop
// shouldn't abort the actual work the user is watching execute.
// Callers can re-derive the run from the in-memory pointer they hold.
func (d *Driver) persist(run *Run) {
	if d.store == nil {
		return
	}
	if err := d.store.Save(run); err != nil {
		d.publish(EventRunWarning, map[string]any{
			"run_id": run.ID,
			"status": string(run.Status),
			"error":  err.Error(),
		})
	}
}

// publish is a no-op when publisher is nil. Keeps the call sites
// terse without nil checks at every usage.
func (d *Driver) publish(eventType string, payload map[string]any) {
	if d.publisher == nil {
		return
	}
	d.publisher(eventType, payload)
}

// briefSoFar stitches together the per-TODO Brief fields of every
// completed TODO that precedes idx. Used to seed the executor's
// sub-agent so it has cheap context on what's already been done
// without dragging the parent transcript along.
func briefSoFar(todos []Todo, untilIdx int) string {
	var b strings.Builder
	for i := 0; i < untilIdx; i++ {
		t := todos[i]
		if t.Status == TodoDone && t.Brief != "" {
			b.WriteString("- ")
			b.WriteString(t.ID)
			b.WriteString(" (")
			b.WriteString(t.Title)
			b.WriteString("): ")
			b.WriteString(t.Brief)
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

// planSummary is a lightweight projection of the TODO list for the
// drive:plan:done event. The full Todo objects would carry too much
// detail for a UI chip; this keeps payloads small.
func planSummary(todos []Todo) []map[string]any {
	out := make([]map[string]any, 0, len(todos))
	for _, t := range todos {
		out = append(out, map[string]any{
			"id":            t.ID,
			"title":         t.Title,
			"deps":          t.DependsOn,
			"origin":        t.Origin,
			"kind":          t.Kind,
			"tag":           t.ProviderTag,
			"worker_class":  t.WorkerClass,
			"skills":        t.Skills,
			"verification":  t.Verification,
			"confidence":    t.Confidence,
			"allowed_tools": t.AllowedTools,
			"status":        string(t.Status),
		})
	}
	return out
}

func executorRoleFor(workerClass string) string {
	switch strings.ToLower(strings.TrimSpace(workerClass)) {
	case "planner":
		return "planner"
	case "researcher":
		return "researcher"
	case "reviewer":
		return "code_reviewer"
	case "tester":
		return "test_engineer"
	case "security":
		return "security_auditor"
	case "synthesizer":
		return "synthesizer"
	case "coder":
		fallthrough
	default:
		return "drive-executor"
	}
}

func executorStepBudgetFor(todo Todo) int {
	switch todoLane(todo) {
	case "discovery":
		return 6
	case "review":
		return 7
	case "verify":
		if strings.EqualFold(strings.TrimSpace(todo.Verification), "deep") {
			return 10
		}
		return 8
	case "synthesize":
		return 6
	default:
		if len(todo.FileScope) >= 3 {
			return 14
		}
		return 12
	}
}

// reasonByID retrieves the per-TODO Error/reason for the skipped
// event payload. Avoids a second pass over the slice in the caller.
func reasonByID(todos []Todo, id string) string {
	for _, t := range todos {
		if t.ID == id {
			return t.Error
		}
	}
	return ""
}

// collectIDsHead returns up to n IDs from the head of the slice. Used
// for the truncation warning event so the user sees which TODOs got
// dropped without dumping the full list.
func collectIDsHead(todos []Todo, n int) []string {
	if n > len(todos) {
		n = len(todos)
	}
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, todos[i].ID)
	}
	return out
}
