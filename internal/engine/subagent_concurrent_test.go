package engine

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/internal/tools"
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

func TestTryEnterSubagentEnforcesMaxConcurrent(t *testing.T) {
	e := &Engine{}
	first, err := e.tryEnterSubagent(1)
	if err != nil {
		t.Fatalf("first sub-agent should enter: %v", err)
	}
	if _, err := e.tryEnterSubagent(1); err == nil || !errors.Is(err, ErrSubagentConcurrencyLimit) {
		t.Fatalf("second sub-agent should hit limit, got %v", err)
	}
	first()

	second, err := e.tryEnterSubagent(1)
	if err != nil {
		t.Fatalf("slot should reopen after release: %v", err)
	}
	second()
}

func TestStatusReportsSubagentRuntimeCapacity(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agent.ParallelBatchSize = 2
	e, err := New(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	release, err := e.tryEnterSubagent(e.subagentConcurrencyLimit())
	if err != nil {
		t.Fatalf("sub-agent should enter: %v", err)
	}
	st := e.Status()
	if st.SubagentsActive != 1 || st.SubagentsLimit != 2 {
		t.Fatalf("unexpected subagent status active/limit: %d/%d", st.SubagentsActive, st.SubagentsLimit)
	}
	release()

	st = e.Status()
	if st.SubagentsActive != 0 || st.SubagentsLimit != 2 {
		t.Fatalf("release should clear active count and keep limit, got %d/%d", st.SubagentsActive, st.SubagentsLimit)
	}
}

func TestRunSubagentRejectsWhenConcurrencyLimitReached(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agent.ParallelBatchSize = 1
	cfg.Providers.Primary = "stub"
	cfg.Providers.Profiles["stub"] = config.ModelConfig{Model: "stub-model", MaxContext: 128000}
	router, err := provider.NewRouter(cfg.Providers)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	stub := &scriptedProvider{
		name:      "stub",
		model:     "stub-model",
		hints:     newNativeHints(),
		responses: []scriptedResponse{{Text: "should not be called"}},
	}
	router.Register(stub)
	bus := NewEventBus()
	events := bus.Subscribe("*")
	defer bus.Unsubscribe("*", events)
	e := &Engine{
		Config:      cfg,
		EventBus:    bus,
		ProjectRoot: t.TempDir(),
		Providers:   router,
		Tools:       tools.New(*cfg),
	}

	release, err := e.tryEnterSubagent(e.subagentConcurrencyLimit())
	if err != nil {
		t.Fatalf("fixture sub-agent should enter: %v", err)
	}
	defer release()

	_, err = e.RunSubagent(context.Background(), tools.SubagentRequest{Task: "overflow"})
	if err == nil || !errors.Is(err, ErrSubagentConcurrencyLimit) {
		t.Fatalf("RunSubagent should reject at concurrency limit, got %v", err)
	}
	if len(stub.requests) != 0 {
		t.Fatalf("provider should not be called after concurrency rejection, got %d request(s)", len(stub.requests))
	}
	var sawFailure bool
drain:
	for {
		select {
		case ev := <-events:
			if ev.Type != "agent:subagent:done" {
				continue
			}
			payload, _ := ev.Payload.(map[string]any)
			if strings.Contains(errStringFromPayload(payload), "concurrency limit") {
				sawFailure = true
			}
		default:
			break drain
		}
	}
	if !sawFailure {
		t.Fatalf("expected subagent failure event for concurrency limit")
	}
}

func errStringFromPayload(payload map[string]any) string {
	if payload == nil {
		return ""
	}
	s, _ := payload["err"].(string)
	return s
}
