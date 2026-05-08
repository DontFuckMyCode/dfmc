package engine

// engine_passthrough_memory.go — thin wrappers around the Memory
// subsystem. Every method nil-checks e.Memory because the engine can
// run in degraded-storage mode (see ErrStoreLocked handling in
// cmd/dfmc/main.go) where Memory is intentionally absent.

import (
	"fmt"

	"github.com/dontfuckmycode/dfmc/internal/memory"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func (e *Engine) MemoryWorking() memory.WorkingMemory {
	if e.Memory == nil {
		return memory.WorkingMemory{}
	}
	return e.Memory.Working()
}

func (e *Engine) MemoryList(tier types.MemoryTier, limit int) ([]types.MemoryEntry, error) {
	if e.Memory == nil {
		return nil, fmt.Errorf("memory store is not initialized")
	}
	return e.Memory.List(tier, limit, e.ProjectRoot)
}

func (e *Engine) MemorySearch(query string, tier types.MemoryTier, limit int) ([]types.MemoryEntry, error) {
	if e.Memory == nil {
		return nil, fmt.Errorf("memory store is not initialized")
	}
	return e.Memory.Search(query, tier, limit, e.ProjectRoot)
}

func (e *Engine) MemoryAdd(entry types.MemoryEntry) error {
	if e.Memory == nil {
		return fmt.Errorf("memory store is not initialized")
	}
	if entry.Project == "" {
		entry.Project = e.ProjectRoot
	}
	return e.Memory.Add(entry)
}

func (e *Engine) MemoryClear(tier types.MemoryTier) error {
	if e.Memory == nil {
		return fmt.Errorf("memory store is not initialized")
	}
	return e.Memory.Clear(tier)
}

// MemoryDelete removes a single entry by ID. Walks both episodic and
// semantic tiers; missing IDs are not errors so callers can treat
// "already gone" as success. Phase H item 1 surface for the TUI panel.
func (e *Engine) MemoryDelete(id string) error {
	if e.Memory == nil {
		return fmt.Errorf("memory store is not initialized")
	}
	return e.Memory.Delete(id)
}

// MemoryUpdate edits the human-editable fields (key/value/category) on
// an existing entry. Tier and Project stay immutable through this path
// — promote moves between tiers, and Project is the bbolt-level scope.
func (e *Engine) MemoryUpdate(id, key, value, category string) error {
	if e.Memory == nil {
		return fmt.Errorf("memory store is not initialized")
	}
	return e.Memory.Update(id, key, value, category)
}

// MemoryPromote graduates an entry from episodic into semantic. No-op
// when the entry is already semantic; errors when the ID isn't found.
func (e *Engine) MemoryPromote(id string) error {
	if e.Memory == nil {
		return fmt.Errorf("memory store is not initialized")
	}
	return e.Memory.Promote(id)
}
