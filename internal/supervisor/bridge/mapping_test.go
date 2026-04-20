package bridge

import (
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/drive"
	"github.com/dontfuckmycode/dfmc/internal/supervisor"
)

func TestTaskDriveRoundTripPreservesExecutionMetadata(t *testing.T) {
	start := time.Now().Add(-time.Minute).Round(time.Second)
	end := start.Add(15 * time.Second)
	task := supervisor.Task{
		ID:           "T2",
		ParentID:     "T1",
		Title:        "Patch auth flow",
		Detail:       "Update the refresh path and add regression coverage.",
		State:        supervisor.TaskRunning,
		DependsOn:    []string{"T1"},
		FileScope:    []string{"internal/auth/service.go", "internal/auth/service_test.go"},
		ReadOnly:     true,
		ProviderTag:  "code",
		WorkerClass:  supervisor.WorkerCoder,
		Skills:       []string{"debug", "test"},
		AllowedTools: []string{"read_file", "edit_file", "run_command"},
		Labels:       []string{"auth", "critical"},
		Verification: supervisor.VerifyDeep,
		Confidence:   0.82,
		Summary:      "refresh path patched",
		Error:        "",
		BlockedReason: "",
		Attempts:     2,
		StartedAt:    start,
		EndedAt:      end,
	}

	todo := TaskToDriveTodo(task)
	if todo.WorkerClass != "coder" {
		t.Fatalf("worker_class lost: %+v", todo)
	}
	if todo.Verification != "deep" {
		t.Fatalf("verification lost: %+v", todo)
	}
	if len(todo.AllowedTools) != 3 {
		t.Fatalf("allowed_tools lost: %+v", todo)
	}
	if !todo.ReadOnly {
		t.Fatalf("read_only lost: %+v", todo)
	}
	if todo.BlockedReason != "" {
		t.Fatalf("expected empty BlockedReason, got: %q", todo.BlockedReason)
	}

	back := TaskFromDriveTodo(todo)
	if back.WorkerClass != supervisor.WorkerCoder {
		t.Fatalf("unexpected worker class after roundtrip: %q", back.WorkerClass)
	}
	if back.Verification != supervisor.VerifyDeep {
		t.Fatalf("unexpected verification after roundtrip: %q", back.Verification)
	}
	if back.Confidence != 0.82 {
		t.Fatalf("confidence lost after roundtrip: %+v", back)
	}
	if !back.ReadOnly {
		t.Fatalf("read_only lost after roundtrip: %+v", back)
	}
	if back.BlockedReason != "" {
		t.Fatalf("expected empty blocked reason after roundtrip, got: %q", back.BlockedReason)
	}
}

func TestTaskDriveRoundTrip_BlockedReasonRoundTrips(t *testing.T) {
	start := time.Now().Add(-time.Minute).Round(time.Second)
	task := supervisor.Task{
		ID:            "T3",
		Title:         "Patch sensitive area",
		State:         supervisor.TaskBlocked,
		WorkerClass:   supervisor.WorkerCoder,
		BlockedReason: "retries_exhausted",
		Error:         "transient failure after 2 attempts",
		Attempts:      2,
		StartedAt:     start,
	}
	todo := TaskToDriveTodo(task)
	if todo.BlockedReason != drive.BlockReasonRetriesExhausted {
		t.Fatalf("expected BlockedReason=retries_exhausted, got: %q", todo.BlockedReason)
	}
	back := TaskFromDriveTodo(todo)
	if back.BlockedReason != "retries_exhausted" {
		t.Fatalf("expected blocked_reason=retries_exhausted after roundtrip, got: %q", back.BlockedReason)
	}
	if back.Error != task.Error {
		t.Fatalf("error lost after roundtrip: got %q, want %q", back.Error, task.Error)
	}
}

func TestRunFromDriveProjectsTodosIntoSupervisorTasks(t *testing.T) {
	run := &drive.Run{
		ID:     "drv-1",
		Task:   "ship auth hardening",
		Status: drive.RunRunning,
		Todos: []drive.Todo{
			{ID: "T1", Title: "survey", WorkerClass: "researcher", Verification: "light", Status: drive.TodoDone},
			{ID: "T2", Title: "fix", WorkerClass: "coder", Verification: "required", Status: drive.TodoPending},
		},
	}

	projected := RunFromDrive(run)
	if len(projected.Tasks) != 2 {
		t.Fatalf("expected 2 projected tasks, got %d", len(projected.Tasks))
	}
	if projected.Tasks[0].WorkerClass != supervisor.WorkerResearcher {
		t.Fatalf("unexpected worker_class: %+v", projected.Tasks[0])
	}
	if projected.Tasks[1].State != supervisor.TaskPending {
		t.Fatalf("unexpected task state: %+v", projected.Tasks[1])
	}
}
