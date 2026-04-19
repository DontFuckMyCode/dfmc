package drive

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestParseSpawnedTodos_StripsPayloadAndRemapsDependencies(t *testing.T) {
	parent := Todo{ID: "T1", Title: "Survey auth", Detail: "Inspect auth flow."}
	existing := []Todo{{ID: "T1", Title: "Survey auth", Detail: "Inspect auth flow.", Status: TodoDone}}
	raw := "Mapped the auth flow and found one missing follow-up.\n\n" +
		`{"spawn_todos":[{"id":"A","title":"Patch token refresh","detail":"Update refresh validation.","depends_on":["A"],"provider_tag":"code","worker_class":"coder","verification":"required","confidence":0.7},{"id":"B","title":"Review token refresh","detail":"Inspect the patch.","depends_on":["A"],"provider_tag":"review","worker_class":"reviewer","verification":"required","confidence":0.6}]}`
	brief, spawned, err := parseSpawnedTodos(raw, parent, existing)
	if err != nil {
		t.Fatalf("parseSpawnedTodos: %v", err)
	}
	if strings.Contains(brief, "spawn_todos") {
		t.Fatalf("expected machine payload stripped from brief, got %q", brief)
	}
	if len(spawned) != 2 {
		t.Fatalf("expected 2 spawned todos, got %+v", spawned)
	}
	if spawned[0].ID != "T2" || spawned[1].ID != "T3" {
		t.Fatalf("expected canonical generated ids, got %+v", spawned)
	}
	if spawned[0].ParentID != "T1" || spawned[1].ParentID != "T1" {
		t.Fatalf("expected parent ids set, got %+v", spawned)
	}
	if len(spawned[0].DependsOn) != 1 || spawned[0].DependsOn[0] != "T1" {
		t.Fatalf("expected first child to depend on parent only, got %+v", spawned[0].DependsOn)
	}
	if len(spawned[1].DependsOn) != 2 || spawned[1].DependsOn[0] != "T1" || spawned[1].DependsOn[1] != "T2" {
		t.Fatalf("expected second child deps remapped to parent+child, got %+v", spawned[1].DependsOn)
	}
}

func TestDriverWorkerSpawnedTodosExpandRun(t *testing.T) {
	runner := &fakeRunner{
		PlanFunc: func(_ PlannerRequest) (string, error) {
			return `{"todos":[{"id":"T1","title":"Survey auth","detail":"Inspect auth flow.","provider_tag":"research","worker_class":"researcher","verification":"light"}]}`, nil
		},
		ExecFunc: func(req ExecuteTodoRequest) (ExecuteTodoResponse, error) {
			if req.TodoID == "T1" {
				return ExecuteTodoResponse{Summary: "Mapped auth flow.\n\n" +
					`{"spawn_todos":[{"id":"child","title":"Patch token refresh","detail":"Update refresh validation.","provider_tag":"code","worker_class":"coder","verification":"required","confidence":0.8}]}`}, nil
			}
			return ExecuteTodoResponse{Summary: "patched refresh"}, nil
		},
	}
	d := NewDriver(runner, nil, nil, Config{MaxParallel: 1})
	run, err := d.Run(context.Background(), "ship auth fix")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Status != RunDone {
		t.Fatalf("expected RunDone, got %s", run.Status)
	}
	if len(run.Todos) != 2 {
		t.Fatalf("expected spawned child todo to be persisted, got %+v", run.Todos)
	}
	if run.Todos[1].Origin != "worker" || run.Todos[1].ParentID != "T1" {
		t.Fatalf("expected worker child todo, got %+v", run.Todos[1])
	}
	if len(runner.Calls) != 2 || runner.Calls[1].TodoID != run.Todos[1].ID {
		t.Fatalf("expected spawned child to execute as second step, calls=%+v todos=%+v", runner.Calls, run.Todos)
	}
	if run.Plan == nil || len(run.Plan.Layers) < 2 {
		t.Fatalf("expected refreshed plan snapshot after expansion, got %+v", run.Plan)
	}
}

func TestDriverSpawnedTodoInsertedBeforeVerifierAndExtendsDeps(t *testing.T) {
	runner := &fakeRunner{
		PlanFunc: func(_ PlannerRequest) (string, error) {
			return `{"todos":[{"id":"T1","title":"Patch auth","detail":"Update auth flow.","provider_tag":"code","worker_class":"coder","verification":"required"}]}`, nil
		},
		ExecFunc: func(req ExecuteTodoRequest) (ExecuteTodoResponse, error) {
			if req.TodoID == "T1" {
				return ExecuteTodoResponse{Summary: "Patched auth flow.\n\n" +
					`{"spawn_todos":[{"id":"follow","title":"Patch refresh guard","detail":"Cover the refresh edge case.","provider_tag":"code","worker_class":"coder","verification":"required","confidence":0.7}]}`}, nil
			}
			return ExecuteTodoResponse{Summary: "ok"}, nil
		},
	}
	d := NewDriver(runner, nil, nil, Config{AutoVerify: true, MaxParallel: 1})
	run, err := d.Run(context.Background(), "ship auth fix")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(runner.Calls) != 3 {
		t.Fatalf("expected original task, spawned child, then verifier, got %+v", runner.Calls)
	}
	lastCall := runner.Calls[len(runner.Calls)-1]
	if !strings.HasPrefix(lastCall.TodoID, "SV") {
		t.Fatalf("expected verifier to remain last after expansion, got %+v", runner.Calls)
	}
	if run.Plan == nil || run.Plan.VerificationID == "" {
		t.Fatalf("expected verification id in plan, got %+v", run.Plan)
	}
	idx, ok := todoIndexByID(run.Todos, run.Plan.VerificationID)
	if !ok {
		t.Fatalf("expected verifier todo present, got %+v", run.Todos)
	}
	if !containsStringFold(run.Todos[idx].DependsOn, "T2") {
		t.Fatalf("expected verifier to depend on spawned child, got %+v", run.Todos[idx])
	}
}

func TestParseSpawnedTodos_CapsPerParent(t *testing.T) {
	parent := Todo{ID: "T1", Title: "Survey auth", Detail: "Inspect auth flow."}
	existing := []Todo{{ID: "T1", Title: "Survey auth", Detail: "Inspect auth flow.", Status: TodoDone}}
	items := make([]string, 0, maxSpawnedTodosPerParent+3)
	for i := 0; i < maxSpawnedTodosPerParent+3; i++ {
		items = append(items, fmt.Sprintf(`{"id":"L%d","title":"Task %d","detail":"Detail %d","provider_tag":"code","worker_class":"coder","verification":"required"}`, i+1, i+1, i+1))
	}
	raw := `{"spawn_todos":[` + strings.Join(items, ",") + `]}`
	_, spawned, err := parseSpawnedTodos(raw, parent, existing)
	if err != nil {
		t.Fatalf("parseSpawnedTodos: %v", err)
	}
	if len(spawned) != maxSpawnedTodosPerParent {
		t.Fatalf("expected cap %d, got %d", maxSpawnedTodosPerParent, len(spawned))
	}
	if spawned[0].ID != "T2" {
		t.Fatalf("expected canonical sequence to start at T2, got %+v", spawned[0])
	}
	if spawned[len(spawned)-1].ID != fmt.Sprintf("T%d", maxSpawnedTodosPerParent+1) {
		t.Fatalf("expected canonical sequence preserved after cap, got %+v", spawned[len(spawned)-1])
	}
}
