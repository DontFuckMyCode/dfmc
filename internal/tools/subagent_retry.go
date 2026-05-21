package tools

import (
	"context"
	"errors"
	"math/rand"
	"strings"
	"sync"
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

// retryWindow holds timestamps of recent retries so callers can ask
// "how many retries fired in the last N minutes?" — a question the
// monotonic counter alone can't answer. The ring is bounded so a
// long-running daemon never accumulates unbounded state. Default
// size 256 covers a 5-minute window at ~50 retries/sec (which
// would already be a fleet incident); operators expecting higher
// rates can raise the cap via agent.retry_window_size to keep the
// rolling count accurate.
const defaultRetryWindowSize = 256

var (
	retryWindowMu             sync.Mutex
	retryWindowBuf            []time.Time // nil until first write or ConfigureRetryWindow
	retryWindowIdx            int         // next slot to write
	retryWindowFull           bool        // wrapped at least once
	retryWindowConfiguredSize int         // 0 = use default
)

// ConfigureRetryWindow sets the ring buffer size used by
// recordRetryEvent / SubagentRetriesInWindow. Engine.Init calls this
// once with cfg.Agent.RetryWindowSize so operators can size the ring
// for their expected retry rate. 0 or negative resets to default.
//
// Resizing wipes existing entries — we don't try to preserve a partial
// window across a config change because the boundary semantics
// ("entries older than what?") get fuzzy. Tests reset state via this.
func ConfigureRetryWindow(n int) {
	retryWindowMu.Lock()
	defer retryWindowMu.Unlock()
	if n <= 0 {
		n = defaultRetryWindowSize
	}
	if retryWindowConfiguredSize == n && retryWindowBuf != nil && len(retryWindowBuf) == n {
		// Idempotent at the same size — don't wipe state on a second
		// call from a config hot-reload that didn't change the value.
		return
	}
	retryWindowConfiguredSize = n
	retryWindowBuf = make([]time.Time, n)
	retryWindowIdx = 0
	retryWindowFull = false
}

// recordRetryEvent stamps a retry into the ring buffer. Called from
// runSubagentRetrying alongside the atomic counter bump. Cost is one
// mutex acquisition + slice write; negligible against a retry path
// that's already about to sleep 600-900ms. Lazy-allocates the ring on
// first call so packages that build a tools.Engine without going
// through engine.Init still get a working window at default size.
func recordRetryEvent(now time.Time) {
	retryWindowMu.Lock()
	if retryWindowBuf == nil {
		size := retryWindowConfiguredSize
		if size <= 0 {
			size = defaultRetryWindowSize
		}
		retryWindowBuf = make([]time.Time, size)
	}
	retryWindowBuf[retryWindowIdx] = now
	retryWindowIdx++
	if retryWindowIdx == len(retryWindowBuf) {
		retryWindowIdx = 0
		retryWindowFull = true
	}
	retryWindowMu.Unlock()
}

// SubagentRetriesInWindow returns the number of retries fired within
// the last `window` duration. Caller-driven window so the same data
// can answer "last minute" and "last 5 minutes" without keeping two
// counters. A non-positive window returns 0 — telemetry consumers that
// pass a bad value should see "no spike" rather than the full ring.
func SubagentRetriesInWindow(window time.Duration) int {
	if window <= 0 {
		return 0
	}
	cutoff := time.Now().Add(-window)
	retryWindowMu.Lock()
	defer retryWindowMu.Unlock()
	if retryWindowBuf == nil {
		return 0
	}
	limit := retryWindowIdx
	if retryWindowFull {
		limit = len(retryWindowBuf)
	}
	count := 0
	for i := 0; i < limit; i++ {
		if !retryWindowBuf[i].IsZero() && retryWindowBuf[i].After(cutoff) {
			count++
		}
	}
	return count
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

	// baseBackoff + jitter: a coordinated upstream outage triggers retries
	// from many concurrent subagents at once. Without jitter every retry
	// lands on the same millisecond, hammering the recovering provider in
	// a synchronized wave. ±20% jitter spreads the retries over a 300ms
	// band, which is enough to break the harmonic without pushing the
	// slowest retry meaningfully past the user's patience threshold.
	const baseBackoff = 750 * time.Millisecond
	const jitterFraction = 0.2

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
		recordRetryEvent(time.Now())
		// Brief sleep before retry. Honour ctx cancel so we don't waste
		// the backoff window after the user pressed Ctrl+C. Jitter is
		// computed per-attempt so concurrent retries don't synchronize
		// even within the same process.
		wait := jitteredBackoff(baseBackoff, jitterFraction)
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return lastRes, ctx.Err()
		}
	}
	return lastRes, lastErr
}

// jitteredBackoff returns base ± (base × fraction) using math/rand.
// math/rand (not crypto/rand) is intentional: the spread only needs to
// be unpredictable enough to break thundering-herd synchronization, not
// to resist an attacker. fraction is clamped to [0, 1) to keep the
// minimum positive; a non-positive base or fraction collapses to base.
func jitteredBackoff(base time.Duration, fraction float64) time.Duration {
	if base <= 0 || fraction <= 0 {
		return base
	}
	if fraction >= 1 {
		fraction = 0.99
	}
	span := float64(base) * fraction
	// rand.Float64 in [0,1); shift to [-1,1) so jitter is symmetric.
	delta := (rand.Float64()*2 - 1) * span
	return base + time.Duration(delta)
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
