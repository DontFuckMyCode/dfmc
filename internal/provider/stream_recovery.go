package provider

import (
	"context"
	"errors"
	"fmt"
)

// streamForwardWithRecovery wraps a provider's stream channel and
// transparently recovers from a "stream opened, immediately failed"
// situation by re-trying the next provider in the cascade. The wrapper
// is conservative: once any text content has been delivered to the
// caller (or any tool_use signal — represented today by a non-empty
// Delta or a non-text terminal event), the stream is no longer
// recoverable and the original error surfaces. Splicing partial text
// across providers risks duplicate output and broken tool_use IDs;
// the safer behaviour is to let the caller handle the error.
//
// The recovery applies to the most common transient streaming failure:
// upstream returns 200 + opens an SSE channel, then drops the connection
// before sending any data (TLS reset, proxy timeout, momentary 5xx in
// the middle frame). Without recovery, the agent loop sees an empty
// answer and either retries the whole turn (losing prompt cache) or
// surfaces a confusing "stream errored" message. With recovery, the
// next provider in the cascade serves the same request silently.
//
// excludeProviders MUST include the provider that produced `current`
// so we don't re-pick it. Caller is responsible for closing `out`
// only via this function — it does so via defer.
func (r *Router) streamForwardWithRecovery(
	ctx context.Context,
	originalReq CompletionRequest,
	current <-chan StreamEvent,
	currentName string,
	out chan<- StreamEvent,
) {
	defer close(out)

	excluded := map[string]struct{}{currentName: {}}

	// forward consumes one upstream stream, copying non-terminal events
	// to `out`. Terminal events (StreamError, StreamDone) are NOT
	// forwarded; instead they are reported via the return value so the
	// outer recovery logic can decide whether to swap providers.
	//
	// Returns (terminalErr, hasContent). hasContent=true means any
	// StreamDelta with non-empty Delta was forwarded — at that point we
	// lose the right to swap providers (splicing would corrupt output),
	// so the caller surfaces the original error directly.
	forward := func(stream <-chan StreamEvent) (terminalErr error, hasContent bool) {
		for ev := range stream {
			if ev.Type == StreamError {
				return ev.Err, hasContent
			}
			if ev.Type == StreamDone {
				// Forward StreamDone unconditionally — it's the success
				// terminator the caller relies on.
				select {
				case out <- ev:
				case <-ctx.Done():
					return ctx.Err(), hasContent
				}
				return nil, hasContent
			}
			if ev.Type == StreamDelta && ev.Delta != "" {
				hasContent = true
			}
			select {
			case out <- ev:
			case <-ctx.Done():
				return ctx.Err(), hasContent
			}
		}
		// Channel closed without a terminal event — treat as an error
		// so the recovery branch can decide whether to retry.
		return errors.New("stream closed without terminal event"), hasContent
	}

	terminalErr, hasContent := forward(current)
	if terminalErr == nil {
		// Stream completed normally; nothing to recover.
		return
	}
	// Helper: surface the original error to the caller as a StreamError.
	// Used when we cannot or will not recover.
	emitErr := func(err error) {
		select {
		case out <- StreamEvent{Type: StreamError, Err: err}:
		case <-ctx.Done():
		}
	}
	if hasContent {
		// Splicing partial text or tool_use across providers risks
		// duplicate output and broken tool_use IDs. Surface the original
		// error and let the caller decide what to do.
		emitErr(terminalErr)
		return
	}
	if errors.Is(terminalErr, context.Canceled) || errors.Is(terminalErr, context.DeadlineExceeded) {
		// Caller-driven cancel — surface the sentinel without wasting a
		// fallback attempt that would also return the same sentinel.
		emitErr(terminalErr)
		return
	}
	if !isTransient(terminalErr) {
		// Non-transient (auth, malformed) — recovery on the next provider
		// will probably hit the same misconfiguration. Surface the
		// original error so the caller sees the real failure reason.
		emitErr(terminalErr)
		return
	}

	// Recovery path: walk the resolved order, skip excluded + circuit-open
	// providers, and try one more stream. At most one recovery attempt
	// per call so we never spiral if the whole cascade is sick.
	order := r.ResolveOrder(originalReq.Provider)
	if len(originalReq.Tools) > 0 {
		order = r.filterToolCapable(order, originalReq.Provider)
	}
	for _, name := range order {
		if _, skip := excluded[name]; skip {
			continue
		}
		if r.shouldSkipForCircuit(name) {
			continue
		}
		p, ok := r.Get(name)
		if !ok {
			continue
		}
		nextStream, _, nerr := r.streamWithProviderRetry(ctx, p, originalReq)
		r.recordProviderHealth(name, nerr)
		if nerr != nil {
			excluded[name] = struct{}{}
			continue
		}
		// Forward the recovered stream. Note: from the caller's
		// perspective, the only visible signal of recovery is a fresh
		// StreamStart event from the new provider (which has Provider /
		// Model fields populated). We accept that the original Provider
		// returned by Stream() may now be wrong — but the caller asked
		// for "a stream that delivers events", and that's what they get.
		// Mid-stream provider switching is the whole point.
		recoveredErr, _ := forward(nextStream)
		if recoveredErr != nil {
			// Recovered stream also failed — surface the secondary error
			// so the caller knows recovery was attempted but did not
			// produce a clean answer. Bound at one recovery attempt.
			emitErr(fmt.Errorf("stream recovery on %s also failed: %w", name, recoveredErr))
			return
		}
		// Recovery succeeded — fire the observer so the engine can
		// publish a `provider:stream:recovered` event. The observer is
		// nil-safe; tests that build a Router without engine wiring see
		// no telemetry but still get correct stream output.
		r.healthMu.Lock()
		obs := r.streamRecoveredObserver
		r.healthMu.Unlock()
		if obs != nil {
			obs(StreamRecoveredEvent{From: currentName, To: name, Err: terminalErr})
		}
		return
	}

	// No fallback succeeded — emit the original error so the caller sees
	// a definitive failure reason instead of a silent close.
	emitErr(fmt.Errorf("stream failed on %s and no fallback succeeded: %w", currentName, terminalErr))
}
