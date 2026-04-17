// Regression tests for the Critical-severity fixes flagged in the
// 2026-04-17 review:
//
//   C1 — Memory.Load error no longer swallowed silently; degraded flag
//        surfaces in Status and an event is published.
//   C3 — /continue no longer grants unbounded fresh MaxSteps budgets;
//        cumulative steps/tokens enforce an outer ceiling.
//   M4 — EventBus.Publish is nil-receiver-safe so shutdown races can't
//        panic a best-effort publisher.

package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/provider"
)

// --- M4: nil EventBus guards -------------------------------------

func TestEventBus_NilPublishDoesNotPanic(t *testing.T) {
	// A nil receiver must be a no-op, not a panic. This is the shape
	// of bug M4: a caller's "check e.EventBus != nil" is not atomic
	// with the Publish call during shutdown, so the receiver can race
	// to nil under concurrent teardown.
	var eb *EventBus
	eb.Publish(Event{Type: "probe"})
}

func TestEventBus_NilSubscribeReturnsClosedChannel(t *testing.T) {
	var eb *EventBus
	ch := eb.Subscribe("whatever")
	// A range loop over a closed channel terminates instead of
	// blocking, so the caller doesn't deadlock.
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected closed channel, got an event")
		}
	default:
		t.Fatal("expected closed channel that receives immediately")
	}
}

func TestEventBus_NilUnsubscribeDoesNotPanic(t *testing.T) {
	var eb *EventBus
	eb.Unsubscribe("x", make(chan Event))
}

// --- C1: Status surfaces memory-degraded flag --------------------

// The Status copy-in must include the degraded flag verbatim, so the
// TUI / Web / remote clients can render a warning. This guards against
// someone adding a new Status field and forgetting to copy one of the
// two memory fields into the return struct.
func TestStatus_ExposesMemoryDegradedFlag(t *testing.T) {
	cfg := config.DefaultConfig()
	e := &Engine{
		Config:   cfg,
		EventBus: NewEventBus(),
		state:    StateReady,
	}
	e.memoryDegraded = true
	e.memoryLoadErr = "bolt: database not open"

	s := e.Status()
	if !s.MemoryDegraded {
		t.Fatal("Status().MemoryDegraded should mirror e.memoryDegraded")
	}
	if s.MemoryLoadErr != "bolt: database not open" {
		t.Fatalf("MemoryLoadErr not surfaced: %q", s.MemoryLoadErr)
	}
}

// --- C3: cumulative resume ceiling -------------------------------

// After the ceiling is reached, a further /continue must refuse with
// a clear error and re-park the snapshot so the user's state isn't
// lost. The core of the fix: Step and TotalTokens reset per resume
// (so each attempt really does progress), but CumulativeSteps keeps
// climbing and eventually trips resumeMaxMultiplier * MaxSteps.
func TestResumeAgent_RefusesAfterCumulativeStepsCeiling(t *testing.T) {
	// Two looping reads + a never-reached final answer — each resume
	// starts a fresh MaxSteps=1 budget, so each round consumes 1 step
	// then parks. With resumeMaxMultiplier=3 and MaxSteps=1, the
	// third resume must refuse.
	eng, _, _ := buildGuardTestEngine(t, 0, 1, []scriptedResponse{
		{ToolCalls: []provider.ToolCall{loopingReadToolCall("c1")}},
		{ToolCalls: []provider.ToolCall{loopingReadToolCall("c2")}},
		{ToolCalls: []provider.ToolCall{loopingReadToolCall("c3")}},
		{ToolCalls: []provider.ToolCall{loopingReadToolCall("c4")}},
	})

	// Initial ask — this consumes the FIRST budget (MaxSteps=1) and
	// parks, accumulating CumulativeSteps=1 after the next resume.
	if _, err := eng.AskWithMetadata(context.Background(), "ceiling check"); err != nil {
		t.Fatalf("initial ask: %v", err)
	}
	if !eng.HasParkedAgent() {
		t.Fatal("expected park after initial MaxSteps=1 round")
	}

	// First resume — consumes the SECOND budget, parks again,
	// cumulative=2 after the next resume.
	if _, err := eng.ResumeAgent(context.Background(), ""); err != nil {
		t.Fatalf("first resume should succeed: %v", err)
	}
	if !eng.HasParkedAgent() {
		t.Fatal("expected park after first resume")
	}

	// Second resume — consumes the THIRD budget, parks again,
	// cumulative=3 after next resume. That equals the ceiling
	// (resumeMaxMultiplier=3 * MaxSteps=1), so the NEXT call must
	// refuse.
	if _, err := eng.ResumeAgent(context.Background(), ""); err != nil {
		t.Fatalf("second resume should succeed: %v", err)
	}
	if !eng.HasParkedAgent() {
		t.Fatal("expected park after second resume")
	}

	// Third resume — this is the one that should refuse.
	_, err := eng.ResumeAgent(context.Background(), "")
	if err == nil {
		t.Fatal("third resume must refuse once cumulative ceiling is reached")
	}
	if !strings.Contains(err.Error(), "ceiling") {
		t.Fatalf("error message should mention ceiling, got %q", err.Error())
	}
	// Snapshot must still be recoverable — the user's work isn't lost.
	if !eng.HasParkedAgent() {
		t.Fatal("refused resume must re-park the snapshot")
	}
}

// Sanity: a resume with no parked state returns the existing "no
// parked agent" error unchanged. The ceiling check must NOT fire here
// because seed is nil.
func TestResumeAgent_NoParkedStateStillErrors(t *testing.T) {
	eng, _, _ := buildGuardTestEngine(t, 0, 1, nil)
	_, err := eng.ResumeAgent(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "no parked agent") {
		t.Fatalf("want 'no parked agent' error, got %v", err)
	}
}
