package drive

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "drive.db")+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	// Create the drive-runs table
	if _, err := db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS "%s" (key TEXT PRIMARY KEY, value BLOB)`, driveBucket)); err != nil {
		t.Fatalf("create table: %v", err)
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

func TestStoreSaveUsesReadableJSONBlob(t *testing.T) {
	db := openTestDB(t)
	store, err := NewStore(db)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	run := &Run{ID: "drv-json", Task: "inspect persistence", Status: RunRunning, CreatedAt: time.Now()}
	if err := store.Save(run); err != nil {
		t.Fatalf("Save: %v", err)
	}
	var raw []byte
	err = db.QueryRow(fmt.Sprintf(`SELECT value FROM "%s" WHERE key = ?`, driveBucket), run.ID).Scan(&raw)
	if err != nil {
		t.Fatalf("read raw blob: %v", err)
	}
	if raw == nil {
		t.Fatalf("missing run blob")
	}
	if !json.Valid(raw) {
		t.Fatalf("drive persistence should remain human-readable JSON, got %q", string(raw))
	}
	if raw[0] != '{' {
		t.Fatalf("expected JSON object blob, got %q", string(raw[:1]))
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
