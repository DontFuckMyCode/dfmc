package taskstore

import (
	"testing"
	"time"

	"go.etcd.io/bbolt"

	"github.com/dontfuckmycode/dfmc/internal/supervisor"
)

func tempDB(t *testing.T) *bbolt.DB {
	tmp := t.TempDir() + "/taskstore_test.db"
	db, err := bbolt.Open(tmp, 0600, &bbolt.Options{})
	if err != nil {
		t.Fatalf("bbolt.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestSaveAndLoad(t *testing.T) {
	db := tempDB(t)
	s := NewStore(db)

	task := &supervisor.Task{
		ID:       "tsk-test-1",
		ParentID: "",
		Origin:   "supervisor",
		Title:    "Implement auth module",
		Detail:   "Add refresh token rotation to the auth service.",
		State:    supervisor.TaskRunning,
		WorkerClass: supervisor.WorkerCoder,
		Labels:   []string{"auth", "security"},
		StartedAt: time.Now(),
	}

	if err := s.SaveTask(task); err != nil {
		t.Fatalf("SaveTask: %v", err)
	}

	loaded, err := s.LoadTask("tsk-test-1")
	if err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected non-nil task")
	}
	if loaded.Title != task.Title {
		t.Fatalf("title: got %q, want %q", loaded.Title, task.Title)
	}
	if loaded.State != supervisor.TaskRunning {
		t.Fatalf("state: got %q, want %q", loaded.State, supervisor.TaskRunning)
	}
	if len(loaded.Labels) != 2 {
		t.Fatalf("labels count: got %d, want 2", len(loaded.Labels))
	}
}

func TestLoadNotFound(t *testing.T) {
	db := tempDB(t)
	s := NewStore(db)

	loaded, err := s.LoadTask("does-not-exist")
	if err != nil {
		t.Fatalf("LoadTask: unexpected error: %v", err)
	}
	if loaded != nil {
		t.Fatalf("expected nil for missing task, got %+v", loaded)
	}
}

func TestUpdateTask(t *testing.T) {
	db := tempDB(t)
	s := NewStore(db)

	s.SaveTask(&supervisor.Task{ID: "tsk-upd-1", Title: "original", State: supervisor.TaskPending})

	err := s.UpdateTask("tsk-upd-1", func(t *supervisor.Task) error {
		t.State = supervisor.TaskDone
		t.Summary = "all good"
		return nil
	})
	if err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}

	updated, _ := s.LoadTask("tsk-upd-1")
	if updated.State != supervisor.TaskDone {
		t.Fatalf("state: got %q, want done", updated.State)
	}
	if updated.Summary != "all good" {
		t.Fatalf("summary: got %q, want %q", updated.Summary, "all good")
	}
}

func TestUpdateTaskNotFound(t *testing.T) {
	db := tempDB(t)
	s := NewStore(db)

	err := s.UpdateTask("ghost", func(t *supervisor.Task) error {
		t.State = supervisor.TaskDone
		return nil
	})
	if err == nil {
		t.Fatal("expected error for missing task")
	}
}

func TestDeleteTask(t *testing.T) {
	db := tempDB(t)
	s := NewStore(db)

	s.SaveTask(&supervisor.Task{ID: "tsk-del-1", Title: "to delete"})
	if err := s.DeleteTask("tsk-del-1"); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}
	if got, _ := s.LoadTask("tsk-del-1"); got != nil {
		t.Fatal("expected nil after delete")
	}
	if err := s.DeleteTask("tsk-del-1"); err != nil {
		t.Fatal("delete should be idempotent")
	}
}

func TestListTasks(t *testing.T) {
	db := tempDB(t)
	s := NewStore(db)

	now := time.Now()
	for i := 0; i < 5; i++ {
		s.SaveTask(&supervisor.Task{
			ID:        "tsk-list-" + string(rune('a'+i)),
			Title:     "task",
			State:     supervisor.TaskPending,
			StartedAt: now.Add(time.Duration(-i) * time.Minute),
		})
	}

	list, err := s.ListTasks(ListOptions{})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(list) != 5 {
		t.Fatalf("got %d tasks, want 5", len(list))
	}

	// newest-first
	if list[0].StartedAt.Before(list[1].StartedAt) {
		t.Fatal("expected newest-first order")
	}
}

func TestListTasksFilters(t *testing.T) {
	db := tempDB(t)
	s := NewStore(db)

	parent := "tsk-parent-x"
	s.SaveTask(&supervisor.Task{ID: "tsk-c1", ParentID: parent, State: supervisor.TaskDone})
	s.SaveTask(&supervisor.Task{ID: "tsk-c2", ParentID: parent, State: supervisor.TaskPending})
	s.SaveTask(&supervisor.Task{ID: "tsk-c3", ParentID: "other-parent", State: supervisor.TaskDone})

	children, err := s.ListTasks(ListOptions{ParentID: parent})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(children) != 2 {
		t.Fatalf("got %d children, want 2", len(children))
	}

	done, err := s.ListTasks(ListOptions{State: string(supervisor.TaskDone)})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(done) != 2 {
		t.Fatalf("got %d done tasks, want 2", len(done))
	}
}

func TestListChildren(t *testing.T) {
	db := tempDB(t)
	s := NewStore(db)

	parent := "tsk-parent-y"
	s.SaveTask(&supervisor.Task{ID: "tsk-child-1", ParentID: parent})
	s.SaveTask(&supervisor.Task{ID: "tsk-child-2", ParentID: parent})
	s.SaveTask(&supervisor.Task{ID: "tsk-other"})

	children, err := s.ListChildren(parent)
	if err != nil {
		t.Fatalf("ListChildren: %v", err)
	}
	if len(children) != 2 {
		t.Fatalf("got %d children, want 2", len(children))
	}
}

func TestListTasksLimitOffset(t *testing.T) {
	db := tempDB(t)
	s := NewStore(db)

	for i := 0; i < 10; i++ {
		s.SaveTask(&supervisor.Task{
			ID:        "tsk-limit-" + string(rune('0'+i)),
			State:     supervisor.TaskPending,
			StartedAt: time.Now(),
		})
	}

	list, err := s.ListTasks(ListOptions{Limit: 3})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("got %d, want 3", len(list))
	}

	list2, err := s.ListTasks(ListOptions{Limit: 3, Offset: 3})
	if err != nil {
		t.Fatalf("ListTasks offset: %v", err)
	}
	if len(list2) != 3 {
		t.Fatalf("got %d, want 3", len(list2))
	}
}
