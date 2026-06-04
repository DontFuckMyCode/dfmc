package taskstore

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/dontfuckmycode/dfmc/internal/supervisor"
)

func tempDB(t *testing.T) *sql.DB {
	tmp := t.TempDir() + "/taskstore_test.db"
	db, err := sql.Open("sqlite", tmp+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	// Create the tasks table
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS "tasks" (key TEXT PRIMARY KEY, value BLOB)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestSaveAndLoad(t *testing.T) {
	db := tempDB(t)
	s := NewStore(db)

	task := &supervisor.Task{
		ID:          "tsk-test-1",
		ParentID:    "",
		Origin:      "supervisor",
		Title:       "Implement auth module",
		Detail:      "Add refresh token rotation to the auth service.",
		State:       supervisor.TaskRunning,
		WorkerClass: supervisor.WorkerCoder,
		Labels:      []string{"auth", "security"},
		StartedAt:   time.Now(),
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

	_ = s.SaveTask(&supervisor.Task{ID: "tsk-upd-1", Title: "original", State: supervisor.TaskPending})

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

// TestUpdateTaskFnErrorIsNotPersisted regresses the contract that a
// mutator returning an error must NOT leave a partial write on disk.
// With the old two-transaction implementation a slow mutator could be
// preempted between LoadTask and SaveTask; the single-SQLite-transaction
// rewrite makes this trivially atomic.
func TestUpdateTaskFnErrorIsNotPersisted(t *testing.T) {
	db := tempDB(t)
	s := NewStore(db)
	_ = s.SaveTask(&supervisor.Task{ID: "tsk-rb-1", Title: "before", State: supervisor.TaskPending})

	wantErr := errors.New("intentional rollback")
	err := s.UpdateTask("tsk-rb-1", func(t *supervisor.Task) error {
		t.Title = "DIRTY"
		t.State = supervisor.TaskDone
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("UpdateTask: got %v, want %v", err, wantErr)
	}
	got, _ := s.LoadTask("tsk-rb-1")
	if got.Title != "before" || got.State != supervisor.TaskPending {
		t.Fatalf("partial write leaked: %+v", got)
	}
}

func TestUpdateTaskRejectsBadInputs(t *testing.T) {
	db := tempDB(t)
	s := NewStore(db)
	if err := s.UpdateTask("", func(t *supervisor.Task) error { return nil }); err == nil {
		t.Fatal("expected error for empty id")
	}
	if err := s.UpdateTask("any", nil); err == nil {
		t.Fatal("expected error for nil fn")
	}
}

// TestUpdateTaskBumpsVersion pins that every successful mutation
// increments the persisted Version field — UpdateTaskCAS callers rely
// on this to detect concurrent writers.
func TestUpdateTaskBumpsVersion(t *testing.T) {
	db := tempDB(t)
	s := NewStore(db)
	_ = s.SaveTask(&supervisor.Task{ID: "tsk-ver-1", Title: "v0", State: supervisor.TaskPending})

	for i := 1; i <= 3; i++ {
		err := s.UpdateTask("tsk-ver-1", func(t *supervisor.Task) error {
			t.Title = "v" + string(rune('0'+i))
			return nil
		})
		if err != nil {
			t.Fatalf("UpdateTask iter %d: %v", i, err)
		}
		got, _ := s.LoadTask("tsk-ver-1")
		if got.Version != i {
			t.Fatalf("after %d updates Version=%d, want %d", i, got.Version, i)
		}
	}
}

// TestUpdateTaskCAS_DetectsConcurrentWriter pins the optimistic
// concurrency contract: a CAS update with a stale expected version
// must return ErrTaskVersionConflict and leave the stored task
// untouched.
func TestUpdateTaskCAS_DetectsConcurrentWriter(t *testing.T) {
	db := tempDB(t)
	s := NewStore(db)
	_ = s.SaveTask(&supervisor.Task{ID: "tsk-cas-1", Title: "fresh", State: supervisor.TaskPending})

	// First update by an "other" writer bumps Version 0 → 1.
	if err := s.UpdateTask("tsk-cas-1", func(t *supervisor.Task) error {
		t.Title = "by-other"
		return nil
	}); err != nil {
		t.Fatalf("UpdateTask (other): %v", err)
	}

	// Our caller still holds expectedVersion=0 from a prior LoadTask.
	err := s.UpdateTaskCAS("tsk-cas-1", 0, func(t *supervisor.Task) error {
		t.Title = "by-stale-caller"
		return nil
	})
	if !errors.Is(err, ErrTaskVersionConflict) {
		t.Fatalf("expected ErrTaskVersionConflict, got %v", err)
	}

	// Stored task must still reflect the other writer's value.
	got, _ := s.LoadTask("tsk-cas-1")
	if got.Title != "by-other" {
		t.Fatalf("stale CAS leaked through: title=%q", got.Title)
	}
	if got.Version != 1 {
		t.Fatalf("Version not preserved: got %d, want 1", got.Version)
	}
}

// TestUpdateTaskCAS_SucceedsWithFreshVersion pins the happy path: a
// CAS update with the current observed version succeeds and bumps
// Version by exactly one.
func TestUpdateTaskCAS_SucceedsWithFreshVersion(t *testing.T) {
	db := tempDB(t)
	s := NewStore(db)
	_ = s.SaveTask(&supervisor.Task{ID: "tsk-cas-2", Title: "fresh"})

	// Bump version once via UpdateTask so we're at v=1.
	_ = s.UpdateTask("tsk-cas-2", func(t *supervisor.Task) error {
		t.State = supervisor.TaskRunning
		return nil
	})

	// Caller observed v=1, then CASes with that.
	err := s.UpdateTaskCAS("tsk-cas-2", 1, func(t *supervisor.Task) error {
		t.State = supervisor.TaskDone
		return nil
	})
	if err != nil {
		t.Fatalf("UpdateTaskCAS happy path: %v", err)
	}
	got, _ := s.LoadTask("tsk-cas-2")
	if got.State != supervisor.TaskDone {
		t.Errorf("CAS update did not apply: state=%q", got.State)
	}
	if got.Version != 2 {
		t.Errorf("Version=%d, want 2", got.Version)
	}
}

func TestDeleteTask(t *testing.T) {
	db := tempDB(t)
	s := NewStore(db)

	_ = s.SaveTask(&supervisor.Task{ID: "tsk-del-1", Title: "to delete"})
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
		_ = s.SaveTask(&supervisor.Task{
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
	_ = s.SaveTask(&supervisor.Task{ID: "tsk-c1", ParentID: parent, State: supervisor.TaskDone})
	_ = s.SaveTask(&supervisor.Task{ID: "tsk-c2", ParentID: parent, State: supervisor.TaskPending})
	_ = s.SaveTask(&supervisor.Task{ID: "tsk-c3", ParentID: "other-parent", State: supervisor.TaskDone})

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
	_ = s.SaveTask(&supervisor.Task{ID: "tsk-child-1", ParentID: parent})
	_ = s.SaveTask(&supervisor.Task{ID: "tsk-child-2", ParentID: parent})
	_ = s.SaveTask(&supervisor.Task{ID: "tsk-other"})

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
		_ = s.SaveTask(&supervisor.Task{
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

	// Past-the-end offset must return an empty page, NOT the whole list.
	// Regression: the old guard turned an out-of-range offset into a
	// no-op, leaking every task as the "next" page.
	beyond, err := s.ListTasks(ListOptions{Offset: 60})
	if err != nil {
		t.Fatalf("ListTasks past-end offset: %v", err)
	}
	if len(beyond) != 0 {
		t.Fatalf("offset past the end must yield 0 tasks, got %d", len(beyond))
	}
	// Offset exactly at len is also past-the-end.
	atEnd, err := s.ListTasks(ListOptions{Offset: 10})
	if err != nil {
		t.Fatalf("ListTasks offset==len: %v", err)
	}
	if len(atEnd) != 0 {
		t.Fatalf("offset == len must yield 0 tasks, got %d", len(atEnd))
	}
}

// --- SaveTask error paths ---

func TestSaveTask_NilTask(t *testing.T) {
	db := tempDB(t)
	s := NewStore(db)
	err := s.SaveTask(nil)
	if err == nil {
		t.Fatal("expected error for nil task")
	}
}

func TestSaveTask_EmptyID(t *testing.T) {
	db := tempDB(t)
	s := NewStore(db)
	err := s.SaveTask(&supervisor.Task{ID: "", Title: "no id"})
	if err == nil {
		t.Fatal("expected error for empty ID")
	}
}

// --- LoadTask error paths ---

func TestLoadTask_EmptyID(t *testing.T) {
	db := tempDB(t)
	s := NewStore(db)
	task, err := s.LoadTask("")
	if err == nil {
		t.Fatal("expected error for empty ID")
	}
	if task != nil {
		t.Errorf("expected nil task, got %v", task)
	}
}

func TestLoadTask_NotFound(t *testing.T) {
	db := tempDB(t)
	s := NewStore(db)
	task, err := s.LoadTask("does-not-exist")
	if err != nil {
		t.Fatalf("LoadTask not-found should not error: %v", err)
	}
	if task != nil {
		t.Errorf("expected nil for missing task, got %v", task)
	}
}

// --- UpdateTaskCAS error paths ---

func TestUpdateTaskCAS_NegativeVersion(t *testing.T) {
	db := tempDB(t)
	s := NewStore(db)
	_ = s.SaveTask(&supervisor.Task{ID: "tsk-cas-neg", Title: "test"})
	err := s.UpdateTaskCAS("tsk-cas-neg", -1, func(t *supervisor.Task) error {
		t.Title = "updated"
		return nil
	})
	if err == nil {
		t.Fatal("expected error for negative expectedVersion")
	}
}

func TestUpdateTaskCAS_VersionMismatch(t *testing.T) {
	db := tempDB(t)
	s := NewStore(db)
	_ = s.SaveTask(&supervisor.Task{ID: "tsk-cas-mismatch", Title: "test"})

	// UpdateTask bumps version from 0 to 1
	_ = s.UpdateTask("tsk-cas-mismatch", func(t *supervisor.Task) error {
		t.State = supervisor.TaskRunning
		return nil
	})

	// Caller observed v=0 (stale), try CAS with it
	err := s.UpdateTaskCAS("tsk-cas-mismatch", 0, func(t *supervisor.Task) error {
		t.Title = "stale-update"
		return nil
	})
	if err == nil {
		t.Fatal("expected ErrTaskVersionConflict for stale version")
	}
	if !errors.Is(err, ErrTaskVersionConflict) {
		t.Errorf("expected ErrTaskVersionConflict, got %v", err)
	}
}

func TestUpdateTaskCAS_FnReturnsError(t *testing.T) {
	db := tempDB(t)
	s := NewStore(db)
	wantErr := errors.New("refuse")
	_ = s.SaveTask(&supervisor.Task{ID: "tsk-cas-fn-err", Title: "test"})
	err := s.UpdateTaskCAS("tsk-cas-fn-err", 0, func(t *supervisor.Task) error {
		return wantErr
	})
	if err == nil {
		t.Fatal("expected error propagated from fn")
	}
}

func TestUpdateTaskCAS_TaskNotFound(t *testing.T) {
	db := tempDB(t)
	s := NewStore(db)
	err := s.UpdateTaskCAS("does-not-exist", 0, func(t *supervisor.Task) error {
		t.Title = "x"
		return nil
	})
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
}

// --- ListTasks filter paths ---

func TestListTasks_RunIDFilter(t *testing.T) {
	db := tempDB(t)
	s := NewStore(db)
	_ = s.SaveTask(&supervisor.Task{ID: "tsk-run-1", RunID: "run-a", Title: "a"})
	_ = s.SaveTask(&supervisor.Task{ID: "tsk-run-2", RunID: "run-b", Title: "b"})
	_ = s.SaveTask(&supervisor.Task{ID: "tsk-run-3", RunID: "run-a", Title: "c"})

	list, err := s.ListTasks(ListOptions{RunID: "run-a"})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("got %d, want 2", len(list))
	}
}

func TestListTasks_LabelFilter(t *testing.T) {
	db := tempDB(t)
	s := NewStore(db)
	_ = s.SaveTask(&supervisor.Task{ID: "tsk-lbl-1", Labels: []string{"auth", "security"}, Title: "a"})
	_ = s.SaveTask(&supervisor.Task{ID: "tsk-lbl-2", Labels: []string{"auth"}, Title: "b"})
	_ = s.SaveTask(&supervisor.Task{ID: "tsk-lbl-3", Labels: []string{"ui"}, Title: "c"})

	list, err := s.ListTasks(ListOptions{Label: "auth"})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("got %d, want 2", len(list))
	}
}

func TestListTasks_LabelNoMatch(t *testing.T) {
	db := tempDB(t)
	s := NewStore(db)
	_ = s.SaveTask(&supervisor.Task{ID: "tsk-lbl-4", Labels: []string{"auth"}, Title: "a"})
	_ = s.SaveTask(&supervisor.Task{ID: "tsk-lbl-5", Labels: []string{"security"}, Title: "b"})

	list, err := s.ListTasks(ListOptions{Label: "ui"})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("got %d, want 0", len(list))
	}
}

func TestListTasks_Empty(t *testing.T) {
	db := tempDB(t)
	s := NewStore(db)
	list, err := s.ListTasks(ListOptions{})
	if err != nil {
		t.Fatalf("ListTasks empty: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("got %d, want 0", len(list))
	}
}
