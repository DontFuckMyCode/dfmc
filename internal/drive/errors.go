// Package drive implements the autonomous drive loop.
package drive

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Sentinel errors for robust failure classification via errors.Is.
var (
	// Denial errors — never retry.
	ErrDenied         = errors.New("denied")
	ErrUserDenied     = errors.New("user denied")
	ErrApprovalDenied = errors.New("approval denied")

	// Auth errors — never retry.
	ErrUnauthorized = errors.New("unauthorized")
	ErrAuth401      = errors.New("auth 401")
	ErrAuth403      = errors.New("auth 403")

	// Rate limit / transient errors — retry immediately.
	ErrRateLimit  = errors.New("rate limit")
	ErrStatus429  = errors.New("status code 429")
	ErrTooManyReq = errors.New("too many requests")

	// Fallback-worthy errors — retry with different provider/model.
	ErrStatus500      = errors.New("status code 500")
	ErrStatus502      = errors.New("status code 502")
	ErrStatus503      = errors.New("status code 503")
	ErrStatus504      = errors.New("status code 504")
	ErrNoSuchHost     = errors.New("no such host")
	ErrConnRefused    = errors.New("connection refused")
	ErrConnReset      = errors.New("connection reset")
	ErrNetUnreachable = errors.New("network unreachable")

	// Timeout errors — retry with same or shorter budget.
	ErrTimeout   = errors.New("timeout")
	ErrTimedOut  = errors.New("timed out")
	ErrIOTimeout = errors.New("i/o timeout")

	// Model-level errors — fallback recommended.
	ErrModelError         = errors.New("model error")
	ErrOverloaded         = errors.New("overloaded")
	ErrServiceUnavailable = errors.New("service unavailable")

	// URL/cert errors — config problem, never retry.
	ErrInvalidURL  = errors.New("invalid url")
	ErrX509        = errors.New("x509")
	ErrCertificate = errors.New("certificate")
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
//
// Three-phase cascade:
//  1. classifyByContext: ctx.Canceled / ctx.DeadlineExceeded — handled
//     first so a user Ctrl+C never falls through to the network heuristics.
//  2. classifyBySentinel: typed sentinel errors (errors.Is) — the
//     preferred shape; survives wrapping via fmt.Errorf("…: %w", err).
//  3. classifyByMessage: lowercased substring match on err.Error() —
//     last-resort for third-party errors that don't expose sentinels.
//
// Each phase returns (FailureClass, true) on a hit, (_, false) to
// fall through. Unknown errors default to RetryTransient (retry once
// before giving up) rather than Fatal (silently dropped work).
func FailureClassify(err error) FailureClass {
	if err == nil {
		return Fatal // nil is not an error; should not reach here
	}
	if class, ok := classifyByContext(err); ok {
		return class
	}
	if class, ok := classifyBySentinel(err); ok {
		return class
	}
	if class, ok := classifyByMessage(err); ok {
		return class
	}
	// Default: unknown error — retry once as transient before giving up.
	return RetryTransient
}

// classifyByContext handles the two stdlib context errors. Cancelled
// is always user-initiated and must NOT be retried; DeadlineExceeded
// is ambiguous (budget overrun vs. transient stall) and treated as
// transient so a tighter retry can still make progress.
func classifyByContext(err error) (FailureClass, bool) {
	if errors.Is(err, context.Canceled) {
		return Fatal, true
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return RetryTransient, true
	}
	return 0, false
}

// classifyBySentinel is the preferred match path — typed sentinels
// survive wrapping via fmt.Errorf("…: %w", err) and errors.Is(),
// unlike string-matching on err.Error() which can silently break
// when an upstream library reflows its message.
func classifyBySentinel(err error) (FailureClass, bool) {
	switch {
	case errors.Is(err, ErrDenied), errors.Is(err, ErrUserDenied), errors.Is(err, ErrApprovalDenied):
		return Fatal, true
	case errors.Is(err, ErrUnauthorized), errors.Is(err, ErrAuth401), errors.Is(err, ErrAuth403):
		return Fatal, true
	case errors.Is(err, ErrRateLimit), errors.Is(err, ErrStatus429), errors.Is(err, ErrTooManyReq):
		return RetryTransient, true
	case errors.Is(err, ErrStatus500), errors.Is(err, ErrStatus502),
		errors.Is(err, ErrStatus503), errors.Is(err, ErrStatus504),
		errors.Is(err, ErrNoSuchHost), errors.Is(err, ErrConnRefused),
		errors.Is(err, ErrConnReset), errors.Is(err, ErrNetUnreachable):
		return RetryWithFallback, true
	case errors.Is(err, ErrTimeout), errors.Is(err, ErrTimedOut), errors.Is(err, ErrIOTimeout):
		return RetryTransient, true
	case errors.Is(err, ErrModelError), errors.Is(err, ErrOverloaded), errors.Is(err, ErrServiceUnavailable):
		return RetryWithFallback, true
	case errors.Is(err, ErrInvalidURL), errors.Is(err, ErrX509), errors.Is(err, ErrCertificate):
		return Fatal, true
	}
	return 0, false
}

// classifyByMessage is the last-resort fallback for wrapped /
// third-party errors that don't expose a typed sentinel. Pattern
// list mirrors classifyBySentinel's groups in the same order so a
// future migration of an error from "string-only" to "has a
// sentinel" doesn't change classification.
func classifyByMessage(err error) (FailureClass, bool) {
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "denied:"), strings.Contains(msg, "user denied"), strings.Contains(msg, "approval denied"):
		return Fatal, true
	case strings.Contains(msg, "unauthorized"),
		strings.Contains(msg, "auth") && (strings.Contains(msg, "401") || strings.Contains(msg, "403")):
		return Fatal, true
	case strings.Contains(msg, "rate limit"),
		strings.Contains(msg, "status code 429"),
		strings.Contains(msg, "429") && strings.Contains(msg, "too many"):
		return RetryTransient, true
	case strings.Contains(msg, "status code 500"), strings.Contains(msg, "status code 502"),
		strings.Contains(msg, "status code 503"), strings.Contains(msg, "status code 504"),
		strings.Contains(msg, "status code: 5"),
		strings.Contains(msg, "no such host"), strings.Contains(msg, "connection refused"),
		strings.Contains(msg, "connection reset"),
		strings.Contains(msg, "network") && strings.Contains(msg, "unreachable"):
		return RetryWithFallback, true
	case strings.Contains(msg, "timeout"), strings.Contains(msg, "timed out"), strings.Contains(msg, "i/o timeout"):
		return RetryTransient, true
	case strings.Contains(msg, "model error"), strings.Contains(msg, "overloaded"), strings.Contains(msg, "service unavailable"):
		return RetryWithFallback, true
	case strings.Contains(msg, "invalid url"), strings.Contains(msg, "x509"), strings.Contains(msg, "certificate"):
		return Fatal, true
	}
	return 0, false
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

