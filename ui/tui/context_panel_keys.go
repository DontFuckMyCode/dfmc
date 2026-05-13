package tui

// context_panel_keys.go — keyboard routers for the Context tab.
// Companion siblings:
//
//   - context_panel.go        view orchestration + state mutators
//   - context_panel_blocks.go pure block rendering helpers (budget,
//                             breakdown, active chunks, ratio bar)
//
// handleContextKey dispatches outside-input-mode keys: action menu,
// scroll, e (edit), enter (rerun), c (clear), a (active context).
// handleContextInputKey is the typing/backspace/enter/esc handler
// while the inline query input is focused.

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) handleContextKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.contextPanel.inputActive {
		return m.handleContextInputKey(msg)
	}
	// Route to Context Manager sub-view when active.
	if nm, cmd, handled := m.handleContextManagerKey(msg); handled {
		return nm, cmd
	}
	if nm, cmd, handled := m.handleActionMenuKey(msg); handled {
		return nm, cmd
	}
	if s := msg.String(); s == "right" || s == "l" {
		if m.contextPanel.manager.active {
			return m.openContextManagerActionMenu(), nil
		}
		return m.openContextActionMenu(), nil
	}
	switch msg.String() {
	case "m":
		// Toggle Context Manager sub-view
		if m.contextPanel.manager.active {
			m = m.deactivateContextManager()
		} else {
			m = m.activateContextManager()
		}
		return m, nil
	case "a", "f":
		m = m.loadActiveContextDebug()
		return m, nil
	case "e":
		m.contextPanel.inputActive = true
		return m, nil
	case "enter":
		if strings.TrimSpace(m.contextPanel.query) != "" {
			m = m.runContextPreview()
		}
		return m, nil
	case "c":
		m.contextPanel.query = ""
		m.contextPanel.preview = nil
		m.contextPanel.breakdown = nil
		m.contextPanel.hints = nil
		m.contextPanel.active = nil
		m.contextPanel.showActive = false
		m.contextPanel.scroll = 0
		m.contextPanel.err = ""
		return m, nil
	case "up", "k":
		if m.contextPanel.scroll > 0 {
			m.contextPanel.scroll--
		}
		return m, nil
	case "down", "j":
		m.contextPanel.scroll++
		return m, nil
	case "pgup":
		m.contextPanel.scroll = maxInt(0, m.contextPanel.scroll-10)
		return m, nil
	case "pgdown":
		m.contextPanel.scroll += 10
		return m, nil
	}
	return m, nil
}

func (m Model) handleContextInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		m.contextPanel.inputActive = false
		m = m.runContextPreview()
		return m, nil
	case tea.KeyEsc:
		m.contextPanel.inputActive = false
		return m, nil
	case tea.KeyBackspace:
		if r := []rune(m.contextPanel.query); len(r) > 0 {
			m.contextPanel.query = string(r[:len(r)-1])
		}
		return m, nil
	case tea.KeyRunes, tea.KeySpace:
		m.contextPanel.query += msg.String()
		return m, nil
	}
	return m, nil
}
