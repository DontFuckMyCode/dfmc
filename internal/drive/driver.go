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
	"errors"
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
		return nil, errors.New(emptyTaskError)
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
		return nil, errors.New(emptyTaskError)
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

