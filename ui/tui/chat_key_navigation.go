package tui

// chat_key_navigation.go — Up/Down arrow handlers for the chat
// composer. Each arm walks autocomplete pickers (slash menu / slash
// arg / mention list / quick action) before falling through to
// multi-line buffer row navigation and finally to history recall.
// Pulled out of chat_key.go to keep the giant switch readable.
// Companion siblings:
//
//   - chat_key.go         handleChatKey dispatcher + small helpers
//                         (isAtMentionOpenKey, openMentionPickerFromKey,
//                         refreshMentionPickerOpen, paste notice)
//   - chat_key_submit.go  submitChatComposer end-of-turn flow

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) handleChatUpKey() (tea.Model, tea.Cmd) {
	suggestions := m.buildChatSuggestionState()
	if !m.chat.sending && m.inputHistory.index >= 0 && m.recallInputHistoryPrev() {
		m.slashMenu.command = 0
		m.slashMenu.commandArg = 0
		m.slashMenu.mention = 0
		m.notice = "History: previous input"
		return m, nil
	}
	if suggestions.slashMenuActive {
		items := suggestions.slashCommands
		if len(items) > 0 {
			idx := clampIndex(m.slashMenu.command, len(items))
			if idx > 0 {
				idx--
			}
			m.slashMenu.command = idx
			m.notice = "Command: " + items[m.slashMenu.command].Template
		}
		return m, nil
	}
	if len(suggestions.slashArgSuggestions) > 0 {
		idx := clampIndex(m.slashMenu.commandArg, len(suggestions.slashArgSuggestions))
		if idx > 0 {
			idx--
		}
		m.slashMenu.commandArg = idx
		m.notice = "Arg: " + suggestions.slashArgSuggestions[m.slashMenu.commandArg]
		return m, nil
	}
	if len(suggestions.mentionSuggestions) > 0 {
		idx := clampIndex(m.slashMenu.mention, len(suggestions.mentionSuggestions))
		if idx > 0 {
			idx--
		}
		m.slashMenu.mention = idx
		m.notice = "Mention: " + suggestions.mentionSuggestions[m.slashMenu.mention].Path
		return m, nil
	}
	if len(suggestions.quickActions) > 0 {
		idx := clampIndex(m.slashMenu.quickAction, len(suggestions.quickActions))
		if idx > 0 {
			idx--
		}
		m.slashMenu.quickAction = idx
		m.notice = "Quick action: " + suggestions.quickActions[idx].PreparedInput
		return m, nil
	}
	// Multi-line buffer navigation. When input spans rows, Up first walks
	// the buffer and only falls through to history navigation when the
	// cursor is already on the first row. Single-line input skips this
	// and goes straight to history, preserving the old behavior.
	if !m.chat.sending && strings.ContainsRune(m.chat.input, '\n') {
		if m.moveChatCursorRowUp() {
			return m, nil
		}
	}
	if !m.chat.sending && m.recallInputHistoryPrev() {
		m.slashMenu.resetIndices()
		m.notice = "History: previous input"
		return m, nil
	}
	return m, nil
}

func (m Model) handleChatDownKey() (tea.Model, tea.Cmd) {
	suggestions := m.buildChatSuggestionState()
	if !m.chat.sending && m.inputHistory.index >= 0 && m.recallInputHistoryNext() {
		m.slashMenu.command = 0
		m.slashMenu.commandArg = 0
		m.slashMenu.mention = 0
		m.notice = "History: next input"
		return m, nil
	}
	if suggestions.slashMenuActive {
		items := suggestions.slashCommands
		if len(items) > 0 {
			idx := clampIndex(m.slashMenu.command, len(items))
			if idx < len(items)-1 {
				idx++
			}
			m.slashMenu.command = idx
			m.notice = "Command: " + items[m.slashMenu.command].Template
		}
		return m, nil
	}
	if len(suggestions.slashArgSuggestions) > 0 {
		idx := clampIndex(m.slashMenu.commandArg, len(suggestions.slashArgSuggestions))
		if idx < len(suggestions.slashArgSuggestions)-1 {
			idx++
		}
		m.slashMenu.commandArg = idx
		m.notice = "Arg: " + suggestions.slashArgSuggestions[m.slashMenu.commandArg]
		return m, nil
	}
	if len(suggestions.mentionSuggestions) > 0 {
		idx := clampIndex(m.slashMenu.mention, len(suggestions.mentionSuggestions))
		if idx < len(suggestions.mentionSuggestions)-1 {
			idx++
		}
		m.slashMenu.mention = idx
		m.notice = "Mention: " + suggestions.mentionSuggestions[m.slashMenu.mention].Path
		return m, nil
	}
	if len(suggestions.quickActions) > 0 {
		idx := clampIndex(m.slashMenu.quickAction, len(suggestions.quickActions))
		if idx < len(suggestions.quickActions)-1 {
			idx++
		}
		m.slashMenu.quickAction = idx
		m.notice = "Quick action: " + suggestions.quickActions[idx].PreparedInput
		return m, nil
	}
	// Symmetric to KeyUp — buffer row navigation when input has \n.
	if !m.chat.sending && strings.ContainsRune(m.chat.input, '\n') {
		if m.moveChatCursorRowDown() {
			return m, nil
		}
	}
	if !m.chat.sending && m.recallInputHistoryNext() {
		m.slashMenu.resetIndices()
		m.notice = "History: next input"
		return m, nil
	}
	return m, nil
}
