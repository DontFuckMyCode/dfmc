package drive

import (
	"testing"
)

// TestNormalizeScope pins the equivalence classes the scheduler relies
// on to detect parallel file-scope conflicts. Before this pass, TODOs
// that declared "./pkg/foo.go" and "pkg/foo.go" (or "pkg\\foo.go" on
// a Windows planner run) were treated as disjoint and could race on
// the same file.
func TestNormalizeScope(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"main.go", "main.go"},
		{"./main.go", "main.go"},
		{"pkg/foo.go", "pkg/foo.go"},
		{"./pkg/foo.go", "pkg/foo.go"},
		{"pkg//foo.go", "pkg/foo.go"},
		{"pkg/./foo.go", "pkg/foo.go"},
		{"pkg/bar/../foo.go", "pkg/foo.go"},
		{"pkg\\foo.go", "pkg/foo.go"},
		{"./pkg\\foo.go", "pkg/foo.go"},
		{".", "."},
		{"./", "."},
	}
	for _, tc := range cases {
		if got := normalizeScope(tc.in); got != tc.want {
			t.Errorf("normalizeScope(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestSchedulerDetectsEquivalentPathConflict is the behavioural
// counterpart: two pending TODOs with semantically-equal but
// textually-different FileScope entries must NOT be scheduled
// together. The scheduler picks one and defers the other; the exact
// pick order is implementation-defined but at most one of the pair
// can be in the returned batch.
func TestSchedulerDetectsEquivalentPathConflict(t *testing.T) {
	cases := []struct {
		label string
		a, b  string
	}{
		{"dot-slash vs plain", "./main.go", "main.go"},
		{"backslash vs slash", "pkg\\foo.go", "pkg/foo.go"},
		{"double-slash vs single", "pkg//foo.go", "pkg/foo.go"},
		{"traversal vs direct", "pkg/bar/../foo.go", "pkg/foo.go"},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			todos := []Todo{
				{ID: "T1", Status: TodoPending, FileScope: []string{tc.a}},
				{ID: "T2", Status: TodoPending, FileScope: []string{tc.b}},
			}
			picks := readyBatch(todos, 5)
			if len(picks) != 1 {
				t.Fatalf("expected exactly 1 pick for conflicting equivalents (%q vs %q), got %d: %v",
					tc.a, tc.b, len(picks), picks)
			}
		})
	}
}
