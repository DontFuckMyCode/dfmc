package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) handleApprovalPanelSlash() (tea.Model, tea.Cmd, bool) {
	m.chat.input = ""
	m.notice = "Approval gate state shown below."
	return m.appendSystemMessage(m.describeApprovalGate()), nil, true
}

func (m Model) handleHooksSlash() (tea.Model, tea.Cmd, bool) {
	m.chat.input = ""
	m.notice = "Lifecycle hooks listed below."
	return m.appendSystemMessage(m.describeHooks()), nil, true
}

func (m Model) handleStatsSlash() (tea.Model, tea.Cmd, bool) {
	m.chat.input = ""
	m.notice = "Session stats below."
	return m.appendSystemMessage(m.describeStats()), nil, true
}

func (m Model) handleWorkflowSlash() (tea.Model, tea.Cmd, bool) {
	m.chat.input = ""
	m.notice = "Workflow snapshot below."
	return m.appendSystemMessage(m.describeWorkflow()), nil, true
}

func (m Model) handleTodosSlash(args []string) (tea.Model, tea.Cmd, bool) {
	m.chat.input = ""
	if len(args) > 0 {
		sub := strings.ToLower(strings.TrimSpace(args[0]))
		if sub == "clear" || sub == "reset" {
			return m.handleTodosClear()
		}
	}
	m.notice = "Shared todo list below."
	return m.appendSystemMessage(m.describeTodos()), nil, true
}

func (m Model) handleTasksSlash(args []string) (tea.Model, tea.Cmd, bool) {
	m.chat.input = ""
	next, out := m.tasksSlash(args)
	if strings.TrimSpace(out) == "" {
		return next, nil, true
	}
	return next.appendSystemMessage(out), nil, true
}

func (m Model) handleKeylogSlash() (tea.Model, tea.Cmd, bool) {
	m.chat.input = ""
	m.ui.keyLogEnabled = !m.ui.keyLogEnabled
	state := "off"
	if m.ui.keyLogEnabled {
		state = "on — press any key and read the footer"
	}
	m.notice = "Key log " + state
	return m.appendSystemMessage("Key event dump is " + state + ". Toggle again with /keylog."), nil, true
}

func (m Model) handleCoachSlash() (tea.Model, tea.Cmd, bool) {
	m.chat.input = ""
	m.ui.coachMuted = !m.ui.coachMuted
	state := "on"
	if m.ui.coachMuted {
		state = "muted"
	}
	m.notice = "Coach " + state + "."
	return m.appendSystemMessage("Coach notes are now " + state + " for this session. Toggle again with /coach."), nil, true
}

func (m Model) handleHintsSlash() (tea.Model, tea.Cmd, bool) {
	m.chat.input = ""
	m.ui.hintsVerbose = !m.ui.hintsVerbose
	state := "hidden"
	if m.ui.hintsVerbose {
		state = "visible"
	}
	m.notice = "Trajectory hints " + state + "."
	return m.appendSystemMessage("Trajectory coach hints between rounds are now " + state + ". Toggle again with /hints."), nil, true
}

func (m Model) handleIntentSlash(args []string) (tea.Model, tea.Cmd, bool) {
	m.chat.input = ""
	sub := ""
	if len(args) > 0 {
		sub = strings.ToLower(strings.TrimSpace(args[0]))
	}
	if sub == "show" {
		return m.appendSystemMessage(m.describeLastIntent()), nil, true
	}
	m.intent.verbose = !m.intent.verbose
	state := "hidden"
	if m.intent.verbose {
		state = "visible"
	}
	m.notice = "Intent rewrites " + state + "."
	return m.appendSystemMessage("Intent layer rewrites are now " + state + " in the transcript. /intent show prints the last decision in full."), nil, true
}

func (m Model) handleMouseSlash() (tea.Model, tea.Cmd, bool) {
	m.chat.input = ""
	var cmdOut tea.Cmd
	m.ui.selectionModeActive = false
	if m.ui.mouseCaptureEnabled {
		m.ui.mouseCaptureEnabled = false
		cmdOut = tea.DisableMouse
		m.notice = "Mouse capture off — drag to select / copy text directly."
	} else {
		m.ui.mouseCaptureEnabled = true
		cmdOut = tea.EnableMouseCellMotion
		m.notice = "Mouse capture on — wheel scrolls transcript. Shift+drag bypasses capture in most terminals."
	}
	return m.appendSystemMessage("Mouse capture toggled. /mouse to flip again; set tui.mouse_capture in .dfmc/config.yaml for the default."), cmdOut, true
}

func (m Model) handleProvidersPanelSlash() (tea.Model, tea.Cmd, bool) {
	m.chat.input = ""
	m = m.activateProvidersPanel("", false)
	m.notice = "Providers panel — F8 also opens it."
	return m, nil, true
}

func (m Model) handleReloadSlash() (tea.Model, tea.Cmd, bool) {
	m.chat.input = ""
	if err := m.reloadEngineConfig(); err != nil {
		m.notice = "reload: " + err.Error()
		return m.appendSystemMessage("Runtime reload failed: " + err.Error()), nil, true
	}
	st := m.status
	return m.appendSystemMessage(fmt.Sprintf("Runtime reloaded.\nProvider/Model: %s / %s", blankFallback(st.Provider, "-"), blankFallback(st.Model, "-"))), loadStatusCmd(m.eng), true
}

func (m Model) handleShortcutsSlash() (tea.Model, tea.Cmd, bool) {
	m.chat.input = ""
	m.ui.showHelpOverlay = true
	m.notice = "Help overlay open — ctrl+h / alt+h / esc to close."
	return m, nil, true
}

func (m Model) handleCancelSlash() (tea.Model, tea.Cmd, bool) {
	m.chat.input = ""
	if !m.chat.sending {
		return m.appendSystemMessage("/cancel: nothing to cancel — main agent is idle. Drive runs use /drive stop."), nil, true
	}
	if m.cancelActiveStream() {
		m.notice = "Cancelling…"
		return m.appendSystemMessage("▸ Cancellation sent to the active turn. Subagents will unwind through their parent context. /drive runs are NOT affected — use /drive stop for those."), nil, true
	}
	return m.appendSystemMessage("/cancel: no cancellable stream attached. The turn may be between rounds — try again in a second or hit Ctrl+C."), nil, true
}
