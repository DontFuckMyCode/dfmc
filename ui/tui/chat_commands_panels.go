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

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) runPanelCommand(cmd string, args []string) (tea.Model, tea.Cmd, bool) {
	switch cmd {
	case "approve", "approvals", "permissions":
		return m.handleApprovalPanelSlash()
	case "hooks":
		return m.handleHooksSlash()
	case "stats", "tokens", "cost":
		return m.handleStatsSlash()
	case "workflow":
		return m.handleWorkflowSlash()
	case "todos", "todo":
		return m.handleTodosSlash(args)
	case "tasks":
		return m.handleTasksSlash(args)
	case "subagents", "workers":
		m.chat.input = ""
		m.notice = "Subagent activity below."
		return m.appendSystemMessage(m.describeSubagents()), nil, true
	case "toolstatus", "toolcalls":
		m.chat.input = ""
		m = m.activateDiagnosticTab("ToolStatus")
		m.notice = "ToolStatus opened. Esc closes; Ctrl+Shift+T toggles from chat."
		return m, nil, true
	case "queue":
		m.chat.input = ""
		return m.handleQueueSlash(args)
	case "keylog":
		return m.handleKeylogSlash()
	case "coach":
		return m.handleCoachSlash()
	case "hints":
		return m.handleHintsSlash()
	case "intent":
		return m.handleIntentSlash(args)
	case "copy", "yank":
		m.chat.input = ""
		return m.handleCopySlash(args)
	case "mouse":
		return m.handleMouseSlash()
	case "select":
		m.chat.input = ""
		return m.toggleSelectionMode()
	case "status":
		m.chat.input = ""
		return m.appendSystemMessage(m.statusCommandSummary()), loadStatusCmd(m.eng), true
	case "log", "calls":
		m.chat.input = ""
		return m.appendSystemMessage(m.providerLogTailSummary()), nil, true
	case "providers":
		return m.handleProvidersPanelSlash()
	case "reload":
		return m.handleReloadSlash()
	case "shortcuts", "keys", "cheatsheet":
		return m.handleShortcutsSlash()
	case "cancel", "abort", "stop":
		return m.handleCancelSlash()
	}
	return m, nil, false
}

// handleTodosClear wipes the shared todo list via the todo_write tool's
// "clear" action so both the in-memory state and the task store are
// reset together. Bound to `/todos clear`.
func (m Model) handleTodosClear() (tea.Model, tea.Cmd, bool) {
	if m.eng == nil || m.eng.Tools == nil {
		return m.appendSystemMessage("/todos clear: engine not initialized — try /reload."), nil, true
	}
	before := len(m.eng.Tools.TodoSnapshot())
	if before == 0 {
		return m.appendSystemMessage("/todos clear: list is already empty."), nil, true
	}
	_, err := m.eng.CallTool(m.ctx, "todo_write", map[string]any{"action": "clear"})
	if err != nil {
		return m.appendSystemMessage("/todos clear failed: " + err.Error()), nil, true
	}
	m.notice = fmt.Sprintf("Cleared %d todo(s).", before)
	return m.appendSystemMessage(fmt.Sprintf("▸ Cleared %d todo(s) — agent will start fresh on the next turn.", before)), nil, true
}
