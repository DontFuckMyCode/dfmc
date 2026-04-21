package supervisor_test

import (
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/supervisor"
)

// TestBuildExecutionPlanWithAutoSurveyAndVerify exercises a Run through
// BuildExecutionPlan with AutoSurvey + AutoVerify to verify layer
// synthesis, lane caps, and the survey/verification IDs.
func TestBuildExecutionPlanWithAutoSurveyAndVerify(t *testing.T) {
	createdAt := time.Now().Add(-time.Hour).Round(time.Second)
	run := supervisor.Run{
		ID:        "drv-lifecycle",
		Task:      "ship auth hardening",
		Status:    "running",
		CreatedAt: createdAt,
		Tasks: []supervisor.Task{
			{
				ID:           "T1",
				Title:        "Survey auth surface",
				State:        supervisor.TaskDone,
				WorkerClass:  supervisor.WorkerResearcher,
				ProviderTag:  "research",
				Verification: supervisor.VerifyLight,
				Skills:       []string{"onboard"},
				AllowedTools: []string{"read_file", "grep_codebase"},
				Labels:       []string{"discovery"},
				FileScope:    []string{"internal/auth/"},
				Confidence:   0.9,
				Summary:      "found 3 entry points",
				StartedAt:    createdAt,
				EndedAt:      createdAt.Add(5 * time.Minute),
			},
			{
				ID:           "T2",
				Title:        "Patch auth flow",
				State:        supervisor.TaskRunning,
				DependsOn:    []string{"T1"},
				WorkerClass:  supervisor.WorkerCoder,
				ProviderTag:  "code",
				Verification: supervisor.VerifyRequired,
				FileScope:    []string{"internal/auth/service.go"},
				ReadOnly:     false,
				Skills:       []string{"debug"},
				AllowedTools: []string{"read_file", "edit_file", "apply_patch"},
				Labels:       []string{"critical"},
				Confidence:   0.75,
				Attempts:     1,
				StartedAt:    createdAt.Add(6 * time.Minute),
			},
			{
				ID:           "T3",
				Title:        "Review patch",
				State:        supervisor.TaskPending,
				DependsOn:    []string{"T2"},
				WorkerClass:  supervisor.WorkerReviewer,
				ProviderTag:  "review",
				Verification: supervisor.VerifyDeep,
				FileScope:    []string{"internal/auth/"},
				ReadOnly:     true,
				Skills:       []string{"security"},
				AllowedTools: []string{"read_file", "grep_codebase"},
				Labels:       []string{"security"},
				Confidence:   0.8,
			},
		},
	}

	plan := supervisor.BuildExecutionPlan(run, supervisor.ExecutionOptions{
		AutoSurvey:  true,
		AutoVerify:  true,
		MaxParallel: 4,
	})

	if len(plan.Tasks) != 4 {
		t.Fatalf("expected 4 tasks: T1(survey) + T2 + T3 + SV1(verify); got %d", len(plan.Tasks))
	}
	// T1 is already a researcher with "Survey" in the title, so hasSurveyTask
	// returns true and no additional survey task is synthesized.
	if plan.SurveyID != "" {
		t.Fatalf("SurveyID should be empty when T1 (researcher) already covers survey: got %q", plan.SurveyID)
	}
	if plan.VerificationID == "" {
		t.Fatal("VerificationID should be set (SV1 synthesized)")
	}
	if plan.LaneCaps["code"] != 4 {
		t.Fatalf("code lane cap: want 4, got %d", plan.LaneCaps["code"])
	}
	if plan.LaneCaps["discovery"] != 1 {
		t.Fatalf("discovery lane cap: want 1, got %d", plan.LaneCaps["discovery"])
	}

	// Verify synthesized tasks have correct properties.
	for _, pt := range plan.Tasks {
		if pt.ID == plan.SurveyID {
			if pt.WorkerClass != supervisor.WorkerResearcher {
				t.Fatalf("survey should be researcher, got %v", pt.WorkerClass)
			}
			if !pt.IsAuto {
				t.Fatalf("survey should be IsAuto=true")
			}
		}
		if pt.ID == plan.VerificationID {
			if pt.WorkerClass != supervisor.WorkerSecurity && pt.WorkerClass != supervisor.WorkerTester {
				t.Fatalf("verify should be tester or security, got %v", pt.WorkerClass)
			}
			if !pt.IsAuto {
				t.Fatalf("verify should be IsAuto=true")
			}
		}
	}
}

