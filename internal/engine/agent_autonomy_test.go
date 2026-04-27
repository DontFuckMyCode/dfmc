package engine

import (
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/planning"
)

func TestTruncateSingleLineLocal_ShortString(t *testing.T) {
	got := truncateSingleLineLocal("hi", 10)
	if got != "hi" {
		t.Errorf("truncateSingleLineLocal(%q, 10) = %q, want %q", "hi", got, "hi")
	}
}

func TestTruncateSingleLineLocal_Truncates(t *testing.T) {
	got := truncateSingleLineLocal("hello world", 5)
	if got != "hello..." {
		t.Errorf("truncateSingleLineLocal(%q, 5) = %q, want %q", "hello world", got, "hello...")
	}
}

func TestTruncateSingleLineLocal_ReplacesNewlines(t *testing.T) {
	got := truncateSingleLineLocal("line1\nline2\rline3", 100)
	if strings.Contains(got, "\n") || strings.Contains(got, "\r") {
		t.Errorf("newlines should be replaced: %q", got)
	}
	if !strings.Contains(got, "line1") || !strings.Contains(got, "line2") || !strings.Contains(got, "line3") {
		t.Errorf("content should be preserved: %q", got)
	}
}

func TestTruncateSingleLineLocal_EmptyString(t *testing.T) {
	got := truncateSingleLineLocal("", 10)
	if got != "" {
		t.Errorf("empty input should return empty, got %q", got)
	}
}

func TestTruncateSingleLineLocal_ZeroLimit(t *testing.T) {
	got := truncateSingleLineLocal("hello", 0)
	if got != "hello" {
		t.Errorf("zero limit should return full string, got %q", got)
	}
}

func TestTruncateSingleLineLocal_ExactFit(t *testing.T) {
	got := truncateSingleLineLocal("hello", 5)
	if got != "hello" {
		t.Errorf("exact fit should not truncate, got %q", got)
	}
}

func TestRenderAutonomyDirective_SequentialPlan(t *testing.T) {
	plan := planning.Plan{
		Subtasks: []planning.Subtask{
			{Title: "Stage 1", Hint: "stage", Description: "Do the first thing"},
			{Title: "Stage 2", Hint: "stage", Description: "Do the second thing"},
		},
		Confidence: 0.80,
		Parallel:   false,
	}
	got := renderAutonomyDirective(plan, false, "auto")
	for _, want := range []string{
		"mode=sequential",
		"confidence=0.80",
		"force_sequential=true",
		"1. [stage] Stage 1",
		"2. [stage] Stage 2",
		"todo_write early",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("directive missing %q", want)
		}
	}
}

func TestRenderAutonomyDirective_ParallelPlan(t *testing.T) {
	plan := planning.Plan{
		Subtasks: []planning.Subtask{
			{Title: "Task A", Hint: "stage", Description: "Read files"},
			{Title: "Task B", Hint: "stage", Description: "Read more files"},
		},
		Confidence: 0.90,
		Parallel:   true,
	}
	got := renderAutonomyDirective(plan, false, "auto")
	for _, want := range []string{
		"mode=parallel",
		"orchestrate",
		"1. [stage] Task A",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("parallel directive missing %q", want)
		}
	}
	if strings.Contains(got, "force_sequential") {
		t.Error("parallel directive should not mention force_sequential")
	}
}

func TestRenderAutonomyDirective_AggressiveMode(t *testing.T) {
	plan := planning.Plan{
		Subtasks: []planning.Subtask{
			{Title: "X", Hint: "stage", Description: "Y"},
		},
		Confidence: 0.50,
		Parallel:   false,
	}
	got := renderAutonomyDirective(plan, false, "aggressive")
	if !strings.Contains(got, "aggressive autonomy mode") {
		t.Error("aggressive mode should include aggressive autonomy notice")
	}
	if !strings.Contains(got, "Start executing now") {
		t.Error("aggressive mode should include immediate execution prompt")
	}
}

func TestRenderAutonomyDirective_TodoSeeded(t *testing.T) {
	plan := planning.Plan{
		Subtasks: []planning.Subtask{
			{Title: "Z", Hint: "stage", Description: "Z desc"},
		},
		Confidence: 0.50,
	}
	got := renderAutonomyDirective(plan, true, "auto")
	if !strings.Contains(got, "pre-seeded") {
		t.Error("todoSeeded=true should mention pre-seeded")
	}
}

func TestRenderAutonomyDirective_FallsBackToDescription(t *testing.T) {
	plan := planning.Plan{
		Subtasks: []planning.Subtask{
			{Title: "", Hint: "stage", Description: "Fallback desc"},
		},
		Confidence: 0.50,
	}
	got := renderAutonomyDirective(plan, false, "auto")
	if !strings.Contains(got, "Fallback desc") {
		t.Error("empty title should fall back to description")
	}
}

