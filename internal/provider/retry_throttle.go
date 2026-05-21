package provider

// retry_throttle.go — same-provider throttle retry. Honors
// ThrottledError with Retry-After (or exponential backoff for
// providers that don't surface one) up to maxThrottleRetries before
// surfacing to the fallback cascade. Other error paths are pass-through.
//
// Contract: Complete-side and Stream-side share throttleRetry[T] so
// retry counts, observer events, and Retry-After honoring stay in
// lockstep across both paths. Mid-stream throttle errors are NOT
// retried — once the channel is open, partial output has been seen
// and retrying would produce duplicate prefixes.

import (
	"context"
	"errors"
	"time"
)

const maxThrottleRetries = 3

// throttleRetry is the body shared by completeWithThrottleRetry and
// streamWithThrottleRetry. Honors ThrottledError with Retry-After or
// exponential backoff up to maxThrottleRetries; respects ctx
// cancellation during waits; emits ThrottleNotice on each retry.
// `stream` only tags the notice; semantics are identical otherwise.
func throttleRetry[T any](
	ctx context.Context,
	r *Router,
	provName string,
	stream bool,
	call func() (T, error),
) (T, error) {
	var zero T
	var lastErr error
	for attempt := 0; attempt <= maxThrottleRetries; attempt++ {
		v, err := call()
		if err == nil {
			return v, nil
		}
		if !errors.Is(err, ErrProviderThrottled) {
			return zero, err
		}
		lastErr = err
		if attempt == maxThrottleRetries {
			break
		}
		wait := throttleWait(err, attempt)
		r.emitThrottleNotice(ThrottleNotice{
			Provider: provName,
			Attempt:  attempt + 1,
			Wait:     wait,
			Stream:   stream,
			Err:      err,
		})
		if wait <= 0 {
			continue
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return zero, ctx.Err()
		case <-timer.C:
		}
	}
	if lastErr != nil {
		return zero, lastErr
	}
	return zero, ctx.Err()
}

// throttleWait prefers the upstream Retry-After hint when present,
// falling back to exponential backoff for providers that didn't surface
// one. Attempts are 0-indexed so the first backoff is 1s.
func throttleWait(err error, attempt int) time.Duration {
	var te *ThrottledError
	if errors.As(err, &te) && te.RetryAfter > 0 {
		return te.RetryAfter
	}
	return backoffForAttempt(attempt)
}

// completeWithThrottleRetry is the same-provider wrapper that honors
// ThrottledError. On 429/503 we wait (Retry-After if supplied,
// otherwise exponential backoff from 1s) and retry up to
// maxThrottleRetries before surfacing the error to the fallback
// cascade. Other error paths are passthrough.
func (r *Router) completeWithThrottleRetry(ctx context.Context, p Provider, req CompletionRequest) (*CompletionResponse, error) {
	return throttleRetry(ctx, r, p.Name(), false, func() (*CompletionResponse, error) {
		return p.Complete(ctx, req)
	})
}

// streamWithThrottleRetry mirrors completeWithThrottleRetry for the
// streaming path. Providers MAY return the throttle error synchronously
// from Stream() before the channel opens — that's the path retried here.
// Errors arriving over an already-open channel are passed through;
// retrying mid-stream is unsafe because partial output has been seen.
func (r *Router) streamWithThrottleRetry(ctx context.Context, p Provider, req CompletionRequest) (<-chan StreamEvent, error) {
	return throttleRetry(ctx, r, p.Name(), true, func() (<-chan StreamEvent, error) {
		return p.Stream(ctx, req)
	})
}
