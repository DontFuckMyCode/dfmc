// Helpers for detecting and wrapping rate-limit / overload responses.
//
// Every HTTP-backed provider (Anthropic, OpenAI-compat, Google) funnels
// its 429 / 503 error paths through newThrottledErrorFromResponse so the
// router sees a consistent ThrottledError with a parsed Retry-After
// hint. Providers that can't populate a response object (e.g. network
// failures) can still synthesize a ThrottledError via
// newThrottledError with a zero hint — the router will fall back to
// exponential backoff.

package provider

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// isThrottleStatus reports whether a status code is a transient
// upstream condition that should trigger retry rather than immediate
// fallback. 429 (Too Many Requests) and 503 (Service Unavailable) are
// the classic retry-worthy codes. 408 (Request Timeout) is tempting
// but rare in practice; we leave it as a hard fail for now to keep
// the policy tight.
func isThrottleStatus(code int) bool {
	return code == http.StatusTooManyRequests || code == http.StatusServiceUnavailable
}

// parseRetryAfter extracts the Retry-After header into a duration.
// HTTP spec allows either a delta-seconds integer ("120") or an
// HTTP-date ("Fri, 31 Dec 1999 23:59:59 GMT"). Absent header or
// unparseable value returns (0, false). The clamp to a sensible
// maximum (5 minutes) prevents a rogue upstream from parking an agent
// indefinitely — callers should treat the hint as advisory.
func parseRetryAfter(h http.Header) (time.Duration, bool) {
	raw := strings.TrimSpace(h.Get("Retry-After"))
	if raw == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(raw); err == nil && secs >= 0 {
		d := time.Duration(secs) * time.Second
		return clampRetryAfter(d), true
	}
	if t, err := http.ParseTime(raw); err == nil {
		d := time.Until(t)
		if d < 0 {
			d = 0
		}
		return clampRetryAfter(d), true
	}
	return 0, false
}

func clampRetryAfter(d time.Duration) time.Duration {
	const maxHint = 5 * time.Minute
	if d < 0 {
		return 0
	}
	if d > maxHint {
		return maxHint
	}
	return d
}

// newThrottledErrorFromResponse wraps an HTTP response that carries a
// 429/503 into a ThrottledError with the Retry-After hint parsed out.
// body is the response body already read by the caller so we can
// include a trimmed excerpt in the error message for operator triage.
func newThrottledErrorFromResponse(providerName string, resp *http.Response, body string) *ThrottledError {
	hint, _ := parseRetryAfter(resp.Header)
	excerpt := strings.TrimSpace(body)
	if len(excerpt) > 300 {
		excerpt = excerpt[:300] + "…"
	}
	return &ThrottledError{
		Provider:   providerName,
		StatusCode: resp.StatusCode,
		RetryAfter: hint,
		Detail:     fmt.Sprintf("%s throttled: status %d (retry-after=%s): %s", providerName, resp.StatusCode, hint, excerpt),
	}
}

// backoffForAttempt returns the default wait between throttle retries
// when the provider didn't supply a Retry-After hint. Exponential
// starting at 1s, clamped at 30s: 1s, 2s, 4s, 8s, 16s, 30s, 30s, ...
// Attempt is 0-indexed; attempt=0 returns the first backoff (1s).
func backoffForAttempt(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	// 1 << 6 == 64s, so any attempt >=6 lands on the cap.
	if attempt > 6 {
		attempt = 6
	}
	d := time.Duration(1<<attempt) * time.Second
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	return d
}
