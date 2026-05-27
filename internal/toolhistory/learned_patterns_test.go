package toolhistory

import (
	"path/filepath"
	"testing"
	"time"
)

func TestInitLearnedPatterns(t *testing.T) {
	tmp := t.TempDir()
	store, err := InitLearnedPatterns(tmp)
	if err != nil {
		t.Fatalf("InitLearnedPatterns() error = %v", err)
	}
	if store == nil {
		t.Fatal("InitLearnedPatterns() returned nil")
	}
	store.Close()
}

func TestInitLearnedPatterns_emptyDir(t *testing.T) {
	store, err := InitLearnedPatterns("")
	if err != nil {
		t.Fatalf("InitLearnedPatterns(\"\") error = %v", err)
	}
	if store != nil {
		t.Error("InitLearnedPatterns(\"\") should return nil store")
	}
}

func TestLearnedPatternStore_Add(t *testing.T) {
	tmp := t.TempDir()
	store, err := InitLearnedPatterns(tmp)
	if err != nil {
		t.Fatalf("InitLearnedPatterns() error = %v", err)
	}
	defer store.Close()

	pattern := store.Add(
		"test-pattern",
		"test situation",
		"old approach",
		"new approach",
		"how to apply",
	)

	if pattern == nil {
		t.Fatal("Add() returned nil pattern")
	}
	if pattern.ID == "" {
		t.Error("Add() returned pattern with empty ID")
	}
	if pattern.Pattern != "test-pattern" {
		t.Errorf("Add() Pattern = %q, want %q", pattern.Pattern, "test-pattern")
	}
	if !pattern.Success {
		t.Error("Add() pattern.Success = false, want true")
	}
	if pattern.UseCount != 1 {
		t.Errorf("Add() UseCount = %d, want 1", pattern.UseCount)
	}
}

func TestLearnedPatternStore_GetAll(t *testing.T) {
	tmp := t.TempDir()
	store, err := InitLearnedPatterns(tmp)
	if err != nil {
		t.Fatalf("InitLearnedPatterns() error = %v", err)
	}
	defer store.Close()

	// Add multiple patterns
	store.Add("pattern-1", "sit1", "old1", "new1", "app1")
	store.Add("pattern-2", "sit2", "old2", "new2", "app2")

	all := store.GetAll()
	if len(all) != 2 {
		t.Errorf("GetAll() returned %d patterns, want 2", len(all))
	}
}

func TestLearnedPatternStore_GetAll_empty(t *testing.T) {
	tmp := t.TempDir()
	store, err := InitLearnedPatterns(tmp)
	if err != nil {
		t.Fatalf("InitLearnedPatterns() error = %v", err)
	}
	defer store.Close()

	all := store.GetAll()
	if all == nil {
		t.Error("GetAll() returned nil")
	}
	if len(all) != 0 {
		t.Errorf("GetAll() returned %d patterns, want 0", len(all))
	}
}

func TestLearnedPatternStore_GetRecent(t *testing.T) {
	tmp := t.TempDir()
	store, err := InitLearnedPatterns(tmp)
	if err != nil {
		t.Fatalf("InitLearnedPatterns() error = %v", err)
	}
	defer store.Close()

	// Add a pattern
	store.Add("pattern-1", "sit1", "old1", "new1", "app1")

	recent := store.GetRecent(7)
	if len(recent) == 0 {
		t.Error("GetRecent(7) returned no patterns")
	}
}

func TestLearnedPatternStore_GetRecent_noPatterns(t *testing.T) {
	tmp := t.TempDir()
	store, err := InitLearnedPatterns(tmp)
	if err != nil {
		t.Fatalf("InitLearnedPatterns() error = %v", err)
	}
	defer store.Close()

	recent := store.GetRecent(7)
	if recent == nil {
		t.Error("GetRecent() returned nil")
	}
	if len(recent) != 0 {
		t.Errorf("GetRecent() returned %d, want 0", len(recent))
	}
}

func TestLearnedPatternStore_MarkUsed(t *testing.T) {
	tmp := t.TempDir()
	store, err := InitLearnedPatterns(tmp)
	if err != nil {
		t.Fatalf("InitLearnedPatterns() error = %v", err)
	}
	defer store.Close()

	pattern := store.Add("pattern-1", "sit1", "old1", "new1", "app1")
	initialCount := pattern.UseCount

	store.MarkUsed(pattern.ID)

	if pattern.UseCount != initialCount+1 {
		t.Errorf("MarkUsed() UseCount = %d, want %d", pattern.UseCount, initialCount+1)
	}
}

func TestLearnedPatternStore_MarkUsed_nonexistent(t *testing.T) {
	tmp := t.TempDir()
	store, err := InitLearnedPatterns(tmp)
	if err != nil {
		t.Fatalf("InitLearnedPatterns() error = %v", err)
	}
	defer store.Close()

	// Should not panic
	store.MarkUsed("nonexistent-id")
}

func TestLearnedPatternStore_ExportForContext(t *testing.T) {
	tmp := t.TempDir()
	store, err := InitLearnedPatterns(tmp)
	if err != nil {
		t.Fatalf("InitLearnedPatterns() error = %v", err)
	}
	defer store.Close()

	store.Add("pattern-1", "sit1", "old1", "new1", "app1")

	result := store.ExportForContext()
	if result == "" {
		t.Error("ExportForContext() returned empty string")
	}
}

