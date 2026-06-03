package drive

import (
	"strings"
	"testing"
)

// FuzzScopeConflictSafety is the property-based counterpart to the
// containment fix (#63). The core safety invariant of readyBatch is: two
// TODOs whose file scopes touch the same filesystem region must never be
// dispatched in the same batch ("worst case is sequential, never racy").
// We fuzz that directly — for any pair of scope strings whose normalized
// forms overlap as paths, readyBatch must pick at most one. A violation is
// a real race (two goroutines mutating the same file).
func FuzzScopeConflictSafety(f *testing.F) {
	seeds := [][2]string{
		{"internal/auth", "internal/auth/service.go"},
		{"a/b", "a/b/c"},
		{"./pkg/foo.go", "pkg/foo.go"},
		{"pkg\\foo.go", "pkg/foo.go"},
		{".", "anything/here.go"},
		{"internal/auth", "internal/authz/service.go"},
		{"a/b/../c.go", "a/c.go"},
		{"", "x.go"},
		{"x.go", "x.go"},
	}
	for _, s := range seeds {
		f.Add(s[0], s[1])
	}

	f.Fuzz(func(t *testing.T, sa, sb string) {
		na, nb := normalizeScope(sa), normalizeScope(sb)
		// Empty normalized scope == scopeAny ("owns everything"); that path
		// has its own exclusivity rule covered elsewhere. Restrict the
		// property to concrete, non-empty paths.
		if na == "" || nb == "" {
			return
		}

		todos := []Todo{
			{ID: "A", Status: TodoPending, FileScope: []string{sa}},
			{ID: "B", Status: TodoPending, FileScope: []string{sb}},
		}
		picks := readyBatch(todos, 5)

		if pathsOverlap(na, nb) && len(picks) > 1 {
			t.Fatalf("overlapping scopes %q (%q) and %q (%q) were BOTH picked %v — readyBatch must serialize them",
				sa, na, sb, nb, picks)
		}
	})
}

// FuzzNormalizeScopeIdempotent pins that normalizeScope is a true
// canonicalization: applying it twice yields the same result. If a second
// pass changed the value, the scopeSet keys built from once-normalized
// paths would not match candidate paths normalized the same way, silently
// reopening the conflict-miss the normalize pass exists to close.
func FuzzNormalizeScopeIdempotent(f *testing.F) {
	for _, s := range []string{"a/b.go", "./a/b.go", "a//b.go", "a/../b.go", "a\\b.go", ".", "./", "", "/", "a/b/"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		once := normalizeScope(s)
		twice := normalizeScope(once)
		if once != twice {
			t.Fatalf("normalizeScope not idempotent: %q -> %q -> %q", s, once, twice)
		}
		// A normalized path must never carry a backslash or a trailing
		// slash (the two shapes the conflict check would otherwise miss).
		if strings.Contains(once, "\\") {
			t.Fatalf("normalizeScope(%q)=%q still contains a backslash", s, once)
		}
		if len(once) > 1 && strings.HasSuffix(once, "/") {
			t.Fatalf("normalizeScope(%q)=%q has a trailing slash", s, once)
		}
	})
}

// FuzzPathsOverlapSymmetric pins that overlap detection is order-independent.
// scopeConflicts compares candidate paths against held paths in one
// direction only per call, but the running-vs-picked and picked-vs-candidate
// checks rely on the relation being symmetric — A conflicts with B iff B
// conflicts with A. An asymmetry would let a conflict slip through depending
// on which TODO happened to start first.
func FuzzPathsOverlapSymmetric(f *testing.F) {
	for _, s := range [][2]string{{"a", "a/b"}, {"a/b", "a"}, {".", "x"}, {"a", "ab"}, {"a/b", "a/c"}} {
		f.Add(s[0], s[1])
	}
	f.Fuzz(func(t *testing.T, a, b string) {
		na, nb := normalizeScope(a), normalizeScope(b)
		if na == "" || nb == "" {
			return
		}
		if pathsOverlap(na, nb) != pathsOverlap(nb, na) {
			t.Fatalf("pathsOverlap asymmetric for %q vs %q", na, nb)
		}
		if !pathsOverlap(na, na) {
			t.Fatalf("pathsOverlap not reflexive for %q", na)
		}
	})
}
