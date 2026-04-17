package engine

import (
	"context"
	"strings"
	"testing"
)

func TestRecentDenials_EmptyWhenUnused(t *testing.T) {
	eng := newApproverTestEngine(t)
	if got := eng.RecentDenials(); got != nil {
		t.Fatalf("fresh engine should have no denials, got %+v", got)
	}
}

func TestRecentDenials_AppendedOnGatedDeny(t *testing.T) {
	eng := newApproverTestEngine(t)
	eng.Config.Tools.RequireApproval = []string{"write_file"}
	eng.SetApprover(ApproverFunc(func(ctx context.Context, req ApprovalRequest) ApprovalDecision {
		return ApprovalDecision{Approved: false, Reason: "too risky"}
	}))

	_, err := eng.executeToolWithLifecycle(context.Background(), "write_file", map[string]any{"path": "a.txt"}, "agent")
	if err == nil {
		t.Fatalf("denied call should return an error")
	}
	denials := eng.RecentDenials()
	if len(denials) != 1 {
		t.Fatalf("expected 1 denial recorded, got %d", len(denials))
	}
	d := denials[0]
	if d.Tool != "write_file" || d.Source != "agent" {
		t.Fatalf("denial fields wrong: %+v", d)
	}
	if !strings.Contains(d.Reason, "too risky") {
		t.Fatalf("denial reason missing: %+v", d)
	}
	if d.At.IsZero() {
		t.Fatalf("denial timestamp should be set")
	}
}

func TestRecentDenials_RingBufferCap(t *testing.T) {
	eng := newApproverTestEngine(t)
	eng.Config.Tools.RequireApproval = []string{"*"}
	eng.SetApprover(ApproverFunc(func(ctx context.Context, req ApprovalRequest) ApprovalDecision {
		return ApprovalDecision{Approved: false, Reason: "nope"}
	}))

	// Fire more than the capacity so the oldest entries get evicted.
	for i := 0; i < recentDenialsCapacity+3; i++ {
		_, _ = eng.executeToolWithLifecycle(context.Background(), "read_file", map[string]any{"path": "hello.txt"}, "agent")
	}
	denials := eng.RecentDenials()
	if len(denials) != recentDenialsCapacity {
		t.Fatalf("ring buffer should cap at %d, got %d", recentDenialsCapacity, len(denials))
	}
}

func TestRecentDenials_ApprovalNotRecorded(t *testing.T) {
	eng := newApproverTestEngine(t)
	eng.Config.Tools.RequireApproval = []string{"read_file"}
	eng.SetApprover(ApproverFunc(func(ctx context.Context, req ApprovalRequest) ApprovalDecision {
		return ApprovalDecision{Approved: true}
	}))

	_, err := eng.executeToolWithLifecycle(context.Background(), "read_file", map[string]any{"path": "hello.txt"}, "agent")
	if err != nil {
		t.Fatalf("approved call should succeed, got %v", err)
	}
	if got := eng.RecentDenials(); len(got) != 0 {
		t.Fatalf("approved calls must not land in the denial log, got %+v", got)
	}
}
