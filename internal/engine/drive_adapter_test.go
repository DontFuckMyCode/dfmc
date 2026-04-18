package engine

import (
	"context"
	"testing"
)

// TestDriveAutoApproverHitGrants: tools in the allowlist are
// approved without consulting the wrapped approver. Reason text
// surfaces "drive auto-approve" so denials downstream can be
// distinguished from manual approvals in the audit trail.
func TestDriveAutoApproverHitGrants(t *testing.T) {
	wrappedCalled := false
	wrapped := ApproverFunc(func(_ context.Context, _ ApprovalRequest) ApprovalDecision {
		wrappedCalled = true
		return ApprovalDecision{Approved: false, Reason: "wrapped denied"}
	})
	a := newDriveAutoApprover(wrapped, []string{"edit_file", "write_file"})

	dec := a.RequestApproval(context.Background(), ApprovalRequest{Tool: "edit_file"})
	if !dec.Approved {
		t.Fatalf("edit_file should be auto-approved, got: %+v", dec)
	}
	if wrappedCalled {
		t.Fatal("wrapped approver must NOT be consulted when allowlist matches")
	}
}

// TestDriveAutoApproverMissFallsThrough: a tool NOT in the allowlist
// delegates to the wrapped approver — the user's TUI modal / stdin
// gate still fires for sensitive tools they didn't pre-approve.
func TestDriveAutoApproverMissFallsThrough(t *testing.T) {
	wrappedCalled := false
	wrapped := ApproverFunc(func(_ context.Context, _ ApprovalRequest) ApprovalDecision {
		wrappedCalled = true
		return ApprovalDecision{Approved: true, Reason: "user said yes"}
	})
	a := newDriveAutoApprover(wrapped, []string{"edit_file"})

	dec := a.RequestApproval(context.Background(), ApprovalRequest{Tool: "run_command"})
	if !dec.Approved {
		t.Fatalf("wrapped approver returned approved, got: %+v", dec)
	}
	if !wrappedCalled {
		t.Fatal("wrapped approver MUST be consulted when allowlist misses")
	}
}

// TestDriveAutoApproverWildcardApprovesEverything: "*" in the list
// approves any tool without consulting the wrapped approver. This is
// the "truly unattended drive" mode — recommended only when the
// caller knows the planner output is trustworthy.
func TestDriveAutoApproverWildcardApprovesEverything(t *testing.T) {
	wrappedCalled := false
	wrapped := ApproverFunc(func(_ context.Context, _ ApprovalRequest) ApprovalDecision {
		wrappedCalled = true
		return ApprovalDecision{Approved: false}
	})
	a := newDriveAutoApprover(wrapped, []string{"*"})

	for _, tool := range []string{"edit_file", "run_command", "git_commit", "anything_at_all"} {
		dec := a.RequestApproval(context.Background(), ApprovalRequest{Tool: tool})
		if !dec.Approved {
			t.Fatalf("wildcard should auto-approve %q, got: %+v", tool, dec)
		}
	}
	if wrappedCalled {
		t.Fatal("wrapped approver must NOT be consulted under wildcard")
	}
}

// TestDriveAutoApproverNilWrappedFailsSafe: when wrapped is nil and
// the tool isn't auto-approved, deny — matches the engine's existing
// "no approver registered = deny" default. Without this, an
// over-narrow auto-approve list would silently approve everything
// (because the gate would short-circuit on nil wrapped).
func TestDriveAutoApproverNilWrappedFailsSafe(t *testing.T) {
	a := newDriveAutoApprover(nil, []string{"edit_file"})
	dec := a.RequestApproval(context.Background(), ApprovalRequest{Tool: "run_command"})
	if dec.Approved {
		t.Fatalf("nil wrapped + miss must deny, got approved: %+v", dec)
	}
	if dec.Reason == "" {
		t.Fatal("denial must include a reason so the model sees actionable feedback")
	}
}

// TestDriveAutoApproverCaseInsensitive: planner output may use
// any casing; the allowlist match must be case-insensitive on the
// tool-name comparison.
func TestDriveAutoApproverCaseInsensitive(t *testing.T) {
	a := newDriveAutoApprover(nil, []string{"Edit_File"})
	dec := a.RequestApproval(context.Background(), ApprovalRequest{Tool: "edit_file"})
	if !dec.Approved {
		t.Fatalf("case-insensitive match should approve, got: %+v", dec)
	}
}

// TestDriveAutoApproverEmptyTokensIgnored: whitespace and empty
// strings in the list should be silently dropped — they're a
// common artifact of CLI parsing (`--auto-approve "edit_file, "`).
func TestDriveAutoApproverEmptyTokensIgnored(t *testing.T) {
	a := newDriveAutoApprover(nil, []string{"", "  ", "edit_file"})
	dec := a.RequestApproval(context.Background(), ApprovalRequest{Tool: "edit_file"})
	if !dec.Approved {
		t.Fatal("non-empty entry should still match after blanks are dropped")
	}
	dec = a.RequestApproval(context.Background(), ApprovalRequest{Tool: "  "})
	if dec.Approved {
		t.Fatal("blank tool name must not collide with the dropped blank list entries")
	}
}
