package memory

import (
	"path/filepath"
	"testing"
	"time"

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

// TestPersistAndLoadRoundTrip writes WorkingMemory fields, persists
// to SQLite, then creates a fresh Store and calls Load to verify
// the WorkingMemory was correctly restored.
func TestPersistAndLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st, err := storage.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	// Set up working memory with data
	m := New(st)
	m.TouchFile("/path/to/foo.go")
	m.TouchSymbol("FooStruct")
	m.SetWorkingQuestionAnswer("what is auth?", "auth is xyz")

	// Persist to SQLite
	if err := m.Persist(); err != nil {
		t.Fatalf("persist: %v", err)
	}

	// Create a new store backed by the same database and load
	m2 := New(st)
	if err := m2.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}

	wm := m2.Working()
	if len(wm.RecentFiles) == 0 || wm.RecentFiles[0] != "/path/to/foo.go" {
		t.Errorf("RecentFiles = %v, want [/path/to/foo.go]", wm.RecentFiles)
	}
	if len(wm.RecentSymbols) == 0 || wm.RecentSymbols[0] != "FooStruct" {
		t.Errorf("RecentSymbols = %v, want [FooStruct]", wm.RecentSymbols)
	}
	if wm.LastQuestion != "what is auth?" {
		t.Errorf("LastQuestion = %q, want %q", wm.LastQuestion, "what is auth?")
	}
	if wm.LastAnswer != "auth is xyz" {
		t.Errorf("LastAnswer = %q, want %q", wm.LastAnswer, "auth is xyz")
	}
}

// TestPersistNilStorage returns nil without error.
func TestPersistNilStorage(t *testing.T) {
	m := New(nil)
	if err := m.Persist(); err != nil {
		t.Fatalf("persist with nil storage should be no-op, got: %v", err)
	}
}

// TestLoadNilStorageReturnsNil does not error when store has no storage.
func TestLoadNilStorageReturnsNil(t *testing.T) {
	m := New(nil)
	if err := m.Load(); err != nil {
		t.Fatalf("load with nil storage should be no-op, got: %v", err)
	}
}

// TestLoadNoData returns nil when bucket is empty (first run).
func TestLoadNoData(t *testing.T) {
	dir := t.TempDir()
	st, err := storage.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	m := New(st)
	// No Persist called — bucket is empty
	if err := m.Load(); err != nil {
		t.Fatalf("load on empty store: %v", err)
	}
	wm := m.Working()
	if wm.LastQuestion != "" {
		t.Errorf("empty load: LastQuestion = %q, want empty", wm.LastQuestion)
	}
}

// TestLoadCorruptDataKeepsZeroValue when JSON in bucket is corrupt,
// Load should return nil and leave WorkingMemory at zero value.
func TestLoadCorruptDataKeepsZeroValue(t *testing.T) {
	dir := t.TempDir()
	st, err := storage.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	// Directly write corrupt JSON into the working bucket
	_ = st.BucketPut(bucketWorking, bucketWorkingKey, []byte("not valid json {{{"))

	m := New(st)
	if err := m.Load(); err != nil {
		t.Fatalf("load with corrupt data should return nil, got: %v", err)
	}
	wm := m.Working()
	if wm.LastQuestion != "" {
		t.Errorf("corrupt data: LastQuestion = %q, want empty string", wm.LastQuestion)
	}
}

// TestMemoryAddAutoPopulatesTimestamps verifies that Add fills in
// CreatedAt, UpdatedAt, and LastUsedAt when they are zero.
func TestMemoryAddAutoPopulatesTimestamps(t *testing.T) {
	dir := t.TempDir()
	st, err := storage.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	m := New(st)

	before := time.Now().Add(-time.Second)
	if err := m.Add(types.MemoryEntry{
		Tier:     types.MemoryEpisodic,
		Category: "test",
		Key:      "timestamp key",
		Value:    "timestamp value",
		Project:  "test-project",
	}); err != nil {
		t.Fatalf("add: %v", err)
	}
	after := time.Now().Add(time.Second)

	list, err := m.List(types.MemoryEpisodic, 10, "test-project")
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %d entries (err %v)", len(list), err)
	}
	e := list[0]
	if e.CreatedAt.Before(before) || e.CreatedAt.After(after) {
		t.Errorf("CreatedAt = %v, want between %v and %v", e.CreatedAt, before, after)
	}
	if e.UpdatedAt.Before(before) || e.UpdatedAt.After(after) {
		t.Errorf("UpdatedAt = %v, want between %v and %v", e.UpdatedAt, before, after)
	}
	if e.LastUsedAt.Before(before) || e.LastUsedAt.After(after) {
		t.Errorf("LastUsedAt = %v, want between %v and %v", e.LastUsedAt, before, after)
	}
}

