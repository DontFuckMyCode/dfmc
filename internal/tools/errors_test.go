package tools

import (
	"context"
	"errors"
	"testing"
)

// TestErrMetaDepthExceededIsTyped pins the contract that hitting the
// meta-tool nesting depth limit returns an error wrapping the typed
// ErrMetaDepthExceeded sentinel — so callers can detect it via
// errors.Is rather than string-matching the human-readable message.
func TestErrMetaDepthExceededIsTyped(t *testing.T) {
	// depth limit 1 means the second nested entry must trip the gate.
	ctx := SeedMetaToolBudgetWithLimits(context.Background(), 64, 1)
	ctx, release1, err := enterMetaBudget(ctx, 1)
	if err != nil {
		t.Fatalf("first enter at depth 1 should succeed: %v", err)
	}
	defer release1()

	_, _, err = enterMetaBudget(ctx, 1)
	if err == nil {
		t.Fatal("second enter must trip depth limit")
	}
	if !errors.Is(err, ErrMetaDepthExceeded) {
		t.Fatalf("error must wrap ErrMetaDepthExceeded, got %v", err)
	}
	// Sanity: the human-readable message is preserved alongside the
	// sentinel — this is the dual-channel contract callers rely on.
	if msg := err.Error(); msg == "" {
		t.Fatal("wrapped error must keep its human-readable message")
	}
}
