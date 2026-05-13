package session

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/tools"
)

func TestNewSession(t *testing.T) {
	s := New()
	if s == nil {
		t.Fatal("New() returned nil")
	}
	if s.AgentCount() != 1 {
		t.Errorf("expected 1 agent, got %d", s.AgentCount())
	}
	root := s.GetAgent(RootAgentID)
	if root == nil {
		t.Fatal("root agent not found")
	}
	if root.ID() != RootAgentID {
		t.Errorf("root agent ID expected %d, got %d", RootAgentID, root.ID())
	}
	if root.parent != 0 {
		t.Errorf("root agent parent expected 0, got %d", root.parent)
	}
	if root.Status() != StatusIdle {
		t.Errorf("root agent status expected idle, got %s", root.Status())
	}
	s.Close()
}

func TestSpawnAgent(t *testing.T) {
	s := New()

	// Spawn a child under the root agent (no initial task - just structural test).
	child, err := s.SpawnAgent(RootAgentID, "", AutonomyFull)
	if err != nil {
		s.Close()
		t.Fatalf("SpawnAgent failed: %v", err)
	}
	if child == nil {
		s.Close()
		t.Fatal("spawned agent is nil")
	}
	if child.ID() != 2 {
		s.Close()
		t.Errorf("expected agent ID 2, got %d", child.ID())
	}
	if child.parent != RootAgentID {
		s.Close()
		t.Errorf("expected parent %d, got %d", RootAgentID, child.parent)
	}
	if s.AgentCount() != 2 {
		s.Close()
		t.Errorf("expected 2 agents, got %d", s.AgentCount())
	}

	// Verify parent has the child in its children list.
	root := s.GetAgent(RootAgentID)
	if len(root.children) != 1 || root.children[0] != child.ID() {
		s.Close()
		t.Errorf("root children = %v, want [%d]", root.children, child.ID())
	}

	s.Close()
}

func TestSpawnDepthCap(t *testing.T) {
	s := New()
	defer s.Close()

	// Build a chain: root → 2 → 3 → 4 → 5 (depth 4 from root).
	var prev AgentID = RootAgentID
	for i := 2; i <= 5; i++ {
		child, err := s.SpawnAgent(prev, "", AutonomyFull)
		if err != nil {
			t.Fatalf("SpawnAgent(%d) failed: %v", i, err)
		}
		prev = child.ID()
	}

	// depth is now 4 (root=0, 2,3,4,5). Adding agent 6 should fail at depth 5.
	_, err := s.SpawnAgent(prev, "", AutonomyFull)
	if err == nil {
		t.Error("expected error for depth cap exceeded, got nil")
	}
}

func TestDelegateFireAndForget(t *testing.T) {
	s := New()
	defer s.Close()

	// Spawn child.
	child, err := s.SpawnAgent(RootAgentID, "", AutonomyFull)
	if err != nil {
		t.Fatalf("SpawnAgent failed: %v", err)
	}
	childID := child.ID()

	// Give it a task fire-and-forget.
	err = s.Delegate(RootAgentID, childID, "do something", "", AutonomyFull, false)
	if err != nil {
		t.Fatalf("Delegate failed: %v", err)
	}

	// Agent should still be alive (waiting for next task) and its conversation
	// should contain the delegated task.
	time.Sleep(300 * time.Millisecond) // allow agent to process
	if child.Status() == StatusDone || child.Status() == StatusFailed {
		t.Errorf("agent should be alive, got status %s", child.Status())
	}
}

func TestDelegateBlocking(t *testing.T) {
	s := New()
	defer s.Close()

	// Spawn child.
	child, err := s.SpawnAgent(RootAgentID, "", AutonomyFull)
	if err != nil {
		t.Fatalf("SpawnAgent failed: %v", err)
	}
	childID := child.ID()

	// Give it a blocking task.
	err = s.Delegate(RootAgentID, childID, "compute answer", "", AutonomyFull, true)
	if err != nil {
		t.Fatalf("Delegate failed: %v", err)
	}

	// Agent should process the task and stay alive (waiting for next task).
	time.Sleep(300 * time.Millisecond)
	if child.Status() == StatusDone || child.Status() == StatusFailed {
		t.Errorf("agent should be alive, got status %s", child.Status())
	}
}

