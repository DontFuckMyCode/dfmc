package tools

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestErrMetaBudgetExhausted(t *testing.T) {
	if ErrMetaBudgetExhausted.Error() != "meta tool budget exhausted" {
		t.Error("unexpected error message")
	}
}

func TestErrMetaDepthExceeded(t *testing.T) {
	if ErrMetaDepthExceeded.Error() != "meta tool nesting exceeded depth limit" {
		t.Error("unexpected error message")
	}
}

func TestErrSubagentDepthExceeded(t *testing.T) {
	if ErrSubagentDepthExceeded.Error() != "sub-agent recursion depth limit exceeded" {
		t.Error("unexpected error message")
	}
}

func TestErrMetaBudgetExhausted_Is(t *testing.T) {
	// Sentinel errors are detectable via errors.Is
	if !errors.Is(ErrMetaBudgetExhausted, ErrMetaBudgetExhausted) {
		t.Error("sentinel should be Is itself")
	}
}

func TestToolTimeoutError(t *testing.T) {
	err := &ToolTimeoutError{Name: "grep_codebase", Limit: 30 * time.Second}
	if err.Error() == "" {
		t.Error("ToolTimeoutError should have message")
	}
	if err.Unwrap() != context.DeadlineExceeded {
		t.Error("Unwrap should return DeadlineExceeded")
	}
}

func TestSelfManagedTimeoutTools(t *testing.T) {
	expected := []string{
		"run_command", "web_fetch", "web_search",
		"delegate_task", "orchestrate", "patch_validation", "benchmark",
	}
	for _, name := range expected {
		if _, ok := selfManagedTimeoutTools[name]; !ok {
			t.Errorf("expected %q in selfManagedTimeoutTools", name)
		}
	}
}