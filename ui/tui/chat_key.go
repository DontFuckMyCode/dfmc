// Chat panel keyboard router. Extracted from tui.go to keep keyboard handling
// next to the chat-composer state. handleChatKey is the dispatch entry: it
// fires the command picker / mention / slash autocomplete pipelines first,
// then falls through to text editing (cursor, word boundaries, history nav,
// transcript scroll).
//
// Text-insertion case bodies (KeyRunes / KeySpace / KeyEnter / KeyCtrlJ
// newline + the printable defensive fallback) and the @-mention picker
// helpers (isAtMentionOpenKey, openMentionPickerFromKey,
// refreshMentionPickerOpen) live in chat_key_text.go. KeyUp/KeyDown
// autocomplete-walk + history-recall logic lives in
// chat_key_navigation.go. submitChatComposer + the paste-notice helpers
// it shares with the rest of the composer surface live in
// chat_key_submit.go. Pure cursor/word manipulation lives in input.go
// and is called from here — do not inline those primitives.

package tui

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
	// Dump the incoming key so we can see what bubbletea delivered. We
	// intentionally dump BEFORE the switch: the notice reflects the
	// arrival, then the render re-runs and shows the picker/input state
	// the user should compare against. Combined with m.chat.input always being
	// rendered in the input box, this tells us both the event and its
	// effect.
	m.syncChatCursor()
	switch msg.Type {
	case tea.KeyRunes:
		return m.handleChatRunesKey(msg)
	case tea.KeySpace:
		return m.handleChatSpaceKey()
	case tea.KeyBackspace, tea.KeyCtrlH:
		m.exitInputHistoryNavigation()
		m.deleteInputBeforeCursor()
		m.refreshMentionPickerOpen()
		m.slashMenu.resetIndices()
		return m, nil
	case tea.KeyDelete:
		m.exitInputHistoryNavigation()
		m.deleteInputAtCursor()
		m.refreshMentionPickerOpen()
		m.slashMenu.resetIndices()
		return m, nil
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
		if m.chat.scrollback > 0 {
			m.chat.scrollback = 0
			m.notice = "Transcript: jumped to latest"
			return m, nil
		}
		m.moveChatCursorEnd()
		return m, nil
	case tea.KeyCtrlLeft:
		// readline-style word-left. Leaves the picker indices alone so the
		// user can re-anchor mid-word without losing their selection.
		m.moveChatCursorWordLeft()
		return m, nil
	case tea.KeyCtrlRight:
		m.moveChatCursorWordRight()
		return m, nil
	case tea.KeyCtrlT:
		return m.openMentionPickerFromKey()
	case tea.KeyCtrlW:
		// Ctrl+W — kill word before cursor. Whitespace-only separator
		// keeps @mentions and [[file:...]] markers atomic.
		m.exitInputHistoryNavigation()
		m.deleteInputWordBeforeCursor()
		m.slashMenu.resetIndices()
		return m, nil
	case tea.KeyCtrlK:
		// Ctrl+K — kill to end of line. Pairs with Ctrl+U (kill whole
		// line) so editors coming from bash/emacs feel at home.
		m.exitInputHistoryNavigation()
		m.deleteInputToEndOfLine()
		m.slashMenu.resetIndices()
		return m, nil
	case tea.KeyCtrlX:
		suggestions := m.buildChatSuggestionState()
		return m.submitChatComposer(suggestions)
	case tea.KeyPgUp:
		m.scrollTranscript(-scrollPageStep)
		return m, nil
	case tea.KeyPgDown:
		m.scrollTranscript(scrollPageStep)
		return m, nil
	case tea.KeyEsc:
		// Esc is purely a UI dismiss key. It handles:
		//   1. Resume prompt dismissal (streaming cancel is ctrl+c — see below)
		//   2. Generic pass-through (returns m, nil for unhandled cases)
		// Note: ctrl+c is the streaming cancel when actively sending.
		// Esc is intentionally NOT a streaming cancel to avoid the confusing
		// interaction where Esc dismisses a resume prompt AND cancels a stream.
		if m.ui.resumePromptActive {
			m.ui.resumePromptActive = false
			m.notice = "Resume prompt dismissed — /continue re-opens it."
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
	case tea.KeyShiftUp, tea.KeyCtrlUp:
		// Finer transcript scroll — Up/Down alone are taken by input
		// history + picker navigation, so we reserve the modifier variants
		// for chat scrolling. scrollFineStep matches the mouse wheel.
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
		if !m.chat.sending {
			suggestions := m.buildChatSuggestionState()
			// Autocomplete outcomes are already visible in the input box —
			// no need to echo them into the footer notice slot.
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
		}
		return m, nil
	case tea.KeyCtrlJ:
		// Ctrl+J — insert a literal newline. This is the reliable cross-
		// terminal way to get a newline in the composer (Shift+Enter is
		// indistinguishable from Enter on most terminals and was a lie in
		// the old help overlay). Alt+Enter is handled at the KeyEnter
		// branch below by checking msg.Alt.
		return m.handleChatNewlineInsert()
	case tea.KeyEnter:
		return m.handleChatEnterKey(msg)
	}
	return m.handleChatPrintableFallback(msg)
}
