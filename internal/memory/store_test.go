package memory

import (
	"path/filepath"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/storage"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func TestMemoryAddListSearchClear(t *testing.T) {
	dir := t.TempDir()
	st, err := storage.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	m := New(st)
	if err := m.Add(types.MemoryEntry{
		Tier:       types.MemoryEpisodic,
		Category:   "interaction",
		Key:        "auth question",
		Value:      "auth answer",
		Confidence: 0.8,
		Project:    "test-project",
	}); err != nil {
		t.Fatalf("add entry: %v", err)
	}

	list, err := m.List(types.MemoryEpisodic, 10, "test-project")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) == 0 {
		t.Fatal("expected at least one memory entry")
	}

	search, err := m.Search("auth", types.MemoryEpisodic, 10, "test-project")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(search) == 0 {
		t.Fatal("expected search hits")
	}

	if err := m.Clear(types.MemoryEpisodic); err != nil {
		t.Fatalf("clear: %v", err)
	}
	after, err := m.List(types.MemoryEpisodic, 10, "test-project")
	if err != nil {
		t.Fatalf("list after clear: %v", err)
	}
	if len(after) != 0 {
		t.Fatalf("expected 0 after clear, got %d", len(after))
	}
}
