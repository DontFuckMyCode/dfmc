package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) handleChatBackspaceKey() (tea.Model, tea.Cmd) {
	m.exitInputHistoryNavigation()
	m.deleteInputBeforeCursor()
	m.refreshMentionPickerOpen()
	m.slashMenu.resetIndices()
	return m, nil
}

func (m Model) handleChatDeleteKey() (tea.Model, tea.Cmd) {
	m.exitInputHistoryNavigation()
	m.deleteInputAtCursor()
	m.refreshMentionPickerOpen()
	m.slashMenu.resetIndices()
	return m, nil
}

func (m Model) handleChatEndKey() (tea.Model, tea.Cmd) {
	if m.chat.scrollback > 0 {
		m.chat.scrollback = 0
		m.notice = "Transcript: jumped to latest"
		return m, nil
	}
	m.moveChatCursorEnd()
	return m, nil
}

func (m Model) handleChatKillWordKey() (tea.Model, tea.Cmd) {
	m.exitInputHistoryNavigation()
	m.deleteInputWordBeforeCursor()
	m.slashMenu.resetIndices()
	return m, nil
}

func (m Model) handleChatKillLineKey() (tea.Model, tea.Cmd) {
	m.exitInputHistoryNavigation()
	m.deleteInputToEndOfLine()
	m.slashMenu.resetIndices()
	return m, nil
}

func (m Model) handleChatEscapeKey() (tea.Model, tea.Cmd) {
	if m.ui.resumePromptActive {
		m.ui.resumePromptActive = false
		m.notice = "Resume prompt dismissed \u2014 /continue re-opens it."
		return m, nil
	}
	if m.chat.mentionPickerOpen {
		m.chat.mentionPickerOpen = false
		m.slashMenu.mention = 0
		m.notice = "File picker closed."
		return m, nil
	}
	if len(m.assistantNextActions.actions) > 0 && strings.TrimSpace(m.chat.input) == "" {
		m.assistantNextActions.actions = nil
		m.notice = "Next-actions dismissed."
		return m, nil
	}
	return m, nil
}

func (m Model) handleChatTabKey() (tea.Model, tea.Cmd) {
	if m.chat.sending {
		return m, nil
	}
	suggestions := m.buildChatSuggestionState()
	if next, ok := autocompleteMentionSelectionFromSuggestions(m.chat.input, m.slashMenu.mention, suggestions.mentionSuggestions); ok {
		m.setChatInput(next)
		m.slashMenu.mention = 0
		m.chat.mentionPickerOpen = false
		return m, nil
	}
	if next, ok := m.autocompleteSlashArg(); ok {
		m.setChatInput(next)
		m.slashMenu.commandArg = 0
		return m, nil
	}
	if next, ok := m.autocompleteSlashCommand(); ok {
		m.setChatInput(next)
		return m, nil
	}
	if len(suggestions.quickActions) > 0 {
		selected := suggestions.quickActions[clampIndex(m.slashMenu.quickAction, len(suggestions.quickActions))]
		m.insertInputText(selected.PreparedInput)
		return m, nil
	}
	return m, nil
}
