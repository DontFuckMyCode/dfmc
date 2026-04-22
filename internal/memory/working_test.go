// Pin tests for the in-memory working tier (recent files / symbols /
// last Q+A). The bbolt-backed Add/List/Search/Clear path is covered
// by store_test.go; this file targets the read/write surface that the
// agent loop hits on every turn.

package memory

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/storage"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func openTempStorage(t *testing.T, dir string) *storage.Store {
	t.Helper()
	st, err := storage.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	return st
}

// TouchFile must dedupe (most-recent-front), trim whitespace, and cap
// the slice. The 50-entry cap is what stops a long session from
// silently swelling the working memory blob.
func TestWorkingMemory_TouchFileDedupAndCap(t *testing.T) {
	m := New(nil)

	m.TouchFile("a.go")
	m.TouchFile("b.go")
	m.TouchFile("a.go") // re-touch — should move to front, not duplicate
	got := m.Working().RecentFiles
	if len(got) != 2 {
		t.Fatalf("expected dedupe to 2 entries, got %d: %v", len(got), got)
	}
	if got[0] != "a.go" {
		t.Fatalf("re-touched file should land at index 0, got %q", got[0])
	}

	// Empty / whitespace-only paths must be ignored.
	m.TouchFile("")
	m.TouchFile("   ")
	if len(m.Working().RecentFiles) != 2 {
		t.Fatalf("blank inputs leaked: %v", m.Working().RecentFiles)
	}

	// Cap at 50: push 60 unique paths, expect at most 50 retained.
	for i := 0; i < 60; i++ {
		m.TouchFile("file" + itoa(i) + ".go")
	}
	if got := len(m.Working().RecentFiles); got > 50 {
		t.Fatalf("RecentFiles cap breached: got %d, want <=50", got)
	}
}

// TouchSymbol uses the same machinery as TouchFile but with a 100-cap.
// Pin both bounds so a future cap change in pushUniqFront doesn't
// silently shift one without the other.
func TestWorkingMemory_TouchSymbolCap(t *testing.T) {
	m := New(nil)
	for i := 0; i < 150; i++ {
		m.TouchSymbol("sym" + itoa(i))
	}
	if got := len(m.Working().RecentSymbols); got > 100 {
		t.Fatalf("RecentSymbols cap breached: got %d, want <=100", got)
	}
}

// SetWorkingQuestionAnswer is the only way to propagate the last
// turn's Q/A into MagicDoc / handoff briefs. A regression here would
// silently empty those briefs.
func TestWorkingMemory_SetQuestionAnswer(t *testing.T) {
	m := New(nil)
	m.SetWorkingQuestionAnswer("why does X fail?", "because Y")
	w := m.Working()
	if w.LastQuestion != "why does X fail?" || w.LastAnswer != "because Y" {
		t.Fatalf("Q/A round-trip lost: %+v", w)
	}
}

// Working() must hand back a deep-enough copy that callers can't
// mutate the store's internal slices. The returned struct is by-value,
// but RecentFiles/RecentSymbols are reference types; the implementation
// uses append([]string(nil), ...) — pin that.
func TestWorkingMemory_ReturnsCopyNotAlias(t *testing.T) {
	m := New(nil)
	m.TouchFile("a.go")
	m.TouchFile("b.go")

	snapshot := m.Working()
	snapshot.RecentFiles[0] = "MUTATED"

	again := m.Working()
	if again.RecentFiles[0] == "MUTATED" {
		t.Fatalf("Working() leaked internal slice: caller mutation reached store")
	}
}

// Concurrent writers must not race the working struct. Run with -race.
// The mutex is the only thing standing between us and a torn read of
// the recent-files slice while the agent is touching files in
// parallel goroutines (delegate_task fan-out, subagent runs).
func TestWorkingMemory_ConcurrentWritesNoRace(t *testing.T) {
	m := New(nil)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				m.TouchFile("g" + itoa(id) + "/f" + itoa(j) + ".go")
				m.TouchSymbol("sym_" + itoa(id) + "_" + itoa(j))
			}
		}(i)
	}
	wg.Wait()

	w := m.Working()
	if len(w.RecentFiles) == 0 || len(w.RecentSymbols) == 0 {
		t.Fatalf("expected non-empty working memory after fan-out: %+v", w)
	}
}

// Load and Persist are intentionally no-ops today (working memory is
// in-process only; bbolt holds episodic/semantic). Pin that contract
// so a future "I'll just persist working too" change is forced to
// update this test and think about the implications.
func TestWorkingMemory_LoadPersistAreNoOps(t *testing.T) {
	m := New(nil)
	if err := m.Load(); err != nil {
		t.Fatalf("Load() should be a no-op when storage is nil; got %v", err)
	}
	if err := m.Persist(); err != nil {
		t.Fatalf("Persist() should be a no-op; got %v", err)
	}
}

