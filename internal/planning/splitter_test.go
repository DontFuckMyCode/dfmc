package planning

import (
	"strings"
	"testing"
	"unicode/utf8"
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

// TestTruncateTitle_MultibyteRuneCut regresses a UTF-8 corruption bug:
// the previous byte-indexed implementation could land its cut inside a
// multi-byte rune when no ASCII space fell within the 15-rune word-boundary
// window. The output then ended in an orphan UTF-8 start byte (0xC3 for
// "ç") immediately followed by the 0xE2 of "…" — not a valid sequence.
// The package's own TestSplitTask_TurkishStages exercises Turkish input,
// so this is a realistic input shape, not a synthetic one.
func TestTruncateTitle_MultibyteRuneCut(t *testing.T) {
	// 51 ASCII chars puts the byte cursor at byte 60 inside the 5th ç,
	// which is two bytes per rune. No spaces in the trailing window.
	in := strings.Repeat("a", 51) + strings.Repeat("ç", 15)
	out := truncateTitle(in, 60)
	if !utf8.ValidString(out) {
		t.Fatalf("truncated title is not valid UTF-8: %x", []byte(out))
	}
	if !strings.HasSuffix(out, "…") {
		t.Fatalf("expected trailing ellipsis, got %q", out)
	}
	// The cut should sit on a rune boundary, so decoding back to runes
	// must give a non-empty string with the ellipsis as its final rune.
	runes := []rune(out)
	if len(runes) == 0 {
		t.Fatalf("truncation produced empty title")
	}
	if runes[len(runes)-1] != '…' {
		t.Fatalf("last rune should be ellipsis, got %q", runes[len(runes)-1])
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
