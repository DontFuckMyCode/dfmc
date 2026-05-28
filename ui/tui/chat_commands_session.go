package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) handleHelpSlash(args []string) (tea.Model, tea.Cmd, bool) {
	m.chat.input = ""
	if len(args) > 0 {
		return m.appendSystemMessage(renderTUICommandHelp(args[0])), nil, true
	}
	m.ui.showHelpOverlay = true
	m.notice = "Help overlay open — ctrl+h / alt+h / esc to close."
	return m, nil, true
}

func (m Model) handleClearSlash() (tea.Model, tea.Cmd, bool) {
	m.chat.input = ""
	m.chat.transcript = nil
	m.chat.scrollback = 0
	m.chat.pinnedAssistantTurns = nil
	m.chat.expandedAssistantTurns = nil
	m.chat.lastSearchQuery = ""
	m.chat.pendingQueue = nil
	m.chat.pendingNoteCount = 0
	m.clearPasteBlocks()
	m.cancelActiveStream()
	m.chat.sending = false
	m.chat.streamIndex = -1
	m.chat.streamMessages = nil
	m.chat.streamCancel = nil
	m.resetAgentRuntime()
	if m.eng != nil {
		m.eng.ConversationStart()
	}
	m.notice = "Transcript cleared."
	return m.appendSystemMessage("Transcript, context, and session cleared. Memory is untouched."), nil, true
}

func (m Model) handleExportSlash(cmd string, args []string) (tea.Model, tea.Cmd, bool) {
	m.chat.input = ""
	if len(m.chat.transcript) == 0 {
		m.notice = "Nothing to export yet — start a conversation first."
		return m.appendSystemMessage("Nothing to export yet. Send a message, then /export to save the transcript."), nil, true
	}
	if cmd == "save" && len(args) == 1 {
		if turn, ok := parseAssistantTurnArg(args[0]); ok {
			return m.handleSaveTurnSlash(turn)
		}
	}
	target := strings.TrimSpace(strings.Join(args, " "))
	path, err := m.exportTranscript(target)
	if err != nil {
		m.notice = "Export failed: " + err.Error()
		return m.appendSystemMessage("Export failed: " + err.Error()), nil, true
	}
	m.notice = "Exported transcript → " + path
	return m.appendSystemMessage("▸ Transcript exported → " + path + " (" + fmt.Sprintf("%d lines", len(m.chat.transcript)) + ")."), nil, true
}

func (m Model) handlePinSlash(cmd string, args []string) (tea.Model, tea.Cmd, bool) {
	m.chat.input = ""
	if len(args) != 1 {
		m.notice = "Usage: /pin <n> | /unpin <n> — n is the assistant turn number shown in the chip line."
		return m.appendSystemMessage("Usage: /pin <n> | /unpin <n>. The chip under each assistant turn shows its number."), nil, true
	}
	turn, ok := parseAssistantTurnArg(args[0])
	if !ok {
		m.notice = "/" + cmd + ": expected a positive integer turn number."
		return m.appendSystemMessage("/" + cmd + " expects a positive integer turn number — try /pin 3."), nil, true
	}
	return m.handlePinTurnSlash(turn, cmd == "pin")
}

func (m Model) handleForkSlash(args []string) (tea.Model, tea.Cmd, bool) {
	m.chat.input = ""
	if len(args) < 1 {
		m.notice = "Usage: /fork <n> [name] — n is the assistant turn number to branch from."
		return m.appendSystemMessage("Usage: /fork <n> [name]. Defaults the branch name to fork-from-<n>-<stamp>."), nil, true
	}
	turn, ok := parseAssistantTurnArg(args[0])
	if !ok {
		m.notice = "/fork: expected a positive integer turn number."
		return m.appendSystemMessage("/fork expects a positive integer turn number — try /fork 3."), nil, true
	}
	name := strings.TrimSpace(strings.Join(args[1:], " "))
	return m.handleForkTurnSlash(turn, name)
}

func (m Model) handleFilePickerSlash() (tea.Model, tea.Cmd, bool) {
	m.chat.input = ""
	if m.chat.sending {
		m.notice = "Cannot open file picker while a turn is streaming."
		return m.appendSystemMessage("A turn is streaming — esc to cancel first."), nil, true
	}
	m.setChatInput("@")
	m.slashMenu.mention = 0
	m.notice = "File picker open — type to filter, tab/enter inserts, esc cancels."
	if len(m.filesView.entries) == 0 && m.eng != nil {
		return m, loadFilesCmd(m.eng), true
	}
	return m, nil, true
}

func (m Model) handlePlanSlash() (tea.Model, tea.Cmd, bool) {
	m.chat.input = ""
	if m.ui.planMode {
		m.notice = "Already in plan mode — type your question, or /code to exit."
		return m.appendSystemMessage("Plan mode is already ON. Your next prompt will investigate without modifying files. Use /code to exit."), nil, true
	}
	m.ui.planMode = true
	m.notice = "Plan mode ON — investigate-only, no file writes. /code exits."
	return m.appendSystemMessage("▸ Plan mode ON. The agent will investigate with read-only tools (read_file, grep_codebase, ast_query, list_dir, glob, git_status, git_diff) and propose changes as a plan. Type /code to exit plan mode when you're ready to apply."), nil, true
}

func (m Model) handleCodeSlash() (tea.Model, tea.Cmd, bool) {
	m.chat.input = ""
	if !m.ui.planMode {
		m.notice = "Already in code mode — plan mode was not active."
		return m.appendSystemMessage("Not in plan mode. Prompts already allow file modifications."), nil, true
	}
	m.ui.planMode = false
	m.notice = "Plan mode OFF — prompts can now modify files."
	return m.appendSystemMessage("▸ Plan mode OFF. Write/update prompts will now route through mutating tools (apply_patch, edit_file, write_file)."), nil, true
}