// ─────────────────────────────────────────────────────────────────
// ErrorCategorizer — collates error statistics for observability
// ─────────────────────────────────────────────────────────────────

// ErrorStats holds aggregated error counts per classification.
type ErrorStats struct {
	Transient  int // RetryTransient count
	Fallback   int // RetryWithFallback count
	Fatal      int // Fatal count
	Total      int // sum of all
	ByProvider map[string]int
	ByTool     map[string]int
}

// NewErrorStats initializes an empty stats accumulator.
func NewErrorStats() *ErrorStats {
	return &ErrorStats{
		ByProvider: make(map[string]int),
		ByTool:     make(map[string]int),
	}
}

// Record increments counts for the given classification, provider, and tool.
func (s *ErrorStats) Record(class FailureClass, provider, tool string) {
	s.Total++
	switch class {
	case RetryTransient:
		s.Transient++
	case RetryWithFallback:
		s.Fallback++
	case Fatal:
		s.Fatal++
	}
	if provider != "" {
		s.ByProvider[provider]++
	}
	if tool != "" {
		s.ByTool[tool]++
	}
}

// Merge adds counts from another ErrorStats into s.
func (s *ErrorStats) Merge(o *ErrorStats) {
	s.Transient += o.Transient
	s.Fallback += o.Fallback
	s.Fatal += o.Fatal
	s.Total += o.Total
	for k, v := range o.ByProvider {
		s.ByProvider[k] += v
	}
	for k, v := range o.ByTool {
		s.ByTool[k] += v
	}
}

