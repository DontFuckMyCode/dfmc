package cli

import (
	"context"
	"testing"
)

func TestRunContextBudgetAndRecent(t *testing.T) {
	eng := newCLITestEngine(t)

	if code := runContext(context.Background(), eng, []string{"budget", "--query", "security audit auth"}, true); code != 0 {
		t.Fatalf("context budget exit=%d", code)
	}

	if code := runContext(context.Background(), eng, []string{"recent"}, true); code != 0 {
		t.Fatalf("context recent exit=%d", code)
	}
}

func TestRunContextUsageForUnknownAction(t *testing.T) {
	eng := newCLITestEngine(t)
	if code := runContext(context.Background(), eng, []string{"unknown"}, true); code != 2 {
		t.Fatalf("expected exit=2 for unknown action, got %d", code)
	}
}
