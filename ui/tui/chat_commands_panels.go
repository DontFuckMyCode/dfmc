package tui

// Diagnostic-panel slash commands: /approve, /hooks, /stats,
// /workflow, /todos, /subagents, /queue, /keylog, /coach, /hints,
// /intent, /copy, /mouse, /select, /status, /reload. Most of these
// either print a describe*() report into the transcript or flip a
// UI toggle (coach mute, hint verbosity, mouse capture, key log).
// Extracted from chat_commands.go so the dispatcher switch stays
// shallow.

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) runPanelCommand(cmd string, args []string) (tea.Model, tea.Cmd, bool) {
	switch cmd {
	case "approve", "approvals", "permissions":
		// Surface the tool-approval gate configuration: which tools are
		// gated, whether an approver is registered, whether a prompt is
		// currently pending. Read-only — editing the gate requires a
		// config change (opt-in by design; we don't want runtime slash
		// commands silently widening the attack surface).
		m.chat.input = ""
		m.notice = "Approval gate state shown below."
		return m.appendSystemMessage(m.describeApprovalGate()), nil, true
	case "hooks":
		// List every lifecycle hook registered with the dispatcher —
		// event → name(condition) command. Counterpart to /approve for
		// the other half of the tool-lifecycle surface.
		m.chat.input = ""
		m.notice = "Lifecycle hooks listed below."
		return m.appendSystemMessage(m.describeHooks()), nil, true
	case "stats", "tokens", "cost":
		// Session metrics at a glance: tool rounds, RTK-style compression
		// savings, context-window fill, agent loop progress. This makes
		// the 'token miser' thesis tangible — users should be able to
		// see how much they're saving, not just trust the claim.
		m.chat.input = ""
		m.notice = "Session stats below."
		return m.appendSystemMessage(m.describeStats()), nil, true
	case "workflow":
		m.chat.input = ""
		m.notice = "Workflow snapshot below."
		return m.appendSystemMessage(m.describeWorkflow()), nil, true
	case "todos", "todo":
		m.chat.input = ""
		m.notice = "Shared todo list below."
		return m.appendSystemMessage(m.describeTodos()), nil, true
	case "subagents", "workers":
		m.chat.input = ""
		m.notice = "Subagent activity below."
		return m.appendSystemMessage(m.describeSubagents()), nil, true
	case "queue":
		m.chat.input = ""
		return m.handleQueueSlash(args)
	case "keylog":
		// Toggle key-event dump into m.notice. Used to diagnose Turkish-
		// keyboard AltGr delivery and similar terminal-specific weirdness
		// without needing a side logfile.
		m.chat.input = ""
		m.ui.keyLogEnabled = !m.ui.keyLogEnabled
		state := "off"
		if m.ui.keyLogEnabled {
			state = "on — press any key and read the footer"
		}
		m.notice = "Key log " + state
		return m.appendSystemMessage("Key event dump is " + state + ". Toggle again with /keylog."), nil, true
	case "coach":
		m.chat.input = ""
		m.ui.coachMuted = !m.ui.coachMuted
		state := "on"
		if m.ui.coachMuted {
			state = "muted"
		}
		m.notice = "Coach " + state + "."
		return m.appendSystemMessage("Coach notes are now " + state + " for this session. Toggle again with /coach."), nil, true
	case "hints":
		m.chat.input = ""
		m.ui.hintsVerbose = !m.ui.hintsVerbose
		state := "hidden"
		if m.ui.hintsVerbose {
			state = "visible"
		}
		m.notice = "Trajectory hints " + state + "."
		return m.appendSystemMessage("Trajectory coach hints between rounds are now " + state + ". Toggle again with /hints."), nil, true
	case "intent":
		// /intent has three sub-commands:
		//   /intent           — toggle verbose (transcript pairs of raw → enriched)
		//   /intent show      — print the most recent decision in full
		//   /intent verbose   — alias of bare /intent
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
	case "copy", "yank":
		m.chat.input = ""
		return m.handleCopySlash(args)
	case "mouse":
		// Toggle bubbletea's mouse-event capture at runtime. With
		// capture ON the wheel scrolls the transcript natively but
		// terminal drag-to-select / right-click-copy is disabled. With
		// capture OFF you get the terminal's native selection — most
		// terminals also let Shift+drag bypass capture when it's on.
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
	case "select":
		m.chat.input = ""
		return m.toggleSelectionMode()
	case "status":
		m.chat.input = ""
		return m.appendSystemMessage(m.statusCommandSummary()), loadStatusCmd(m.eng), true
	case "reload":
		m.chat.input = ""
		if err := m.reloadEngineConfig(); err != nil {
			m.notice = "reload: " + err.Error()
			return m.appendSystemMessage("Runtime reload failed: " + err.Error()), nil, true
		}
		st := m.status
		return m.appendSystemMessage(fmt.Sprintf("Runtime reloaded.\nProvider/Model: %s / %s", blankFallback(st.Provider, "-"), blankFallback(st.Model, "-"))), loadStatusCmd(m.eng), true
	}
	return m, nil, false
}
