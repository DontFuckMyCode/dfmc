// run_drainer.go — terminal-status drain phase for the Drive runner.
//
// drainAndFinalize collects the outcomes of in-flight workers before
// stamping the final run status. Pulled out of run_executor.go so the
// drain semantics (non-blocking pass + bounded grace window + accurate
// "WAS running when we stopped" persistence) are documented in one
// place. Resume always resets Running -> Pending so a TODO still
// in-flight at drain time gets re-attempted on the next run.

package drive

import (
	"context"
	"time"
)

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
