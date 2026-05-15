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
	"log"
	"os"
	"path/filepath"
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
	runner          Runner
	store           *Store
	publisher       Publisher
	cfg             Config
	reportDir       string
	plannerBreaker  *CircuitBreaker
	executorBreaker *CircuitBreaker
}

// SetReportDir configures the directory where finalize() writes a
// Markdown rollup for every terminal run. Empty string disables the
// behaviour. Idempotent — calling with the same value is a no-op.
func (d *Driver) SetReportDir(dir string) {
	if d == nil {
		return
	}
	d.reportDir = strings.TrimSpace(dir)
}

const emptyTaskError = "drive task is empty; pass a non-empty task description"

// NewDriver wires the dependencies. cfg.Apply() runs internally so
// callers can pass a zero Config and get the defaults. Publisher may
// be nil; store may be nil for in-memory test runs.
func NewDriver(runner Runner, store *Store, publisher Publisher, cfg Config) *Driver {
	cfg = cfg.Apply()
	d := &Driver{
		runner:          runner,
		store:           store,
		publisher:       publisher,
		cfg:             cfg,
		plannerBreaker:  NewCircuitBreaker(cfg.PlannerCircuitBreaker),
		executorBreaker: NewCircuitBreaker(cfg.ExecutorCircuitBreaker),
	}
	return d
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
			unregister(run.ID) // clean up registry even on panic
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
	if !tryRegister(run.ID, run.Task, cancel) {
		cancel()
		return run, fmt.Errorf("run %q already active in this process", run.ID)
	}
	defer unregister(run.ID)
	defer cancel()
	ctx = cancelCtx
	deadlineCtx, deadlineCancel := context.WithDeadline(ctx, run.CreatedAt.Add(d.cfg.MaxWallTime))
	defer deadlineCancel()
	ctx = deadlineCtx

	d.publish(EventRunStart, map[string]any{"run_id": run.ID, "task": run.Task})

	// Activate the per-run auto-approve scope. Released on every
	// return path via defer so a panic in plan/execute doesn't leave
	// a wide-open approver for subsequent /chat or /ask calls.
	release := d.runner.BeginAutoApprove(d.cfg.AutoApprove)
	defer release()

	// Plan stage owns its own persistence + EventPlanStart/Done/Failed
	// + EventRunFailed publish, so we just propagate its error here.
	if err := d.runPlannerPhase(ctx, run); err != nil {
		return run, err
	}

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
func (d *Driver) Resume(ctx context.Context, runID string) (retRun *Run, retErr error) {
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
	switch run.Status {
	case RunDone, RunFailed:
		return run, fmt.Errorf("run %q already terminal (status=%s)", runID, run.Status)
	}
	for i := range run.Todos {
		switch run.Todos[i].Status {
		case TodoRunning:
			run.Todos[i].Status = TodoPending
		case TodoRetrying:
			// Retry not yet scheduled — reset so it can be picked up
			// by the scheduler on the next tick.
			run.Todos[i].Status = TodoPending
		}
	}
	run.Status = RunRunning
	run.EndedAt = time.Time{}
	run.Reason = ""
	d.persist(run)
	retRun = run
	// Mirror RunPrepared's panic recover: any crash inside the
	// executor loop (slice bounds in applyOutcome, nil ptr from a
	// stale plan, etc.) would otherwise leak the registry entry,
	// hold BeginAutoApprove permanently for follow-up turns, and
	// leave the run in RunRunning forever on disk. The defer below
	// finalizes the run as RunFailed, releases the registry, and
	// surfaces the panic to the caller as a typed error so HTTP
	// /drive/resume callers see a 500 instead of an opaque hang.
	defer func() {
		if r := recover(); r != nil {
			run.Status = RunFailed
			run.Reason = fmt.Sprintf("resume panic: %v", r)
			run.EndedAt = time.Now()
			d.persist(run)
			d.publish(EventRunFailed, map[string]any{
				"run_id": run.ID,
				"reason": run.Reason,
			})
			switch v := r.(type) {
			case error:
				retErr = fmt.Errorf("drive resume panic: %w", v)
			default:
				retErr = fmt.Errorf("drive resume panic: %v", v)
			}
			retRun = run
			unregister(run.ID) // clean up registry even on panic
		}
	}()
	// Same registry hook as Run(). Use the original run ID so
	// `dfmc drive stop <id>` works on a resumed run too.
	cancelCtx, cancel := context.WithCancel(ctx)
	if !tryRegister(run.ID, run.Task, cancel) {
		cancel()
		return run, fmt.Errorf("run %q already active in this process", run.ID)
	}
	defer unregister(run.ID)
	defer cancel()
	ctx = cancelCtx
	deadlineCtx, deadlineCancel := context.WithTimeout(ctx, d.cfg.MaxWallTime)
	defer deadlineCancel()
	ctx = deadlineCtx

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
	d.writeRunReport(run)
}

// writeRunReport persists a Markdown rollup under reportDir. Best-
// effort: any I/O failure is published as a warning event (so the user
// sees it) but never blocks the run-done path. Mirrors persist()'s
// "swallow errors mid-finalize" stance; the run is already terminal at
// this point and the JSON record is the source of truth either way.
func (d *Driver) writeRunReport(run *Run) {
	if d == nil || run == nil || strings.TrimSpace(d.reportDir) == "" {
		return
	}
	body := RenderRunReport(run)
	if strings.TrimSpace(body) == "" {
		return
	}
	if err := os.MkdirAll(d.reportDir, 0o755); err != nil {
		d.publish(EventRunWarning, map[string]any{
			"run_id": run.ID,
			"status": string(run.Status),
			"error":  "report mkdir: " + err.Error(),
		})
		return
	}
	path := filepath.Join(d.reportDir, "drive-"+run.ID+".md")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		d.publish(EventRunWarning, map[string]any{
			"run_id": run.ID,
			"status": string(run.Status),
			"error":  "report write: " + err.Error(),
		})
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
		log.Printf("drive: persist failed for run %s (status=%s): %v", run.ID, run.Status, err)
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
