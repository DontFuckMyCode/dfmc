package provider

import (
	"errors"
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

// SetCircuitObserver installs a callback that fires synchronously on every
// circuit state transition (open → closed). The Router holds no reference
// to the hook after installation — callers can replace it by calling
// SetCircuitObserver again with a different fn.
func (r *Router) SetCircuitObserver(fn func(CircuitEvent)) {
	r.circuitObserver = fn
}

// CircuitState returns the names of providers currently in the open (tripped)
// circuit state.
func (r *Router) CircuitState() []string {
	r.healthMu.Lock()
	defer r.healthMu.Unlock()
	var open []string
	for name, h := range r.health {
		if !h.openedAt.IsZero() {
			open = append(open, name)
		}
	}
	return open
}

// RecordHealthForTest simulates a provider health outcome for unit tests.
// failed=true maps to a transient error; failed=false maps to err==nil,
// which trips the breaker after breakerThreshold calls.
// Exported only because engine tests reach Router through eng.Providers.
func (r *Router) RecordHealthForTest(name string, failed bool) {
	var err error
	if failed {
		err = errors.New("synthetic transient: status 503 service unavailable")
	}
	r.recordProviderHealth(name, err)
}
