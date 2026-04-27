package tools

import (
	"context"
	"testing"

	"go.etcd.io/bbolt"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/supervisor"
	"github.com/dontfuckmycode/dfmc/internal/taskstore"
)

// TestTodoStatusToTaskState pins the alias table so a new alias never
// breaks the filter match silently. The mapping is the only thing
// between the LLM's "in_progress"/"completed" vocabulary and the
// taskstore's canonical "running"/"done".
func TestTodoStatusToTaskState(t *testing.T) {
	cases := []struct {
		in   string
		want supervisor.TaskState
	}{
		{"", supervisor.TaskPending},
		{"pending", supervisor.TaskPending},
		{"TODO", supervisor.TaskPending},
		{"in_progress", supervisor.TaskRunning},
		{"IN-PROGRESS", supervisor.TaskRunning},
		{" active ", supervisor.TaskRunning},
		{"doing", supervisor.TaskRunning},
		{"running", supervisor.TaskRunning},
		{"completed", supervisor.TaskDone},
		{"done", supervisor.TaskDone},
		{"finished", supervisor.TaskDone},
		{"blocked", supervisor.TaskBlocked},
		{"skipped", supervisor.TaskSkipped},
		{"garbage-unknown", supervisor.TaskPending}, // default fallthrough
	}
	for _, tc := range cases {
		if got := todoStatusToTaskState(tc.in); got != tc.want {
			t.Errorf("todoStatusToTaskState(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestTodoWritePersistsCanonicalTaskState exercises the end-to-end flow
// that was broken before: todo_write with status="in_progress" used to
// persist state "in_progress" verbatim, which caused
// ListTasks(State:"running") to skip the entry — no /api/v1/task filter
// would ever surface the model's in-flight work.
func TestTodoWritePersistsCanonicalTaskState(t *testing.T) {
	tmp := t.TempDir() + "/todo_state.db"
	db, err := bbolt.Open(tmp, 0600, &bbolt.Options{})
	if err != nil {
		t.Fatalf("bbolt.Open: %v", err)
	}
	defer db.Close()
	store := taskstore.NewStore(db)

	eng := New(*config.DefaultConfig())
	eng.SetTaskStore(store)

	_, err = eng.Execute(context.Background(), "todo_write", Request{
		Params: map[string]any{
			"action": "set",
			"todos": []any{
				map[string]any{"content": "wire router", "status": "in_progress"},
				map[string]any{"content": "write tests", "status": "pending"},
				map[string]any{"content": "ship", "status": "completed"},
			},
		},
	})
	if err != nil {
		t.Fatalf("todo_write set: %v", err)
	}

	running, err := store.ListTasks(taskstore.ListOptions{State: string(supervisor.TaskRunning)})
	if err != nil {
		t.Fatalf("ListTasks running: %v", err)
	}
	if len(running) != 1 || running[0].Title != "wire router" {
		t.Fatalf("expected 1 running task titled 'wire router', got %+v", running)
	}

	done, err := store.ListTasks(taskstore.ListOptions{State: string(supervisor.TaskDone)})
	if err != nil {
		t.Fatalf("ListTasks done: %v", err)
	}
	if len(done) != 1 || done[0].Title != "ship" {
		t.Fatalf("expected 1 done task titled 'ship', got %+v", done)
	}

	pending, err := store.ListTasks(taskstore.ListOptions{State: string(supervisor.TaskPending)})
	if err != nil {
		t.Fatalf("ListTasks pending: %v", err)
	}
	if len(pending) != 1 || pending[0].Title != "write tests" {
		t.Fatalf("expected 1 pending task titled 'write tests', got %+v", pending)
	}
}

// TestTodoWriteRoundTripsLLMVocabulary verifies the reverse mapping:
// items returned from a list call after a store-backed set should echo
// the LLM-friendly vocabulary ("in_progress"/"completed"), not the
// canonical supervisor states ("running"/"done"). Keeps the tool's
// documented API stable even though internal storage is canonical.
func TestTodoWriteRoundTripsLLMVocabulary(t *testing.T) {
	tmp := t.TempDir() + "/todo_roundtrip.db"
	db, err := bbolt.Open(tmp, 0600, &bbolt.Options{})
	if err != nil {
		t.Fatalf("bbolt.Open: %v", err)
	}
	defer db.Close()
	store := taskstore.NewStore(db)

	eng := New(*config.DefaultConfig())
	eng.SetTaskStore(store)

	_, err = eng.Execute(context.Background(), "todo_write", Request{
		Params: map[string]any{
			"action": "set",
			"todos": []any{
				map[string]any{"content": "wire router", "status": "in_progress"},
				map[string]any{"content": "ship", "status": "completed"},
			},
		},
	})
	if err != nil {
		t.Fatalf("todo_write set: %v", err)
	}

	res, err := eng.Execute(context.Background(), "todo_write", Request{
		Params: map[string]any{"action": "list"},
	})
	if err != nil {
		t.Fatalf("todo_write list: %v", err)
	}
	raw, ok := res.Data["items"].([]TodoItem)
	if !ok {
		t.Fatalf("items not of expected type: %T", res.Data["items"])
	}
	statuses := map[string]string{}
	for _, it := range raw {
		statuses[it.Content] = it.Status
	}
	if statuses["wire router"] != "in_progress" {
		t.Errorf("round-trip status for 'wire router' = %q, want 'in_progress'", statuses["wire router"])
	}
	if statuses["ship"] != "completed" {
		t.Errorf("round-trip status for 'ship' = %q, want 'completed'", statuses["ship"])
	}
}
