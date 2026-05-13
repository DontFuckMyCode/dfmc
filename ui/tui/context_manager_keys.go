package tui

// context_manager_keys.go — keyboard routers for the Context Manager
// sub-view (the interactive message selector/deleter inside the Context
// panel). Companion of context_manager.go which owns the state types,
// activation/refresh logic, and render helpers.
//
// handleContextManagerKey dispatches arrow navigation, space-mark,
// delete, select-all, enter-confirm, and esc-back keys while the
// manager sub-view is active.

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// handleContextManagerKey routes keys while the interactive Context
// Manager sub-view is active. Returns (newModel, cmd, handled).
func (m Model) handleContextManagerKey(msg tea.KeyMsg) (Model, tea.Cmd, bool) {
	if !m.contextPanel.manager.active {
		return m, nil, false
	}

	mgr := &m.contextPanel.manager
	rowCount := len(mgr.rows)

	switch msg.String() {
	case "esc":
		if mgr.confirmDelete {
			mgr.confirmDelete = false
			mgr.statusMsg = "delete cancelled"
			return m, nil, true
		}
		m = m.deactivateContextManager()
		return m, nil, true

	case "up", "k":
		if mgr.cursor > 0 {
			mgr.cursor--
		}
		return m, nil, true

	case "down", "j":
		if mgr.cursor < rowCount-1 {
			mgr.cursor++
		}
		return m, nil, true

	case "pgup":
		mgr.cursor = maxInt(0, mgr.cursor-10)
		return m, nil, true

	case "pgdown":
		mgr.cursor = minInt(rowCount-1, mgr.cursor+10)
		return m, nil, true

	case "home", "g":
		mgr.cursor = 0
		return m, nil, true

	case "end", "G":
		if rowCount > 0 {
			mgr.cursor = rowCount - 1
		}
		return m, nil, true

	case " ":
		// Toggle mark on current row
		if rowCount > 0 {
			mgr.marked[mgr.cursor] = !mgr.marked[mgr.cursor]
			if !mgr.marked[mgr.cursor] {
				delete(mgr.marked, mgr.cursor)
			}
			// Auto-advance
			if mgr.cursor < rowCount-1 {
				mgr.cursor++
			}
		}
		return m, nil, true

	case "p":
		if rowCount > 0 && mgr.cursor >= 0 && mgr.cursor < rowCount {
			row := mgr.rows[mgr.cursor]
			id := strings.TrimSpace(row.id)
			if id != "" && id != "(unset)" {
				if mgr.pinned == nil {
					mgr.pinned = make(map[string]bool)
				}
				mgr.pinned[id] = !mgr.pinned[id]
				if !mgr.pinned[id] {
					delete(mgr.pinned, id)
				}
				delete(mgr.marked, mgr.cursor)
				m = m.refreshContextManager()
				m.contextPanel.manager.statusMsg = "pin toggled for " + id
			}
		}
		return m, nil, true

	case "K":
		if rowCount > 0 && mgr.cursor >= 0 && mgr.cursor < rowCount {
			row := mgr.rows[mgr.cursor]
			id := strings.TrimSpace(row.id)
			if id != "" && id != "(unset)" {
				if mgr.kept == nil {
					mgr.kept = make(map[string]bool)
				}
				mgr.kept[id] = !mgr.kept[id]
				if !mgr.kept[id] {
					delete(mgr.kept, id)
				}
				delete(mgr.marked, mgr.cursor)
				m = m.refreshContextManager()
				m.contextPanel.manager.statusMsg = "keep toggled for " + id
			}
		}
		return m, nil, true

	case "C":
		count := 0
		for i, row := range mgr.rows {
			if row.action == "compact" || row.action == "drop" {
				if mgr.pinned[row.id] || mgr.kept[row.id] {
					continue
				}
				mgr.marked[i] = true
				count++
			}
		}
		mgr.statusMsg = fmt.Sprintf("%d compact/drop candidate(s) marked", count)
		return m, nil, true

	case "a":
		// Toggle all: if all marked, unmark all; else mark all
		allMarked := len(mgr.marked) == rowCount && rowCount > 0
		if allMarked {
			mgr.marked = make(map[int]bool)
			mgr.statusMsg = "all unmarked"
		} else {
			for i := 0; i < rowCount; i++ {
				mgr.marked[i] = true
			}
			mgr.statusMsg = fmt.Sprintf("all %d marked", rowCount)
		}
		return m, nil, true

	case "x", "d":
		// Initiate delete for marked (or cursor) messages
		ids := m.collectDeleteIDs()
		if len(ids) == 0 {
			mgr.statusMsg = "nothing selected \u2014 use space to mark messages"
			return m, nil, true
		}
		mgr.confirmDelete = true
		mgr.statusMsg = fmt.Sprintf("press Enter to delete %d message(s), Esc to cancel", len(ids))
		return m, nil, true

	case "D":
		// Quick-delete: delete the single message under cursor
		if rowCount == 0 || mgr.cursor < 0 || mgr.cursor >= rowCount {
			return m, nil, true
		}
		row := mgr.rows[mgr.cursor]
		id := strings.TrimSpace(row.id)
		if id == "" || id == "(unset)" {
			mgr.statusMsg = "message has no ID \u2014 cannot delete"
			return m, nil, true
		}
		if m.eng == nil || m.eng.Conversation == nil {
			mgr.statusMsg = "engine not available"
			return m, nil, true
		}
		dropped := m.eng.Conversation.RemoveMessagesByID([]string{id})
		mgr.statusMsg = fmt.Sprintf("deleted message #%d (id=%s, dropped=%d)", row.index, id, dropped)
		m = m.refreshContextManager()
		return m, nil, true

	case "enter":
		if mgr.confirmDelete {
			m = m.deleteContextManagerSelected()
			return m, nil, true
		}
		// If not confirming, treat enter as toggle mark
		if rowCount > 0 {
			mgr.marked[mgr.cursor] = !mgr.marked[mgr.cursor]
			if !mgr.marked[mgr.cursor] {
				delete(mgr.marked, mgr.cursor)
			}
		}
		return m, nil, true
	}

	return m, nil, false
}

