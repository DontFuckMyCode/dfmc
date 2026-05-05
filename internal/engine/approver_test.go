package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	t.Cleanup(func() { _ = eng.Shutdown() })
	eng.ProjectRoot = tmp
	return eng
}

func TestRequiresApproval_EmptyListAllowsAll(t *testing.T) {
	eng := newApproverTestEngine(t)
	if eng.requiresApproval("read_file", "agent") {
		t.Fatalf("empty RequireApproval list should not gate anything")
	}
}

func TestRequiresApproval_NamedToolGated(t *testing.T) {
	eng := newApproverTestEngine(t)
	eng.Config.Tools.RequireApproval = []string{"write_file"}
	if !eng.requiresApproval("write_file", "agent") {
		t.Fatalf("write_file should be gated")
	}
	if eng.requiresApproval("read_file", "agent") {
		t.Fatalf("read_file should not be gated")
	}
}

func TestRequiresApproval_WildcardGatesEverything(t *testing.T) {
	eng := newApproverTestEngine(t)
	eng.Config.Tools.RequireApproval = []string{"*"}
	if !eng.requiresApproval("read_file", "agent") || !eng.requiresApproval("write_file", "agent") {
		t.Fatalf("wildcard should gate every tool")
	}
}

func TestRequiresApproval_WhitespaceTolerant(t *testing.T) {
	eng := newApproverTestEngine(t)
	eng.Config.Tools.RequireApproval = []string{"  write_file  ", ""}
	if !eng.requiresApproval("write_file", "agent") {
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

func TestSetApproverWithToken_RestoresPreviousApprover(t *testing.T) {
	eng := newApproverTestEngine(t)
	eng.SetApprover(ApproverFunc(func(context.Context, ApprovalRequest) ApprovalDecision {
		return ApprovalDecision{Approved: false, Reason: "base"}
	}))
	token := &struct{}{}
	eng.SetApproverWithToken(ApproverFunc(func(context.Context, ApprovalRequest) ApprovalDecision {
		return ApprovalDecision{Approved: true, Reason: "override"}
	}), token)

	eng.ReleaseApproverWithToken(token)

	ap := eng.approver()
	if ap == nil {
		t.Fatalf("release should restore the previous approver, got nil")
	}
	if got := ap.RequestApproval(context.Background(), ApprovalRequest{Tool: "read_file"}).Reason; got != "base" {
		t.Fatalf("release restored wrong approver: %q", got)
	}
}

func TestSetApproverWithToken_OutOfOrderReleaseDoesNotResurrect(t *testing.T) {
	eng := newApproverTestEngine(t)
	eng.SetApprover(ApproverFunc(func(context.Context, ApprovalRequest) ApprovalDecision {
		return ApprovalDecision{Reason: "base"}
	}))
	tokenA := &struct{ name string }{"a"}
	tokenB := &struct{ name string }{"b"}
	eng.SetApproverWithToken(ApproverFunc(func(context.Context, ApprovalRequest) ApprovalDecision {
		return ApprovalDecision{Reason: "a"}
	}), tokenA)
	eng.SetApproverWithToken(ApproverFunc(func(context.Context, ApprovalRequest) ApprovalDecision {
		return ApprovalDecision{Reason: "b"}
	}), tokenB)

	eng.ReleaseApproverWithToken(tokenA)
	if got := eng.approver().RequestApproval(context.Background(), ApprovalRequest{Tool: "read_file"}).Reason; got != "b" {
		t.Fatalf("early release should leave current override intact, got %q", got)
	}
	eng.ReleaseApproverWithToken(tokenB)
	if got := eng.approver().RequestApproval(context.Background(), ApprovalRequest{Tool: "read_file"}).Reason; got != "base" {
		t.Fatalf("later release resurrected stale override, got %q", got)
	}
}

func TestSetApproverWithToken_LIFOReleaseRestoresNestedOverride(t *testing.T) {
	eng := newApproverTestEngine(t)
	eng.SetApprover(ApproverFunc(func(context.Context, ApprovalRequest) ApprovalDecision {
		return ApprovalDecision{Reason: "base"}
	}))
	tokenA := &struct{ name string }{"a"}
	tokenB := &struct{ name string }{"b"}
	eng.SetApproverWithToken(ApproverFunc(func(context.Context, ApprovalRequest) ApprovalDecision {
		return ApprovalDecision{Reason: "a"}
	}), tokenA)
	eng.SetApproverWithToken(ApproverFunc(func(context.Context, ApprovalRequest) ApprovalDecision {
		return ApprovalDecision{Reason: "b"}
	}), tokenB)

	eng.ReleaseApproverWithToken(tokenB)
	if got := eng.approver().RequestApproval(context.Background(), ApprovalRequest{Tool: "read_file"}).Reason; got != "a" {
		t.Fatalf("nested release should restore previous override, got %q", got)
	}
	eng.ReleaseApproverWithToken(tokenA)
	if got := eng.approver().RequestApproval(context.Background(), ApprovalRequest{Tool: "read_file"}).Reason; got != "base" {
		t.Fatalf("final release should restore base approver, got %q", got)
	}
}

func TestSetApproverWithToken_ConcurrentReleasesRestoreBase(t *testing.T) {
	eng := newApproverTestEngine(t)
	eng.SetApprover(ApproverFunc(func(context.Context, ApprovalRequest) ApprovalDecision {
		return ApprovalDecision{Reason: "base"}
	}))

	const n = 32
	tokens := make([]any, n)
	for i := 0; i < n; i++ {
		token := &struct{ id int }{id: i}
		tokens[i] = token
		reason := "override"
		eng.SetApproverWithToken(ApproverFunc(func(context.Context, ApprovalRequest) ApprovalDecision {
			return ApprovalDecision{Reason: reason}
		}), token)
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, token := range tokens {
		wg.Add(1)
		go func(token any) {
			defer wg.Done()
			<-start
			eng.ReleaseApproverWithToken(token)
		}(token)
	}
	close(start)
	wg.Wait()

	ap := eng.approver()
	if ap == nil {
		t.Fatalf("concurrent releases should restore base approver, got nil")
	}
	if got := ap.RequestApproval(context.Background(), ApprovalRequest{Tool: "read_file"}).Reason; got != "base" {
		t.Fatalf("concurrent releases restored wrong approver: %q", got)
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

// REPORT.md #1 (per-engine approver/denial map leak risk) is now
// solved structurally: approver + recentDenials live on the Engine
// struct, so they go away when *Engine is GC'd. The previous tests
// (TestShutdown_CleansApproverState / TestCleanupApproverState_*)
// pinned the package-global cleanup hook contract that no longer
// exists — kept here as a tombstone so a future contributor doesn't
// reintroduce the package-globals pattern thinking the tests vanished
// by accident.
