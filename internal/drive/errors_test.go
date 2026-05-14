package drive

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestFailureClassify_SentinelErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want FailureClass
	}{
		// Denial errors → Fatal
		{"ErrDenied", ErrDenied, Fatal},
		{"ErrUserDenied", ErrUserDenied, Fatal},
		{"ErrApprovalDenied", ErrApprovalDenied, Fatal},
		// Auth errors → Fatal
		{"ErrUnauthorized", ErrUnauthorized, Fatal},
		{"ErrAuth401", ErrAuth401, Fatal},
		{"ErrAuth403", ErrAuth403, Fatal},
		// Rate limit / transient → RetryTransient
		{"ErrRateLimit", ErrRateLimit, RetryTransient},
		{"ErrStatus429", ErrStatus429, RetryTransient},
		{"ErrTooManyReq", ErrTooManyReq, RetryTransient},
		// Fallback-worthy → RetryWithFallback
		{"ErrStatus500", ErrStatus500, RetryWithFallback},
		{"ErrStatus502", ErrStatus502, RetryWithFallback},
		{"ErrStatus503", ErrStatus503, RetryWithFallback},
		{"ErrStatus504", ErrStatus504, RetryWithFallback},
		{"ErrNoSuchHost", ErrNoSuchHost, RetryWithFallback},
		{"ErrConnRefused", ErrConnRefused, RetryWithFallback},
		{"ErrConnReset", ErrConnReset, RetryWithFallback},
		{"ErrNetUnreachable", ErrNetUnreachable, RetryWithFallback},
		// Timeout errors → RetryTransient
		{"ErrTimeout", ErrTimeout, RetryTransient},
		{"ErrTimedOut", ErrTimedOut, RetryTransient},
		{"ErrIOTimeout", ErrIOTimeout, RetryTransient},
		// Model-level errors → RetryWithFallback
		{"ErrModelError", ErrModelError, RetryWithFallback},
		{"ErrOverloaded", ErrOverloaded, RetryWithFallback},
		{"ErrServiceUnavailable", ErrServiceUnavailable, RetryWithFallback},
		// Config errors → Fatal
		{"ErrInvalidURL", ErrInvalidURL, Fatal},
		{"ErrX509", ErrX509, Fatal},
		{"ErrCertificate", ErrCertificate, Fatal},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FailureClassify(tc.err)
			if got != tc.want {
				t.Errorf("FailureClassify(%v) = %v; want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestFailureClassify_WrappedErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want FailureClass
	}{
		// Wrapped denial → still Fatal
		{"wrapped ErrDenied", fmt.Errorf("api call failed: %w", ErrDenied), Fatal},
		{"double-wrapped ErrDenied", fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", ErrDenied)), Fatal},
		{"wrapped ErrUnauthorized", fmt.Errorf("auth: %w", ErrUnauthorized), Fatal},
		{"wrapped ErrAuth403", fmt.Errorf("http: %w", ErrAuth403), Fatal},
		// Wrapped rate limit → RetryTransient
		{"wrapped ErrRateLimit", fmt.Errorf("request: %w", ErrRateLimit), RetryTransient},
		{"wrapped ErrStatus429", fmt.Errorf("response: %w", ErrStatus429), RetryTransient},
		// Wrapped fallback-worthy → RetryWithFallback
		{"wrapped ErrStatus500", fmt.Errorf("upstream: %w", ErrStatus500), RetryWithFallback},
		{"wrapped ErrStatus503", fmt.Errorf("service: %w", ErrStatus503), RetryWithFallback},
		{"wrapped ErrNoSuchHost", fmt.Errorf("network: %w", ErrNoSuchHost), RetryWithFallback},
		{"wrapped ErrConnRefused", fmt.Errorf("dial: %w", ErrConnRefused), RetryWithFallback},
		// Wrapped timeout → RetryTransient
		{"wrapped ErrTimeout", fmt.Errorf("call: %w", ErrTimeout), RetryTransient},
		{"wrapped ErrIOTimeout", fmt.Errorf("io: %w", ErrIOTimeout), RetryTransient},
		// Wrapped model errors → RetryWithFallback
		{"wrapped ErrOverloaded", fmt.Errorf("model: %w", ErrOverloaded), RetryWithFallback},
		{"wrapped ErrServiceUnavailable", fmt.Errorf("provider: %w", ErrServiceUnavailable), RetryWithFallback},
		// Wrapped config errors → Fatal
		{"wrapped ErrX509", fmt.Errorf("tls: %w", ErrX509), Fatal},
		{"wrapped ErrCertificate", fmt.Errorf("cert: %w", ErrCertificate), Fatal},
		// Wrapped context errors
		{"wrapped DeadlineExceeded", fmt.Errorf("context: %w", context.DeadlineExceeded), RetryTransient},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FailureClassify(tc.err)
			if got != tc.want {
				t.Errorf("FailureClassify(%v) = %v; want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestFailureClassify_ContextErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want FailureClass
	}{
		{"context.Canceled", context.Canceled, Fatal},
		{"context.DeadlineExceeded", context.DeadlineExceeded, RetryTransient},
		{"wrapped Canceled", fmt.Errorf("operation: %w", context.Canceled), Fatal},
		{"wrapped DeadlineExceeded", fmt.Errorf("operation: %w", context.DeadlineExceeded), RetryTransient},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FailureClassify(tc.err)
			if got != tc.want {
				t.Errorf("FailureClassify(%v) = %v; want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestFailureClassify_StringFallback(t *testing.T) {
	// String matching fallback for third-party/unknown errors
	cases := []struct {
		name string
		err  error
		want FailureClass
	}{
		{"denied: permission", errors.New("denied: permission"), Fatal},
		{"unauthorized access", errors.New("unauthorized access"), Fatal},
		{"rate limit exceeded", errors.New("rate limit exceeded"), RetryTransient},
		{"status code: 429", errors.New("status code: 429"), RetryTransient},
		{"status code: 500", errors.New("status code: 500"), RetryWithFallback},
		{"timeout exceeded", errors.New("timeout exceeded"), RetryTransient},
		{"connection refused", errors.New("connection refused"), RetryWithFallback},
		{"connection reset by peer", errors.New("connection reset by peer"), RetryWithFallback},
		// Unknown → default to RetryTransient (conservative)
		{"unknown error", errors.New("something went wrong"), RetryTransient},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FailureClassify(tc.err)
			if got != tc.want {
				t.Errorf("FailureClassify(%q) = %v; want %v", tc.err.Error(), got, tc.want)
			}
		})
	}
}

func TestFailureClassify_NilAndEdgeCases(t *testing.T) {
	// nil → Fatal (should not reach here)
	got := FailureClassify(nil)
	if got != Fatal {
		t.Errorf("FailureClassify(nil) = %v; want %v", got, Fatal)
	}
}

func TestFailureClass_String(t *testing.T) {
	cases := []struct {
		c    FailureClass
		want string
	}{
		{RetryTransient, "transient"},
		{RetryWithFallback, "fallback"},
		{Fatal, "fatal"},
		{FailureClass(255), "unknown"}, // invalid value
	}

	for _, tc := range cases {
		got := tc.c.String()
		if got != tc.want {
			t.Errorf("FailureClass(%d).String() = %q; want %q", tc.c, got, tc.want)
		}
	}
}
