package tui

// memory.go owns the Memory panel state messages, load command, action
// menu, and entry mutations. Keyboard dispatch lives in memory_keys.go;
// rendering lives in memory_render.go.

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

const (
	memoryListLimit = 200
	memoryTierAll   = "all"
)

type memoryLoadedMsg struct {
	entries []types.MemoryEntry
	tier    string
	err     error
}

func loadMemoryCmd(eng *engine.Engine, tier string) tea.Cmd {
	return func() tea.Msg {
		if eng == nil || eng.Memory == nil {
			return memoryLoadedMsg{tier: tier, err: fmt.Errorf("memory not available (engine not initialized)")}
		}
		var (
			entries []types.MemoryEntry
			err     error
		)
		switch tier {
		case string(types.MemoryEpisodic), string(types.MemorySemantic):
			entries, err = eng.MemoryList(types.MemoryTier(tier), memoryListLimit)
		default:
			tier = memoryTierAll
			ep, e1 := eng.MemoryList(types.MemoryEpisodic, memoryListLimit)
			if e1 != nil {
				return memoryLoadedMsg{tier: tier, err: e1}
			}
			sem, e2 := eng.MemoryList(types.MemorySemantic, memoryListLimit)
			if e2 != nil {
				return memoryLoadedMsg{tier: tier, err: e2}
			}
			entries = append(ep, sem...)
		}
		return memoryLoadedMsg{entries: entries, tier: tier, err: err}
	}
}

func (m Model) openMemoryActionMenu() Model {
	actions := []panelAction{
		{Label: "Cycle tier (all -> episodic -> semantic)", Accel: "t", Handler: func(m Model) (Model, tea.Cmd) {
			m.memory.tier = nextMemoryTier(m.memory.tier)
			m.memory.scroll = 0
			m.memory.loading = true
			m.memory.err = ""
			return m, loadMemoryCmd(m.eng, m.memory.tier)
		}},
		{Label: "Refresh from store", Accel: "r", Handler: func(m Model) (Model, tea.Cmd) {
			m.memory.loading = true
			m.memory.err = ""
			return m, loadMemoryCmd(m.eng, m.memory.tier)
		}},
		{Label: "Search...", Accel: "/", Handler: func(m Model) (Model, tea.Cmd) {
			m.memory.searchActive = true
			return m, nil
		}},
		{Label: "Clear search query", Accel: "c", Handler: func(m Model) (Model, tea.Cmd) {
			m.memory.query = ""
			m.memory.scroll = 0
			return m, nil
		}},
		{Label: "Delete the highlighted entry", Accel: "d", Handler: func(m Model) (Model, tea.Cmd) {
			return m.deleteSelectedMemoryEntry()
		}},
		{Label: "Promote highlighted entry to semantic tier", Accel: "p", Handler: func(m Model) (Model, tea.Cmd) {
			return m.promoteSelectedMemoryEntry()
		}},
	}
	return m.openActionMenu("Memory", "Memory actions", actions)
}

func (m Model) selectedMemoryEntry() (types.MemoryEntry, bool) {
	filtered := filteredMemoryEntries(m.memory.entries, m.memory.query)
	if len(filtered) == 0 || m.memory.scroll < 0 || m.memory.scroll >= len(filtered) {
		return types.MemoryEntry{}, false
	}
	return filtered[m.memory.scroll], true
}

func (m Model) deleteSelectedMemoryEntry() (Model, tea.Cmd) {
	entry, ok := m.selectedMemoryEntry()
	if !ok {
		m.notice = "No memory entry selected - j/k highlights a row, then d deletes it."
		return m, nil
	}
	if m.eng == nil {
		m.notice = "Engine not ready - cannot delete memory entries yet."
		return m, nil
	}
	if err := m.eng.MemoryDelete(entry.ID); err != nil {
		m.notice = "Delete failed: " + err.Error()
		return m, nil
	}
	m.memory.entries = removeMemoryEntryByID(m.memory.entries, entry.ID)
	if m.memory.scroll >= len(filteredMemoryEntries(m.memory.entries, m.memory.query)) && m.memory.scroll > 0 {
		m.memory.scroll--
	}
	if m.memory.expandedID == entry.ID {
		m.memory.expandedID = ""
	}
	m.notice = "Deleted memory entry - store updated."
	m.memory.loading = true
	return m, loadMemoryCmd(m.eng, m.memory.tier)
}

func (m Model) promoteSelectedMemoryEntry() (Model, tea.Cmd) {
	entry, ok := m.selectedMemoryEntry()
	if !ok {
		m.notice = "No memory entry selected - j/k highlights a row, then p promotes it."
		return m, nil
	}
	if entry.Tier == types.MemorySemantic {
		m.notice = "Entry is already semantic - nothing to promote."
		return m, nil
	}
	if m.eng == nil {
		m.notice = "Engine not ready - cannot promote memory entries yet."
		return m, nil
	}
	if err := m.eng.MemoryPromote(entry.ID); err != nil {
		m.notice = "Promote failed: " + err.Error()
		return m, nil
	}
	m.notice = "Promoted episodic entry to semantic tier."
	m.memory.loading = true
	return m, loadMemoryCmd(m.eng, m.memory.tier)
}

func removeMemoryEntryByID(entries []types.MemoryEntry, id string) []types.MemoryEntry {
	out := make([]types.MemoryEntry, 0, len(entries))
	for _, e := range entries {
		if e.ID == id {
			continue
		}
		out = append(out, e)
	}
	return out
}
