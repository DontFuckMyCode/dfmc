package drive

import "testing"

// TestSchedulerDetectsDirectoryContainmentConflict pins that a TODO scoped to
// a directory and another scoped to a file *inside* that directory are treated
// as conflicting — they must NOT run in parallel. Planners routinely emit a
// directory ("internal/auth/") for a broad TODO and a specific file
// ("internal/auth/service.go") for a narrow one (the supervisor e2e fixtures
// do exactly this). Exact-path equality misses the overlap, which would let
// the scheduler dispatch both concurrently and race on service.go — exactly
// the corruption readyBatch's "worst case is sequential, never racy" contract
// promises to prevent.
func TestSchedulerDetectsDirectoryContainmentConflict(t *testing.T) {
	cases := []struct {
		label string
		a, b  string
	}{
		{"dir vs file inside", "internal/auth/", "internal/auth/service.go"},
		{"file inside vs dir", "internal/auth/service.go", "internal/auth/"},
		{"dir vs nested dir", "internal/auth", "internal/auth/sub"},
		{"repo root vs file", ".", "internal/auth/service.go"},
		{"dir vs deep file", "internal", "internal/auth/service.go"},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			todos := []Todo{
				{ID: "T1", Status: TodoPending, FileScope: []string{tc.a}},
				{ID: "T2", Status: TodoPending, FileScope: []string{tc.b}},
			}
			picks := readyBatch(todos, 5)
			if len(picks) != 1 {
				t.Fatalf("expected exactly 1 pick for overlapping scopes (%q vs %q), got %d: %v",
					tc.a, tc.b, len(picks), picks)
			}
		})
	}
}

// TestSchedulerAllowsDisjointSiblingFiles is the negative control: two distinct
// files under the same directory do NOT overlap and SHOULD run in parallel.
// A too-aggressive prefix check (plain string HasPrefix without a path-segment
// boundary) would wrongly serialize these, killing legitimate parallelism.
func TestSchedulerAllowsDisjointSiblingFiles(t *testing.T) {
	cases := []struct {
		label string
		a, b  string
	}{
		{"sibling files", "internal/auth/service.go", "internal/auth/middleware.go"},
		{"prefix-but-not-path", "internal/auth", "internal/authz/service.go"},
		{"shared stem", "internal/foo.go", "internal/foobar.go"},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			todos := []Todo{
				{ID: "T1", Status: TodoPending, FileScope: []string{tc.a}},
				{ID: "T2", Status: TodoPending, FileScope: []string{tc.b}},
			}
			picks := readyBatch(todos, 5)
			if len(picks) != 2 {
				t.Fatalf("expected both disjoint scopes picked (%q vs %q), got %d: %v",
					tc.a, tc.b, len(picks), picks)
			}
		})
	}
}
