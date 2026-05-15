package tui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) handleChatControlShortcut(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	switch msg.String() {
	case "ctrl+c", "ctrl+q":
		if m.activeTab == 0 && len(m.chat.pasteBlocks) > 0 {
			m.clearPasteBlocks()
			m.setChatInput("")
			m.notice = "Paste cancelled."
			return m, nil, true
		}
		if m.chat.sending {
			if m.cancelActiveStream() {
				m.notice = "Cancelling…"
				return m, nil, true
			}
		}
		return m, tea.Quit, true
	case "ctrl+u":
		if m.activeTab == 0 {
			m.clearPasteBlocks()
			m.setChatInput("")
			m.chat.cursor = 0
			m.slashMenu.resetIndices()
			m.notice = "Input cleared."
			return m, nil, true
		}
	case "ctrl+h":
		m.ui.showHelpOverlay = !m.ui.showHelpOverlay
		return m, nil, true
	case "esc":
		return m.handleEscapeShortcut(msg)
	case "j", "k", "up", "down", "enter", "r":
		if m.activeTab == 0 && m.ui.showTasksPanel {
			nm, cmd := m.handleTasksPanelKey(msg)
			return nm, cmd, true
		}
		if m.activeTab == 0 && m.ui.statsPanelFocusLocked && m.ui.statsPanelMode == statsPanelModeProviders {
			nm, cmd := m.handleStatsPanelProviderKey(msg)
			return nm, cmd, true
		}
	}
	return m, nil, false
}

func (m Model) handleEscapeShortcut(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	if m.ui.showHelpOverlay {
		filter := strings.TrimSpace(m.chat.input)
		if filter != "" {
			m.clearPasteBlocks()
			m.setChatInput("")
			m.chat.cursor = 0
			m.notice = "Filter cleared."
		} else {
			m.ui.showHelpOverlay = false
		}
		return m, nil, true
	}
	if nm, closed := m.closePanelOverlay(); closed {
		return nm, nil, true
	}
	if m.activeTab == 0 && m.ui.showTasksPanel {
		nm, cmd := m.handleTasksPanelKey(msg)
		return nm, cmd, true
	}
	if m.activeTab == 0 && m.ui.statsPanelFocusLocked {
		m.ui.statsPanelFocusLocked = false
		m.ui.statsPanelBoostUntil = time.Time{}
		m.notice = "Stats panel focus unlocked."
		return m, nil, true
	}
	if m.chat.sending && m.cancelActiveStream() {
		m.notice = "Cancelling…"
		return m, nil, true
	}
	return m, nil, false
}

func (m Model) handleStatsPanelShortcut(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	switch msg.String() {
	case "ctrl+s":
		m.ui.selectionModeActive = false
		m.ui.statsPanelFocusLocked = false
		m.ui.statsPanelBoostUntil = time.Time{}
		m.ui.showStatsPanel = !m.ui.showStatsPanel
		if m.ui.showStatsPanel && m.ui.statsPanelMode == "" {
			m.ui.statsPanelMode = statsPanelModeOverview
		}
		return m, nil, true
	case "pgup":
		if m.activeTab == 0 && m.statsPanelVisible(100) {
			m.ui.statsPanelScroll = max(0, m.ui.statsPanelScroll-3)
			return m, nil, true
		}
	case "pgdn":
		if m.activeTab == 0 && m.statsPanelVisible(100) {
			m.ui.statsPanelScroll++
			return m, nil, true
		}
	case "alt+x":
		if m.activeTab == 0 {
			nm, cmd := m.setSelectionMode(!m.ui.selectionModeActive)
			return nm, cmd, true
		}
	case "alt+a":
		if m.activeTab == 0 {
			m.activateStatsPanelMode(statsPanelModeOverview, "overview")
			return m, nil, true
		}
	case "alt+s":
		if m.activeTab == 0 {
			m.activateStatsPanelMode(statsPanelModeTodos, "todos")
			return m, nil, true
		}
	case "alt+d":
		if m.activeTab == 0 {
			m.activateStatsPanelMode(statsPanelModeTasks, "tasks")
			return m, nil, true
		}
	case "alt+f":
		if m.activeTab == 0 {
			m.activateStatsPanelMode(statsPanelModeSubagents, "subagents")
			return m, nil, true
		}
	case "alt+p":
		if m.activeTab == 0 {
			m.activateStatsPanelMode(statsPanelModeProviders, "providers")
			return m, nil, true
		}
	}
	return m, nil, false
}

func (m Model) handlePickerShortcut(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	switch msg.String() {
	case "alt+m":
		if m.activeTab == 0 && !m.commandPicker.active {
			m = m.startCommandPicker("model", "", false)
			return m, nil, true
		}
	case "alt+shift+p", "alt+P":
		if m.activeTab == 0 && !m.commandPicker.active {
			m = m.startCommandPicker("provider", "", false)
			return m, nil, true
		}
	case "ctrl+p":
		m.activeTab = 0
		m.setChatInput("/")
		m.slashMenu.resetIndices()
		return m, nil, true
	case "ctrl+b":
		if m.panelSwitcher.active {
			m = m.closePanelSwitcher()
		} else {
			m = m.openPanelSwitcher()
		}
		return m, nil, true
	case "ctrl+g":
		m = m.activateDiagnosticTab("Activity")
		return m, nil, true
	case "alt+h":
		m.ui.showHelpOverlay = !m.ui.showHelpOverlay
		return m, nil, true
	}
	return m, nil, false
}

