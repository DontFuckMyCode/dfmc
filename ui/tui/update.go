// update.go — bubbletea Update reducer for the TUI Model.
//
// Update itself is a pure router: switch on tea.Msg type, dispatch to
// the matching handle<Type>Msg in a sibling file. Per-domain handlers
// live in:
//
//   update_window.go    — WindowSizeMsg, MouseMsg
//   update_data.go      — *LoadedMsg, sync/patch/undo/tool/git
//                         "data arrived" notifications
//   update_stream.go    — chat lifecycle (delta/done/err/closed),
//                         spinner/heartbeat ticks, event subscription,
//                         approvalRequested
//   update_keypress.go  — tea.KeyMsg (modal, global shortcuts, per-tab
//                         routing)
//
// When adding a new tea.Msg type:
//   1. Define the message type alongside the cmd that emits it
//   2. Add a `case xxxMsg:` here and call into a Model.handleXxxMsg method
//      placed in the sibling that owns that domain
//   3. update.go MUST stay a pure dispatcher — no business logic inline

package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const statsPanelBoostDuration = 4 * time.Second

func (m *Model) activateStatsPanelMode(mode statsPanelMode, label string) {
	now := time.Now()
	m.ui.selectionModeActive = false
	m.ui.showStatsPanel = true
	if m.ui.statsPanelMode == mode && m.statsPanelBoostActive(now) {
		if m.ui.statsPanelFocusLocked {
			m.ui.statsPanelFocusLocked = false
			m.ui.statsPanelBoostUntil = now.Add(statsPanelBoostDuration)
			m.notice = "Stats panel: " + label + " (expanded)"
			return
		}
		m.ui.statsPanelFocusLocked = true
		m.ui.statsPanelBoostUntil = time.Time{}
		m.notice = "Stats panel: " + label + " (locked)"
		return
	}
	m.ui.statsPanelMode = mode
	m.ui.statsPanelFocusLocked = false
	m.ui.statsPanelBoostUntil = now.Add(statsPanelBoostDuration)
	m.notice = "Stats panel: " + label + " (expanded)"
}

func (m *Model) autoActivateStatsPanelMode(mode statsPanelMode, label string) {
	if m.activeTab != 0 || m.ui.selectionModeActive || m.ui.statsPanelFocusLocked {
		return
	}
	now := time.Now()
	m.ui.showStatsPanel = true
	if m.ui.statsPanelMode != mode || !m.statsPanelBoostActive(now) {
		m.ui.statsPanelMode = mode
		m.ui.statsPanelBoostUntil = now.Add(statsPanelBoostDuration)
		m.notice = "Workflow focus: " + label
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m.ensureDiagnostics()
	m.chat.suppressPasteRender = false
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m.handleWindowSizeMsg(msg)
	case tea.MouseMsg:
		return m.handleMouseMsg(msg)

	case eventSubscribedMsg:
		return m.handleEventSubscribedMsg(msg)
	case engineEventMsg:
		return m.handleEngineEventMsg(msg)

	case statusLoadedMsg:
		return m.handleStatusLoadedMsg(msg)
	case workspaceLoadedMsg:
		return m.handleWorkspaceLoadedMsg(msg)
	case latestPatchLoadedMsg:
		return m.handleLatestPatchLoadedMsg(msg)
	case filesLoadedMsg:
		return m.handleFilesLoadedMsg(msg)
	case filePreviewLoadedMsg:
		return m.handleFilePreviewLoadedMsg(msg)
	case memoryLoadedMsg:
		return m.handleMemoryLoadedMsg(msg)
	case codemapLoadedMsg:
		return m.handleCodemapLoadedMsg(msg)
	case conversationsLoadedMsg:
		return m.handleConversationsLoadedMsg(msg)
	case conversationPreviewMsg:
		return m.handleConversationPreviewMsg(msg)
	case promptsLoadedMsg:
		return m.handlePromptsLoadedMsg(msg)
	case securityLoadedMsg:
		return m.handleSecurityLoadedMsg(msg)
	case syncModelsDevMsg:
		return m.handleSyncModelsDevMsg(msg)
	case providerProbeMsg:
		next, cmd := m.handleProviderProbeMsg(msg)
		return next, cmd
	case patchApplyMsg:
		return m.handlePatchApplyMsg(msg)
	case conversationUndoMsg:
		return m.handleConversationUndoMsg(msg)
	case toolRunMsg:
		return m.handleToolRunMsg(msg)
	case gitInfoLoadedMsg:
		return m.handleGitInfoLoadedMsg(msg)

	case chatDeltaMsg:
		return m.handleChatDeltaMsg(msg)
	case spinnerTickMsg:
		return m.handleSpinnerTickMsg(msg)
	case heartbeatTickMsg:
		return m.handleHeartbeatTickMsg(msg)
	case chatDoneMsg:
		return m.handleChatDoneMsg(msg)
	case chatErrMsg:
		return m.handleChatErrMsg(msg)
	case streamClosedMsg:
		return m.handleStreamClosedMsg(msg)
	case approvalRequestedMsg:
		return m.handleApprovalRequestedMsg(msg)
	case telegramMessageAddedMsg:
		return m.handleTelegramMessageAdded(msg)

	case tea.KeyMsg:
		return m.handleKeyMsg(msg)
	}
	return m, nil
}
