package tui

// Chat panel keyboard router. Extracted from tui.go to keep keyboard handling
// next to the chat-composer state. handleChatKey is the dispatch entry: it
// fires the command picker / mention / slash autocomplete pipelines first,
// then falls through to text editing, cursor movement, history recall, and
// transcript scroll.

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) handleChatKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.commandPicker.active {
		return m.handleCommandPickerKey(msg)
	}
	if isAtMentionOpenKey(msg) {
		return m.openMentionPickerFromKey()
	}
	m.syncChatCursor()
	switch msg.Type {
	case tea.KeyRunes:
		return m.handleChatRunesKey(msg)
	case tea.KeySpace:
		return m.handleChatSpaceKey()
	case tea.KeyBackspace, tea.KeyCtrlH:
		return m.handleChatBackspaceKey()
	case tea.KeyDelete:
		return m.handleChatDeleteKey()
	case tea.KeyLeft:
		m.moveChatCursor(-1)
		return m, nil
	case tea.KeyRight:
		m.moveChatCursor(1)
		return m, nil
	case tea.KeyHome, tea.KeyCtrlA:
		m.moveChatCursorHome()
		return m, nil
	case tea.KeyEnd, tea.KeyCtrlE:
		return m.handleChatEndKey()
	case tea.KeyCtrlLeft:
		m.moveChatCursorWordLeft()
		return m, nil
	case tea.KeyCtrlRight:
		m.moveChatCursorWordRight()
		return m, nil
	case tea.KeyCtrlT:
		return m.openMentionPickerFromKey()
	case tea.KeyCtrlW:
		return m.handleChatKillWordKey()
	case tea.KeyCtrlY:
		// Yank-mode tap: when the composer is empty (or whitespace
		// only) copy the last assistant response to the clipboard.
		// When the user is mid-typing we let the rune through to
		// preserve the readline-style "yank insert" muscle memory.
		if strings.TrimSpace(m.chat.input) == "" {
			nm, cmd, _ := m.copyAssistantResponseAt(-1)
			return nm, cmd
		}
	case tea.KeyCtrlK:
		return m.handleChatKillLineKey()
	case tea.KeyCtrlX:
		suggestions := m.buildChatSuggestionState()
		return m.submitChatComposer(suggestions)
	case tea.KeyPgUp:
		m.scrollTranscript(-scrollPageStep)
		return m, nil
	case tea.KeyPgDown:
		m.scrollTranscript(scrollPageStep)
		return m, nil
	case tea.KeyCtrlHome:
		// Jump to the top of the transcript. Uses a sentinel-large
		// negative delta so the clamp inside scrollTranscript pins
		// us at maxBack (the "at top of history" notice fires).
		m.scrollTranscript(-1 << 30)
		return m, nil
	case tea.KeyCtrlEnd:
		// Jump to the latest message. Symmetric mirror of Ctrl+Home;
		// the clamp pins scrollback to 0 and the notice updates.
		m.scrollTranscript(1 << 30)
		return m, nil
	case tea.KeyEsc:
		return m.handleChatEscapeKey()
	case tea.KeyShiftUp, tea.KeyCtrlUp:
		m.scrollTranscript(-scrollFineStep)
		return m, nil
	case tea.KeyShiftDown, tea.KeyCtrlDown:
		m.scrollTranscript(scrollFineStep)
		return m, nil
	case tea.KeyUp:
		return m.handleChatUpKey()
	case tea.KeyDown:
		return m.handleChatDownKey()
	case tea.KeyTab:
		return m.handleChatTabKey()
	case tea.KeyCtrlJ:
		return m.handleChatNewlineInsert()
	case tea.KeyEnter:
		return m.handleChatEnterKey(msg)
	}
	return m.handleChatPrintableFallback(msg)
}