func TestDelegateInvalidParentChild(t *testing.T) {
	s := New()
	defer s.Close()

	// Spawn two children.
	child1, _ := s.SpawnAgent(RootAgentID, "", AutonomyFull)
	child2, _ := s.SpawnAgent(RootAgentID, "", AutonomyFull)

	// Try to delegate from child1 to child2 — should fail (not a direct child relationship).
	err := s.Delegate(child1.ID(), child2.ID(), "task", "", AutonomyFull, false)
	if err == nil {
		t.Error("expected error for invalid parent-child relationship, got nil")
	}
}

func TestAgentTree(t *testing.T) {
	s := New()
	defer s.Close()

	// Build tree: root → 2 → 3 (linear chain of depth 2).
	agent2, _ := s.SpawnAgent(RootAgentID, "", AutonomyFull)
	child3, _ := s.SpawnAgent(agent2.ID(), "", AutonomyFull) // 2 → 3

	tree := s.AgentTree()
	if len(tree) != 3 {
		t.Errorf("expected 3 nodes (root, agent2, agent3), got %d", len(tree))
	}

	// Check root is depth 0 and child3 is depth 2.
	for _, n := range tree {
		if n.ID == RootAgentID && n.Depth != 0 {
			t.Errorf("root depth expected 0, got %d", n.Depth)
		}
		if n.ID == child3.ID() && n.Depth != 2 {
			t.Errorf("agent3 depth expected 2, got %d", n.Depth)
		}
	}
}

func TestSetActiveAgent(t *testing.T) {
	s := New()
	defer s.Close()

	child, _ := s.SpawnAgent(RootAgentID, "", AutonomyFull)

	// Switch to child.
	err := s.SetActiveAgent(child.ID())
	if err != nil {
		t.Fatalf("SetActiveAgent failed: %v", err)
	}
	if s.ActiveAgent().ID() != child.ID() {
		t.Errorf("expected active agent %d, got %d", child.ID(), s.ActiveAgent().ID())
	}

	// Switch to non-existent agent.
	err = s.SetActiveAgent(999)
	if err == nil {
		t.Error("expected error for non-existent agent")
	}
}

func TestKillAgent(t *testing.T) {
	s := New()
	defer s.Close()

	child, _ := s.SpawnAgent(RootAgentID, "", AutonomyFull)
	grandchild, _ := s.SpawnAgent(child.ID(), "", AutonomyFull)

	// Kill the child — should also kill grandchild.
	err := s.KillAgent(child.ID())
	if err != nil {
		t.Fatalf("KillAgent failed: %v", err)
	}

	if s.AgentCount() != 1 {
		t.Errorf("expected 1 agent after kill, got %d", s.AgentCount())
	}
	if s.GetAgent(child.ID()) != nil {
		t.Error("child agent should be nil after kill")
	}
	if s.GetAgent(grandchild.ID()) != nil {
		t.Error("grandchild agent should be nil after kill")
	}

	// Cannot kill root.
	err = s.KillAgent(RootAgentID)
	if err == nil {
		t.Error("expected error killing root agent")
	}
}

// ─── EngineProvider stub for integration testing ───────────────────────────────

// stubEngine is a no-op EngineProvider for testing without the real Engine.
type stubEngine struct{}

func (e *stubEngine) ExecuteTool(ctx context.Context, agentID AgentID, name string, params map[string]any) (tools.Result, error) {
	return tools.Result{Success: true, Output: "stub: " + name}, nil
}