// Add must reject when storage is missing — agent calls during a
// degraded boot must surface an error rather than silently succeeding
// against an in-memory void.
func TestMemory_AddRejectsWhenStorageMissing(t *testing.T) {
	m := New(nil)
	err := m.Add(types.MemoryEntry{Tier: types.MemoryEpisodic, Key: "k", Value: "v"})
	if err == nil {
		t.Fatalf("expected error when storage is nil; got nil")
	}
	if !strings.Contains(err.Error(), "storage") {
		t.Fatalf("error should mention storage, got %q", err.Error())
	}
}

// List must return nil slice (not an error) when storage is unavailable.
// The caller handles nil gracefully; an error would break the degraded-path
// call sites that use List as a "get what you can" probe.
func TestMemory_ListReturnsNilWhenStorageNil(t *testing.T) {
	m := New(nil)
	got, err := m.List(types.MemoryEpisodic, 10)
	if err != nil {
		t.Fatalf("List should not error when storage is nil; got %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil slice when storage is nil, got %v", got)
	}
}

// Search delegates to List; when storage is nil both must be nil-safe.
func TestMemory_SearchIsSafeWhenStorageNil(t *testing.T) {
	m := New(nil)
	got, err := m.Search("any query", types.MemoryEpisodic, 10)
	if err != nil {
		t.Fatalf("Search should not error when storage is nil; got %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty result when storage is nil, got %v", got)
	}
}

// Clear must be safe to call even when storage is nil — a corrupt-db
// recovery that wipes memory state mid-boot must not panic.
func TestMemory_ClearIsSafeWhenStorageNil(t *testing.T) {
	m := New(nil)
	if err := m.Clear(types.MemoryEpisodic); err != nil {
		t.Fatalf("Clear should not error when storage is nil; got %v", err)
	}
}

// T4: Concurrent memory operations with nil storage must not panic.
// TouchFile/TouchSymbol are working-tier (in-memory); List/Search
// fall back to nil-safes. The fan-out exercises all three code paths.
func TestMemory_ConcurrentAccessNilStorage(t *testing.T) {
	m := New(nil)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.TouchFile("a.go")
			_, _ = m.List(types.MemoryEpisodic, 10)
			_, _ = m.Search("q", types.MemoryEpisodic, 10)
		}()
	}
	wg.Wait()
	// If we got here without panicking, the test passes.
}

// TouchFile/TouchSymbol are working-memory operations — they are entirely
// in-memory and must work even when bbolt storage is unavailable.
func TestWorkingMemory_WorksWhenStorageIsNil(t *testing.T) {
	m := New(nil)
	m.TouchFile("a.go")
	m.TouchSymbol("Foo")
	w := m.Working()
	if len(w.RecentFiles) != 1 || w.RecentFiles[0] != "a.go" {
		t.Fatalf("TouchFile failed when storage nil: %v", w.RecentFiles)
	}
	if len(w.RecentSymbols) != 1 || w.RecentSymbols[0] != "Foo" {
		t.Fatalf("TouchSymbol failed when storage nil: %v", w.RecentSymbols)
	}
	m.SetWorkingQuestionAnswer("q", "a")
	if w.LastQuestion != "" || w.LastAnswer != "" {
		t.Fatalf("Working() returned alias instead of copy before Set call")
	}
	w2 := m.Working()
	if w2.LastQuestion != "q" || w2.LastAnswer != "a" {
		t.Fatalf("SetWorkingQuestionAnswer failed when storage nil: %v", w2)
	}
}

// AddEpisodicInteraction is the convenience wrapper agent loops use;
// it must populate the structured fields that downstream search +
// list rely on (Tier=Episodic, Category="interaction").
func TestAddEpisodicInteraction_FieldsPopulated(t *testing.T) {
	dir := t.TempDir()
	store := openTempStorage(t, dir)
	t.Cleanup(func() { _ = store.Close() })

	m := New(store)
	if err := m.AddEpisodicInteraction("proj", "Q?", "A.", 0.5); err != nil {
		t.Fatalf("add: %v", err)
	}
	list, err := m.List(types.MemoryEpisodic, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(list))
	}
	got := list[0]
	if got.Tier != types.MemoryEpisodic {
		t.Fatalf("tier wrong: %v", got.Tier)
	}
	if got.Category != "interaction" {
		t.Fatalf("category wrong: %q", got.Category)
	}
	if got.Project != "proj" || got.Key != "Q?" || got.Value != "A." {
		t.Fatalf("fields lost: %+v", got)
	}
	if got.Confidence != 0.5 {
		t.Fatalf("confidence lost: %v", got.Confidence)
	}
}

// Tiny helper so we don't import strconv in every assertion above.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
