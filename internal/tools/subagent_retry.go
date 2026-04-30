package tools

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"time"
)

// subagentRetriesTotal is a process-wide cumulative counter of the
// number of times runSubagentRetrying has actually fired a retry —
// not the number of subagent calls, just retries. Engine.Status()
// reads this through SubagentRetriesTotal() so operators can see
// whether transient subagent failures are happening (and how often)
// without grepping logs.
var subagentRetriesTotal int64

// SubagentRetriesTotal returns the cumulative count of retries fired
// by runSubagentRetrying since process start. Monotonic; safe for
// concurrent use. 0 means either no subagent activity or all calls
// succeeded on the first attempt.
func SubagentRetriesTotal() int64 {
	return atomic.LoadInt64(&subagentRetriesTotal)
}

// defaultSubagentRetryAttempts is the maximum number of retry attempts
// for a transient sub-agent failure. The first call counts as attempt 1,
// so a value of 2 here means up-to-one retry. Tight by default — most
// sub-agent failures are deterministic (the inner loop just hit its
// MaxSteps cap, or the task is malformed) and re-running them just
// burns tokens. The retry is meant to absorb network-class blips that
// the provider's own retry already failed to mask.
const defaultSubagentRetryAttempts = 2

// runSubagentRetrying wraps a SubagentRunner.RunSubagent call with a
// bounded retry policy targeting transient failures only. Returns the
// result+error from the last attempt; the attempts counter is included
// in result.Data["attempts"] when retries actually fired so the caller
// can surface it without re-counting.
//
// Retry decisions:
//   - ctx already dead → no retry, return immediately.
//   - Non-transient error (auth, malformed args, MaxSteps hit) → no retry.
//   - Transient error (network, 5xx, ErrProviderUnavailable phrasing) →
//     retry up to attempts-1 more times with a short fixed backoff so
//     a flaky provider gets a chance to recover.
//
// The retry budget is conservative because each attempt is a full
// sub-agent loop, not a single HTTP call. Burning two attempts that
// each cost a 30-step loop is much more expensive than burning two
// HTTP retries inside a single agent step.
func runSubagentRetrying(ctx context.Context, runner SubagentRunner, req SubagentRequest, attempts int) (SubagentResult, error) {
	if attempts < 1 {
		attempts = 1
	}
	if runner == nil {
		return SubagentResult{}, errors.New("subagent runner not wired")
	}

	const backoff = 750 * time.Millisecond

	var lastRes SubagentResult
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if cerr := ctx.Err(); cerr != nil {
			return lastRes, cerr
		}
		res, err := runner.RunSubagent(ctx, req)
		if err == nil {
			if attempt > 1 {
				if res.Data == nil {
					res.Data = map[string]any{}
				}
				res.Data["attempts"] = attempt
			}
			return res, nil
		}
		lastRes = res
		lastErr = err

		if !isSubagentTransientError(err) {
			break
		}
		if attempt == attempts {
			break
		}
		// Count the retry now (we've decided to retry, the upcoming
		// sleep is unconditional barring ctx cancel). Doing this here
		// rather than after time.After means a ctx-cancel mid-backoff
		// still records the retry intent — which matches operator
		// intuition: "we tried to recover".
		atomic.AddInt64(&subagentRetriesTotal, 1)
		// Brief sleep before retry. Honour ctx cancel so we don't waste
		// the backoff window after the user pressed Ctrl+C.
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return lastRes, ctx.Err()
		}
	}
	return lastRes, lastErr
}

// isSubagentTransientError classifies a sub-agent error string as
// retryable. We can't import internal/provider here without creating
// a cycle, so this is a substring-only check that mirrors the
// provider package's transient classifier (see internal/provider/router.go
// isTransient). The list MUST stay in sync — when a new transient
// marker is added there, mirror it here so the retry catches it.
func isSubagentTransientError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, marker := range []string{
		"status 500", "status 502", "status 503", "status 504",
		"status code 500", "status code 502", "status code 503", "status code 504",
		"connection refused",
		"connection reset",
		"no such host",
		"i/o timeout",
		"unexpected eof",
		"broken pipe",
		"tls handshake timeout",
		"providerunavailable",
		"provider unavailable",
		"stream failed on",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}
