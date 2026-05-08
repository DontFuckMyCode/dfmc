// run_executor.go — parallel dispatch loop for the Drive runner.
//
// Owns the executor side of the plan->execute->persist pipeline:
// ready-batch dispatch under MaxParallel, goroutine-per-TODO with a
// buffered results channel, and per-result state updates. The drain
// phase (terminal-status stamping after ctx cancel / deadline / max-
// failed cutoff) lives in its sibling run_drainer.go.

package drive

import (
	"context"
	"errors"
	"fmt"
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
	if ctxDeadline, ok := ctx.Deadline(); ok {
		deadline = ctxDeadline
	} else if !run.EndedAt.IsZero() {
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
				driveContextStopReason(ctx, d.cfg.MaxWallTime, err))
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
				driveContextStopReason(ctx, d.cfg.MaxWallTime, ctx.Err()))
			return
		case res := <-results:
			inFlight--
			d.applyOutcome(run, res, &consecutiveBlocked)
		}
	}
}

func driveContextStopReason(ctx context.Context, maxWallTime time.Duration, err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Sprintf("max_wall_time exceeded (%s)", maxWallTime)
	}
	return fmt.Sprintf("ctx cancelled: %v", err)
}

// dispatchTodo + todoOutcome + applyOutcome live in
// run_executor_outcome.go.
