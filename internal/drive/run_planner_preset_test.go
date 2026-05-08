// Verifies that a Run pre-loaded with TODOs (e.g. via TodosFromSpec)
// skips the planner LLM entirely and goes straight into execute. The
// fake runner's PlanFunc panics so a regression that re-introduces an
// LLM call here would surface immediately rather than as a subtle
// "wrong plan" — pinning the skip is the whole point of this test.

package drive

import (
	"context"
	"sync"
	"testing"
)

func TestRunPlannerPhase_PresetTodosSkipPlanner(t *testing.T) {
	runner := &fakeRunner{
		PlanFunc: func(req PlannerRequest) (string, error) {
			t.Fatalf("planner LLM must not be called for preset Todos; got user=%q", req.User)
			return "", nil
		},
	}
	var events []string
	var payloads []map[string]any
	var mu sync.Mutex

	d := NewDriver(runner, nil, captureEventPayloads(&payloads, &events, &mu), Config{}.Apply())

	run, err := NewRun("preset run")
	if err != nil {
		t.Fatalf("NewRun: %v", err)
	}
	run.Todos = []Todo{
		{ID: "S1", Title: "first preset", Detail: "do thing 1", Status: TodoPending, Origin: "spec"},
		{ID: "S2", Title: "second preset", Detail: "do thing 2", Status: TodoPending, Origin: "spec"},
	}

	run, err = d.RunPrepared(context.Background(), run)
	if err != nil {
		t.Fatalf("RunPrepared: %v", err)
	}
	if run.Status != RunDone {
		t.Fatalf("expected RunDone, got %s (reason=%q)", run.Status, run.Reason)
	}
	done, _, _, _ := run.Counts()
	if done != 2 {
		t.Errorf("expected 2 done, got %d", done)
	}

	// Plan events must carry source=preset so subscribers can tell a
	// literal-execution run from a planner-driven one.
	mu.Lock()
	defer mu.Unlock()
	sawPresetStart := false
	sawPresetDone := false
	for _, p := range payloads {
		switch p["_type"] {
		case EventPlanStart:
			if p["source"] == "preset" {
				sawPresetStart = true
			}
		case EventPlanDone:
			if p["source"] == "preset" {
				sawPresetDone = true
			}
		}
	}
	if !sawPresetStart {
		t.Errorf("EventPlanStart must carry source=preset; events=%v", events)
	}
	if !sawPresetDone {
		t.Errorf("EventPlanDone must carry source=preset; events=%v", events)
	}
}
