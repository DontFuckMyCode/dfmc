package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

// newApproverTestEngine spins up a temporary-root engine with a full Init()
// so Tools and EventBus are wired — enough to exercise the lifecycle path
// end-to-end from approval gate through the real tools.Engine.Execute.
func newApproverTestEngine(t *testing.T) *Engine {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("HOME", tmp)
	if err := os.WriteFile(filepath.Join(tmp, "hello.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	cfg := config.DefaultConfig()
	eng, err := New(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.Init(context.Background()); err != nil {
		t.Fatalf("init engine: %v", err)
	}
	t.Cleanup(func() { eng.Shutdown() })
	eng.ProjectRoot = tmp
	return eng
}

func TestRequiresApproval_EmptyListAllowsAll(t *testing.T) {
	eng := newApproverTestEngine(t)
	if eng.requiresApproval("read_file") {
		t.Fatalf("empty RequireApproval list should not gate anything")
	}
}

func TestRequiresApproval_NamedToolGated(t *testing.T) {
	eng := newApproverTestEngine(t)
	eng.Config.Tools.RequireApproval = []string{"write_file"}
	if !eng.requiresApproval("write_file") {
		t.Fatalf("write_file should be gated")
	}
	if eng.requiresApproval("read_file") {
		t.Fatalf("read_file should not be gated")
	}
}

func TestRequiresApproval_WildcardGatesEverything(t *testing.T) {
	eng := newApproverTestEngine(t)
	eng.Config.Tools.RequireApproval = []string{"*"}
	if !eng.requiresApproval("read_file") || !eng.requiresApproval("write_file") {
		t.Fatalf("wildcard should gate every tool")
	}
}

func TestRequiresApproval_WhitespaceTolerant(t *testing.T) {
	eng := newApproverTestEngine(t)
	eng.Config.Tools.RequireApproval = []string{"  write_file  ", ""}
	if !eng.requiresApproval("write_file") {
		t.Fatalf("whitespace-padded entries must still match")
	}
}

func TestAskToolApproval_NoApproverDenies(t *testing.T) {
	eng := newApproverTestEngine(t)
	decision := eng.askToolApproval(context.Background(), "write_file", nil, "agent")
	if decision.Approved {
		t.Fatalf("nil approver must implicit-deny, got approved")
	}
	if decision.Reason == "" {
		t.Fatalf("deny decision should carry a reason")
	}
}

func TestSetApprover_AutoApproveAllowsGatedTool(t *testing.T) {
	eng := newApproverTestEngine(t)
	eng.Config.Tools.RequireApproval = []string{"read_file"}
	var seen atomic.Int32
	eng.SetApprover(ApproverFunc(func(ctx context.Context, req ApprovalRequest) ApprovalDecision {
		seen.Add(1)
		if req.Tool != "read_file" {
			t.Errorf("unexpected tool: %q", req.Tool)
		}
		if req.Source != "agent" {
			t.Errorf("expected source=agent, got %q", req.Source)
		}
		return ApprovalDecision{Approved: true}
	}))
	_, err := eng.executeToolWithLifecycle(context.Background(), "read_file", map[string]any{"path": "hello.txt"}, "agent")
	if err != nil {
		t.Fatalf("read_file should succeed when approver returns Approved: %v", err)
	}
	if seen.Load() != 1 {
		t.Fatalf("approver should have been called exactly once, saw %d", seen.Load())
	}
}

func TestSetApprover_DenyBlocksExecution(t *testing.T) {
	eng := newApproverTestEngine(t)
	eng.Config.Tools.RequireApproval = []string{"write_file"}
	eng.SetApprover(ApproverFunc(func(ctx context.Context, req ApprovalRequest) ApprovalDecision {
		return ApprovalDecision{Approved: false, Reason: "too risky"}
	}))

	denials := eng.EventBus.Subscribe("tool:denied")
	defer eng.EventBus.Unsubscribe("tool:denied", denials)

	_, err := eng.executeToolWithLifecycle(context.Background(), "write_file", map[string]any{
		"path":    "nope.txt",
		"content": "should not land",
	}, "agent")
	if err == nil {
		t.Fatalf("denied approval must surface an error")
	}
	if !strings.Contains(err.Error(), "too risky") {
		t.Fatalf("error should carry denial reason, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(eng.ProjectRoot, "nope.txt")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("write_file should have been blocked; stat err=%v", statErr)
	}
	select {
	case ev := <-denials:
		payload, ok := ev.Payload.(map[string]any)
		if !ok {
			t.Fatalf("tool:denied payload has wrong type: %T", ev.Payload)
		}
		reason, _ := payload["reason"].(string)
		if !strings.Contains(reason, "too risky") {
			t.Fatalf("tool:denied event missing reason, got %q", reason)
		}
	case <-time.After(time.Second):
		t.Fatalf("expected tool:denied event within 1s")
	}
}

func TestExecuteToolWithLifecycle_UserSourceBypassesGate(t *testing.T) {
	eng := newApproverTestEngine(t)
	eng.Config.Tools.RequireApproval = []string{"*"}
	// No approver registered — with source=user the gate must skip entirely.
	_, err := eng.executeToolWithLifecycle(context.Background(), "read_file", map[string]any{"path": "hello.txt"}, "user")
	if err != nil {
		t.Fatalf("user-initiated call should bypass approval gate, got err: %v", err)
	}
}
