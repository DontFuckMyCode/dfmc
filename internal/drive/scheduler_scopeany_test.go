package drive

import "testing"

// TestReadyBatch_EmptyStringScopeIsExclusive pins that an empty-string entry
// in FileScope (the scopeAny "owns everything" sentinel) is treated as
// conflicting on the CANDIDATE side, not just the held side. A malformed-but-
// plausible planner output like file_scope:[""] must not let the TODO run
// beside a TODO it could race with.
func TestReadyBatch_EmptyStringScopeIsExclusive(t *testing.T) {
	// "scoped" is picked first (input order); "empty" claims every file via
	// the "" sentinel, so it must NOT join the same batch.
	todos := []Todo{
		{ID: "scoped", Status: TodoPending, FileScope: []string{"internal/foo.go"}},
		{ID: "empty", Status: TodoPending, FileScope: []string{""}},
	}
	got := readyBatch(todos, 3)
	if len(got) != 1 {
		ids := make([]string, len(got))
		for i, idx := range got {
			ids[i] = todos[idx].ID
		}
		t.Fatalf("empty-scope TODO must be exclusive; expected 1 picked, got %d: %v", len(got), ids)
	}
}

// TestReadyBatch_EmptyStringScopeVsRunning pins the running-conflict side: a
// candidate carrying the "" sentinel must not be dispatched while another
// TODO is Running (it could touch the same file).
func TestReadyBatch_EmptyStringScopeVsRunning(t *testing.T) {
	todos := []Todo{
		{ID: "running", Status: TodoRunning, FileScope: []string{"internal/foo.go"}},
		{ID: "empty", Status: TodoPending, FileScope: []string{""}},
	}
	got := readyBatch(todos, 3)
	if len(got) != 0 {
		t.Fatalf("empty-scope candidate must wait behind a running TODO; got %v", got)
	}
}
