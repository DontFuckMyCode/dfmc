package supervisor

import (
	"errors"
	"testing"
	"time"
)

func TestClassifyFailure_Retryable(t *testing.T) {
	cases := []struct {
		name     string
		errMsg   string
		toolName string
	}{
		{"rate_limit", "rate limit exceeded", "read_file"},
		{"429", "request returned 429", "run_command"},
		{"503", "service unavailable: 503", "web_fetch"},
		{"timeout", "request timeout", "grep_codebase"},
		{"context_limit", "context limit exceeded", "edit_file"},
		{"budget_exhausted", "budget exhausted", "codemap"},
		{"max_steps", "max steps reached", "delegate_task"},
		{"token_limit", "token limit exceeded", "apply_patch"},
		{"read_timeout", "read timeout: connection timed out", "run_command"},
		{"conn_reset", "connection reset by peer", "web_search"},
		{"temp_failure", "temporary failure", "grep_codebase"},
		{"unavailable", "service temporarily unavailable", "run_command"},
		{"exit_code_1", "command exited with exit code 1", "run_command"},
		{"non_zero", "non-zero exit: 1", "run_command"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := errors.New(c.errMsg)
			fc := ClassifyFailure(err, c.toolName)
			if fc != FailureRetryable {
				t.Errorf("ClassifyFailure(%q, %q) = %v; want FailureRetryable", c.errMsg, c.toolName, fc)
			}
		})
	}
}

func TestClassifyFailure_Permanent(t *testing.T) {
	cases := []struct {
		name     string
		errMsg   string
		toolName string
	}{
		{"syntax_error", "syntax error: unexpected token", "run_command"},
		{"type_error", "type error: cannot use string as int", "apply_patch"},
		{"compile_failed", "compile failed: errors", "run_command"},
		{"cannot_find_symbol", "cannot find symbol: Foo", "find_symbol"},
		{"undefined", "undefined identifier: bar", "edit_file"},
		{"rejected_edit", "edit rejected: unsafe operation", "edit_file"},
		{"refused_edit", "edit refused by user", "apply_patch"},
		{"invalid_patch", "invalid patch: hunk failed", "apply_patch"},
		{"malformed", "malformed request: missing field", "run_command"},
		{"no_such_file", "no such file or directory", "run_command"},
		{"permission_denied", "permission denied", "write_file"},
		{"hash_mismatch", "hash mismatch after edit", "edit_file"},
		{"concurrent_modification", "concurrent file modification detected", "apply_patch"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := errors.New(c.errMsg)
			fc := ClassifyFailure(err, c.toolName)
			if fc != FailurePermanent {
				t.Errorf("ClassifyFailure(%q, %q) = %v; want FailurePermanent", c.errMsg, c.toolName, fc)
			}
		})
	}
}

func TestClassifyFailure_Escalate(t *testing.T) {
	cases := []struct {
		name   string
		errMsg string
	}{
		{"security", "security: potential code injection detected"},
		{"infinite_loop", "infinite loop detected in generated code"},
		{"malicious", "malicious pattern detected"},
		{"data_exfiltration", "potential data exfiltration attempt"},
		{"prompt_injection", "prompt injection detected"},
		{"unsafe_edit", "unsafe edit rejected: dangerous operation"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := errors.New(c.errMsg)
			fc := ClassifyFailure(err, "")
			if fc != FailureEscalate {
				t.Errorf("ClassifyFailure(%q) = %v; want FailureEscalate", c.errMsg, fc)
			}
		})
	}
}

func TestClassifyFailure_Unknown(t *testing.T) {
	err := errors.New("something went wrong but we are not sure what")
	fc := ClassifyFailure(err, "some_tool")
	// Fail open: unknown errors are treated as retryable
	if fc != FailureRetryable {
		t.Errorf("ClassifyFailure(%q) = %v; want FailureRetryable for unknown error", err.Error(), fc)
	}
}

func TestClassifyFailure_NilError(t *testing.T) {
	fc := ClassifyFailure(nil, "read_file")
	if fc != FailurePermanent {
		t.Errorf("ClassifyFailure(nil) = %v; want FailurePermanent", fc)
	}
}

func TestRetryPolicy_ShouldRetry(t *testing.T) {
	p := DefaultRetryPolicy // MaxAttempts: 3

	tests := []struct {
		attempt int
		fc      FailureClass
		want    bool
	}{
		{1, FailureRetryable, true},
		{2, FailureRetryable, true},
		{3, FailureRetryable, false}, // MaxAttempts=3, attempt 3 is last
		{4, FailureRetryable, false},
		{1, FailurePermanent, false},
		{2, FailurePermanent, false},
		{1, FailureEscalate, false},
		{2, FailureEscalate, false},
	}
	for _, tt := range tests {
		got := p.ShouldRetry(tt.fc, tt.attempt)
		if got != tt.want {
			t.Errorf("ShouldRetry(%v, %d) = %v; want %v", tt.fc, tt.attempt, got, tt.want)
		}
	}
}

func TestRetryPolicy_BackoffFor(t *testing.T) {
	p := RetryPolicy{
		MaxAttempts: 3,
		BaseBackoff: 2 * time.Second,
		MaxBackoff:  30 * time.Second,
	}

	if g := p.BackoffFor(1); g != 0 {
		t.Errorf("BackoffFor(1) = %v; want 0 (first retry has no wait)", g)
	}
	if g := p.BackoffFor(2); g != 4*time.Second {
		t.Errorf("BackoffFor(2) = %v; want 4s", g)
	}
	if g := p.BackoffFor(3); g != 8*time.Second {
		t.Errorf("BackoffFor(3) = %v; want 8s", g)
	}
}

func TestRetryPolicy_MaxBackoffCap(t *testing.T) {
	p := RetryPolicy{
		MaxAttempts: 5,
		BaseBackoff: 10 * time.Second,
		MaxBackoff:  20 * time.Second,
	}
	if g := p.BackoffFor(4); g != 20*time.Second {
		t.Errorf("BackoffFor(4) = %v; want 20s (capped)", g)
	}
}

func TestRetryPolicy_ZeroBaseBackoff(t *testing.T) {
	p := RetryPolicy{MaxAttempts: 3, BaseBackoff: 0}
	if g := p.BackoffFor(1); g != 0 {
		t.Errorf("BackoffFor(1) with zero base = %v; want 0", g)
	}
	if g := p.BackoffFor(2); g != 0 {
		t.Errorf("BackoffFor(2) with zero base = %v; want 0", g)
	}
}

func TestFailureClass_String(t *testing.T) {
	tests := []struct {
		fc    FailureClass
		match string
	}{
		{FailureRetryable, "retryable"},
		{FailurePermanent, "permanent"},
		{FailureEscalate, "escalate"},
		{FailureClass(99), "unknown"},
	}
	for _, tt := range tests {
		if g := tt.fc.String(); g != tt.match {
			t.Errorf("FailureClass(%d).String() = %q; want %q", tt.fc, g, tt.match)
		}
	}
}