// RetryRate returns the fraction of errors that are RetryTransient (0..1).
func (s *ErrorStats) RetryRate() float64 {
	if s.Total == 0 {
		return 0
	}
	return float64(s.Transient) / float64(s.Total)
}

// FatalRate returns the fraction of errors that are Fatal (0..1).
func (s *ErrorStats) FatalRate() float64 {
	if s.Total == 0 {
		return 0
	}
	return float64(s.Fatal) / float64(s.Total)
}

// ErrorCategorizer tracks error stats and can classify new errors.
// Zero-value is ready to use.
type ErrorCategorizer struct {
	Stats       *ErrorStats
	ByProvider  map[string]*ErrorStats // per-provider breakdown
	ByTool      map[string]*ErrorStats // per-tool breakdown
	TotalErrors int
}

// NewErrorCategorizer creates a fresh categorizer.
func NewErrorCategorizer() *ErrorCategorizer {
	return &ErrorCategorizer{
		Stats:      NewErrorStats(),
		ByProvider: make(map[string]*ErrorStats),
		ByTool:     make(map[string]*ErrorStats),
	}
}

// RecordError classifies err and records it into aggregate and breakdown stats.
func (c *ErrorCategorizer) RecordError(err error, provider, tool string) {
	class := FailureClassify(err)
	c.Stats.Record(class, provider, tool)
	c.TotalErrors++

	if provider != "" {
		if c.ByProvider[provider] == nil {
			c.ByProvider[provider] = NewErrorStats()
		}
		c.ByProvider[provider].Record(class, provider, tool)
	}

	if tool != "" {
		if c.ByTool[tool] == nil {
			c.ByTool[tool] = NewErrorStats()
		}
		c.ByTool[tool].Record(class, provider, tool)
	}
}

// TopProviders returns the top N providers by total error count.
func (c *ErrorCategorizer) TopProviders(n int) []string {
	type kv struct {
		k string
		v int
	}
	var sorted []kv
	for k, v := range c.ByProvider {
		sorted = append(sorted, kv{k, v.Total})
	}
	for i := 0; i < len(sorted)-1; i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].v > sorted[i].v {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	if n > len(sorted) {
		n = len(sorted)
	}
	out := make([]string, n)
	for i := 0; i < n; i++ {
		out[i] = sorted[i].k
	}
	return out
}

// Summary returns a human-readable summary of the categorizer stats.
func (c *ErrorCategorizer) Summary() string {
	if c.TotalErrors == 0 {
		return "ErrorCategorizer: no errors recorded"
	}
	return fmt.Sprintf(
		"ErrorCategorizer: %d total | retry=%d (%.1f%%) | fallback=%d | fatal=%d (%.1f%%)",
		c.TotalErrors,
		c.Stats.Transient, c.Stats.RetryRate()*100,
		c.Stats.Fallback,
		c.Stats.Fatal, c.Stats.FatalRate()*100,
	)
}

// MetricsExport represents a structured export of all error stats.
type MetricsExport struct {
	Total      ErrorStats            `json:"total"`
	ByProvider map[string]ErrorStats `json:"by_provider"`
	ByTool     map[string]ErrorStats `json:"by_tool"`
	RetryRate  float64               `json:"retry_rate"`
	FatalRate  float64               `json:"fatal_rate"`
	Timestamp  time.Time             `json:"timestamp"`
}

// Export returns a serializable metrics snapshot.
func (c *ErrorCategorizer) Export() MetricsExport {
	byProvider := make(map[string]ErrorStats)
	for k, v := range c.ByProvider {
		byProvider[k] = *v
	}
	byTool := make(map[string]ErrorStats)
	for k, v := range c.ByTool {
		byTool[k] = *v
	}
	return MetricsExport{
		Total:      *c.Stats,
		ByProvider: byProvider,
		ByTool:     byTool,
		RetryRate:  c.Stats.RetryRate(),
		FatalRate:  c.Stats.FatalRate(),
		Timestamp:  time.Now(),
	}
}
