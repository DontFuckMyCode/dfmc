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
		return m.handleContextManagerEscape(), nil, true

	case "up", "k":
		mgr.moveCursor(-1)
		return m, nil, true

	case "down", "j":
		mgr.moveCursor(1)
		return m, nil, true

	case "pgup":
		mgr.pageCursor(-10)
		return m, nil, true

	case "pgdown":
		mgr.pageCursor(10)
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
		mgr.toggleCurrentMark(true)
		return m, nil, true

	case "p":
		return m.toggleContextManagerPin(), nil, true

	case "K":
		return m.toggleContextManagerKeep(), nil, true

	case "C":
		mgr.markCompactDropCandidates()
		return m, nil, true

	case "a":
		mgr.toggleAllMarks()
		return m, nil, true

	case "x", "d":
		return m.beginContextManagerDelete(), nil, true

	case "D":
		return m.deleteContextManagerCursor(), nil, true

	case "enter":
		return m.handleContextManagerEnter(), nil, true
	}

	return m, nil, false
}

// openContextManagerActionMenu \u2014 arrow-driven discovery for the Context
// Manager sub-view.
func (m Model) openContextManagerActionMenu() Model {
	actions := []panelAction{
		{Label: "Toggle mark (space)", Accel: "space",
			Handler: func(m Model) (Model, tea.Cmd) {
				if len(m.contextPanel.manager.rows) > 0 {
					cursor := m.contextPanel.manager.cursor
					m.contextPanel.manager.marked[cursor] = !m.contextPanel.manager.marked[cursor]
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
		{Label: "Back to Context view",
			Handler: func(m Model) (Model, tea.Cmd) {
				return m.deactivateContextManager(), nil
			}},
	}
	return m.openActionMenu("CtxMgr", "Context Manager", actions)
}
