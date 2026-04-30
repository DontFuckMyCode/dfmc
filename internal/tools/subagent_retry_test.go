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