func TestAutonomousPlanningMode_OffVariants(t *testing.T) {
	for _, val := range []string{"off", "false", "no", "0", "manual"} {
		e := &Engine{Config: &config.Config{Agent: config.AgentConfig{AutonomousPlanning: val}}}
		if got := e.autonomousPlanningMode(); got != "off" {
			t.Errorf("mode(%q) = %q, want off", val, got)
		}
	}
}

func TestAutonomousPlanningMode_AggressiveVariants(t *testing.T) {
	for _, val := range []string{"aggressive", "aggr", "strict", "force"} {
		e := &Engine{Config: &config.Config{Agent: config.AgentConfig{AutonomousPlanning: val}}}
		if got := e.autonomousPlanningMode(); got != "aggressive" {
			t.Errorf("mode(%q) = %q, want aggressive", val, got)
		}
	}
}

func TestAutonomousPlanningMode_DefaultAuto(t *testing.T) {
	e := &Engine{Config: &config.Config{Agent: config.AgentConfig{AutonomousPlanning: ""}}}
	if got := e.autonomousPlanningMode(); got != "auto" {
		t.Errorf("empty mode = %q, want auto", got)
	}
}

func TestAutonomousPlanningMode_NilEngine(t *testing.T) {
	var e *Engine
	if got := e.autonomousPlanningMode(); got != "auto" {
		t.Errorf("nil engine mode = %q, want auto", got)
	}
}

func TestAutonomousPlanningEnabled(t *testing.T) {
	e := &Engine{Config: &config.Config{Agent: config.AgentConfig{AutonomousPlanning: "off"}}}
	if e.autonomousPlanningEnabled() {
		t.Error("off mode should not be enabled")
	}
	e2 := &Engine{Config: &config.Config{Agent: config.AgentConfig{AutonomousPlanning: "auto"}}}
	if !e2.autonomousPlanningEnabled() {
		t.Error("auto mode should be enabled")
	}
}

func TestAutonomySubtaskTitles(t *testing.T) {
	plan := planning.Plan{
		Subtasks: []planning.Subtask{
			{Title: "Alpha", Description: "Alpha desc"},
			{Title: "", Description: "Beta desc"},
		},
	}
	titles := autonomySubtaskTitles(plan)
	if len(titles) != 2 {
		t.Fatalf("expected 2 titles, got %d", len(titles))
	}
	if titles[0] != "Alpha" {
		t.Errorf("titles[0] = %q, want Alpha", titles[0])
	}
	if titles[1] != "Beta desc" {
		t.Errorf("titles[1] = %q, want Beta desc (fallback)", titles[1])
	}
}

func TestShouldAutoKickoffAutonomy_NilPreflight(t *testing.T) {
	if shouldAutoKickoffAutonomy(nil) {
		t.Error("nil preflight should not auto-kickoff")
	}
}

func TestShouldAutoKickoffAutonomy_BelowThreshold(t *testing.T) {
	pf := &autonomyPreflight{
		Plan: planning.Plan{Confidence: 0.30, Subtasks: []planning.Subtask{{}, {}}},
		Mode: "auto",
	}
	if shouldAutoKickoffAutonomy(pf) {
		t.Error("low confidence auto mode should not auto-kickoff")
	}
}

func TestShouldAutoKickoffAutonomy_AggressiveBelowNormalThreshold(t *testing.T) {
	pf := &autonomyPreflight{
		Plan: planning.Plan{Confidence: 0.30, Subtasks: []planning.Subtask{{}, {}}},
		Mode: "aggressive",
	}
	if shouldAutoKickoffAutonomy(pf) {
		t.Error("aggressive mode with confidence 0.30 should stay below the kickoff threshold")
	}
}

func TestShouldAutoKickoffAutonomy_TooFewSubtasks(t *testing.T) {
	pf := &autonomyPreflight{
		Plan: planning.Plan{Confidence: 0.80, Subtasks: []planning.Subtask{{}}},
		Mode: "auto",
	}
	if shouldAutoKickoffAutonomy(pf) {
		t.Error("single subtask should not auto-kickoff")
	}
}

func TestShouldAutoKickoffAutonomy_AggressiveThresholdRequiresThreeSubtasks(t *testing.T) {
	pf := &autonomyPreflight{
		Plan:  planning.Plan{Confidence: 0.40, Subtasks: []planning.Subtask{{}, {}, {}}},
		Mode:  "aggressive",
		Scope: "top_level",
	}
	if !shouldAutoKickoffAutonomy(pf) {
		t.Error("aggressive mode at confidence 0.40 with 3 subtasks should auto-kickoff")
	}
}