// openContextManagerActionMenu \u2014 arrow-driven discovery for the Context
// Manager sub-view.
func (m Model) openContextManagerActionMenu() Model {
	mgr := m.contextPanel.manager
	actions := []panelAction{
		{Label: "Toggle mark (space)", Accel: "space",
			Handler: func(m Model) (Model, tea.Cmd) {
				if len(mgr.rows) > 0 {
					m.contextPanel.manager.marked[mgr.cursor] = !m.contextPanel.manager.marked[mgr.cursor]
				}
				return m, nil
			}},
		{Label: "Pin message", Accel: "p",
			Handler: func(m Model) (Model, tea.Cmd) {
				if len(m.contextPanel.manager.rows) > 0 {
					row := m.contextPanel.manager.rows[m.contextPanel.manager.cursor]
					if m.contextPanel.manager.pinned == nil {
						m.contextPanel.manager.pinned = make(map[string]bool)
					}
					m.contextPanel.manager.pinned[row.id] = !m.contextPanel.manager.pinned[row.id]
					m = m.refreshContextManager()
				}
				return m, nil
			}},
		{Label: "Keep message", Accel: "K",
			Handler: func(m Model) (Model, tea.Cmd) {
				if len(m.contextPanel.manager.rows) > 0 {
					row := m.contextPanel.manager.rows[m.contextPanel.manager.cursor]
					if m.contextPanel.manager.kept == nil {
						m.contextPanel.manager.kept = make(map[string]bool)
					}
					m.contextPanel.manager.kept[row.id] = !m.contextPanel.manager.kept[row.id]
					m = m.refreshContextManager()
				}
				return m, nil
			}},
		{Label: "Mark compact/drop candidates", Accel: "C",
			Handler: func(m Model) (Model, tea.Cmd) {
				count := 0
				for i, row := range m.contextPanel.manager.rows {
					if row.action == "compact" || row.action == "drop" {
						if m.contextPanel.manager.pinned[row.id] || m.contextPanel.manager.kept[row.id] {
							continue
						}
						m.contextPanel.manager.marked[i] = true
						count++
					}
				}
				m.contextPanel.manager.statusMsg = fmt.Sprintf("%d compact/drop candidate(s) marked", count)
				return m, nil
			}},
		{Label: "Select all / deselect all", Accel: "a",
			Handler: func(m Model) (Model, tea.Cmd) {
				allMarked := len(m.contextPanel.manager.marked) == len(m.contextPanel.manager.rows)
				if allMarked {
					m.contextPanel.manager.marked = make(map[int]bool)
				} else {
					for i := 0; i < len(m.contextPanel.manager.rows); i++ {
						m.contextPanel.manager.marked[i] = true
					}
				}
				return m, nil
			}},
		{Label: "Delete selected messages", Accel: "x",
			Handler: func(m Model) (Model, tea.Cmd) {
				ids := m.collectDeleteIDs()
				if len(ids) > 0 {
					m.contextPanel.manager.confirmDelete = true
				}
				return m, nil
			}},
		{Label: "Delete message under cursor", Accel: "D",
			Handler: func(m Model) (Model, tea.Cmd) {
				if len(m.contextPanel.manager.rows) > 0 {
					row := m.contextPanel.manager.rows[m.contextPanel.manager.cursor]
					if m.eng != nil && m.eng.Conversation != nil {
						id := strings.TrimSpace(row.id)
						if id != "" && id != "(unset)" {
							m.eng.Conversation.RemoveMessagesByID([]string{id})
							m = m.refreshContextManager()
						}
					}
				}
				return m, nil
			}},
		{Label: "Back to Context view", Accel: "esc",
			Handler: func(m Model) (Model, tea.Cmd) {
				return m.deactivateContextManager(), nil
			}},
	}
	return m.openActionMenu("CtxMgr", "Context Manager", actions)
}
