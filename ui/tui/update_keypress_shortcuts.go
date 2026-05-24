// update_keypress_shortcuts.go — global keyboard shortcut table.
// Sibling of update_keypress.go which keeps the Update→KeyMsg entry
// (handleKeyMsg, handleApprovalKey, routeKeyByActiveTab); this file
// owns handleGlobalShortcuts only — the big msg.String() switch over
// ctrl/alt/F-keys, tab/shift+tab, and the stats-panel sub-mode toggles
// (alt+a/s/d/f/p). Returns handled=true when the key was consumed so
// the caller falls through to per-tab routing only on miss.

package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
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
	for _, handler := range []func(tea.KeyMsg) (tea.Model, tea.Cmd, bool){
		m.handleChatControlShortcut,
		m.handleStatsPanelShortcut,
		m.handlePickerShortcut,
	} {
		if nm, cmd, handled := handler(msg); handled {
			return nm, cmd, true
		}
	}
	return m, nil, false
}

func isToolStatusShortcut(msg tea.KeyMsg) bool {
	key := strings.ToLower(strings.TrimSpace(msg.String()))
	switch key {
	case "ctrl+shift+t":
		return true
	}
	if msg.Alt && msg.Type == tea.KeyCtrlT {
		return true
	}
	return false
}
