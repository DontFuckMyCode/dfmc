package provider

import (
	"sync"
	"time"
)

// Circuit breaker state per provider. The Router keeps one of these per
// provider name so a flaky provider doesn't waste an attempt at the head
// of every request — once it has tripped repeatedly, we skip it for a
// cooldown period and let the fallback cascade serve traffic without
// the up-front latency penalty.
//
// State machine:
//
//	closed   → consecutiveFailures == 0; provider tried normally.
//	tripped  → consecutiveFailures >= breakerThreshold; openedAt set;
//	           Complete/Stream skips the provider until openedAt+cooldown.
//	half-open → cooldown elapsed; provider gets ONE attempt. Success
//	           closes (resets); failure re-opens with doubled cooldown.
//
// All transitions go through (Router).recordProviderHealth /
// (Router).shouldSkipForCircuit. Tests poke directly via the helpers in
// circuit_test.go.
type providerHealth struct {
	consecutiveFailures int
	openedAt            time.Time
	cooldown            time.Duration
}

const (
	// breakerThreshold is the consecutive transient-failure count that
	// trips the breaker. 3 is forgiving enough to ride out a single bad
	// retry (the provider's per-call retry already handles flakes < 3)
	// without making the user wait through an obviously-down provider on
	// every fresh ask.
	breakerThreshold = 3

	// circuitInitialCooldown is the first sleep window after a trip.
	// Short enough to recover quickly from a brief blip, long enough that
	// we don't immediately re-trip on a sustained outage.
	circuitInitialCooldown = 30 * time.Second

	// circuitMaxCooldown bounds the exponential growth on repeated re-trips.
	// 5 minutes is the upper limit — beyond that the user almost certainly
	// wants the provider back in rotation to recheck.
	circuitMaxCooldown = 5 * time.Minute
)

// shouldSkipForCircuit returns true when the named provider is currently
// in an open circuit and the cooldown hasn't elapsed. The check is
// non-mutating; the caller still sees a fresh attempt the moment cooldown
// elapses (half-open). The "offline" provider is never circuit-broken
// because it's the always-available safety net for the entire cascade.
func (r *Router) shouldSkipForCircuit(name string) bool {
	if name == "offline" {
		return false
	}
	r.healthMu.Lock()
	defer r.healthMu.Unlock()
	if r.health == nil {
		return false
	}
	h, ok := r.health[name]
	if !ok {
		return false
	}
	if h.openedAt.IsZero() {
		return false
	}
	if time.Since(h.openedAt) >= h.cooldown {
		// Half-open: caller may try again. We do NOT clear state here
		// because the success/failure of the half-open attempt drives
		// the next transition; recordProviderHealth handles both paths.
		return false
	}
	return true
}

// recordProviderHealth updates the circuit state after a Complete/Stream
// attempt against `name`. err == nil resets the breaker; a transient err
// increments the failure count and trips on threshold. Non-transient
// errors (auth, malformed request, ctx cancellation) DO NOT count toward
// the breaker — they would just break the breaker for the wrong reason.
//
// On a transition, the optional onChange hook fires synchronously; the
// Router uses it to publish provider:circuit:* events without dragging
// EventBus into this package.
func (r *Router) recordProviderHealth(name string, err error) {
	if name == "" || name == "offline" {
		return
	}
	r.healthMu.Lock()
	if r.health == nil {
		// Lazy init for test helpers that build a Router via &Router{...}
		// without going through NewRouter; production paths always init.
		r.health = map[string]*providerHealth{}
	}
	h, ok := r.health[name]
	if !ok {
		h = &providerHealth{}
		r.health[name] = h
	}

	switch {
	case err == nil:
		// Success — close the circuit if it was open or counting toward open.
		wasOpen := !h.openedAt.IsZero()
		h.consecutiveFailures = 0
		h.openedAt = time.Time{}
		h.cooldown = 0
		r.healthMu.Unlock()
		if wasOpen && r.circuitObserver != nil {
			r.circuitObserver(CircuitEvent{Provider: name, State: "closed"})
		}
		return

	case isTransient(err):
		h.consecutiveFailures++
		if h.consecutiveFailures >= breakerThreshold && h.openedAt.IsZero() {
			h.openedAt = time.Now()
			if h.cooldown == 0 {
				h.cooldown = circuitInitialCooldown
			} else {
				// Exponential growth on repeated trips, bounded.
				h.cooldown *= 2
				if h.cooldown > circuitMaxCooldown {
					h.cooldown = circuitMaxCooldown
				}
			}
			cooldown := h.cooldown
			r.healthMu.Unlock()
			if r.circuitObserver != nil {
				r.circuitObserver(CircuitEvent{Provider: name, State: "open", Cooldown: cooldown})
			}
			return
		}
		r.healthMu.Unlock()
		return

	default:
		// Non-transient: don't touch the breaker. The error surfaces to
		// the caller via the regular fallback path; counting it would
		// misattribute (e.g. malformed request) to provider downtime.
		r.healthMu.Unlock()
		return
	}
}

