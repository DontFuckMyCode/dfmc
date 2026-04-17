// Regression test for L2: codemap cycle paths must be bounded so a
// pathological dependency chain doesn't balloon the snapshot.

package tui

import (
	"strings"
	"testing"
)

func TestTruncateCyclePath_PassesShortPathsThrough(t *testing.T) {
	short := []string{"a", "b", "c"}
	got := truncateCyclePath(short, 8)
	if len(got) != len(short) {
		t.Fatalf("short path mutated: got=%v", got)
	}
	for i := range got {
		if got[i] != short[i] {
			t.Fatalf("short path contents differ at %d: %q vs %q", i, got[i], short[i])
		}
	}
}

func TestTruncateCyclePath_CollapsesLongPath(t *testing.T) {
	long := make([]string, 200)
	for i := range long {
		long[i] = "node_" + itoa3(i)
	}
	got := truncateCyclePath(long, 32)
	if len(got) != 32 {
		t.Fatalf("want len 32, got %d", len(got))
	}
	// Endpoints preserved.
	if got[0] != long[0] {
		t.Fatalf("head not preserved: got %q want %q", got[0], long[0])
	}
	if got[len(got)-1] != long[len(long)-1] {
		t.Fatalf("tail not preserved: got %q want %q", got[len(got)-1], long[len(long)-1])
	}
	// An ellipsis marker sits somewhere in the middle.
	foundEllipsis := false
	for _, v := range got {
		if v == "…" {
			foundEllipsis = true
			break
		}
	}
	if !foundEllipsis {
		t.Fatalf("middle ellipsis missing: %v", got)
	}
}

func TestTruncateCyclePath_HandlesEdgeCases(t *testing.T) {
	if got := truncateCyclePath(nil, 5); got != nil {
		t.Fatalf("nil input must pass through, got %v", got)
	}
	if got := truncateCyclePath([]string{"x"}, 0); len(got) != 1 {
		t.Fatalf("zero limit should be a no-op (defensive), got %v", got)
	}
}

// itoa3 formats small ints without importing strconv — keeps this
// test file dependency-free.
func itoa3(n int) string {
	if n < 0 {
		return "-" + itoa3(-n)
	}
	if n < 10 {
		return string(rune('0' + n))
	}
	return itoa3(n/10) + itoa3(n%10)
}

// Guard: if someone removes the ellipsis marker by accident, the
// renderer could show a truncated cycle as a normal one. Pin the
// marker value.
func TestTruncateCyclePath_EllipsisIsSingleRune(t *testing.T) {
	long := make([]string, 100)
	for i := range long {
		long[i] = "n"
	}
	got := truncateCyclePath(long, 10)
	marker := ""
	for _, v := range got {
		if strings.Contains(v, "…") {
			marker = v
			break
		}
	}
	if marker != "…" {
		t.Fatalf("ellipsis marker changed: got %q", marker)
	}
}
