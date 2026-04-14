package conversation

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/storage"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func TestConversationManagerSaveLoadSearch(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	mgr := New(store)
	mgr.Start("offline", "offline-analyzer-v1")
	mgr.AddMessage("offline", "offline-analyzer-v1", types.Message{
		Role:      types.RoleUser,
		Content:   "how does auth work",
		Timestamp: time.Now(),
	})
	mgr.AddMessage("offline", "offline-analyzer-v1", types.Message{
		Role:      types.RoleAssistant,
		Content:   "auth flow explanation",
		Timestamp: time.Now(),
	})
	if err := mgr.SaveActive(); err != nil {
		t.Fatalf("save active: %v", err)
	}

	active := mgr.Active()
	if active == nil {
		t.Fatal("expected active conversation")
	}
	_, err = mgr.Load(active.ID)
	if err != nil {
		t.Fatalf("load conversation: %v", err)
	}

	results, err := mgr.Search("auth", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected search results")
	}
}

func TestConversationBranchCompare(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	mgr := New(store)
	mgr.Start("offline", "offline-analyzer-v1")
	mgr.AddMessage("offline", "offline-analyzer-v1", types.Message{
		Role:      types.RoleUser,
		Content:   "q1",
		Timestamp: time.Now(),
	})
	mgr.AddMessage("offline", "offline-analyzer-v1", types.Message{
		Role:      types.RoleAssistant,
		Content:   "a1",
		Timestamp: time.Now(),
	})

	if err := mgr.BranchCreate("alt"); err != nil {
		t.Fatalf("branch create: %v", err)
	}
	if err := mgr.BranchSwitch("alt"); err != nil {
		t.Fatalf("branch switch: %v", err)
	}
	mgr.AddMessage("offline", "offline-analyzer-v1", types.Message{
		Role:      types.RoleUser,
		Content:   "q2-alt",
		Timestamp: time.Now(),
	})

	comp, err := mgr.BranchCompare("main", "alt")
	if err != nil {
		t.Fatalf("branch compare: %v", err)
	}
	if comp.SharedPrefixN != 2 {
		t.Fatalf("expected shared prefix 2, got %d", comp.SharedPrefixN)
	}
	if comp.OnlyA != 0 || comp.OnlyB != 1 {
		t.Fatalf("unexpected compare result: %+v", comp)
	}
}

func TestConversationUndoLast(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	mgr := New(store)
	mgr.Start("offline", "offline-analyzer-v1")
	mgr.AddMessage("offline", "offline-analyzer-v1", types.Message{
		Role:      types.RoleUser,
		Content:   "q1",
		Timestamp: time.Now(),
	})
	mgr.AddMessage("offline", "offline-analyzer-v1", types.Message{
		Role:      types.RoleAssistant,
		Content:   "a1",
		Timestamp: time.Now(),
	})

	removed, err := mgr.UndoLast()
	if err != nil {
		t.Fatalf("undo last: %v", err)
	}
	if removed != 2 {
		t.Fatalf("expected removed=2, got %d", removed)
	}
	active := mgr.Active()
	if active == nil {
		t.Fatal("expected active conversation")
	}
	if got := len(active.Messages()); got != 0 {
		t.Fatalf("expected no messages after undo, got %d", got)
	}
}
