package supervisor

import "testing"

func TestBuildExecutionPlan_DerivesLayersAndWorkerCounts(t *testing.T) {
	run := Run{
		ID:   "drv-1",
		Task: "ship auth hardening",
		Tasks: []Task{
			{ID: "T1", Title: "Survey auth", WorkerClass: WorkerResearcher, Verification: VerifyLight},
			{ID: "T2", Title: "Patch auth", DependsOn: []string{"T1"}, WorkerClass: WorkerCoder, Verification: VerifyRequired},
			{ID: "T3", Title: "Review patch", DependsOn: []string{"T2"}, WorkerClass: WorkerReviewer, Verification: VerifyRequired},
		},
	}
	plan := BuildExecutionPlan(run, ExecutionOptions{MaxParallel: 3})
	if len(plan.Layers) != 3 {
		t.Fatalf("expected 3 layers, got %+v", plan.Layers)
	}
	if got := plan.Layers[0][0]; got != "T1" {
		t.Fatalf("unexpected layer ordering: %+v", plan.Layers)
	}
	if plan.WorkerCounts[string(WorkerCoder)] != 1 || plan.WorkerCounts[string(WorkerReviewer)] != 1 {
		t.Fatalf("unexpected worker counts: %+v", plan.WorkerCounts)
	}
	if len(plan.Roots) != 1 || plan.Roots[0] != "T1" {
		t.Fatalf("unexpected roots: %+v", plan.Roots)
	}
	if len(plan.Leaves) != 1 || plan.Leaves[0] != "T3" {
		t.Fatalf("unexpected leaves: %+v", plan.Leaves)
	}
}

func TestBuildExecutionPlan_AutoVerifySynthesizesVerificationTask(t *testing.T) {
	run := Run{
		ID:   "drv-2",
		Task: "ship auth patch",
		Tasks: []Task{
			{ID: "T1", Title: "Patch auth", WorkerClass: WorkerCoder, Verification: VerifyRequired, FileScope: []string{"internal/auth/service.go"}},
			{ID: "T2", Title: "Review auth", DependsOn: []string{"T1"}, WorkerClass: WorkerSecurity, Verification: VerifyDeep, FileScope: []string{"internal/auth/middleware.go"}},
		},
	}
	plan := BuildExecutionPlan(run, ExecutionOptions{AutoVerify: true})
	if len(plan.Tasks) != 3 {
		t.Fatalf("expected synthesized verification task, got %d tasks", len(plan.Tasks))
	}
	last := plan.Tasks[len(plan.Tasks)-1]
	if last.ID != "SV1" || !last.IsAuto || (last.WorkerClass != WorkerSecurity && last.WorkerClass != WorkerVerifier) {
		t.Fatalf("unexpected synthesized task: %+v", last)
	}
	if plan.VerificationID != "SV1" {
		t.Fatalf("expected verification id recorded, got %+v", plan)
	}
	if last.Layer != 2 {
		t.Fatalf("expected verifier to run in the final layer, got %+v", last)
	}
}

func TestBuildExecutionPlan_AutoSurveyPrependsDiscoveryRoot(t *testing.T) {
	run := Run{
		ID:   "drv-survey",
		Task: "ship auth patch",
		Tasks: []Task{
			{ID: "T1", Title: "Patch auth", WorkerClass: WorkerCoder, Verification: VerifyRequired},
			{ID: "T2", Title: "Review auth", DependsOn: []string{"T1"}, WorkerClass: WorkerSecurity, Verification: VerifyDeep},
		},
	}
	plan := BuildExecutionPlan(run, ExecutionOptions{AutoSurvey: true})
	if len(plan.Tasks) != 3 {
		t.Fatalf("expected synthesized survey task, got %d tasks", len(plan.Tasks))
	}
	first := plan.Tasks[0]
	if first.ID != "S1" || !first.IsAuto || first.WorkerClass != WorkerResearcher {
		t.Fatalf("unexpected survey task: %+v", first)
	}
	if plan.SurveyID != "S1" {
		t.Fatalf("expected survey id captured, got %+v", plan)
	}
	if len(plan.Layers) < 2 || plan.Layers[0][0] != "S1" {
		t.Fatalf("survey should become first layer root, got %+v", plan.Layers)
	}
}

func TestBuildExecutionPlan_DoesNotDuplicateExplicitVerifier(t *testing.T) {
	run := Run{
		ID:   "drv-3",
		Task: "ship auth patch",
		Tasks: []Task{
			{ID: "T1", Title: "Patch auth", WorkerClass: WorkerCoder, Verification: VerifyRequired},
			{ID: "T2", Title: "Verification pass", DependsOn: []string{"T1"}, WorkerClass: WorkerTester, ProviderTag: "test", Verification: VerifyRequired},
		},
	}
	plan := BuildExecutionPlan(run, ExecutionOptions{AutoVerify: true})
	if len(plan.Tasks) != 2 {
		t.Fatalf("expected existing verifier to be reused, got %+v", plan.Tasks)
	}
}

func TestBuildExecutionPlan_DerivesLanePolicy(t *testing.T) {
	run := Run{
		ID:   "drv-lanes",
		Task: "ship auth patch",
		Tasks: []Task{
			{ID: "T1", Title: "Survey", WorkerClass: WorkerResearcher, Verification: VerifyLight},
			{ID: "T2", Title: "Patch", DependsOn: []string{"T1"}, WorkerClass: WorkerCoder, Verification: VerifyRequired},
			{ID: "T3", Title: "Review", DependsOn: []string{"T2"}, WorkerClass: WorkerReviewer, Verification: VerifyRequired},
			{ID: "T4", Title: "Verify", DependsOn: []string{"T3"}, WorkerClass: WorkerTester, Verification: VerifyRequired},
		},
	}
	plan := BuildExecutionPlan(run, ExecutionOptions{MaxParallel: 4})
	if got := plan.LaneCaps["code"]; got != 4 {
		t.Fatalf("expected code lane cap to follow max parallel, got %+v", plan.LaneCaps)
	}
	if got := plan.LaneCaps["discovery"]; got != 1 {
		t.Fatalf("expected discovery lane cap 1, got %+v", plan.LaneCaps)
	}
	if got := plan.LaneCaps["review"]; got != 1 {
		t.Fatalf("expected review lane cap 1, got %+v", plan.LaneCaps)
	}
	if got := plan.LaneCaps["verify"]; got != 1 {
		t.Fatalf("expected verify lane cap 1, got %+v", plan.LaneCaps)
	}
	if len(plan.LaneOrder) != 4 {
		t.Fatalf("expected lane order to cover all present lanes, got %+v", plan.LaneOrder)
	}
	want := []string{"discovery", "code", "review", "verify"}
	for i, lane := range want {
		if plan.LaneOrder[i] != lane {
			t.Fatalf("unexpected lane order: %+v", plan.LaneOrder)
		}
	}
}
