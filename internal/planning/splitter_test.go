package planning

import (
	"strings"
	"testing"
)

func TestSplitTask_Numbered(t *testing.T) {
	plan := SplitTask("do three things: 1) survey the tool registry 2) map the provider router 3) document context manager")
	if len(plan.Subtasks) != 3 {
		t.Fatalf("expected 3 subtasks, got %d: %+v", len(plan.Subtasks), plan.Subtasks)
	}
	if !plan.Parallel {
		t.Fatalf("numbered list without stage markers should be parallel")
	}
	for _, s := range plan.Subtasks {
		if s.Hint != "numbered-list" {
			t.Fatalf("expected numbered-list hint, got %q", s.Hint)
		}
	}
}

func TestSplitTask_StagesAreSequential(t *testing.T) {
	plan := SplitTask("first run the tests, then inspect the failures, then write the fix")
	if len(plan.Subtasks) < 3 {
		t.Fatalf("expected >=3 stages, got %d", len(plan.Subtasks))
	}
	if plan.Parallel {
		t.Fatalf("stage-marked plan must not be parallel")
	}
	if plan.Subtasks[0].Hint != "stage" {
		t.Fatalf("expected stage hint, got %q", plan.Subtasks[0].Hint)
	}
}

func TestSplitTask_NumberedBeatsStage(t *testing.T) {
	// Numbered list is stronger signal than stages — the numbers mean
	// the user is enumerating, not sequencing.
	plan := SplitTask("check these: 1) engine.go 2) router.go 3) manager.go")
	if len(plan.Subtasks) != 3 {
		t.Fatalf("expected 3 subtasks, got %d", len(plan.Subtasks))
	}
	if !plan.Parallel {
		t.Fatalf("numbered items without stage markers should be parallel")
	}
}

func TestSplitTask_ConjunctionsRequireTwoConnectors(t *testing.T) {
	// Single "and" — treat as one task.
	plan := SplitTask("fix the parser and run the tests")
	if len(plan.Subtasks) != 1 {
		t.Fatalf("single conjunction should NOT split, got %d subtasks: %+v", len(plan.Subtasks), plan.Subtasks)
	}
}

func TestSplitTask_MultipleConjunctionsSplit(t *testing.T) {
	plan := SplitTask("survey engine.go, and map the router, and document the manager")
	if len(plan.Subtasks) < 2 {
		t.Fatalf("multiple conjunctions should split, got %d", len(plan.Subtasks))
	}
	if !plan.Parallel {
		t.Fatalf("plain conjunctions without stage markers should be parallel")
	}
}

func TestSplitTask_SingleTask(t *testing.T) {
	plan := SplitTask("add a toolbar to the settings panel")
	if len(plan.Subtasks) != 1 {
		t.Fatalf("single task query should yield 1 subtask, got %d", len(plan.Subtasks))
	}
	if plan.Subtasks[0].Hint != "single" {
		t.Fatalf("expected single hint, got %q", plan.Subtasks[0].Hint)
	}
	if plan.Confidence > 0.3 {
		t.Fatalf("single-task confidence should be low, got %f", plan.Confidence)
	}
}

func TestSplitTask_EmptyQuery(t *testing.T) {
	plan := SplitTask("   ")
	if len(plan.Subtasks) != 0 {
		t.Fatalf("empty query should yield 0 subtasks, got %d", len(plan.Subtasks))
	}
}

func TestSplitTask_TitleTruncated(t *testing.T) {
	long := "do this enormously long thing that has many many words and keeps going on and on past any sensible title length"
	plan := SplitTask(long)
	if len(plan.Subtasks) != 1 {
		t.Fatalf("expected 1 subtask for long single task")
	}
	if len(plan.Subtasks[0].Title) > 62 {
		t.Fatalf("title should be truncated, got %d chars", len(plan.Subtasks[0].Title))
	}
	if !strings.HasSuffix(plan.Subtasks[0].Title, "…") {
		t.Fatalf("truncated title should end with ellipsis, got %q", plan.Subtasks[0].Title)
	}
}

func TestSplitTask_TurkishStages(t *testing.T) {
	plan := SplitTask("önce testleri çalıştır, sonra hatayı incele, ardından düzelt")
	if len(plan.Subtasks) < 3 {
		t.Fatalf("expected Turkish stage markers to trigger split, got %d", len(plan.Subtasks))
	}
	if plan.Parallel {
		t.Fatalf("Turkish stages should not be parallel")
	}
}
