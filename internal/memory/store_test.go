package memory

import (
	"path/filepath"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/storage"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func TestMemoryAddListSearchClear(t *testing.T) {
	dir := t.TempDir()
	st, err := storage.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	m := New(st)
	if err := m.Add(types.MemoryEntry{
		Tier:       types.MemoryEpisodic,
		Category:   "interaction",
		Key:        "auth question",
		Value:      "auth answer",
		Confidence: 0.8,
		Project:    "test-project",
	}); err != nil {
		t.Fatalf("add entry: %v", err)
	}

	list, err := m.List(types.MemoryEpisodic, 10, "test-project")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) == 0 {
		t.Fatal("expected at least one memory entry")
	}

	search, err := m.Search("auth", types.MemoryEpisodic, 10, "test-project")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(search) == 0 {
		t.Fatal("expected search hits")
	}

	if err := m.Clear(types.MemoryEpisodic); err != nil {
		t.Fatalf("clear: %v", err)
	}
	after, err := m.List(types.MemoryEpisodic, 10, "test-project")
	if err != nil {
		t.Fatalf("list after clear: %v", err)
	}
	if len(after) != 0 {
		t.Fatalf("expected 0 after clear, got %d", len(after))
	}
}

// TestMemoryDeleteUpdatePromote — Phase H item 1 mutation surface.
// Walks the lifecycle: add an episodic entry → Update mutates fields →
// Promote moves it to semantic → Delete removes it. Each transition is
// verified by re-listing the relevant tier so we exercise the SQLite
// round-trip rather than just the in-memory field assignment.
func TestMemoryDeleteUpdatePromote(t *testing.T) {
	dir := t.TempDir()
	st, err := storage.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	m := New(st)
	if err := m.Add(types.MemoryEntry{
		Tier:     types.MemoryEpisodic,
		Category: "interaction",
		Key:      "auth question",
		Value:    "auth answer",
		Project:  "test-project",
	}); err != nil {
		t.Fatalf("add: %v", err)
	}
	list, err := m.List(types.MemoryEpisodic, 10, "test-project")
	if err != nil || len(list) != 1 {
		t.Fatalf("expected one episodic entry, got %d (err %v)", len(list), err)
	}
	id := list[0].ID

	// Update — mutate fields, confirm via re-list.
	if err := m.Update(id, "auth refresh", "rotate every 24h", "fact"); err != nil {
		t.Fatalf("update: %v", err)
	}
	updated, _ := m.List(types.MemoryEpisodic, 10, "test-project")
	if len(updated) != 1 {
		t.Fatalf("update should not change count, got %d", len(updated))
	}
	if updated[0].Key != "auth refresh" || updated[0].Value != "rotate every 24h" || updated[0].Category != "fact" {
		t.Fatalf("update did not persist new fields: %+v", updated[0])
	}

	// Promote — entry moves from episodic to semantic.
	if err := m.Promote(id); err != nil {
		t.Fatalf("promote: %v", err)
	}
	episodicAfter, _ := m.List(types.MemoryEpisodic, 10, "test-project")
	if len(episodicAfter) != 0 {
		t.Fatalf("episodic tier should be empty after promote, got %d", len(episodicAfter))
	}
	semanticAfter, _ := m.List(types.MemorySemantic, 10, "test-project")
	if len(semanticAfter) != 1 || semanticAfter[0].ID != id {
		t.Fatalf("semantic tier should now hold the entry, got %+v", semanticAfter)
	}
	if semanticAfter[0].Tier != types.MemorySemantic {
		t.Fatalf("promoted entry should carry MemorySemantic tier, got %q", semanticAfter[0].Tier)
	}

	// Promote again on a now-semantic entry should be a no-op.
	if err := m.Promote(id); err != nil {
		t.Fatalf("re-promote should be a no-op, got error: %v", err)
	}

	// Delete — round-trip out of semantic.
	if err := m.Delete(id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	semanticGone, _ := m.List(types.MemorySemantic, 10, "test-project")
	if len(semanticGone) != 0 {
		t.Fatalf("semantic tier should be empty after delete, got %d", len(semanticGone))
	}

	// Idempotent delete — removing a missing ID is not an error so the
	// TUI can call delete after a concurrent prune without surfacing
	// noise to the user.
	if err := m.Delete(id); err != nil {
		t.Fatalf("delete on missing id should be a no-op, got: %v", err)
	}

	// Update on a missing ID surfaces a clear error so the TUI can show
	// it instead of silently swallowing the mutation.
	if err := m.Update(id, "x", "y", "z"); err == nil {
		t.Fatalf("update on missing id should error")
	}
}

// TestMemoryMutatorsRejectEmptyID — guards against accidental
// `Delete("")` / `Update("")` from a buggy caller wiping/mutating the
// wrong row. Strict ID validation keeps the SQLite-level surface safe.
func TestMemoryMutatorsRejectEmptyID(t *testing.T) {
	dir := t.TempDir()
	st, err := storage.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	m := New(st)
	if err := m.Delete(""); err == nil {
		t.Fatal("Delete should reject empty ID")
	}
	if err := m.Delete("   "); err == nil {
		t.Fatal("Delete should reject whitespace-only ID")
	}
	if err := m.Update("", "k", "v", "c"); err == nil {
		t.Fatal("Update should reject empty ID")
	}
	if err := m.Promote(""); err == nil {
		t.Fatal("Promote should reject empty ID")
	}
}
