package drive

import (
	"testing"
)

// TestReadyBatch_ScopeConflict verifies that two ready TODOs with
// overlapping FileScope cannot both be picked in the same batch.
// a and b both touch internal/foo.go — only one can be picked.
// c touches internal/bar.go (different from a/b) — c can join the batch.
func TestReadyBatch_ScopeConflict(t *testing.T) {
	todos := []Todo{
		{ID: "a", Status: TodoPending, FileScope: []string{"internal/foo.go"}},
		{ID: "b", Status: TodoPending, FileScope: []string{"internal/foo.go"}},
		{ID: "c", Status: TodoPending, FileScope: []string{"internal/bar.go"}},
	}
	got := readyBatch(todos, 3)
	// a and c don't conflict, b conflicts with a — expect a and c.
	if len(got) != 2 {
		t.Fatalf("expected 2 picked (a + c), got %d: %v", len(got), got)
	}
	pickedIDs := map[string]bool{}
	for _, idx := range got {
		pickedIDs[todos[idx].ID] = true
	}
	if !pickedIDs["a"] || !pickedIDs["c"] {
		t.Fatalf("expected a and c picked, got: %v", got)
	}
}

// TestReadyBatch_UnscopedRunsAlone verifies that an empty FileScope
// TODO (non-read-only) gets an exclusive slot and runs alone in its
// batch even when other scoped TODOs are ready.
func TestReadyBatch_UnscopedRunsAlone(t *testing.T) {
	todos := []Todo{
		{ID: "unscoped", Status: TodoPending, FileScope: nil},
		{ID: "scoped1", Status: TodoPending, FileScope: []string{"pkg/a.go"}},
		{ID: "scoped2", Status: TodoPending, FileScope: []string{"pkg/b.go"}},
	}
	got := readyBatch(todos, 3)
	if len(got) != 1 {
		t.Fatalf("unscoped should run alone, got %d: %v", len(got), got)
	}
	if todos[got[0]].ID != "unscoped" {
		t.Fatalf("expected unscoped picked alone, got %q", todos[got[0]].ID)
	}
}

// TestReadyBatch_MultipleUnscoped verifies that two empty-scope TODOs
// do not conflict with each other (they both claim scopeAny, but since
// neither is read-only they must still be serialized). This tests the
// "unscoped runs alone" rule applies to the batch: only one unscoped
// per batch, but subsequent batches can pick the next unscoped.
func TestReadyBatch_MultipleUnscoped(t *testing.T) {
	todos := []Todo{
		{ID: "unscoped1", Status: TodoPending, FileScope: nil},
		{ID: "unscoped2", Status: TodoPending, FileScope: nil},
		{ID: "scoped1", Status: TodoPending, FileScope: []string{"pkg/a.go"}},
	}
	// First batch: picks unscoped1 alone.
	got := readyBatch(todos, 3)
	if len(got) != 1 || todos[got[0]].ID != "unscoped1" {
		t.Fatalf("first batch: expected unscoped1, got %v", got)
	}
}

// TestReadyBatch_ScopedParallelism verifies that two scoped TODOs
// with non-overlapping scopes can run in the same batch.
func TestReadyBatch_ScopedParallelism(t *testing.T) {
	todos := []Todo{
		{ID: "read", Status: TodoPending, FileScope: []string{"pkg/a.go"}},
		{ID: "write", Status: TodoPending, FileScope: []string{"pkg/b.go"}},
	}
	got := readyBatch(todos, 3)
	if len(got) != 2 {
		t.Fatalf("expected 2 (non-overlapping scopes), got %d: %v", len(got), got)
	}
}

// TestReadyBatch_AllDone returns empty when all TODOs are done.
func TestReadyBatch_AllDone(t *testing.T) {
	todos := []Todo{
		{ID: "done1", Status: TodoDone},
		{ID: "done2", Status: TodoDone},
	}
	got := readyBatch(todos, 3)
	if len(got) != 0 {
		t.Fatalf("expected 0 when all done, got %v", got)
	}
}

// TestReadyBatch_BlockedDeps returns empty when remaining TODOs
// have Blocked deps.
func TestReadyBatch_BlockedDeps(t *testing.T) {
	todos := []Todo{
		{ID: "blocked", Status: TodoBlocked, DependsOn: []string{"missing"}},
	}
	got := readyBatch(todos, 3)
	if len(got) != 0 {
		t.Fatalf("expected 0 when blocked, got %v", got)
	}
}

// TestReadyBatch_SkippedDepsAreNotDone verifies that a TODO depending
// on a Skipped TODO is not considered ready (Skipped is not Done).
func TestReadyBatch_SkippedDepsAreNotDone(t *testing.T) {
	todos := []Todo{
		{ID: "skipped1", Status: TodoSkipped},
		{ID: "depends", Status: TodoPending, DependsOn: []string{"skipped1"}},
	}
	got := readyBatch(todos, 3)
	if len(got) != 0 {
		t.Fatalf("expected 0 when dep is skipped, got %v", got)
	}
}

// TestReadyBatch_ReadOnlyDoesNotClaimScope verifies that a read-only
// TODO (ReadOnly=true) with empty FileScope does NOT claim scopeAny
// and does not conflict with other TODOs.
func TestReadyBatch_ReadOnlyDoesNotClaimScope(t *testing.T) {
	todos := []Todo{
		{ID: "readonly", Status: TodoPending, FileScope: nil, ReadOnly: true},
		{ID: "writer", Status: TodoPending, FileScope: []string{"pkg/a.go"}},
	}
	got := readyBatch(todos, 3)
	// Both should fit in the same batch: readonly doesn't claim scopeAny.
	if len(got) != 2 {
		t.Fatalf("expected 2 (readonly + writer), got %d: %v", len(got), got)
	}
}