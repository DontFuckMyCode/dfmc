package provider

import (
	"testing"
	"time"
)

// TestCircuitHalfOpenFailureReopens pins that a FAILED half-open probe
// re-opens the breaker with a grown cooldown, instead of leaving it stuck
// half-open. If it stays half-open, a persistently-down provider is retried
// on every request after the first cooldown — defeating the breaker — and the
// exponential cooldown growth becomes dead code.
func TestCircuitHalfOpenFailureReopens(t *testing.T) {
	r := &Router{}
	const name = "primary"

	// Trip the breaker with threshold consecutive transient failures.
	for i := 0; i < breakerThreshold; i++ {
		r.recordProviderHealth(name, errTransient)
	}
	r.healthMu.Lock()
	h := r.health[name]
	if h == nil || h.openedAt.IsZero() {
		r.healthMu.Unlock()
		t.Fatal("breaker should be open after threshold failures")
	}
	firstCooldown := h.cooldown
	// Simulate the cooldown window elapsing — move openedAt into the past.
	h.openedAt = time.Now().Add(-firstCooldown - time.Second)
	r.healthMu.Unlock()

	// Half-open: the provider is eligible for exactly one probe.
	if r.shouldSkipForCircuit(name) {
		t.Fatal("breaker should be half-open (skip=false) after cooldown elapsed")
	}

	// The half-open probe FAILS. The breaker must re-open.
	r.recordProviderHealth(name, errTransient)

	if !r.shouldSkipForCircuit(name) {
		t.Fatal("after a failed half-open probe the breaker must re-open (skip=true); it stayed half-open, so the provider would be retried on every request")
	}
	r.healthMu.Lock()
	got := r.health[name].cooldown
	r.healthMu.Unlock()
	if got <= firstCooldown {
		t.Errorf("cooldown must grow after a re-trip: was %v, still %v", firstCooldown, got)
	}
}
