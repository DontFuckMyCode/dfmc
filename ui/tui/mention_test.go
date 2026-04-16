package tui

import (
	"strings"
	"testing"
)

func TestMentionRanker_PrefersExactBasenameAndBoostsRecency(t *testing.T) {
	files := []string{
		"internal/util/pkg.go",
		"ui/cli/utilities.go",
		"pkg/types/util_test.go",
		"util.go",
	}
	recent := []string{"pkg/types/util_test.go"}

	ranker := newMentionRanker(files, recent)
	got := ranker.rank("util", 5)
	if len(got) == 0 {
		t.Fatalf("expected matches, got none")
	}
	// util.go is the exact basename match so it should always be #1.
	if got[0].path != "util.go" {
		t.Fatalf("expected util.go first (exact basename), got %q", got[0].path)
	}
	// util_test.go has recency bonus; it should beat internal/util/pkg.go
	// (which has a higher raw score but no recency).
	rankOf := func(path string) int {
		for i, c := range got {
			if c.path == path {
				return i
			}
		}
		return -1
	}
	if rankOf("pkg/types/util_test.go") < 0 {
		t.Fatalf("recent file missing from ranking: %v", got)
	}
	if rankOf("pkg/types/util_test.go") > rankOf("ui/cli/utilities.go") {
		t.Fatalf("recency bonus did not lift util_test.go above utilities.go: %v", got)
	}
}

func TestMentionRanker_SubsequenceMatching(t *testing.T) {
	files := []string{
		"ui/tui/tui.go",
		"internal/engine/engine.go",
		"pkg/types/types.go",
	}
	got := newMentionRanker(files, nil).rank("eeng", 5)
	if len(got) == 0 || got[0].path != "internal/engine/engine.go" {
		t.Fatalf("expected subsequence match to hit engine.go, got %+v", got)
	}
}

func TestMentionRanker_EmptyQueryReturnsRecentFirst(t *testing.T) {
	files := []string{
		"a.go",
		"b.go",
		"c.go",
	}
	recent := []string{"c.go", "b.go"}
	got := newMentionRanker(files, recent).rank("", 3)
	if len(got) != 3 {
		t.Fatalf("expected all three files back, got %d", len(got))
	}
	if got[0].path != "c.go" {
		t.Fatalf("expected c.go first (most recent), got %q", got[0].path)
	}
	if got[1].path != "b.go" {
		t.Fatalf("expected b.go second (next recent), got %q", got[1].path)
	}
}

func TestResolveMentionQuery_ConfidenceFloor(t *testing.T) {
	files := []string{"apps/web/frontend/src/components/Navbar.tsx"}

	// Tight substring — should resolve.
	if path, ok := resolveMentionQuery(files, nil, "Navbar"); !ok || !strings.HasSuffix(path, "Navbar.tsx") {
		t.Fatalf("expected Navbar to resolve, got %q, %v", path, ok)
	}

	// Gibberish that happens to share a character — subsequence match scores
	// below the 400 floor, so we should leave the literal @xyz alone.
	if _, ok := resolveMentionQuery(files, nil, "zq"); ok {
		t.Fatalf("expected low-confidence match to be rejected")
	}
}

func TestExpandAtFileMentionsWithRecent_PicksBestOnAmbiguity(t *testing.T) {
	files := []string{
		"internal/memory/store.go",
		"internal/storage/store.go",
	}
	// Recency should pick storage over memory even though they tie on score.
	out := expandAtFileMentionsWithRecent("please read @store", files, []string{"internal/storage/store.go"})
	if !strings.Contains(out, "internal/storage/store.go") {
		t.Fatalf("expected storage/store.go (recent) to win, got %q", out)
	}
	// Without recency, ties break on alphabetical — memory wins.
	out2 := expandAtFileMentionsWithRecent("please read @store", files, nil)
	if !strings.Contains(out2, "internal/memory/store.go") {
		t.Fatalf("expected memory/store.go (alphabetical tiebreak), got %q", out2)
	}
}