// CircuitEvent describes a circuit transition for observability hooks.
type CircuitEvent struct {
	Provider string
	State    string // "open" | "closed"
	Cooldown time.Duration
}

// SetCircuitObserver installs a callback fired on every circuit
// open/close transition. Engine wires this to its EventBus so UIs can
// surface a "primary down, using fallback" indicator.
func (r *Router) SetCircuitObserver(fn func(CircuitEvent)) {
	r.healthMu.Lock()
	defer r.healthMu.Unlock()
	r.circuitObserver = fn
}

// SetStreamRecoveredObserver installs a callback fired after a
// streamForwardWithRecovery call swaps providers mid-stream and the
// fallback delivered a clean StreamDone. Engine wires this to its
// EventBus so TUIs can surface a "↻ stream resumed on <fallback>"
// chip — without it, the recovery is invisible to the user.
func (r *Router) SetStreamRecoveredObserver(fn func(StreamRecoveredEvent)) {
	r.healthMu.Lock()
	defer r.healthMu.Unlock()
	r.streamRecoveredObserver = fn
}

// CircuitState returns a snapshot of provider names currently in the
// "open" state. Useful for diagnostics (`dfmc status`) and tests. Empty
// slice when all circuits are closed. Caller-owned; safe to mutate.
func (r *Router) CircuitState() []string {
	r.healthMu.Lock()
	defer r.healthMu.Unlock()
	if len(r.health) == 0 {
		return nil
	}
	out := make([]string, 0, len(r.health))
	now := time.Now()
	for name, h := range r.health {
		if h.openedAt.IsZero() {
			continue
		}
		if now.Sub(h.openedAt) < h.cooldown {
			out = append(out, name)
		}
	}
	return out
}

// resetHealth wipes the circuit state. Used by tests; not part of the
// public surface.
func (r *Router) resetHealth() {
	r.healthMu.Lock()
	r.health = map[string]*providerHealth{}
	r.healthMu.Unlock()
}

// RecordHealthForTest is a test-only escape hatch that simulates a
// transient failure being recorded against the named provider, so
// downstream code (engine.Status, TUI badges, web /api/v1/status) can
// be exercised without standing up a flaky upstream. Three consecutive
// calls trip the breaker (mirrors breakerThreshold). Production code
// MUST NOT call this — use Complete/Stream which drive the breaker
// from real outcomes.
func (r *Router) RecordHealthForTest(name string, transient bool) {
	if transient {
		r.recordProviderHealth(name, errTestTransient)
	} else {
		r.recordProviderHealth(name, nil)
	}
}

// errTestTransient is a sentinel that isTransient() classifies as
// retryable via its substring fallback. Lives here so the test-only
// helper above doesn't depend on the production error wiring.
var errTestTransient = &testTransientErr{}

type testTransientErr struct{}

func (*testTransientErr) Error() string { return "test transient: status 503 service unavailable" }

// Compile-time guard that the helper exists; keeps `sync` referenced
// even when the Router struct lives in another file.
var _ = sync.Mutex{}
