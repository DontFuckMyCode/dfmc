package drive

import (
	"path/filepath"
	"testing"
	"time"

	"go.etcd.io/bbolt"
)

func openTestDB(t *testing.T) *bbolt.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := bbolt.Open(filepath.Join(dir, "drive.db"), 0o600, nil)
	if err != nil {
		t.Fatalf("open bbolt: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestStoreSaveLoadRoundTrip(t *testing.T) {
	db := openTestDB(t)
	store, err := NewStore(db)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	run := &Run{
		ID:        "drv-test-1",
		Task:      "do something useful",
		Status:    RunRunning,
		CreatedAt: time.Now(),
		Todos: []Todo{
			{ID: "T1", Title: "first", Detail: "do first", Status: TodoDone, Brief: "did first"},
			{ID: "T2", Title: "second", Detail: "do second", Status: TodoPending, DependsOn: []string{"T1"}},
		},
	}
	if err := store.Save(run); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := store.Load("drv-test-1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded == nil {
		t.Fatal("Load returned nil for stored ID")
	}
	if loaded.Task != run.Task {
		t.Fatalf("Task mismatch: %q vs %q", loaded.Task, run.Task)
	}
	if len(loaded.Todos) != 2 {
		t.Fatalf("expected 2 todos, got %d", len(loaded.Todos))
	}
	if loaded.Todos[0].Brief != "did first" {
		t.Fatalf("brief lost in round-trip: %q", loaded.Todos[0].Brief)
	}
	if loaded.Todos[1].Status != TodoPending {
		t.Fatalf("status lost: %s", loaded.Todos[1].Status)
	}
}

func TestStoreLoadMissReturnsNilNoError(t *testing.T) {
	db := openTestDB(t)
	store, _ := NewStore(db)
	got, err := store.Load("nope")
	if err != nil {
		t.Fatalf("unexpected error on miss: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil on miss, got %+v", got)
	}
}

func TestStoreListSortsNewestFirst(t *testing.T) {
	db := openTestDB(t)
	store, _ := NewStore(db)
	now := time.Now()
	older := &Run{ID: "drv-old", Task: "old", Status: RunDone, CreatedAt: now.Add(-time.Hour)}
	newer := &Run{ID: "drv-new", Task: "new", Status: RunRunning, CreatedAt: now}
	if err := store.Save(older); err != nil {
		t.Fatalf("save older: %v", err)
	}
	if err := store.Save(newer); err != nil {
		t.Fatalf("save newer: %v", err)
	}
	list, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 runs, got %d", len(list))
	}
	if list[0].ID != "drv-new" {
		t.Fatalf("newest must be first, got %s", list[0].ID)
	}
}

func TestStoreDeleteRemovesRun(t *testing.T) {
	db := openTestDB(t)
	store, _ := NewStore(db)
	run := &Run{ID: "drv-del", Task: "x", Status: RunDone, CreatedAt: time.Now()}
	_ = store.Save(run)
	if err := store.Delete("drv-del"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	got, _ := store.Load("drv-del")
	if got != nil {
		t.Fatalf("expected nil after delete, got %+v", got)
	}
	// Idempotent: deleting again is a no-op.
	if err := store.Delete("drv-del"); err != nil {
		t.Fatalf("second Delete must be no-op, got: %v", err)
	}
}
