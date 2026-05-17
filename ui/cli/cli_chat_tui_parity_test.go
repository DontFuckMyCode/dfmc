package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/supervisor"
)

func TestSlashTasksListAndShowMirrorTUIFormatter(t *testing.T) {
	eng := newCLITestEngine(t)
	store := eng.Tools.TaskStore()
	if store == nil {
		t.Fatal("task store should be initialized")
	}
	task := &supervisor.Task{
		ID:           "task-cli-parity",
		Title:        "Patch CLI task parity",
		Detail:       "Exercise the same inline task view used by the TUI.",
		State:        supervisor.TaskRunning,
		WorkerClass:  supervisor.WorkerCoder,
		Verification: supervisor.VerifyLight,
		StartedAt:    time.Now(),
	}
	if err := store.SaveTask(task); err != nil {
		t.Fatalf("SaveTask: %v", err)
	}

	listOut := captureStdout(t, func() {
		exit, handled := runChatSlash(context.Background(), eng, "/tasks list")
		if exit || !handled {
			t.Fatalf("/tasks list exit=%t handled=%t", exit, handled)
		}
	})
	if !strings.Contains(listOut, "Patch CLI task parity") {
		t.Fatalf("/tasks list should include task title, got:\n%s", listOut)
	}
	if strings.Contains(listOut, "TUI-only") {
		t.Fatalf("/tasks list should be a real CLI view, got:\n%s", listOut)
	}

	showOut := captureStdout(t, func() {
		exit, handled := runChatSlash(context.Background(), eng, "/tasks show task-cli-parity")
		if exit || !handled {
			t.Fatalf("/tasks show exit=%t handled=%t", exit, handled)
		}
	})
	for _, want := range []string{"Patch CLI task parity", "detail:", "worker:", "verify:"} {
		if !strings.Contains(showOut, want) {
			t.Fatalf("/tasks show missing %q, got:\n%s", want, showOut)
		}
	}
}

func TestSlashTasksClearKeepsDriveOwnedTasks(t *testing.T) {
	eng := newCLITestEngine(t)
	store := eng.Tools.TaskStore()
	if store == nil {
		t.Fatal("task store should be initialized")
	}
	if err := store.SaveTask(&supervisor.Task{ID: "local-task", Title: "Local task", State: supervisor.TaskPending}); err != nil {
		t.Fatalf("SaveTask local: %v", err)
	}
	if err := store.SaveTask(&supervisor.Task{ID: "drive-task", Title: "Drive task", State: supervisor.TaskPending, RunID: "run-1"}); err != nil {
		t.Fatalf("SaveTask drive: %v", err)
	}

	out := captureStdout(t, func() {
		exit, handled := runChatSlash(context.Background(), eng, "/tasks clear")
		if exit || !handled {
			t.Fatalf("/tasks clear exit=%t handled=%t", exit, handled)
		}
	})
	for _, want := range []string{"Cleared 1 task", "1 drive-owned task"} {
		if !strings.Contains(out, want) {
			t.Fatalf("/tasks clear missing %q, got:\n%s", want, out)
		}
	}
	if got, err := store.LoadTask("local-task"); err != nil || got != nil {
		t.Fatalf("local task should be deleted, got=%#v err=%v", got, err)
	}
	if got, err := store.LoadTask("drive-task"); err != nil || got == nil {
		t.Fatalf("drive task should remain, got=%#v err=%v", got, err)
	}
}
