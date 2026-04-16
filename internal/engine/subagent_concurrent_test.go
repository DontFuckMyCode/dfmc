package engine

import (
	"sync"
	"testing"
	"time"
)

// TestEnterSubagentProtectsParentParkedState verifies the reference-counted
// save/restore that lets tool_batch_call(delegate_task) fan sub-agents out
// in parallel without racing on the shared agentParked field.
//
// Before this guard, two concurrent sub-agents could both observe "nothing
// parked" (because the first takeParkedAgent wins) and then both call
// ClearParkedAgent on exit, stomping the parent's state.
func TestEnterSubagentProtectsParentParkedState(t *testing.T) {
	e := &Engine{}
	parent := &parkedAgentState{Question: "parent task", Step: 7}
	e.saveParkedAgent(parent)

	if got := e.HasParkedAgent(); !got {
		t.Fatalf("expected parent parked state to be visible before sub-agents start")
	}

	// Spawn 8 concurrent sub-agents. Each one enters the sub-agent scope,
	// briefly "works" while setting/clearing transient parked state of its
	// own, and exits. The parent's state must survive the whole fan-out.
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range 8 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			exit := e.enterSubagent()
			defer exit()

			// Simulate the sub-agent parking its own transient state — the
			// outermost exit must discard everything a sub-agent wrote, not
			// restore it to the parent.
			e.saveParkedAgent(&parkedAgentState{Question: "child", Step: idx})
			time.Sleep(2 * time.Millisecond)
		}(i)
	}
	close(start)
	wg.Wait()

	// After all sub-agents finish, the parent's original parked state must
	// be restored and no child state must linger.
	if !e.HasParkedAgent() {
		t.Fatalf("parent parked state was lost after concurrent sub-agents")
	}
	got := e.takeParkedAgent()
	if got == nil || got.Question != "parent task" || got.Step != 7 {
		t.Fatalf("parent state corrupted after concurrent sub-agents, got %#v", got)
	}
}

// TestEnterSubagentNestedCallsKeepCounterConsistent covers the "sub-agent
// calls delegate_task from within itself" shape. The counter must treat
// nested entries as additive and only restore on the outermost exit.
func TestEnterSubagentNestedCallsKeepCounterConsistent(t *testing.T) {
	e := &Engine{}
	e.saveParkedAgent(&parkedAgentState{Question: "root"})

	outer := e.enterSubagent()
	if e.HasParkedAgent() {
		t.Fatal("outer sub-agent entry should stash the parent parked state")
	}

	// Nested entry: parent is already stashed, this is just incrementing.
	inner := e.enterSubagent()
	if e.HasParkedAgent() {
		t.Fatal("nested sub-agent entry must not expose parent state")
	}
	inner()
	// After inner exits but outer is still in flight, parent stays stashed.
	if e.HasParkedAgent() {
		t.Fatal("parent should not be restored while outer sub-agent is still in flight")
	}
	outer()

	// Only now — after the outermost exit — is the parent visible again.
	if !e.HasParkedAgent() {
		t.Fatal("parent should be restored once the outermost sub-agent exits")
	}
}
