package tools

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// stubRunner is a SubagentRunner whose behaviour is driven by a
// programmable sequence of responses. Used to assert the retry policy.
type stubRunner struct {
	responses []stubResponse
	calls     int
}

type stubResponse struct {
	res SubagentResult
	err error
}

func (r *stubRunner) RunSubagent(_ context.Context, _ SubagentRequest) (SubagentResult, error) {
	idx := r.calls
	r.calls++
	if idx >= len(r.responses) {
		return SubagentResult{}, errors.New("stubRunner: ran out of programmed responses")
	}
	rsp := r.responses[idx]
	return rsp.res, rsp.err
}

// TestRunSubagentRetrying_FirstCallSucceeds asserts the happy path: a
// single successful call returns immediately with no Data["attempts"]
// (we only mark the field on actual retries to avoid noise).
func TestRunSubagentRetrying_FirstCallSucceeds(t *testing.T) {
	r := &stubRunner{
		responses: []stubResponse{
			{res: SubagentResult{Summary: "ok"}, err: nil},
		},
	}
	res, err := runSubagentRetrying(context.Background(), r, SubagentRequest{Task: "x"}, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Summary != "ok" {
		t.Errorf("Summary=%q, want ok", res.Summary)
	}
	if r.calls != 1 {
		t.Errorf("calls=%d, want 1", r.calls)
	}
	if _, ok := res.Data["attempts"]; ok {
		t.Errorf("attempts field should be absent on first-try success, got %v", res.Data["attempts"])
	}
}

// TestRunSubagentRetrying_TransientErrorRetries asserts that a
// transient first failure followed by success transparently delivers
// the success result and tags Data["attempts"] = 2 so callers can
// surface the recovery for diagnostics.
func TestRunSubagentRetrying_TransientErrorRetries(t *testing.T) {
	r := &stubRunner{
		responses: []stubResponse{
			{err: errors.New("upstream returned status 503")},
			{res: SubagentResult{Summary: "recovered"}, err: nil},
		},
	}
	// Override backoff timing via shorter ctx pacing isn't necessary —
	// the helper sleeps 750ms internally; this test waits for it.
	res, err := runSubagentRetrying(context.Background(), r, SubagentRequest{Task: "x"}, 2)
	if err != nil {
		t.Fatalf("retry should succeed, got: %v", err)
	}
	if res.Summary != "recovered" {
		t.Errorf("Summary=%q, want recovered", res.Summary)
	}
	if r.calls != 2 {
		t.Errorf("calls=%d, want 2", r.calls)
	}
	if got, _ := res.Data["attempts"].(int); got != 2 {
		t.Errorf("attempts=%v, want 2", res.Data["attempts"])
	}
}

// TestRunSubagentRetrying_NonTransientErrorDoesNotRetry pins that
// deterministic failures (auth, malformed task, MaxSteps) are not
// retried — repeating them would just burn the budget on the same
// guaranteed-to-fail call.
func TestRunSubagentRetrying_NonTransientErrorDoesNotRetry(t *testing.T) {
	r := &stubRunner{
		responses: []stubResponse{
			{err: errors.New("invalid api key")},
			// If retry kicks in, this would unmask the misconfiguration.
			{res: SubagentResult{Summary: "should not see"}, err: nil},
		},
	}
	_, err := runSubagentRetrying(context.Background(), r, SubagentRequest{Task: "x"}, 2)
	if err == nil {
		t.Fatal("expected error to surface, got nil")
	}
	if r.calls != 1 {
		t.Errorf("calls=%d, want 1 (no retry for non-transient)", r.calls)
	}
}

// TestRunSubagentRetrying_RespectsContextCancel asserts the helper
// honours ctx.Done() during the retry backoff and returns ctx.Err()
// instead of soldiering on into a doomed retry attempt.
func TestRunSubagentRetrying_RespectsContextCancel(t *testing.T) {
	r := &stubRunner{
		responses: []stubResponse{
			{err: errors.New("connection reset")},
			{res: SubagentResult{Summary: "should not see"}, err: nil},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	// Cancel during the backoff window. The helper sleeps 750ms; we
	// cancel after 50ms so it bails out long before the second attempt.
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	_, err := runSubagentRetrying(ctx, r, SubagentRequest{Task: "x"}, 3)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if r.calls != 1 {
		t.Errorf("calls=%d, want 1 (cancelled before retry fired)", r.calls)
	}
}

// TestIsSubagentTransientError_KnownMarkers spot-checks a few of the
// substring markers the retry classifier looks for. Ensures the
// classifier doesn't drift away from the provider package's list.
func TestIsSubagentTransientError_KnownMarkers(t *testing.T) {
	transient := []string{
		"upstream returned status 503",
		"connection reset by peer",
		"i/o timeout",
		"unexpected eof from upstream",
		"providerunavailable",
		"stream failed on anthropic",
	}
	for _, m := range transient {
		if !isSubagentTransientError(errors.New(m)) {
			t.Errorf("expected %q to be classified transient", m)
		}
	}

	deterministic := []string{
		"invalid api key",
		"task description is empty",
		"runner not wired",
	}
	for _, m := range deterministic {
		if isSubagentTransientError(errors.New(m)) {
			t.Errorf("expected %q to be classified deterministic, but it's flagged transient", m)
		}
	}
	if isSubagentTransientError(nil) {
		t.Error("nil error must not be classified as transient")
	}
	if isSubagentTransientError(context.Canceled) {
		t.Error("context.Canceled must not be classified as transient")
	}
}

// TestSubagentRetriesTotal_IncrementsOnActualRetry asserts the
// process-wide counter ticks exactly once per retry that fired —
// not per call, not per failed call. A first-attempt success leaves
// the counter alone; a transient-then-success bumps it by one.
func TestSubagentRetriesTotal_IncrementsOnActualRetry(t *testing.T) {
	before := SubagentRetriesTotal()

	// First-try success: counter must NOT move.
	good := &stubRunner{
		responses: []stubResponse{
			{res: SubagentResult{Summary: "ok"}, err: nil},
		},
	}
	_, _ = runSubagentRetrying(context.Background(), good, SubagentRequest{Task: "x"}, 2)
	if SubagentRetriesTotal() != before {
		t.Fatalf("first-try success must not bump retry counter (before=%d after=%d)", before, SubagentRetriesTotal())
	}

	// Transient then success: counter ticks once.
	flaky := &stubRunner{
		responses: []stubResponse{
			{err: errors.New("upstream returned status 503")},
			{res: SubagentResult{Summary: "recovered"}, err: nil},
		},
	}
	_, _ = runSubagentRetrying(context.Background(), flaky, SubagentRequest{Task: "x"}, 2)
	if got := SubagentRetriesTotal() - before; got != 1 {
		t.Fatalf("expected exactly +1 retry on transient-then-success, got +%d", got)
	}
}

// TestSubagentRetriesInWindow_TracksRecentEvents pins the windowed
// counter: only events stamped within the last `window` duration
// count, older entries fall out without needing eviction. Uses
// recordRetryEvent directly so the test isn't gated on the 750ms
// jittered backoff that runSubagentRetrying sleeps through.
func TestSubagentRetriesInWindow_TracksRecentEvents(t *testing.T) {
	// Reset the window so prior tests in the same package run don't
	// pollute counts. ConfigureRetryWindow is idempotent at the same
	// size and reallocates a clean slice — exactly what we want here.
	ConfigureRetryWindow(defaultRetryWindowSize)
	// The reallocation path skips when the size matches; force it by
	// asking for a different size first then resetting.
	ConfigureRetryWindow(defaultRetryWindowSize - 1)
	ConfigureRetryWindow(defaultRetryWindowSize)

	now := time.Now()
	// 3 events within the last second.
	for i := 0; i < 3; i++ {
		recordRetryEvent(now.Add(-100 * time.Millisecond))
	}
	// 2 events ~10 minutes old (outside any reasonable on-call window).
	recordRetryEvent(now.Add(-10 * time.Minute))
	recordRetryEvent(now.Add(-10 * time.Minute))

	if got := SubagentRetriesInWindow(time.Second); got != 3 {
		t.Errorf("1s window should see 3 recent retries, got %d", got)
	}
	if got := SubagentRetriesInWindow(15 * time.Minute); got != 5 {
		t.Errorf("15min window should see all 5 events, got %d", got)
	}
	if got := SubagentRetriesInWindow(5 * time.Minute); got != 3 {
		t.Errorf("5min window should drop the 10min-old events, got %d", got)
	}
	if got := SubagentRetriesInWindow(0); got != 0 {
		t.Errorf("non-positive window should return 0, got %d", got)
	}
}

// TestConfigureRetryWindow_ResizesAndWipes pins the configurable
// ring size: a fresh size allocates a new slice and discards prior
// stamps; the same size is idempotent (no allocation, state preserved).
func TestConfigureRetryWindow_ResizesAndWipes(t *testing.T) {
	// Force a clean slate at the default first.
	ConfigureRetryWindow(0) // 0 → default
	ConfigureRetryWindow(64)

	// Stamp 10 events into the size-64 ring.
	now := time.Now()
	for i := 0; i < 10; i++ {
		recordRetryEvent(now)
	}
	if got := SubagentRetriesInWindow(time.Hour); got != 10 {
		t.Fatalf("size-64 ring should hold 10 fresh stamps, got %d", got)
	}

	// Resize to a different size — wipes.
	ConfigureRetryWindow(8)
	if got := SubagentRetriesInWindow(time.Hour); got != 0 {
		t.Errorf("resize should wipe existing stamps, got %d", got)
	}

	// Stamp past the new cap; oldest entries must roll off.
	for i := 0; i < 12; i++ {
		recordRetryEvent(now)
	}
	if got := SubagentRetriesInWindow(time.Hour); got != 8 {
		t.Errorf("size-8 ring with 12 stamps should hold 8, got %d", got)
	}
}

// TestConfigureRetryWindow_NonPositiveResetsToDefault asserts that 0
// or negative arguments collapse to the default. We verify by going
// from a non-default size → 0/negative → confirming the buffer was
// reallocated at default size (not stuck at the prior custom size).
func TestConfigureRetryWindow_NonPositiveResetsToDefault(t *testing.T) {
	// Start at a small custom size and fill past it so we know the
	// state is non-trivial.
	ConfigureRetryWindow(4)
	for i := 0; i < 10; i++ {
		recordRetryEvent(time.Now())
	}
	if got := SubagentRetriesInWindow(time.Hour); got != 4 {
		t.Fatalf("custom size 4 should hold 4 stamps, got %d", got)
	}

	// 0 → default. Resize wipes.
	ConfigureRetryWindow(0)
	if got := SubagentRetriesInWindow(time.Hour); got != 0 {
		t.Errorf("0 should reset to default and wipe, got %d", got)
	}
	// Confirm the new buffer is default-sized by stamping more than
	// the old custom cap (4) and asserting all survive.
	for i := 0; i < 10; i++ {
		recordRetryEvent(time.Now())
	}
	if got := SubagentRetriesInWindow(time.Hour); got != 10 {
		t.Errorf("default-sized ring should hold all 10 stamps (size > 4), got %d", got)
	}

	// Negative argument is the same path. Resize away first so the
	// idempotency-on-same-size short-circuit doesn't apply.
	ConfigureRetryWindow(2)
	ConfigureRetryWindow(-5)
	for i := 0; i < 10; i++ {
		recordRetryEvent(time.Now())
	}
	if got := SubagentRetriesInWindow(time.Hour); got != 10 {
		t.Errorf("negative arg should reset to default-sized ring, got %d", got)
	}
}

// TestJitteredBackoff_StaysWithinBand asserts the jitter window is
// symmetric around base and bounded by ±fraction. Probabilistically
// — over many samples — we should see the spread but never escape the
// band. 1000 samples is enough to surface a stuck-at-base bug or a
// math typo without flaking on legitimate randomness.
func TestJitteredBackoff_StaysWithinBand(t *testing.T) {
	base := 750 * time.Millisecond
	frac := 0.2
	min := base - time.Duration(float64(base)*frac)
	max := base + time.Duration(float64(base)*frac)

	var sawBelowBase, sawAboveBase bool
	for i := 0; i < 1000; i++ {
		got := jitteredBackoff(base, frac)
		if got < min || got > max {
			t.Fatalf("jittered backoff escaped band [%v, %v]: got %v", min, max, got)
		}
		if got < base {
			sawBelowBase = true
		}
		if got > base {
			sawAboveBase = true
		}
	}
	// Both halves of the symmetric band should be exercised in 1000 draws.
	if !sawBelowBase || !sawAboveBase {
		t.Errorf("jitter is not symmetric: belowBase=%v aboveBase=%v", sawBelowBase, sawAboveBase)
	}
}

// TestJitteredBackoff_DegenerateInputs pins the no-op branches: zero or
// negative base / fraction must collapse to base so a misconfigured
// caller can't introduce negative sleeps or NaN durations.
func TestJitteredBackoff_DegenerateInputs(t *testing.T) {
	if got := jitteredBackoff(0, 0.2); got != 0 {
		t.Errorf("zero base must return 0, got %v", got)
	}
	if got := jitteredBackoff(750*time.Millisecond, 0); got != 750*time.Millisecond {
		t.Errorf("zero fraction must return base unchanged, got %v", got)
	}
	if got := jitteredBackoff(-1, 0.2); got != -1 {
		t.Errorf("negative base must pass through unchanged, got %v", got)
	}
}

// Compile-time hint that strings is intended-imported (defensive in
// case future edits drop the only call to strings.Contains).
var _ = strings.Contains
