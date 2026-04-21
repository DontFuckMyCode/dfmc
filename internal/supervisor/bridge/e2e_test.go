package bridge

import (
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/drive"
	"github.com/dontfuckmycode/dfmc/internal/supervisor"
)

// TestFullSupervisorRunThroughBridge exercises a supervisor.Run through
// the full bridge: Run → RunToDrive → RunFromDrive, verifying that
// every field (summary, state, confidence, attempts, blocked reason,
// file scope, labels, skills) round-trips correctly.
func TestFullSupervisorRunThroughBridge(t *testing.T) {
	createdAt := time.Now().Add(-time.Hour).Round(time.Second)
	run := supervisor.Run{
		ID:        "drv-bridge-e2e",
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
				Attempts:     0,
				StartedAt:    createdAt,
				EndedAt:      createdAt.Add(5 * time.Minute),
			},
			{
				ID:            "T2",
				Title:         "Patch auth flow",
				State:         supervisor.TaskBlocked,
				DependsOn:     []string{"T1"},
				WorkerClass:   supervisor.WorkerCoder,
				ProviderTag:   "code",
				Verification:  supervisor.VerifyRequired,
				FileScope:     []string{"internal/auth/service.go"},
				ReadOnly:      false,
				Skills:        []string{"debug"},
				AllowedTools:  []string{"read_file", "edit_file"},
				Labels:        []string{"critical"},
				Confidence:    0.6,
				BlockedReason: "retries_exhausted",
				Error:         "context window exceeded",
				Attempts:      3,
				StartedAt:     createdAt.Add(6 * time.Minute),
				EndedAt:       createdAt.Add(15 * time.Minute),
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
				Attempts:     0,
			},
		},
	}

	driveRun := RunToDrive(run)
	if driveRun.ID != run.ID {
		t.Fatalf("run ID lost: want %q, got %q", run.ID, driveRun.ID)
	}
	if len(driveRun.Todos) != len(run.Tasks) {
		t.Fatalf("todo count mismatch: want %d, got %d", len(run.Tasks), len(driveRun.Todos))
	}

	// T1: done task with summary.
	t1 := driveRun.Todos[0]
	if t1.ID != "T1" {
		t.Fatalf("t1 ordering wrong")
	}
	if t1.Status != drive.TodoDone {
		t.Fatalf("T1 state: want TodoDone, got %v", t1.Status)
	}
	if t1.Brief != "found 3 entry points" {
		t.Fatalf("T1 summary lost: want %q, got %q", "found 3 entry points", t1.Brief)
	}
	if t1.Confidence != 0.9 {
		t.Fatalf("T1 confidence lost: want 0.9, got %v", t1.Confidence)
	}

	// T2: blocked task with retries_exhausted and error.
	t2 := driveRun.Todos[1]
	if t2.ID != "T2" {
		t.Fatalf("t2 ordering wrong")
	}
	if t2.Status != drive.TodoBlocked {
		t.Fatalf("T2 state: want TodoBlocked, got %v", t2.Status)
	}
	if t2.BlockedReason != drive.BlockReasonRetriesExhausted {
		t.Fatalf("T2 blocked reason: want retries_exhausted, got %v", t2.BlockedReason)
	}
	if t2.Error != "context window exceeded" {
		t.Fatalf("T2 error lost: want %q, got %q", "context window exceeded", t2.Error)
	}
	if t2.Attempts != 3 {
		t.Fatalf("T2 attempts lost: want 3, got %d", t2.Attempts)
	}
	if t2.ReadOnly {
		t.Fatalf("T2 should not be read-only (it has file edits)")
	}
	if len(t2.FileScope) != 1 || t2.FileScope[0] != "internal/auth/service.go" {
		t.Fatalf("T2 file scope lost: got %v", t2.FileScope)
	}

	// T3: pending reviewer task.
	t3 := driveRun.Todos[2]
	if t3.Status != drive.TodoPending {
		t.Fatalf("T3 state: want TodoPending, got %v", t3.Status)
	}
	if t3.ProviderTag != "review" {
		t.Fatalf("T3 provider_tag lost: want review, got %q", t3.ProviderTag)
	}
	if t3.WorkerClass != "reviewer" {
		t.Fatalf("T3 worker_class lost: want reviewer, got %q", t3.WorkerClass)
	}

	// Round-trip back.
	back := RunFromDrive(driveRun)
	if len(back.Tasks) != len(run.Tasks) {
		t.Fatalf("round-trip task count: want %d, got %d", len(run.Tasks), len(back.Tasks))
	}
	for _, bt := range back.Tasks {
		switch bt.ID {
		case "T1":
			if bt.Summary != "found 3 entry points" {
				t.Fatalf("T1 summary lost in round-trip: want %q, got %q", "found 3 entry points", bt.Summary)
			}
			if bt.State != supervisor.TaskDone {
				t.Fatalf("T1 state lost in round-trip: want TaskDone, got %v", bt.State)
			}
			if bt.Confidence != 0.9 {
				t.Fatalf("T1 confidence lost: want 0.9, got %v", bt.Confidence)
			}
		case "T2":
			if bt.BlockedReason != "retries_exhausted" {
				t.Fatalf("T2 blocked reason lost in round-trip: want %q, got %q", "retries_exhausted", bt.BlockedReason)
			}
			if bt.Error != "context window exceeded" {
				t.Fatalf("T2 error lost in round-trip: want %q, got %q", "context window exceeded", bt.Error)
			}
			if bt.Attempts != 3 {
				t.Fatalf("T2 attempts lost in round-trip: want 3, got %d", bt.Attempts)
			}
			if bt.WorkerClass != supervisor.WorkerCoder {
				t.Fatalf("T2 worker class lost: want coder, got %v", bt.WorkerClass)
			}
		case "T3":
			if bt.Verification != supervisor.VerifyDeep {
				t.Fatalf("T3 verification lost: want deep, got %v", bt.Verification)
			}
			if bt.ReadOnly != true {
				t.Fatalf("T3 read_only lost: want true, got %v", bt.ReadOnly)
			}
		}
	}
}

