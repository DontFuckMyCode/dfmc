package web

import (
	"context"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func TestWebApprover_AutoYes(t *testing.T) {
	t.Setenv("DFMC_APPROVE", "yes")
	ap := newWebApprover()
	decision := ap.RequestApproval(context.Background(), engine.ApprovalRequest{Tool: "write_file"})
	if !decision.Approved {
		t.Fatalf("DFMC_APPROVE=yes must auto-approve, got %+v", decision)
	}
}

func TestWebApprover_AutoNo(t *testing.T) {
	t.Setenv("DFMC_APPROVE", "no")
	ap := newWebApprover()
	decision := ap.RequestApproval(context.Background(), engine.ApprovalRequest{Tool: "run_command"})
	if decision.Approved {
		t.Fatalf("DFMC_APPROVE=no must auto-deny, got %+v", decision)
	}
	if decision.Reason == "" {
		t.Fatalf("deny must carry a reason")
	}
}

func TestWebApprover_UnsetDenyByDefault(t *testing.T) {
	t.Setenv("DFMC_APPROVE", "")
	ap := newWebApprover()
	decision := ap.RequestApproval(context.Background(), engine.ApprovalRequest{Tool: "write_file"})
	if decision.Approved {
		t.Fatalf("web approver with no DFMC_APPROVE must deny by default")
	}
	if !strings.Contains(decision.Reason, "DFMC_APPROVE=yes") {
		t.Fatalf("deny reason should tell operator how to opt in, got %q", decision.Reason)
	}
}

// TestWebApprover_DenyReasonMentionsTUI — the operator running `dfmc
// serve` should learn how to resolve a deny from the reason string
// (nudge toward TUI or DFMC_APPROVE=yes), otherwise the gate looks
// like a black-box failure.
func TestWebApprover_DenyReasonMentionsTUI(t *testing.T) {
	t.Setenv("DFMC_APPROVE", "")
	ap := newWebApprover()
	decision := ap.RequestApproval(context.Background(), engine.ApprovalRequest{Tool: "write_file"})
	if !strings.Contains(strings.ToLower(decision.Reason), "tui") {
		t.Fatalf("deny reason should mention TUI as an alternative, got %q", decision.Reason)
	}
}
