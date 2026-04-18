package conversation

import (
	"os"
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

func TestConversationSaveLoadPreservesBranchesAndMetadata(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	mgr := New(store)
	conv := mgr.Start("offline", "offline-analyzer-v1")
	conv.Metadata["ticket"] = "ABC-123"
	mgr.mu.Lock()
	mgr.active.Metadata["ticket"] = "ABC-123"
	mgr.mu.Unlock()
	mgr.AddMessage("offline", "offline-analyzer-v1", types.Message{
		Role:      types.RoleUser,
		Content:   "q1",
		Timestamp: time.Now(),
	})
	if err := mgr.BranchCreate("alt"); err != nil {
		t.Fatalf("branch create: %v", err)
	}
	if err := mgr.BranchSwitch("alt"); err != nil {
		t.Fatalf("branch switch: %v", err)
	}
	mgr.AddMessage("offline", "offline-analyzer-v1", types.Message{
		Role:      types.RoleAssistant,
		Content:   "alt answer",
		Timestamp: time.Now(),
	})
	if err := mgr.SaveActive(); err != nil {
		t.Fatalf("save active: %v", err)
	}

	loaded, err := mgr.Load(conv.ID)
	if err != nil {
		t.Fatalf("load conversation: %v", err)
	}
	if loaded.Provider != "offline" || loaded.Model != "offline-analyzer-v1" {
		t.Fatalf("provider/model lost on load: %+v", loaded)
	}
	if loaded.Branch != "alt" {
		t.Fatalf("expected active branch alt, got %q", loaded.Branch)
	}
	if loaded.Metadata["ticket"] != "ABC-123" {
		t.Fatalf("metadata lost on load: %#v", loaded.Metadata)
	}
	if len(loaded.Branches["main"]) != 1 || len(loaded.Branches["alt"]) != 2 {
		t.Fatalf("branch history lost on load: %#v", loaded.Branches)
	}
	if loaded.StartedAt.IsZero() {
		t.Fatal("expected StartedAt to survive round-trip")
	}
}

func TestConversationLoadFallsBackToLegacyJSONL(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	msgs := []types.Message{
		{Role: types.RoleUser, Content: "legacy", Timestamp: time.Now()},
	}
	if err := store.SaveConversationLog("conv_legacy", msgs); err != nil {
		t.Fatalf("save legacy log: %v", err)
	}

	mgr := New(store)
	loaded, err := mgr.Load("conv_legacy")
	if err != nil {
		t.Fatalf("load legacy conversation: %v", err)
	}
	if loaded == nil || len(loaded.Branches["main"]) != 1 {
		t.Fatalf("expected legacy log to load through fallback, got %#v", loaded)
	}
}

func TestConversationListUsesLegacyLogModTimeForStartedAt(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	oldMsgs := []types.Message{{Role: types.RoleUser, Content: "old", Timestamp: time.Now()}}
	newMsgs := []types.Message{{Role: types.RoleUser, Content: "new", Timestamp: time.Now()}}
	if err := store.SaveConversationLog("conv_old", oldMsgs); err != nil {
		t.Fatalf("save old log: %v", err)
	}
	if err := store.SaveConversationLog("conv_new", newMsgs); err != nil {
		t.Fatalf("save new log: %v", err)
	}

	base := filepath.Join(store.ArtifactsDir(), "conversations")
	oldTime := time.Now().Add(-2 * time.Hour)
	newTime := time.Now().Add(-1 * time.Hour)
	if err := os.Chtimes(filepath.Join(base, "conv_old.jsonl"), oldTime, oldTime); err != nil {
		t.Fatalf("chtimes old: %v", err)
	}
	if err := os.Chtimes(filepath.Join(base, "conv_new.jsonl"), newTime, newTime); err != nil {
		t.Fatalf("chtimes new: %v", err)
	}

	mgr := New(store)
	list, err := mgr.List()
	if err != nil {
		t.Fatalf("list conversations: %v", err)
	}
	if len(list) < 2 {
		t.Fatalf("expected at least 2 conversations, got %d", len(list))
	}
	if list[0].ID != "conv_new" || list[1].ID != "conv_old" {
		t.Fatalf("expected modtime ordering for legacy logs, got %#v", list[:2])
	}
	if !list[0].StartedAt.Equal(newTime) {
		t.Fatalf("expected new StartedAt=%v, got %v", newTime, list[0].StartedAt)
	}
	if !list[1].StartedAt.Equal(oldTime) {
		t.Fatalf("expected old StartedAt=%v, got %v", oldTime, list[1].StartedAt)
	}
}