func (e *stubEngine) Complete(ctx context.Context, req CompletionRequest) CompletionResponse {
	return CompletionResponse{
		Content:    `{"content":"stub response"}`,
		StopReason: "stop",
		Usage:      TokenUsage{InputTokens: 10, OutputTokens: 5},
	}
}

func (e *stubEngine) NewContextManager(agentID AgentID) ContextManagerHandle {
	return &stubContext{}
}

func (e *stubEngine) PublishAttention(event AttentionEvent) {}

// stubContext implements ContextManagerHandle as a no-op.
type stubContext struct{}

func (c *stubContext) Build(ctx context.Context, msgs []Message) (string, TokenUsage, error) {
	return "", TokenUsage{}, nil
}
func (c *stubContext) BudgetRemaining() int   { return 100000 }
func (c *stubContext) SetBudget(int)          {}
func (c *stubContext) RecordUsage(TokenUsage) {}
func (c *stubContext) Reset()                 {}

// TestWithStubEngine tests that an agent runs and completes with a stub engine.
func TestWithStubEngine(t *testing.T) {
	s := New()
	defer s.Close()

	stub := &stubEngine{}
	s.AttachEngine(stub)

	// Give root a simple task.
	root := s.ActiveAgent()
	root.SetMaxSteps(5)

	// Run root synchronously in a goroutine with a signal.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		root.Run(root.runCtx, "say hello")
	}()

	// Wait up to 2 seconds for completion.
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Completed successfully.
	case <-time.After(2 * time.Second):
		t.Error("agent did not complete within timeout")
	}
}

// TestCoordinatorWaitingChild tests that the parent can see a child's status
// and evaluate what to do.
func TestCoordinatorWaitingChild(t *testing.T) {
	s := New()
	defer s.Close()

	// Spawn a child with limited autonomy.
	child, err := s.SpawnAgent(RootAgentID, "", AutonomyLimited)
	if err != nil {
		t.Fatalf("SpawnAgent failed: %v", err)
	}
	childID := child.ID()

	// Give the child a task with limited autonomy.
	err = s.Delegate(RootAgentID, childID, "do something", "", AutonomyLimited, false)
	if err != nil {
		t.Fatalf("Delegate failed: %v", err)
	}

	// Wait for child to process the task.
	// With stub engine, agent completes the task immediately and stays alive
	// as waiting_delegation (not waiting_user_input — that requires budget exhaustion).
	time.Sleep(300 * time.Millisecond)

	// Child should be alive and waiting for next task.
	status := child.Status()
	if status != StatusWaitingDelegation && status != StatusDone {
		t.Errorf("child status expected waiting_delegation or done, got %s", status)
	}

	// Parent (root) should have the child in its children list.
	root := s.GetAgent(RootAgentID)
	if len(root.children) != 1 || root.children[0] != childID {
		t.Errorf("root children mismatch: got %v", root.children)
	}

	// EvaluateChildStatus should return CoordIgnore for waiting_delegation,
	// or CoordSurface/CoordRedelegate for waiting_user_input.
	decision := root.EvaluateChildStatus(child)
	if decision == CoordIgnore {
		// waiting_delegation → ignore is correct
	} else if decision == CoordSurface || decision == CoordRedelegate {
		// waiting_user_input → coordinator should act
	} else {
		t.Errorf("unexpected coordinator decision: %v", decision)
	}
}

// TestSharedAttentionSubscription tests that the parent is subscribed to child's events.
func TestSharedAttentionSubscription(t *testing.T) {
	s := New()
	defer s.Close()

	// Spawn a child.
	child, _ := s.SpawnAgent(RootAgentID, "", AutonomyFull)

	// Publish an event from the child.
	s.attention.Publish(AttentionEvent{
		From:    child.ID(),
		Type:    AttentionToolResult,
		Payload: []byte(`{"tool":"test","output":"ok"}`),
	})

	// Root should be able to see the event.
	events := s.attention.EventsFor(RootAgentID)
	if len(events) == 0 {
		t.Error("expected attention event for root, got none")
	}
}