// TestBuildExecutionPlanCycleRecovery verifies that a DAG with cycles
// falls back to linear single-task layers instead of panicking.
func TestBuildExecutionPlanCycleRecovery(t *testing.T) {
	run := supervisor.Run{
		ID:   "drv-cycle",
		Task: "cycle recovery test",
		Tasks: []supervisor.Task{
			{ID: "A", DependsOn: []string{"C"}},
			{ID: "B", DependsOn: []string{"A"}},
			{ID: "C", DependsOn: []string{"B"}},
		},
	}
	plan := supervisor.BuildExecutionPlan(run, supervisor.ExecutionOptions{MaxParallel: 2})
	if len(plan.Layers) != 3 {
		t.Fatalf("expected 3 single-task layers for cycle recovery, got %d", len(plan.Layers))
	}
	for i, layer := range plan.Layers {
		if len(layer) != 1 {
			t.Fatalf("layer %d: expected 1 task, got %d (%v)", i, len(layer), layer)
		}
	}
}

// TestBuildExecutionPlanEmptyRun handles nil/empty run gracefully.
func TestBuildExecutionPlanEmptyRun(t *testing.T) {
	plan := supervisor.BuildExecutionPlan(supervisor.Run{}, supervisor.ExecutionOptions{})
	if plan.Tasks == nil {
		t.Fatal("Tasks should not be nil for empty run")
	}
	if len(plan.Layers) != 0 {
		t.Fatalf("empty run should produce 0 layers, got %d", len(plan.Layers))
	}
}

// TestBuildExecutionPlanParallelismScheduling verifies MaxParallel correctly
// caps the code lane and distributes independent tasks across layers.
func TestBuildExecutionPlanParallelismScheduling(t *testing.T) {
	tasks := make([]supervisor.Task, 8)
	for i := range tasks {
		tasks[i] = supervisor.Task{
			ID:           string(rune('A' + i)),
			Title:        "Task " + string(rune('A'+i)),
			WorkerClass:  supervisor.WorkerCoder,
			ProviderTag:  "code",
			Verification: supervisor.VerifyRequired,
			AllowedTools: []string{"read_file"},
			State:        supervisor.TaskPending,
		}
	}
	tasks[1].DependsOn = []string{"A"}
	tasks[2].DependsOn = []string{"A"}
	tasks[3].DependsOn = []string{"A"}
	tasks[4].DependsOn = []string{}
	tasks[5].DependsOn = []string{}
	tasks[6].DependsOn = []string{}
	tasks[7].DependsOn = []string{}

	run := supervisor.Run{ID: "drv-par", Task: "parallel test", Tasks: tasks}
	plan := supervisor.BuildExecutionPlan(run, supervisor.ExecutionOptions{MaxParallel: 4})

	layer0 := plan.Layers[0]
	// A (no deps) and E,F,G,H (no deps) are all independent → layer 0.
	if len(layer0) != 5 {
		t.Fatalf("layer 0 should have 5 tasks (A,E,F,G,H), got %d: %v", len(layer0), layer0)
	}
	if plan.LaneCaps["code"] != 4 {
		t.Fatalf("code lane cap: want 4, got %d", plan.LaneCaps["code"])
	}
	if plan.WorkerCounts["coder"] != 8 {
		t.Fatalf("coder count: want 8, got %d", plan.WorkerCounts["coder"])
	}
}

// TestBuildExecutionPlanDeepVerificationSynthesizesSecurityVerifier verifies that
// a task with Verification=VerifyDeep produces a security-class verifier.
func TestBuildExecutionPlanDeepVerificationSynthesizesSecurityVerifier(t *testing.T) {
	run := supervisor.Run{
		ID:   "drv-deep",
		Task: "ship security hardening",
		Tasks: []supervisor.Task{
			{
				ID:           "T1",
				Title:        "Patch SQL injection",
				WorkerClass:  supervisor.WorkerCoder,
				ProviderTag:  "code",
				Verification: supervisor.VerifyDeep,
				FileScope:    []string{"internal/auth/service.go"},
				State:        supervisor.TaskDone,
				AllowedTools: []string{"read_file", "edit_file"},
			},
		},
	}
	plan := supervisor.BuildExecutionPlan(run, supervisor.ExecutionOptions{AutoVerify: true, MaxParallel: 2})
	if plan.VerificationID == "" {
		t.Fatal("expected synthesized verification task")
	}
	for _, pt := range plan.Tasks {
		if pt.ID == plan.VerificationID {
			if pt.WorkerClass != supervisor.WorkerSecurity {
				t.Fatalf("deep verification should use security worker, got %v", pt.WorkerClass)
			}
			if pt.Verification != supervisor.VerifyDeep {
				t.Fatalf("verification level should be deep, got %v", pt.Verification)
			}
		}
	}
}
