package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func (m Model) handleMemoryKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.memory.searchActive {
		return m.handleMemorySearchKey(msg)
	}
	if nm, cmd, handled := m.handleActionMenuKey(msg); handled {
		return nm, cmd
	}
	if s := msg.String(); s == "right" || s == "l" {
		return m.openMemoryActionMenu(), nil
	}

	total := len(filteredMemoryEntries(m.memory.entries, m.memory.query))
	switch msg.String() {
	case "j", "down":
		m.memory.scroll = scrollIndexDown(m.memory.scroll, total, 1)
	case "k", "up":
		m.memory.scroll = scrollIndexUp(m.memory.scroll, 1)
	case "pgdown":
		m.memory.scroll = scrollIndexDown(m.memory.scroll, total, 10)
	case "pgup":
		m.memory.scroll = scrollIndexUp(m.memory.scroll, 10)
	case "g":
		m.memory.scroll = 0
	case "G":
		m.memory.scroll = lastScrollIndex(total)
	case "enter":
		return m.toggleSelectedMemoryExpansion(), nil
	case "t":
		m.memory.tier = nextMemoryTier(m.memory.tier)
		m.memory.scroll = 0
		m.memory.loading = true
		m.memory.err = ""
		return m, loadMemoryCmd(m.eng, m.memory.tier)
	case "r":
		m.memory.loading = true
		m.memory.err = ""
		return m, loadMemoryCmd(m.eng, m.memory.tier)
	case "/":
		m.memory.searchActive = true
	case "c":
		m.memory.query = ""
		m.memory.scroll = 0
	case "d":
		return m.deleteSelectedMemoryEntry()
	case "p":
		return m.promoteSelectedMemoryEntry()
	}
	return m, nil
}

func (m Model) toggleSelectedMemoryExpansion() Model {
	entry, ok := m.selectedMemoryEntry()
	if !ok {
		return m
	}
	if m.memory.expandedID == entry.ID {
		m.memory.expandedID = ""
	} else {
		m.memory.expandedID = entry.ID
	}
	return m
}

func (m Model) handleMemorySearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		m.memory.searchActive = false
		m.memory.scroll = 0
	case tea.KeyEsc:
		m.memory.searchActive = false
	default:
		if query, ok := applyInlineSearchTextKey(m.memory.query, msg); ok {
			m.memory.query = query
		}
	}
	return m, nil
}

func nextMemoryTier(current string) string {
	switch current {
	case "", string(types.MemoryWorking):
		return string(types.MemoryEpisodic)
	case string(types.MemoryEpisodic):
		return string(types.MemorySemantic)
	case string(types.MemorySemantic):
		return memoryTierAll
	default:
		return string(types.MemoryWorking)
	}
}
