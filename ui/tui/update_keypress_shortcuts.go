// update_keypress_shortcuts.go — global keyboard shortcut table.
// Sibling of update_keypress.go which keeps the Update→KeyMsg entry
// (handleKeyMsg, handleApprovalKey, routeKeyByActiveTab); this file
// owns handleGlobalShortcuts only — the big msg.String() switch over
// ctrl/alt/F-keys, tab/shift+tab, and the stats-panel sub-mode toggles
// (alt+a/s/d/f/p). Returns handled=true when the key was consumed so
// the caller falls through to per-tab routing only on miss.

package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/session"
)

// handleGlobalShortcuts covers keys that work regardless of which tab
// is focused (with a few "only on Chat" guards). Returns handled=true
// when a key was consumed; the caller falls through to per-tab
// routing otherwise.
func (m Model) handleGlobalShortcuts(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	if isToolStatusShortcut(msg) {
		if m.ui.panelOverlayKind == "toolstatus" {
			m.ui.panelOverlayKind = ""
		} else {
			m = m.activateDiagnosticTab("ToolStatus")
		}
		return m, nil, true
	}
	if nm, cmd, handled := m.handlePanelNavigationShortcut(msg); handled {
		return nm, cmd, true
	}
	switch msg.String() {
	case "ctrl+c", "ctrl+q":
		// Ctrl+C cancels paste blocks first; then cancels streaming
		// (if actively sending); finally rage-quits.
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
		// Unix readline-style "clear input line". Only useful on the
		// Chat tab — other panels don't have a live composer.
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
		// Help overlay first — its title strip advertises "esc to clear"
		// (when a filter is active) and "ctrl+h to close" (when empty).
		// We honour BOTH: a filtered overlay clears the filter on esc,
		// and an unfiltered overlay closes on esc. Without this branch
		// the title hint was lying — neither esc nor ctrl+h alone did
		// what users tried first.
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
	case "j", "k", "up", "down", "enter", "r":
		if m.activeTab == 0 && m.ui.showTasksPanel {
			nm, cmd := m.handleTasksPanelKey(msg)
			return nm, cmd, true
		}
		// Route to stats panel handler when providers sub-mode is focused
		if m.activeTab == 0 && m.ui.statsPanelFocusLocked && m.ui.statsPanelMode == statsPanelModeProviders {
			nm, cmd := m.handleStatsPanelProviderKey(msg)
			return nm, cmd, true
		}
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
	case "alt+m":
		// Direct model picker — open the same fuzzy picker that
		// `/model` opens but without the user having to type the
		// slash command first. One keystroke from chat to a
		// focused model switcher; arrow keys + enter applies and
		// auto-saves to the winning config scope.
		if m.activeTab == 0 && !m.commandPicker.active {
			m = m.startCommandPicker("model", "", false)
			return m, nil, true
		}
	case "alt+shift+p", "alt+P":
		// Same idea for providers. Lowercase alt+p stays bound to
		// the read-only stats sub-panel so keep alt+P (or the
		// alt+shift+p form some terminals send) for the picker.
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
		// Panel switcher — fuzzy-filter overlay over every panel. The
		// fallback for users whose terminal eats specific F-keys (F11
		// goes fullscreen on most terminals, F1 opens terminal help on
		// some, F4 closes tabs in others). With Ctrl+B you type three
		// letters of the panel name and hit enter — works everywhere.
		// Toggles closed when already open.
		if m.panelSwitcher.active {
			m = m.closePanelSwitcher()
		} else {
			m = m.openPanelSwitcher()
		}
		return m, nil, true
	case "ctrl+g":
		m = m.activateDiagnosticTab("Activity")
		return m, nil, true
	case "ctrl+alt+1", "ctrl+alt+2", "ctrl+alt+3", "ctrl+alt+4", "ctrl+alt+5":
		if m.session != nil {
			target, err := strconv.Atoi(msg.String()[len(msg.String())-1:])
			if err == nil && target >= 1 && target <= 5 {
				tree := m.session.AgentTree()
				seen := 0
				for _, n := range tree {
					if n.ID == session.RootAgentID {
						continue
					}
					seen++
					if seen == target {
						m.session.SwitchToAgent(n.ID)
						m.notice = fmt.Sprintf("Agent %d", n.ID)
						break
					}
				}
			}
		}
		return m, nil, true
	case "ctrl+alt+a":
		if m.session != nil {
			m.session.overlayOpen = !m.session.overlayOpen
		}
		return m, nil, true
	case "alt+h":
		// Phase K (help unification): alt+h flips the same Ctrl+H help
		// overlay rather than the legacy Shortcuts panel. One help
		// surface, three triggers (ctrl+h / alt+h / /help).
		m.ui.showHelpOverlay = !m.ui.showHelpOverlay
		return m, nil, true
	}
	return m, nil, false
}

func isToolStatusShortcut(msg tea.KeyMsg) bool {
	key := strings.ToLower(strings.TrimSpace(msg.String()))
	switch key {
	case "ctrl+shift+t", "ctrl+alt+t", "alt+ctrl+t", "alt+t":
		return true
	}
	if msg.Alt && msg.Type == tea.KeyCtrlT {
		return true
	}
	return false
}
