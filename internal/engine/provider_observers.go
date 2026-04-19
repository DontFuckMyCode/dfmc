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
}
