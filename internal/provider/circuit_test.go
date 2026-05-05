package provider

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// errTransient is a string-only error that isTransient() classifies as
// retryable via its substring fallback (status 503). Useful in tests
// where we don't want to construct StatusError values.
var errTransient = errors.New("upstream returned status 503: service unavailable")

// TestCircuitOpensAfterThreeConsecutiveFailures asserts the breaker
// trips after exactly breakerThreshold transient failures and then
// short-circuits subsequent calls without touching the provider.
func TestCircuitOpensAfterThreeConsecutiveFailures(t *testing.T) {
	primary := &stubProvider{name: "primary", err: errTransient, supportsTools: true}
	fallback := &stubProvider{name: "fallback", text: "ok", supportsTools: true}
	r := newRouterWith(primary, fallback)
	r.primary = "primary"
	r.fallback = []string{"fallback"}

	// Three failed asks should land the breaker in open state.
	for i := 0; i < 3; i++ {
		_, _, _ = r.Complete(context.Background(), CompletionRequest{
			Messages: []Message{{Role: types.RoleUser, Content: "ping"}},
		})
	}

	open := r.CircuitState()
	if len(open) != 1 || open[0] != "primary" {
		t.Fatalf("expected primary in open state, got %v", open)
	}

	// Fourth ask: primary should be skipped entirely. Calls counter
	// must NOT increment (that's the whole point of the breaker).
	callsBefore := atomic.LoadInt32(&primary.calls)
	resp, used, err := r.Complete(context.Background(), CompletionRequest{
		Messages: []Message{{Role: types.RoleUser, Content: "ping"}},
	})
	if err != nil {
		t.Fatalf("expected fallback to serve while primary breaker is open, got %v", err)
	}
	if used != "fallback" {
		t.Errorf("used=%q, want fallback (primary breaker open)", used)
	}
	if resp.Text != "ok" {
		t.Errorf("resp.Text=%q, want ok", resp.Text)
	}
	if got := atomic.LoadInt32(&primary.calls); got != callsBefore {
		t.Errorf("primary was called %d times while breaker open (expected no new calls)", got-callsBefore)
	}
}

// TestNonTransientErrorDoesNotTripBreaker asserts that auth/4xx-class
// errors don't accumulate toward the threshold — those are deterministic
// and counting them would mark a misconfigured but otherwise-up provider
// as down.
func TestNonTransientErrorDoesNotTripBreaker(t *testing.T) {
	primary := &stubProvider{
		name:          "primary",
		err:           errors.New("invalid api key"), // not in isTransient's substring list
		supportsTools: true,
	}
	fallback := &stubProvider{name: "fallback", text: "ok", supportsTools: true}
	r := newRouterWith(primary, fallback)
	r.primary = "primary"
	r.fallback = []string{"fallback"}

	// Five asks, every one fails on primary deterministically.
	for i := 0; i < 5; i++ {
		_, _, _ = r.Complete(context.Background(), CompletionRequest{
			Messages: []Message{{Role: types.RoleUser, Content: "ping"}},
		})
	}

	if open := r.CircuitState(); len(open) != 0 {
		t.Errorf("expected no open circuits for non-transient errors, got %v", open)
	}
	// Primary should have been called every time (no skip).
	if got := atomic.LoadInt32(&primary.calls); got != 5 {
		t.Errorf("primary calls=%d, want 5 (no breaker skip on non-transient)", got)
	}
}

// TestCircuitHalfOpenAndCloseOnSuccess asserts that once the cooldown
// elapses, the next attempt may hit the provider; on success the
// circuit closes and the failure counter resets.
func TestCircuitHalfOpenAndCloseOnSuccess(t *testing.T) {
	r := newRouterWith()
	// Manually trip the breaker with a very short cooldown so the test
	// doesn't have to sleep 30 seconds.
	r.health["primary"] = &providerHealth{
		consecutiveFailures: breakerThreshold,
		openedAt:            time.Now().Add(-100 * time.Millisecond),
		cooldown:            50 * time.Millisecond,
	}

	if r.shouldSkipForCircuit("primary") {
		t.Fatal("expected breaker to be half-open after cooldown elapsed")
	}

	// A successful recordProviderHealth should fully close the breaker.
	r.recordProviderHealth("primary", nil)
	if open := r.CircuitState(); len(open) != 0 {
		t.Errorf("expected closed after success, got %v open", open)
	}
}

// TestCircuitObserverFires asserts the observer hook receives transitions.
func TestCircuitObserverFires(t *testing.T) {
	primary := &stubProvider{name: "primary", err: errTransient, supportsTools: true}
	fallback := &stubProvider{name: "fallback", text: "ok", supportsTools: true}
	r := newRouterWith(primary, fallback)
	r.primary = "primary"
	r.fallback = []string{"fallback"}

	var events []CircuitEvent
	r.SetCircuitObserver(func(ev CircuitEvent) {
		events = append(events, ev)
	})

	for i := 0; i < 3; i++ {
		_, _, _ = r.Complete(context.Background(), CompletionRequest{
			Messages: []Message{{Role: types.RoleUser, Content: "ping"}},
		})
	}

	if len(events) != 1 {
		t.Fatalf("expected 1 transition event, got %d: %v", len(events), events)
	}
	if events[0].Provider != "primary" || events[0].State != "open" {
		t.Errorf("unexpected event: %+v", events[0])
	}
	if events[0].Cooldown != circuitInitialCooldown {
		t.Errorf("cooldown=%v, want %v", events[0].Cooldown, circuitInitialCooldown)
	}
}

// TestOfflineProviderNeverBroken asserts the offline safety net is
// immune to circuit breaking — it must always be reachable so the
// fallback cascade can never fully starve a request.
func TestOfflineProviderNeverBroken(t *testing.T) {
	r := newRouterWith()
	// Manually mark offline as if it had failed (shouldn't matter).
	r.health["offline"] = &providerHealth{
		consecutiveFailures: 99,
		openedAt:            time.Now(),
		cooldown:            10 * time.Minute,
	}
	if r.shouldSkipForCircuit("offline") {
		t.Error("offline must never be circuit-broken")
	}
	r.recordProviderHealth("offline", errTransient)
	if r.shouldSkipForCircuit("offline") {
		t.Error("offline must never be circuit-broken even after explicit failure record")
	}
}
