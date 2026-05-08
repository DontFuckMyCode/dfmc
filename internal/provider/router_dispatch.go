package provider

// router_dispatch.go — Complete and Stream, the top-level entry points
// callers go through to make a request. Both walk ResolveOrder, run
// the per-provider retry layer, record health for the circuit breaker,
// and emit fallback events when stepping past a failing provider.
// filterToolCapable trims providers that can't honour tool calls out
// of the cascade so a mid-loop fallback never silently lands on the
// offline placeholder. Sibling files: router.go (Router struct +
// observer wiring + ResolveOrder + construction).

import (
	"context"
	"errors"
	"fmt"
)

func (r *Router) Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, string, error) {
	order := r.ResolveOrder(req.Provider)
	// When the caller asked for tool-calling, strip providers that can't
	// honour tools out of the fallback cascade. Without this filter, a
	// mid-agent-loop error on the primary silently falls through to
	// offline (Hints.SupportsTools=false), which returns a canned analyzer
	// response with zero tool_calls — the agent loop then treats that as
	// the final answer and the user sees an "offline" reply to what was a
	// live tool-using task. Skipped providers are still eligible when the
	// caller explicitly names one via req.Provider.
	if len(req.Tools) > 0 {
		order = r.filterToolCapable(order, req.Provider)
	}
	if len(order) == 0 {
		return nil, "", fmt.Errorf("%w: all registered providers lack tool support for this request", ErrNoCapableProvider)
	}
	var errs []error

	for i, name := range order {
		// If the caller's context is already dead, there is no point
		// trying the next provider — each attempt would just immediately
		// return ctx.Err() and the real cancel/deadline reason would get
		// buried inside errors.Join below. Surface it directly so agent
		// loops and cancellable CLI commands return the exact sentinel
		// (context.Canceled / context.DeadlineExceeded) the caller
		// expects.
		if cerr := ctx.Err(); cerr != nil {
			if len(errs) == 0 {
				return nil, "", cerr
			}
			return nil, "", errors.Join(append(errs, cerr)...)
		}
		// Circuit breaker: if this provider has tripped recently, skip
		// straight to the next one. Saves the user a doomed primary
		// attempt on every fresh ask while the upstream is down.
		if r.shouldSkipForCircuit(name) {
			errs = append(errs, fmt.Errorf("%s: %w", name, ErrProviderUnavailable))
			if next := nextProviderName(order, i+1); next != "" {
				r.emitFallback(name, next, ErrProviderUnavailable, i)
			}
			continue
		}
		p, ok := r.Get(name)
		if !ok {
			errs = append(errs, fmt.Errorf("%w: %s", ErrProviderNotFound, name))
			if next := nextProviderName(order, i+1); next != "" {
				r.emitFallback(name, next, ErrProviderNotFound, i)
			}
			continue
		}
		resp, usedModel, err := r.completeWithProviderRetry(ctx, p, req)
		r.recordProviderHealth(name, err)
		if err == nil {
			return resp, usedModel, nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", p.Name(), err))
		// Fire fallback transition only when there's actually a next
		// provider to try — the last-attempt failure isn't a "fallback,"
		// it's terminal.
		if next := nextProviderName(order, i+1); next != "" {
			r.emitFallback(p.Name(), next, err, i)
		}
	}

	return nil, "", errors.Join(errs...)
}

// nextProviderName returns the next provider name in the order list at
// index `from`, or "" when from is past the end. Used to detect "is there
// somewhere to fall back to" before firing the observer — the LAST failure
// in the cascade is terminal, not a transition.
func nextProviderName(order []string, from int) string {
	if from < 0 || from >= len(order) {
		return ""
	}
	return order[from]
}

// filterToolCapable returns the subset of `order` whose providers report
// SupportsTools=true, with one exception: if the caller explicitly named
// `requested`, that provider survives even when it lacks tool support. That
// way `--provider offline` still works for users who actively opt in; only
// the silent-fallback path is closed off.
func (r *Router) filterToolCapable(order []string, requested string) []string {
	req := normalizeProviderName(requested)
	out := make([]string, 0, len(order))
	for _, name := range order {
		if name == req {
			out = append(out, name)
			continue
		}
		p, ok := r.Get(name)
		if !ok {
			// Keep unknown names so the caller still gets the existing
			// ErrProviderNotFound message instead of an empty cascade.
			out = append(out, name)
			continue
		}
		if p.Hints().SupportsTools {
			out = append(out, name)
		}
	}
	return out
}

func (r *Router) Stream(ctx context.Context, req CompletionRequest) (<-chan StreamEvent, string, error) {
	order := r.ResolveOrder(req.Provider)
	if len(req.Tools) > 0 {
		order = r.filterToolCapable(order, req.Provider)
	}
	if len(order) == 0 {
		return nil, "", fmt.Errorf("%w: all registered providers lack tool support for this request", ErrNoCapableProvider)
	}
	var errs []error

	for _, name := range order {
		// Same preflight as Complete: if the caller's ctx is already dead,
		// every provider's Stream would just echo ctx.Err() back and the
		// real cancel/deadline reason gets buried in errors.Join. Surface
		// it directly so callers see the sentinel they expect.
		if cerr := ctx.Err(); cerr != nil {
			if len(errs) == 0 {
				return nil, "", cerr
			}
			return nil, "", errors.Join(append(errs, cerr)...)
		}
		// Skip providers whose circuit is currently open. Mirrors Complete
		// so streaming asks under a sustained outage don't pay the round-trip
		// to the down primary on every fresh request.
		if r.shouldSkipForCircuit(name) {
			errs = append(errs, fmt.Errorf("%s: %w", name, ErrProviderUnavailable))
			continue
		}
		p, ok := r.Get(name)
		if !ok {
			errs = append(errs, fmt.Errorf("%w: %s", ErrProviderNotFound, name))
			continue
		}
		stream, usedModel, err := r.streamWithProviderRetry(ctx, p, req)
		// Stream errors at this point are stream-open errors only — once
		// we hand back the channel, mid-stream failures are out of band.
		// That's enough to drive the breaker because connection failures,
		// auth, and rate-limit-on-open all surface here.
		r.recordProviderHealth(name, err)
		if err == nil {
			// Wrap with mid-stream recovery: if the upstream drops the
			// connection before any content is delivered, the wrapper
			// silently re-tries the next eligible provider. Once content
			// has been forwarded to the caller, recovery is disabled
			// (splicing is too risky). See stream_recovery.go.
			out := make(chan StreamEvent, 32)
			go r.streamForwardWithRecovery(ctx, req, stream, name, out)
			return out, usedModel, nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", p.Name(), err))
	}

	return nil, "", errors.Join(errs...)
}
