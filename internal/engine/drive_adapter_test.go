package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/drive"
	supervisorbridge "github.com/dontfuckmycode/dfmc/internal/supervisor/bridge"
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
	a := newDriveAutoApprover(wrapped, []string{"edit_file", "write_file"}, "drive")

	dec := a.RequestApproval(context.Background(), ApprovalRequest{Tool: "edit_file", Source: "drive"})
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
	a := newDriveAutoApprover(wrapped, []string{"edit_file"}, "drive")

	dec := a.RequestApproval(context.Background(), ApprovalRequest{Tool: "run_command", Source: "drive"})
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
	a := newDriveAutoApprover(wrapped, []string{"*"}, "drive")

	for _, tool := range []string{"edit_file", "run_command", "git_commit", "anything_at_all"} {
		dec := a.RequestApproval(context.Background(), ApprovalRequest{Tool: tool, Source: "drive"})
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
	a := newDriveAutoApprover(nil, []string{"edit_file"}, "drive")
	dec := a.RequestApproval(context.Background(), ApprovalRequest{Tool: "run_command", Source: "drive"})
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
	a := newDriveAutoApprover(nil, []string{"Edit_File"}, "drive")
	dec := a.RequestApproval(context.Background(), ApprovalRequest{Tool: "edit_file", Source: "drive"})
	if !dec.Approved {
		t.Fatalf("case-insensitive match should approve, got: %+v", dec)
	}
}

// TestDriveAutoApproverEmptyTokensIgnored: whitespace and empty
// strings in the list should be silently dropped — they're a
// common artifact of CLI parsing (`--auto-approve "edit_file, "`).
func TestDriveAutoApproverEmptyTokensIgnored(t *testing.T) {
	a := newDriveAutoApprover(nil, []string{"", "  ", "edit_file"}, "drive")
	dec := a.RequestApproval(context.Background(), ApprovalRequest{Tool: "edit_file", Source: "drive"})
	if !dec.Approved {
		t.Fatal("non-empty entry should still match after blanks are dropped")
	}
	dec = a.RequestApproval(context.Background(), ApprovalRequest{Tool: "  ", Source: "drive"})
	if dec.Approved {
		t.Fatal("blank tool name must not collide with the dropped blank list entries")
	}
}

func TestDriveAutoApproverNonDriveSourceFallsThrough(t *testing.T) {
	wrappedCalled := false
	wrapped := ApproverFunc(func(_ context.Context, req ApprovalRequest) ApprovalDecision {
		wrappedCalled = true
		if req.Source != "agent" {
			t.Fatalf("expected wrapped approver to receive original source, got %q", req.Source)
		}
		return ApprovalDecision{Approved: false, Reason: "manual deny"}
	})
	a := newDriveAutoApprover(wrapped, []string{"edit_file"}, "drive")

	dec := a.RequestApproval(context.Background(), ApprovalRequest{Tool: "edit_file", Source: "agent"})
	if dec.Approved {
		t.Fatalf("non-drive source must not inherit drive auto-approval, got %+v", dec)
	}
	if !wrappedCalled {
		t.Fatal("wrapped approver must be consulted for non-drive sources")
	}
}

func TestBuildDriveTodoPromptIncludesExecutionMetadata(t *testing.T) {
	prompt := buildDriveTodoPrompt(drive.ExecuteTodoRequest{
		TodoID:       "T7",
		Title:        "Audit auth cache",
		Detail:       "Review cache invalidation and session reuse behavior.",
		Brief:        "T1 mapped the auth stack.",
		Role:         "security_auditor",
		Skills:       []string{"audit", "debug"},
		Labels:       []string{"auth", "cache"},
		Verification: "deep",
	})
	for _, needle := range []string{
		"Context from prior TODOs",
		"Worker role: security_auditor",
		"Labels: auth, cache",
		"Verification expectation: deep",
		"Suggested runtime capabilities: audit, debug",
	} {
		if !strings.Contains(prompt, needle) {
			t.Fatalf("expected prompt to contain %q, got:\n%s", needle, prompt)
		}
	}
}

func TestDecorateDriveTaskWithSkillsPrependsMarkers(t *testing.T) {
	got := decorateDriveTaskWithSkills([]string{"review", "audit", "review"}, "inspect auth boundary")
	if want := "[[skill:review]]"; !strings.Contains(got, want) {
		t.Fatalf("missing %q in decorated task: %s", want, got)
	}
	if want := "[[skill:audit]]"; !strings.Contains(got, want) {
		t.Fatalf("missing %q in decorated task: %s", want, got)
	}
	if got[len(got)-len("inspect auth boundary"):] != "inspect auth boundary" {
		t.Fatalf("original task should be preserved at the end: %s", got)
	}
}

func TestDriveRunnerExecuteTodo_NormalizesPolicyAndSelectsProfile(t *testing.T) {
	eng, _, _ := buildGuardTestEngine(t, 0, 2, []scriptedResponse{{Text: "done"}})
	eng.Config.Providers.Profiles["anthropic-review"] = config.ModelConfig{
		Model:      "claude-sonnet-4-6",
		MaxContext: 1000000,
	}
	runner := &driveRunner{e: eng}

	req := drive.ExecuteTodoRequest{
		TodoID: "T1",
		Title:  "Audit auth boundary",
		Detail: "Review token validation and permission checks.",
	}
	policyReq := req
	policyReq = normalizeDriveRequestForTest(t, runner, policyReq)
	if policyReq.Role != "security_auditor" {
		t.Fatalf("expected normalized security role, got %+v", policyReq)
	}
	if !strings.Contains(strings.ToLower(policyReq.Model), "anthropic") {
		t.Fatalf("expected anthropic-family profile selection, got %+v", policyReq)
	}
	if !strings.Contains(buildDriveTodoPrompt(policyReq), "Verification expectation: deep") {
		t.Fatalf("expected normalized prompt metadata, got:\n%s", buildDriveTodoPrompt(policyReq))
	}
}

func normalizeDriveRequestForTest(t *testing.T, runner *driveRunner, req drive.ExecuteTodoRequest) drive.ExecuteTodoRequest {
	t.Helper()
	if runner == nil || runner.e == nil {
		t.Fatal("runner not initialized")
	}
	if runner.e.Config == nil {
		t.Fatal("engine config not initialized")
	}
	req = supervisorbridge.NormalizeDriveExecution(req)
	req.Model = supervisorbridge.SelectDriveProfile(req, runner.e.Config.Providers.Profiles, runner.e.provider())
	return req
}