// TestTaskToDriveTodoSkippedState verifies that the skipped task state
// maps correctly through the bridge.
func TestTaskToDriveTodoSkippedState(t *testing.T) {
	task := supervisor.Task{
		ID:           "T1",
		Title:        "Superseded by parent patch",
		State:        supervisor.TaskSkipped,
		WorkerClass:  supervisor.WorkerCoder,
		ProviderTag:  "code",
		Verification: supervisor.VerifyRequired,
		AllowedTools: []string{"read_file", "edit_file"},
		ReadOnly:     false,
		Skills:       []string{"debug"},
		Labels:       []string{"superseded"},
		Confidence:   0.5,
	}
	todo := TaskToDriveTodo(task)
	if todo.Status != drive.TodoSkipped {
		t.Fatalf("skipped state not mapped: want TodoSkipped, got %v", todo.Status)
	}
}

// TestRunToDrivePreservesRunMetadata verifies run-level fields (Reason,
// CreatedAt, EndedAt) survive the bridge crossing.
func TestRunToDrivePreservesRunMetadata(t *testing.T) {
	before := time.Now().Add(-2 * time.Hour).Round(time.Second)
	after := time.Now().Add(-1 * time.Hour).Round(time.Second)
	run := supervisor.Run{
		ID:        "drv-meta",
		Task:      "ship it",
		Status:    "done",
		Reason:    "all todos terminal",
		CreatedAt: before,
		EndedAt:   after,
		Tasks: []supervisor.Task{
			{ID: "T1", Title: "done", State: supervisor.TaskDone},
		},
	}
	driveRun := RunToDrive(run)
	if driveRun.Reason != "all todos terminal" {
		t.Fatalf("reason lost: want %q, got %q", "all todos terminal", driveRun.Reason)
	}
	if !driveRun.CreatedAt.Equal(before) {
		t.Fatalf("CreatedAt lost: want %v, got %v", before, driveRun.CreatedAt)
	}
	if !driveRun.EndedAt.Equal(after) {
		t.Fatalf("EndedAt lost: want %v, got %v", after, driveRun.EndedAt)
	}
	back := RunFromDrive(driveRun)
	if back.Reason != "all todos terminal" {
		t.Fatalf("reason lost in round-trip: want %q, got %q", "all todos terminal", back.Reason)
	}
}

// TestTaskToDriveTodoNormalizesWhitespace verifies that provider_tag and
// blocked_reason are trimmed of surrounding whitespace in both directions.
func TestTaskToDriveTodoNormalizesWhitespace(t *testing.T) {
	task := supervisor.Task{
		ID:            " T2 ",
		Title:         " Patch ",
		State:         supervisor.TaskBlocked,
		WorkerClass:   supervisor.WorkerCoder,
		ProviderTag:   " code ",
		BlockedReason: " retries_exhausted ",
		Error:         " boom ",
		Attempts:      2,
	}
	todo := TaskToDriveTodo(task)
	if todo.ID != "T2" {
		t.Fatalf("ID should be trimmed: got %q", todo.ID)
	}
	if todo.ProviderTag != "code" {
		t.Fatalf("ProviderTag should be trimmed: got %q", todo.ProviderTag)
	}
	if todo.BlockedReason != drive.BlockReasonRetriesExhausted {
		t.Fatalf("BlockedReason should be trimmed: got %v", todo.BlockedReason)
	}

	back := TaskFromDriveTodo(todo)
	if back.BlockedReason != "retries_exhausted" {
		t.Fatalf("blocked reason should be trimmed after round-trip: got %q", back.BlockedReason)
	}
	if back.ProviderTag != "code" {
		t.Fatalf("provider tag should be trimmed after round-trip: got %q", back.ProviderTag)
	}
}
