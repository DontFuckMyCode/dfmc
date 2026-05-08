// update_window.go — terminal-shape and pointer messages: WindowSizeMsg
// (width/height repaint) and MouseMsg (wheel-scrolling the chat
// transcript). Each handler returns the same `(tea.Model, tea.Cmd)`
// shape Update would, so the top-level dispatcher in update.go can
// just forward.

package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) handleWindowSizeMsg(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.width = msg.Width
	m.height = msg.Height
	return m, nil
}

// handleMouseMsg routes wheel events to the chat transcript scroller.
// We deliberately only react on press edges — bubbletea emits a
// press+release pair per wheel tick, so handling both would
// double-scroll. The input box (tail) stays pinned; only the
// transcript head clips. Shift+wheel jumps a half-page so power
// users can travel a long history quickly. Other tabs ignore the
// wheel (their content is static enough to fit in-panel).
func (m Model) handleMouseMsg(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.tabs[m.activeTab] != "Chat" {
		return m, nil
	}
	if len(m.chat.transcript) == 0 {
		return m, nil
	}
	if msg.Action != tea.MouseActionPress {
		return m, nil
	}
	step := mouseWheelStep
	if msg.Shift {
		step = mouseWheelPageStep
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		m.scrollTranscript(-step)
	case tea.MouseButtonWheelDown:
		m.scrollTranscript(step)
	}
	return m, nil
}
