package memory

import (
	"testing"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// TestSearchNegativeLimitDoesNotPanic guards against a crash on a negative
// limit. List() clamps limit<=0 to a default, but Search() did not — it ran
// `make([]types.MemoryEntry, 0, limit)` directly, which panics with
// "makeslice: cap out of range" for a negative cap. The limit reaches Search
// straight from the `dfmc memory search --limit <n>` flag (a plain int with no
// lower bound), so `--limit -1` crashed the CLI with a stack trace instead of
// returning a graceful (empty) result.
func TestSearchNegativeLimitDoesNotPanic(t *testing.T) {
	s := New(nil) // nil storage is fine: the panic was before any DB access

	for _, lim := range []int{-1, -1000, 0} {
		// A non-empty query takes the make()/loop path (an empty query
		// delegates to List, which already clamps).
		if _, err := s.Search("anything", types.MemoryEpisodic, lim, ""); err != nil {
			t.Fatalf("Search(limit=%d) returned error: %v", lim, err)
		}
	}
}
