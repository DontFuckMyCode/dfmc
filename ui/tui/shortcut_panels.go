package tui

import tea "github.com/charmbracelet/bubbletea"

func (m Model) handlePanelNavigationShortcut(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	switch msg.String() {
	case "tab":
		if m.tabs[m.activeTab] != "Chat" {
			m.ui.panelOverlayKind = ""
			m.activeTab = (m.activeTab + 1) % len(m.tabs)
			return m, nil, true
		}
	case "shift+tab":
		if m.tabs[m.activeTab] != "Chat" {
			m.ui.panelOverlayKind = ""
			m.activeTab--
			if m.activeTab < 0 {
				m.activeTab = len(m.tabs) - 1
			}
			return m, nil, true
		}
	case "f1", "alt+1":
		m = m.resetTabSwitchAffordances()
		m.ui.panelOverlayKind = ""
		m.activeTab = 0 // Chat
		return m, nil, true
	case "f2", "alt+2":
		m = m.resetTabSwitchAffordances()
		m.ui.panelOverlayKind = ""
		m.activeTab = 1 // Files
		if len(m.filesView.entries) == 0 {
			return m, loadFilesCmd(m.eng), true
		}
		return m, nil, true
	case "f3", "alt+3":
		m = m.resetTabSwitchAffordances()
		m.ui.panelOverlayKind = ""
		m.activeTab = 2 // Patch
		return m, tea.Batch(loadWorkspaceCmd(m.eng), loadLatestPatchCmd(m.eng), loadGitInfoCmd(m.projectRoot())), true
	case "f4", "alt+4":
		m = m.resetTabSwitchAffordances()
		m.ui.panelOverlayKind = ""
		m.activeTab = 3 // Workflow
		m = m.refreshWorkflowOnTabEnter()
		return m, nil, true
	case "f5", "alt+5":
		m = m.resetTabSwitchAffordances()
		m.ui.panelOverlayKind = ""
		m.activeTab = 4 // Activity
		return m, nil, true
	case "f6", "alt+6":
		m = m.resetTabSwitchAffordances()
		m.ui.panelOverlayKind = ""
		m.activeTab = 5 // Memory
		if m.memory.entries == nil && !m.memory.loading {
			m.memory.loading = true
			return m, loadMemoryCmd(m.eng, m.memory.tier), true
		}
		return m, nil, true
	case "f7", "alt+7":
		m = m.resetTabSwitchAffordances()
		m.ui.panelOverlayKind = ""
		m.activeTab = 6 // Conversations
		if !m.conversations.loaded && !m.conversations.loading {
			m.conversations.loading = true
			return m, loadConversationsCmd(m.eng), true
		}
		return m, nil, true
	case "f8", "alt+8":
		m = m.activateProvidersPanel("", false)
		return m, nil, true
	case "f9":
		m = m.activateDiagnosticTab("Status")
		return m, nil, true
	case "f10":
		m = m.activateDiagnosticTab("CodeMap")
		if !m.codemap.loaded && !m.codemap.loading {
			m.codemap.loading = true
			return m, loadCodemapCmd(m.eng), true
		}
		return m, nil, true
	case "f11":
		m = m.activateDiagnosticTab("Tools")
		return m, nil, true
	case "f12":
		m = m.activateDiagnosticTab("Security")
		if !m.security.loaded && !m.security.loading {
			m.security.loading = true
			return m, loadSecurityCmd(m.eng), true
		}
		return m, nil, true
	case "shift+f1", "f13":
		m = m.activateDiagnosticTab("Prompts")
		if !m.prompts.loaded && !m.prompts.loading {
			m.prompts.loading = true
			return m, loadPromptsCmd(m.eng), true
		}
		return m, nil, true
	case "shift+f2", "f14":
		m = m.activatePlansPanel("", false)
		return m, nil, true
	case "shift+f3", "f15":
		m = m.activateContextPanel("", false)
		return m, nil, true
	case "shift+f4", "f16":
		m = m.activateDiagnosticTab("Orchestrate")
		return m, nil, true
	case "shift+f5", "f17":
		m = m.activateDiagnosticTab("Shortcuts")
		return m, nil, true
	case "shift+f6", "f18":
		m = m.activateDiagnosticTab("Contexts")
		return m, nil, true
	case "shift+f7", "f19", "ctrl+l":
		m = m.activateDiagnosticTab("ProviderLog")
		return m, nil, true
	case "shift+f8", "f20":
		m = m.activateDiagnosticTab("Telegram")
		return m, nil, true
	case "alt+9", "alt+0":
		m.ui.showHelpOverlay = !m.ui.showHelpOverlay
		return m, nil, true
	case "alt+i":
		m = m.activateDiagnosticTab("Tools")
		return m, nil, true
	case "ctrl+shift+t", "ctrl+alt+t", "alt+ctrl+t", "alt+t", "alt+o":
		m = m.activateDiagnosticTab("ToolStatus")
		return m, nil, true
	case "ctrl+i":
		m = m.activateDiagnosticTab("Status")
		return m, nil, true
	case "ctrl+y":
		m = m.activatePlansPanel("", false)
		return m, nil, true
	case "ctrl+w":
		if m.activeTab != 0 {
			m = m.activateContextPanel("", false)
			return m, nil, true
		}
	case "ctrl+o":
		m = m.activateProvidersPanel("", false)
		return m, nil, true
	case "alt+r":
		m = m.activateDiagnosticTab("Orchestrate")
		return m, nil, true
	}
	return m, nil, false
}
