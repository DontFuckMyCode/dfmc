package provider

// retry_chain.go — provider-model chain retry. Walks p.Models()
// trying each model with a per-model throttle retry; on
// ErrContextOverflow it compacts the request via retry_context.go's
// compactMessagesForRetry and retries the SAME model once. Transient
// (5xx / network) errors continue to the next model in the chain;
// deterministic (auth, malformed request) errors break out so we don't
// burn the whole chain on the same root cause.
//
// Two thin wrappers expose the chain to the Complete and Stream paths
// in router.go. Empty Models() collapses to a single throttle call
// against the request as-is.

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// modelChainRetry is the body shared by completeWithProviderRetry and
// streamWithProviderRetry. It walks p.Models() trying each model with
// a per-model throttle retry; on ErrContextOverflow it compacts the
// request and retries the SAME model once before falling through to
// the next. Transient (5xx/network) errors continue to the next model;
// deterministic ones break out. Empty model chain → single throttle
// call against the request as-is.
func modelChainRetry[T any](
	ctx context.Context,
	r *Router,
	p Provider,
	req CompletionRequest,
	throttle func(CompletionRequest) (T, error),
) (T, string, error) {
	var zero T
	models := modelAttemptOrder(req.Model, p.Models())
	if len(models) == 0 {
		v, err := throttle(req)
		return v, p.Name(), err
	}
	var errs []error
	for _, model := range models {
		// Per-iteration ctx check: once ctx is dead, every subsequent
		// model would just echo the sentinel back, so surface it
		// directly instead of running the rest of the chain on an
		// already-cancelled context.
		if cerr := ctx.Err(); cerr != nil {
			if len(errs) == 0 {
				return zero, p.Name(), cerr
			}
			return zero, p.Name(), errors.Join(append(errs, cerr)...)
		}
		reqM := req
		reqM.Model = model
		v, err := throttle(reqM)
		if err == nil {
			return v, p.Name(), nil
		}
		if isContextOverflow(err) {
			// Compact and retry the SAME model before giving up.
			compacted, trimmed := compactMessagesForRetry(reqM.Messages)
			if trimmed > 0 {
				retryReq := reqM
				retryReq.Messages = compacted
				v2, err2 := throttle(retryReq)
				if err2 == nil {
					return v2, p.Name(), nil
				}
				errs = append(errs, fmt.Errorf("%s (model %s, context overflow, compacted %d msgs, retry failed): %w", p.Name(), model, trimmed, err2))
			} else {
				errs = append(errs, fmt.Errorf("%s (model %s): %w", p.Name(), model, err))
			}
			continue // try next model in chain
		}
		errs = append(errs, fmt.Errorf("%s (model %s): %w", p.Name(), model, err))
		// Transient (5xx, I/O, network) failures are what FallbackModels
		// exist for — try the next model. Auth/4xx-non-throttle and
		// other deterministic failures break so we don't burn the whole
		// chain on the same root cause.
		if !isTransient(err) {
			break
		}
	}
	return zero, p.Name(), errors.Join(errs...)
}

func modelAttemptOrder(requested string, chain []string) []string {
	requested = strings.TrimSpace(requested)
	out := make([]string, 0, len(chain)+1)
	if requested != "" {
		out = append(out, requested)
	}
	for _, model := range chain {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		seen := false
		for _, existing := range out {
			if strings.EqualFold(existing, model) {
				seen = true
				break
			}
		}
		if !seen {
			out = append(out, model)
		}
	}
	return out
}

// completeWithProviderRetry tries a provider's model chain (primary + FallbackModels)
// for non-streaming completions.
func (r *Router) completeWithProviderRetry(ctx context.Context, p Provider, req CompletionRequest) (*CompletionResponse, string, error) {
	return modelChainRetry(ctx, r, p, req, func(rq CompletionRequest) (*CompletionResponse, error) {
		return r.completeWithThrottleRetry(ctx, p, rq)
	})
}

// streamWithProviderRetry tries a provider's model chain for streaming
// completions. Same retry/compaction/transient-fallback semantics as
// completeWithProviderRetry; see modelChainRetry for the body.
func (r *Router) streamWithProviderRetry(ctx context.Context, p Provider, req CompletionRequest) (<-chan StreamEvent, string, error) {
	return modelChainRetry(ctx, r, p, req, func(rq CompletionRequest) (<-chan StreamEvent, error) {
		return r.streamWithThrottleRetry(ctx, p, rq)
	})
}

// isTransient reports whether err looks recoverable by retrying — either
// the same model after backoff, or the next model in the provider's
// fallback chain. Intentionally conservative: we only return true for
// signals that are very unlikely to repeat on the next attempt. Auth
// failures, malformed requests, and ctx cancellation are all NOT
// transient and must surface immediately so the caller sees the real
// reason instead of a wall of retry noise.
//
// ErrProviderThrottled is handled separately by completeWithThrottleRetry
// (with Retry-After + bounded retries on the SAME model), so we don't
// duplicate that branch here.
func isTransient(err error) bool {
	if err == nil {
		return false
	}
	// Caller-driven cancel/deadline is authoritative — never retry past it.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, ErrProviderUnavailable) {
		return true
	}
	// Primary path: providers that wrap upstream HTTP failures in
	// *StatusError get classified by exact StatusCode. All first-party
	// providers (anthropic, openai-compatible, google) do this; the
	// status_error_contract_test.go suite locks the contract in.
	var se *StatusError
	if errors.As(err, &se) {
		return se.IsTransient()
	}
	// Fallback path — strictly belt-and-suspenders, NOT load-bearing.
	// Reached only when:
	//   - a future contributor wires up a Provider that returns plain
	//     fmt.Errorf strings instead of *StatusError, OR
	//   - the failure originates below the provider layer (raw net.* /
	//     transport errors that never get wrapped before reaching us).
	// If you find yourself relying on the substrings below for a
	// first-party provider, that provider has regressed — fix it to
	// return *StatusError instead of expanding this list.
	msg := strings.ToLower(err.Error())
	for _, marker := range []string{
		// Stringified upstream 5xx, both common phrasings.
		" status 500", " status 502", " status 503", " status 504",
		"status code 500", "status code 502", "status code 503", "status code 504",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	for _, marker := range []string{
		// Network-layer hiccups — DNS, dial, mid-request EOF, idle reset.
		"connection refused",
		"connection reset",
		"no such host",
		"i/o timeout",
		"unexpected eof",
		"broken pipe",
		"tls handshake timeout",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}
