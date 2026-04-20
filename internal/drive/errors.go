// Package drive implements the autonomous drive loop.
package drive

import (
	"context"
	"errors"
	"strings"
)

// FailureClass categorizes a sub-agent error into a retry strategy.
// This determines how applyOutcome handles a failed TODO.
type FailureClass int

const (
	// RetryTransient errors should be retried immediately with the same
	// provider — they represent self-correcting conditions (rate limits,
	// timeouts, temporary unavailability).
	RetryTransient FailureClass = iota
	// RetryWithFallback errors should be retried with a different provider
	// or model — the current provider is struggling with this specific
	// task but another may succeed.
	RetryWithFallback
	// Fatal errors should never be retried — retrying would produce the
	// same outcome and wastes budget. Examples: denied tools, bad auth,
	// malformed task.
	Fatal
)

// String returns a human-readable label for the failure class.
func (c FailureClass) String() string {
	switch c {
	case RetryTransient:
		return "transient"
	case RetryWithFallback:
		return "fallback"
	case Fatal:
		return "fatal"
	default:
		return "unknown"
	}
}

// FailureClassify returns the retry strategy for a given error.
// The classification is conservative: when in doubt, RetryTransient wins
// to avoid silently dropping work.
func FailureClassify(err error) FailureClass {
	if err == nil {
		return Fatal // nil is not an error; should not reach here
	}
	// Context cancellations are user-initiated stops — never retry.
	if errors.Is(err, context.Canceled) {
		return Fatal
	}
	// DeadlineExceeded is ambiguous: it may be a timeout (transient)
	// or a hard budget limit. Treat as transient — a retry with the same
	// or shorter budget may still make progress.
	if errors.Is(err, context.DeadlineExceeded) {
		return RetryTransient
	}
	msg := strings.ToLower(err.Error())

	// Fatal: user or approval denial. The model hit a hard wall.
	if strings.Contains(msg, "denied:") || strings.Contains(msg, "user denied") {
		return Fatal
	}
	if strings.Contains(msg, "approval denied") {
		return Fatal
	}

	// Fatal: authentication / authorization failures. No point retrying.
	if strings.Contains(msg, "unauthorized") ||
		strings.Contains(msg, "auth") && (strings.Contains(msg, "401") || strings.Contains(msg, "403")) {
		return Fatal
	}

	// Transient: explicit rate-limiting from a provider.
	if strings.Contains(msg, "rate limit") ||
		strings.Contains(msg, "status code 429") ||
		strings.Contains(msg, "429") && strings.Contains(msg, "too many") {
		return RetryTransient
	}

	// RetryWithFallback: provider/server errors that may succeed on a
	// different model or endpoint. Check these BEFORE generic timeout
	// to avoid "gateway timeout" being classified as transient.
	if strings.Contains(msg, "status code 500") ||
		strings.Contains(msg, "status code 502") ||
		strings.Contains(msg, "status code 503") ||
		strings.Contains(msg, "status code 504") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "network") && strings.Contains(msg, "unreachable") {
		return RetryWithFallback
	}

	// Transient: timeout-like conditions.
	if strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "timed out") ||
		strings.Contains(msg, "i/o timeout") {
		return RetryTransient
	}

	// RetryWithFallback: model-level errors from the LLM.
	if strings.Contains(msg, "model error") ||
		strings.Contains(msg, "overloaded") ||
		strings.Contains(msg, "service unavailable") {
		return RetryWithFallback
	}

	// Fatal: URL parse / certificate errors from BaseURL misconfig.
	if strings.Contains(msg, "invalid url") ||
		strings.Contains(msg, "x509") ||
		strings.Contains(msg, "certificate") {
		return Fatal
	}

	// Default: unknown error — retry once as transient before giving up.
	return RetryTransient
}

// RetryDecision describes what applyOutcome should do with a failed TODO.
type RetryDecision struct {
	Class   FailureClass
	Retried bool // true if this is already a retry attempt
}

// Decide returns the retry strategy for a failed TODO with the given
// error and current attempt count. Retries capped by cfg.Retries.
func (d *Driver) Decide(err error, attempt, maxRetries int) RetryDecision {
	class := FailureClassify(err)
	if class == Fatal {
		return RetryDecision{Class: Fatal}
	}
	if attempt < maxRetries {
		return RetryDecision{Class: class, Retried: true}
	}
	return RetryDecision{Class: class, Retried: false}
}

// IsTransient reports true when err is classified as RetryTransient.
func FailureClassifyIsTransient(err error) bool {
	return FailureClassify(err) == RetryTransient
}

// IsFallbackWorthy reports true when err should trigger a provider/model
// fallback on the next retry.
func FailureClassifyIsFallbackWorthy(err error) bool {
	return FailureClassify(err) == RetryWithFallback
}

// IsFatal reports true when err should never be retried.
func FailureClassifyIsFatal(err error) bool {
	return FailureClassify(err) == Fatal
}