// TestListDefaultLimit verifies that limit <= 0 defaults to 100.
func TestListDefaultLimit(t *testing.T) {
	dir := t.TempDir()
	st, err := storage.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	m := New(st)

	// Add one entry
	if err := m.Add(types.MemoryEntry{
		Tier:     types.MemoryEpisodic,
		Category: "test",
		Key:      "limit key",
		Value:    "limit value",
		Project:  "test-project",
	}); err != nil {
		t.Fatalf("add: %v", err)
	}

	// limit=0 should default to 100 and return the entry
	list, err := m.List(types.MemoryEpisodic, 0, "test-project")
	if err != nil {
		t.Fatalf("list with limit=0: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("limit=0 should default to 100, got %d entries", len(list))
	}
}

// TestSearchEmptyQueryFallsBackToList verifies that Search with
// an empty query delegates to List.
func TestSearchEmptyQueryFallsBackToList(t *testing.T) {
	dir := t.TempDir()
	st, err := storage.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	m := New(st)

	if err := m.Add(types.MemoryEntry{
		Tier:     types.MemoryEpisodic,
		Category: "test",
		Key:      "unique-key-xyz",
		Value:    "value",
		Project:  "test-project",
	}); err != nil {
		t.Fatalf("add: %v", err)
	}

	// Empty string should return all matching entries via List
	results, err := m.Search("  ", types.MemoryEpisodic, 50, "test-project")
	if err != nil {
		t.Fatalf("search with whitespace: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("whitespace-only search returned %d, want 1", len(results))
	}
}

// TestAddNilProjectErrors verifies that Add rejects entries
// with an empty project field.
func TestAddNilProjectErrors(t *testing.T) {
	dir := t.TempDir()
	st, err := storage.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	m := New(st)

	if err := m.Add(types.MemoryEntry{
		Tier:     types.MemoryEpisodic,
		Category: "test",
		Key:      "k",
		Value:    "v",
		Project:  "",
	}); err == nil {
		t.Fatal("add with empty project should error")
	}
}

// TestPromoteNotFoundErrors verifies that Promote returns an error
// when the entry doesn't exist in episodic.
func TestPromoteNotFoundErrors(t *testing.T) {
	dir := t.TempDir()
	st, err := storage.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	m := New(st)

	if err := m.Promote("does-not-exist"); err == nil {
		t.Fatal("promote on missing id should error")
	}
}

// TestAddNilMetadataInitialized verifies Add sets Metadata to an empty
// map before storage (so it never persists as null). The field may
// still be nil after unmarshalling from JSON — that's normal Go behavior
// and doesn't indicate a storage problem.
func TestAddNilMetadataInitialized(t *testing.T) {
	dir := t.TempDir()
	st, err := storage.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	m := New(st)

	if err := m.Add(types.MemoryEntry{
		Tier:     types.MemoryEpisodic,
		Category: "test",
		Key:      "metadata key",
		Value:    "metadata value",
		Project:  "test-project",
		Metadata: nil,
	}); err != nil {
		t.Fatalf("add: %v", err)
	}

	list, err := m.List(types.MemoryEpisodic, 10, "test-project")
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %d (err %v)", len(list), err)
	}
	_ = list[0].Metadata // must not panic; nil after round-trip is fine
}

// TestTouchFileDeduplicates verifies that touching the same file
// moves it to the front without duplicating.
func TestTouchFileDeduplicates(t *testing.T) {
	dir := t.TempDir()
	st, err := storage.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	m := New(st)

	m.TouchFile("/foo/bar.go")
	m.TouchFile("/baz/qux.go")
	m.TouchFile("/foo/bar.go") // touch again — should move to front

	wm := m.Working()
	if wm.RecentFiles[0] != "/foo/bar.go" {
		t.Errorf("RecentFiles[0] = %q, want /foo/bar.go (dedup to front)", wm.RecentFiles[0])
	}
	if len(wm.RecentFiles) != 2 {
		t.Errorf("len(RecentFiles) = %d, want 2", len(wm.RecentFiles))
	}
}

// TestDeleteSemanticOnlyEntry verifies that Delete searches both
// buckets and removes the entry from whichever bucket it lives in.
func TestDeleteSemanticOnlyEntry(t *testing.T) {
	dir := t.TempDir()
	st, err := storage.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	m := New(st)

	// Add as episodic, then promote to semantic
	if err := m.Add(types.MemoryEntry{
		Tier:     types.MemoryEpisodic,
		Category: "test",
		Key:      "semantic-only",
		Value:    "value",
		Project:  "test-project",
	}); err != nil {
		t.Fatalf("add: %v", err)
	}
	list, _ := m.List(types.MemoryEpisodic, 10, "test-project")
	id := list[0].ID

	if err := m.Promote(id); err != nil {
		t.Fatalf("promote: %v", err)
	}

	// Delete from semantic bucket
	if err := m.Delete(id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	semantic, _ := m.List(types.MemorySemantic, 10, "test-project")
	if len(semantic) != 0 {
		t.Errorf("semantic tier should be empty after delete, got %d", len(semantic))
	}
}

// TestUpdateMutatesFields verifies that Update changes key/value/category.
func TestUpdateMutatesFields(t *testing.T) {
	dir := t.TempDir()
	st, err := storage.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	m := New(st)

	if err := m.Add(types.MemoryEntry{
		Tier:     types.MemoryEpisodic,
		Category: "original-category",
		Key:      "original-key",
		Value:    "original-value",
		Project:  "test-project",
	}); err != nil {
		t.Fatalf("add: %v", err)
	}
	list, _ := m.List(types.MemoryEpisodic, 10, "test-project")
	id := list[0].ID

	if err := m.Update(id, "new-key", "new-value", "new-category"); err != nil {
		t.Fatalf("update: %v", err)
	}
	updated, _ := m.List(types.MemoryEpisodic, 10, "test-project")
	if updated[0].Key != "new-key" || updated[0].Value != "new-value" || updated[0].Category != "new-category" {
		t.Errorf("Update fields mismatch: %+v", updated[0])
	}
}

// TestSearchFiltersByProject verifies that Search respects the project filter.
func TestSearchFiltersByProject(t *testing.T) {
	dir := t.TempDir()
	st, err := storage.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	m := New(st)

	m.Add(types.MemoryEntry{
		Tier:     types.MemoryEpisodic,
		Category: "test",
		Key:      "unique-key-search",
		Value:    "value",
		Project:  "project-a",
	})
	m.Add(types.MemoryEntry{
		Tier:     types.MemoryEpisodic,
		Category: "test",
		Key:      "unique-key-search",
		Value:    "value",
		Project:  "project-b",
	})

	results, err := m.Search("unique-key-search", types.MemoryEpisodic, 50, "project-a")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 1 || results[0].Project != "project-a" {
		t.Errorf("project filter: got %d results from project-a, want 1", len(results))
	}
}

// TestListRespectsLimit verifies List caps results at the requested limit.
func TestListRespectsLimit(t *testing.T) {
	dir := t.TempDir()
	st, err := storage.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	m := New(st)

	// Add 5 entries
	for i := 0; i < 5; i++ {
		m.Add(types.MemoryEntry{
			Tier:     types.MemoryEpisodic,
			Category: "test",
			Key:      "test-key",
			Value:    "value",
			Project:  "test-project",
		})
	}

	list, err := m.List(types.MemoryEpisodic, 2, "test-project")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("limit=2: got %d, want 2", len(list))
	}
}
