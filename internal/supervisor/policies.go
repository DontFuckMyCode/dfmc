package supervisor

import (
	"strings"
	"time"
)

// FailureClass classifies why a task failed so the supervisor can decide
// whether to retry, abort, or escalate.
type FailureClass uint8

const (
	// FailureRetryable means the failure was transient — rate limit, timeout,
	// network glitch, or similar. The task can be retried.
	FailureRetryable FailureClass = iota

	// FailurePermanent means the task failed due to a code or logic error that
	// retrying will not fix (syntax error, type error, rejected edit, etc.).
	FailurePermanent

	// FailureEscalate means the failure indicates a serious problem requiring
	// human attention — security issue, infinite loop detected, budget panic,
	// or similar.
	FailureEscalate

	// FailureWaiting means the task cannot proceed and is waiting on an
	// external signal — user input, another system, manual approval, etc.
	FailureWaiting

	// FailureExternalReview means the task requires human review before
	// it can proceed (e.g., a security finding needs human judgment).
	FailureExternalReview
)

// String returns a human-readable class name.
func (fc FailureClass) String() string {
	switch fc {
	case FailureRetryable:
		return "retryable"
	case FailurePermanent:
		return "permanent"
	case FailureEscalate:
		return "escalate"
	case FailureWaiting:
		return "waiting"
	case FailureExternalReview:
		return "external_review"
	default:
		return "unknown"
	}
}

// RetryPolicy governs how many times a task may be retried and with what
// backoff schedule.
type RetryPolicy struct {
	// MaxAttempts is the total number of attempts allowed (including the
	// initial attempt). A value <= 0 means no retries.
	MaxAttempts int
	// BaseBackoff is the initial wait time between retries. Each subsequent
	// retry doubles the wait (exponential backoff). A zero value means
	// retry immediately.
	BaseBackoff time.Duration
	// MaxBackoff caps the backoff at this value. A zero value means no cap.
	MaxBackoff time.Duration
}

// DefaultRetryPolicy is the policy used when none is specified.
var DefaultRetryPolicy = RetryPolicy{
	MaxAttempts:  3,
	BaseBackoff:  2 * time.Second,
	MaxBackoff:  30 * time.Second,
}

// ShouldRetry returns true when the given failure class should be retried
// at the given attempt number.
func (p RetryPolicy) ShouldRetry(fc FailureClass, attempt int) bool {
	if attempt >= p.MaxAttempts {
		return false
	}
	switch fc {
	case FailureRetryable:
		return true
	case FailurePermanent, FailureEscalate, FailureWaiting, FailureExternalReview:
		return false
	default:
		return false
	}
}

// BackoffFor returns the wait duration before the given retry attempt number.
// Attempt 1 means first retry (no wait before it with DefaultRetryPolicy).
func (p RetryPolicy) BackoffFor(attempt int) time.Duration {
	if attempt <= 1 {
		return 0 // first retry is immediate
	}
	backoff := p.BaseBackoff * time.Duration(1<<(attempt-1))
	if p.MaxBackoff > 0 && backoff > p.MaxBackoff {
		return p.MaxBackoff
	}
	return backoff
}

// ClassifyFailure inspects an error and the associated tool name and returns
// a FailureClass. The classifier uses string matching on the error message
// and tool name as a best-effort heuristic.
func ClassifyFailure(err error, toolName string) FailureClass {
	if err == nil {
		return FailurePermanent // nil error means success; this should never be called
	}
	msg := strings.ToLower(err.Error())

	// Escalate signals — never retry
	if strings.Contains(msg, "security") ||
		strings.Contains(msg, "infinite loop") ||
		strings.Contains(msg, "malicious") ||
		strings.Contains(msg, "data exfiltration") ||
		strings.Contains(msg, "prompt injection") ||
		strings.Contains(msg, "unsafe edit") {
		return FailureEscalate
	}

	// Permanent failures — retry will not help
	if strings.Contains(msg, "syntax error") ||
		strings.Contains(msg, "type error") ||
		strings.Contains(msg, "compile failed") ||
		strings.Contains(msg, "cannot find") && strings.Contains(msg, "symbol") ||
		strings.Contains(msg, "undefined") ||
		strings.Contains(msg, "rejected") && strings.Contains(msg, "edit") ||
		strings.Contains(msg, "refused") && strings.Contains(msg, "edit") ||
		strings.Contains(msg, "invalid patch") ||
		strings.Contains(msg, "malformed") ||
		(strings.Contains(msg, "no such file") && !strings.Contains(msg, "read")) ||
		strings.Contains(msg, "permission denied") ||
		strings.Contains(msg, "hash mismatch") ||
		strings.Contains(msg, "concurrent") {
		return FailurePermanent
	}

	// Retryable signals
	if strings.Contains(msg, "rate limit") ||
		strings.Contains(msg, "429") ||
		strings.Contains(msg, "503") ||
		strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "context limit") ||
		strings.Contains(msg, "budget exhausted") ||
		strings.Contains(msg, "max steps") ||
		strings.Contains(msg, "token limit") ||
		strings.Contains(msg, "read timeout") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "temporary failure") ||
		strings.Contains(msg, "unavailable") {
		return FailureRetryable
	}

	// Tool-specific heuristics
	switch toolName {
	case "run_command":
		if strings.Contains(msg, "exit code 1") ||
			strings.Contains(msg, "non-zero") ||
			strings.Contains(msg, "failed") {
			return FailureRetryable
		}
	}

	// Default: fail open — treat unknown errors as retryable so we don't
	// permanently block a task for an unexpected but recoverable error.
	return FailureRetryable
}
