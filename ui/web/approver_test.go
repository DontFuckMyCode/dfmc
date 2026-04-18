package web

import (
	"context"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func TestWebApprover_AutoYes_NonDestructive(t *testing.T) {
	// DFMC_APPROVE=yes alone auto-approves only read-only tools — destructive
	// tools require the second knob (DFMC_APPROVE_DESTRUCTIVE=yes).
	t.Setenv("DFMC_APPROVE", "yes")
	t.Setenv("DFMC_APPROVE_DESTRUCTIVE", "")
	ap := newWebApprover()
	decision := ap.RequestApproval(context.Background(), engine.ApprovalRequest{Tool: "read_file"})
	if !decision.Approved {
		t.Fatalf("DFMC_APPROVE=yes must auto-approve read_file, got %+v", decision)
	}
}

// TestWebApprover_AutoYes_DestructiveDeniedWithoutSecondKnob pins the
// two-knob gate on the web surface: a publicly-reachable `dfmc serve`
// with a leaked DFMC_APPROVE=yes must not silently grant write/shell.
// Operators have to also set DFMC_APPROVE_DESTRUCTIVE=yes.
func TestWebApprover_AutoYes_DestructiveDeniedWithoutSecondKnob(t *testing.T) {
	t.Setenv("DFMC_APPROVE", "yes")
	t.Setenv("DFMC_APPROVE_DESTRUCTIVE", "")
	ap := newWebApprover()
	for _, tool := range []string{"write_file", "edit_file", "apply_patch", "run_command", "delegate_task"} {
		decision := ap.RequestApproval(context.Background(), engine.ApprovalRequest{Tool: tool})
		if decision.Approved {
			t.Fatalf("DFMC_APPROVE=yes must NOT auto-approve %s without the second knob; got %+v", tool, decision)
		}
		if !strings.Contains(decision.Reason, "DFMC_APPROVE_DESTRUCTIVE") {
			t.Fatalf("deny reason for %s should explain the second knob, got %q", tool, decision.Reason)
		}
	}
}

// TestWebApprover_AutoYes_DestructiveAllowedWithSecondKnob: explicit
// opt-in with both knobs auto-approves writes/shell.
func TestWebApprover_AutoYes_DestructiveAllowedWithSecondKnob(t *testing.T) {
	t.Setenv("DFMC_APPROVE", "yes")
	t.Setenv("DFMC_APPROVE_DESTRUCTIVE", "yes")
	ap := newWebApprover()
	for _, tool := range []string{"write_file", "run_command"} {
		decision := ap.RequestApproval(context.Background(), engine.ApprovalRequest{Tool: tool})
		if !decision.Approved {
			t.Fatalf("both knobs set must auto-approve %s, got %+v", tool, decision)
		}
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