func TestLearnedPatternStore_ExportForContext_noPatterns(t *testing.T) {
	tmp := t.TempDir()
	store, err := InitLearnedPatterns(tmp)
	if err != nil {
		t.Fatalf("InitLearnedPatterns() error = %v", err)
	}
	defer store.Close()

	result := store.ExportForContext()
	if result != "" {
		t.Errorf("ExportForContext() = %q, want empty string", result)
	}
}

func TestLearnedPatternStore_Close(t *testing.T) {
	tmp := t.TempDir()
	store, err := InitLearnedPatterns(tmp)
	if err != nil {
		t.Fatalf("InitLearnedPatterns() error = %v", err)
	}

	store.Add("pattern-1", "sit1", "old1", "new1", "app1")

	if err := store.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

func TestLearnedPatternStore_MergeFrom(t *testing.T) {
	tmp1 := t.TempDir()
	tmp2 := t.TempDir()

	store1, err := InitLearnedPatterns(tmp1)
	if err != nil {
		t.Fatalf("InitLearnedPatterns() error = %v", err)
	}
	defer store1.Close()

	store2, err := InitLearnedPatterns(tmp2)
	if err != nil {
		t.Fatalf("InitLearnedPatterns() error = %v", err)
	}
	defer store2.Close()

	store1.Add("pattern-1", "sit1", "old1", "new1", "app1")
	store2.Add("pattern-2", "sit2", "old2", "new2", "app2")

	store1.MergeFrom(store2)

	all := store1.GetAll()
	if len(all) != 2 {
		t.Errorf("After MergeFrom, GetAll() returned %d, want 2", len(all))
	}
}

func TestLearnedPatternStore_MergeFrom_nil(t *testing.T) {
	tmp := t.TempDir()
	store, err := InitLearnedPatterns(tmp)
	if err != nil {
		t.Fatalf("InitLearnedPatterns() error = %v", err)
	}
	defer store.Close()

	// Should not panic
	store.MergeFrom(nil)
}

func TestLearnedPatternStore_MergeFrom_duplicate(t *testing.T) {
	tmp1 := t.TempDir()
	tmp2 := t.TempDir()

	store1, _ := InitLearnedPatterns(tmp1)
	defer store1.Close()

	store2, _ := InitLearnedPatterns(tmp2)
	defer store2.Close()

	// MergeFrom dedupes on ID. Use Add for store1's entry, then inject
	// a pattern with the SAME ID directly into store2 (Add always
	// generates a fresh unique ID, so we can't force a collision
	// through the public surface without bypassing the dedup).
	first := store1.Add("pattern-1", "sit1", "old1", "new1", "app1")
	store2.mu.Lock()
	store2.patterns[first.ID] = &LearnedPattern{
		ID:          first.ID,
		Date:        first.Date,
		Pattern:     "different name same id",
		Application: "store2 wins on its own but loses on merge",
		Success:     true,
		UseCount:    1,
	}
	store2.mu.Unlock()

	store1.MergeFrom(store2)

	all := store1.GetAll()
	if len(all) != 1 {
		t.Errorf("MergeFrom with duplicate ID: got %d patterns, want 1", len(all))
	}
	if all[0].Pattern != "pattern-1" {
		t.Errorf("local pattern should take precedence on dup ID, got Pattern=%q", all[0].Pattern)
	}
}

func TestLearnedPatternStore_path(t *testing.T) {
	tmp := t.TempDir()
	store, err := InitLearnedPatterns(tmp)
	if err != nil {
		t.Fatalf("InitLearnedPatterns() error = %v", err)
	}
	defer store.Close()

	p := store.path()
	expected := filepath.Join(tmp, "learned_patterns", "patterns.jsonl")
	if p != expected {
		t.Errorf("path() = %q, want %q", p, expected)
	}
}

func TestLearnedPatternStore_saveAndLoad(t *testing.T) {
	tmp := t.TempDir()
	store, err := InitLearnedPatterns(tmp)
	if err != nil {
		t.Fatalf("InitLearnedPatterns() error = %v", err)
	}

	store.Add("pattern-1", "sit1", "old1", "new1", "app1")
	store.Add("pattern-2", "sit2", "old2", "new2", "app2")
	store.Close()

	// Create new store from same directory
	store2, err := InitLearnedPatterns(tmp)
	if err != nil {
		t.Fatalf("InitLearnedPatterns() second call error = %v", err)
	}
	defer store2.Close()

	all := store2.GetAll()
	if len(all) < 1 {
		t.Errorf("After save and reload, GetAll() returned %d patterns", len(all))
	}
}

func TestLearnedPattern_Fields(t *testing.T) {
	now := time.Now().UTC()
	pattern := &LearnedPattern{
		ID:          "test-id",
		Date:        now.Format("2006-01-02"),
		Pattern:     "test pattern",
		Situation:   "test situation",
		OldApproach: "old",
		NewApproach: "new",
		Application: "apply like this",
		Success:     true,
		LastUsed:    now.Format(time.RFC3339),
		UseCount:    5,
	}

	if pattern.ID != "test-id" {
		t.Errorf("ID = %q, want %q", pattern.ID, "test-id")
	}
	if pattern.Pattern != "test pattern" {
		t.Errorf("Pattern = %q, want %q", pattern.Pattern, "test pattern")
	}
	if pattern.UseCount != 5 {
		t.Errorf("UseCount = %d, want 5", pattern.UseCount)
	}
}
