package engine

import (
	"errors"

	"github.com/dontfuckmycode/dfmc/internal/provider"
)

func (e *Engine) attachProviderObservers(r *provider.Router) {
	if e == nil || r == nil || e.EventBus == nil {
		return
	}
	r.SetThrottleObserver(func(n provider.ThrottleNotice) {
		retryAfterMs := int(n.Wait.Milliseconds())
		payload := map[string]any{
			"provider":       n.Provider,
			"attempt":        n.Attempt,
			"wait_ms":        retryAfterMs,
			"stream":         n.Stream,
			"error":          errString(n.Err),
			"retry_after_ms": retryAfterMs,
		}
		var te *provider.ThrottledError
		if errors.As(n.Err, &te) {
			payload["status_code"] = te.StatusCode
			payload["detail"] = te.Detail
		}
		e.EventBus.Publish(Event{
			Type:    "provider:throttle:retry",
			Source:  "provider",
			Payload: payload,
		})
	})
	// Circuit breaker transitions feed the same EventBus so the TUI
	// header / web Workbench / CLI status surface can render a
	// "primary down, using fallback" badge. open/closed is the only
	// transition state we publish; the cooldown duration is included
	// on open so UIs can show a countdown.
	r.SetCircuitObserver(func(ev provider.CircuitEvent) {
		payload := map[string]any{
			"provider": ev.Provider,
			"state":    ev.State,
		}
		if ev.Cooldown > 0 {
			payload["cooldown_ms"] = ev.Cooldown.Milliseconds()
		}
		eventType := "provider:circuit:open"
		if ev.State == "closed" {
			eventType = "provider:circuit:closed"
		}
		e.EventBus.Publish(Event{
			Type:    eventType,
			Source:  "provider",
			Payload: payload,
		})
	})
	// Stream recovery telemetry. Fires after the router silently swaps
	// providers mid-stream and the fallback delivered a clean StreamDone.
	// Without this hook, the recovery is invisible to the user — they
	// see "answer arrived" but not "the primary blew up halfway and
	// the fallback finished the job". TUI and web Workbench render a
	// transient "↻ resumed on <fallback>" chip on this event.
	r.SetStreamRecoveredObserver(func(ev provider.StreamRecoveredEvent) {
		e.EventBus.Publish(Event{
			Type:   "provider:stream:recovered",
			Source: "provider",
			Payload: map[string]any{
				"from":  ev.From,
				"to":    ev.To,
				"error": errString(ev.Err),
			},
		})
	})
}
