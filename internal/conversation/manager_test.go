package conversation

import (
	"encoding/json"
	"errors"
	"io"
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

func TestConversationCopiesAreDeepEnoughToPreventMutationLeak(t *testing.T) {
	mgr := New(openConvStore(t))
	mgr.Start("offline", "offline-v1")
	msg := types.Message{
		Role:      types.RoleAssistant,
		Content:   "hello",
		Timestamp: time.Now(),
		Metadata:  map[string]string{"source": "seed"},
		ToolCalls: []types.ToolCallRecord{{
			Name:      "read_file",
			Params:    map[string]any{"path": "a.txt"},
			Timestamp: time.Now(),
			Metadata:  map[string]string{"kind": "read"},
		}},
		Results: []types.ToolResultRecord{{
			Name:      "read_file",
			Output:    "ok",
			Success:   true,
			Timestamp: time.Now(),
			Metadata:  map[string]string{"status": "ok"},
		}},
	}
	mgr.AddMessage("offline", "offline-v1", msg)

	active := mgr.Active()
	if active == nil {
		t.Fatal("expected active conversation")
	}
	got := active.Messages()
	got[0].Metadata["source"] = "mutated"
	got[0].ToolCalls[0].Params["path"] = "b.txt"
	got[0].ToolCalls[0].Metadata["kind"] = "changed"
	got[0].Results[0].Metadata["status"] = "changed"

	fresh := mgr.Active()
	msgs := fresh.Messages()
	if msgs[0].Metadata["source"] != "seed" {
		t.Fatalf("message metadata leaked through clone: %#v", msgs[0].Metadata)
	}
	if msgs[0].ToolCalls[0].Params["path"] != "a.txt" {
		t.Fatalf("tool call params leaked through clone: %#v", msgs[0].ToolCalls[0].Params)
	}
	if msgs[0].ToolCalls[0].Metadata["kind"] != "read" {
		t.Fatalf("tool call metadata leaked through clone: %#v", msgs[0].ToolCalls[0].Metadata)
	}
	if msgs[0].Results[0].Metadata["status"] != "ok" {
		t.Fatalf("result metadata leaked through clone: %#v", msgs[0].Results[0].Metadata)
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

func TestLoadReadOnly(t *testing.T) {
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
		Content:   "hello",
		Timestamp: time.Now(),
	})
	if err := mgr.SaveActive(); err != nil {
		t.Fatalf("save: %v", err)
	}

	active := mgr.Active()
	ro, err := mgr.LoadReadOnly(active.ID)
	if err != nil {
		t.Fatalf("LoadReadOnly: %v", err)
	}
	if ro.ID != active.ID {
		t.Errorf("LoadReadOnly ID: got %q", ro.ID)
	}
	if len(ro.Messages()) != 1 {
		t.Errorf("LoadReadOnly Messages: got %d", len(ro.Messages()))
	}
}

func TestLoadReadOnly_Nonexistent(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	mgr := New(store)
	_, err = mgr.LoadReadOnly("nonexistent-id")
	if err == nil {
		t.Fatal("expected error for nonexistent id")
	}
}

// isJSONError is exercised via loadFromStore when a corrupted JSON file exists.
// We test this by manually writing a malformed JSON state file and verifying
// that LoadReadOnly falls back to JSONL (which may be empty, but doesn't error).
func TestLoadReadOnly_CorruptedJSONFallsBack(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	mgr := New(store)
	mgr.Start("offline", "offline-analyzer-v1")
	if err := mgr.SaveActive(); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Corrupt the JSON state file by writing truncated garbage
	active := mgr.Active()
	statePath := filepath.Join(dir, "data", active.ID+".json")
	if err := os.WriteFile(statePath, []byte("{truncated json}"), 0o644); err != nil {
		t.Fatalf("corrupt json: %v", err)
	}

	// Should fall back gracefully (load returns the conversation from jsonl or empty)
	ro, err := mgr.LoadReadOnly(active.ID)
	if err != nil {
		t.Fatalf("LoadReadOnly with corrupted JSON: %v", err)
	}
	if ro.ID != active.ID {
		t.Errorf("ID mismatch: got %q", ro.ID)
	}
}

// isJSONError unit tests

func TestIsJSONError_Nil(t *testing.T) {
	if isJSONError(nil) {
		t.Error("nil error should return false")
	}
}

func TestIsJSONError_EOF(t *testing.T) {
	if !isJSONError(io.EOF) {
		t.Error("io.EOF should return true")
	}
}

func TestIsJSONError_SyntaxError(t *testing.T) {
	// Use json.Unmarshal on invalid data to produce a *json.SyntaxError
	var v map[string]int
	err := json.Unmarshal([]byte("{invalid"), &v)
	if err == nil {
		t.Fatal("expected error from invalid JSON")
	}
	if !isJSONError(err) {
		t.Error("json.SyntaxError should return true")
	}
}

func TestIsJSONError_UnmarshalTypeError(t *testing.T) {
	if !isJSONError(&json.UnmarshalTypeError{}) {
		t.Error("json.UnmarshalTypeError should return true")
	}
}

func TestIsJSONError_OtherError(t *testing.T) {
	if isJSONError(errors.New("some other error")) {
		t.Error("other error should return false")
	}
}
