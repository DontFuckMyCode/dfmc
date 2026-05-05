// Chat panel keyboard router. Extracted from tui.go to keep keyboard handling
// next to the chat-composer state. handleChatKey is the dispatch entry: it
// fires the command picker / mention / slash autocomplete pipelines first,
// then falls through to text editing (cursor, word boundaries, history nav,
// transcript scroll).
//
// Co-located helpers (handleChatTabKey, handleChatEnterKey, etc.) belong
// in this file. Pure cursor/word manipulation lives in input.go and is
// called from here — do not inline those primitives.

package tui

import (
	"fmt"
	"strings"
	"time"
	"unicode"

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
		if len(msg.Runes) == 1 && strings.TrimSpace(m.chat.input) == "" && len(m.chat.transcript) == 0 && !m.chat.sending {
			if template, ok := starterTemplateForDigit(msg.Runes[0]); ok {
				m.exitInputHistoryNavigation()
				m.chat.input = template
				m.chat.cursor = len([]rune(template))
				m.notice = fmt.Sprintf("Starter: %s", template)
				return m, nil
			}
		}
		m.exitInputHistoryNavigation()
		m.slashMenu.command = 0
		m.slashMenu.commandArg = 0
		m.slashMenu.mention = 0
		m.slashMenu.quickAction = 0
		inserted := string(msg.Runes)
		if msg.Paste || strings.ContainsAny(inserted, "\r\n") {
			m.clearPasteBurst()
			block := m.addPasteBlock(inserted)
			m.notice = fmt.Sprintf("PASTE #%d: %d lines, %d bytes", block.blockNum, block.lineCount, len(block.content))
			return m, nil
		}
		now := time.Now()
		if m.appendPasteBurstText(inserted, now) {
			m.notice = "PASTE collecting..."
			return m, nil
		}
		insertedRunes := len([]rune(inserted))
		if insertedRunes >= pasteChunkRuneThreshold {
			m.clearPasteBurst()
			block := m.addPasteBlock(inserted)
			m.activatePasteBurstBlock(block, now)
			m.notice = fmt.Sprintf("PASTE #%d: %d lines, %d bytes", block.blockNum, block.lineCount, len(block.content))
			return m, nil
		}
		start, end := m.insertInputTextRange(inserted)
		if insertedRunes > 1 {
			m.armPasteBurstCandidateMode(start, end, insertedRunes, true, now)
		} else {
			m.extendPasteBurstCandidate(start, end, insertedRunes, false, now)
		}
		if m.shouldPromotePasteCandidateDuringInput(now) && m.promotePasteCandidateDuringInput(now) {
			m.notice = "PASTE collecting..."
			return m, nil
		}
		if !m.pasteBurstCandidateActive(now) && !m.pasteBurstActive(now) {
			m.clearPasteBurst()
		}
		if strings.ContainsRune(string(msg.Runes), '@') {
			m.chat.mentionPickerOpen = true
			if len(m.filesView.entries) == 0 && m.eng != nil {
				return m, loadFilesCmd(m.eng)
			}
		}
		return m, nil
	case tea.KeySpace:
		m.exitInputHistoryNavigation()
		now := time.Now()
		if m.appendPasteBurstText(" ", now) {
			m.notice = "PASTE collecting..."
			return m, nil
		}
		start, end := m.insertInputTextRange(" ")
		m.extendPasteBurstCandidate(start, end, 1, false, now)
		if m.shouldPromotePasteCandidateDuringInput(now) && m.promotePasteCandidateDuringInput(now) {
			m.notice = "PASTE collecting..."
			return m, nil
		}
		m.slashMenu.command = 0
		m.slashMenu.commandArg = 0
		m.slashMenu.mention = 0
		m.slashMenu.quickAction = 0
		return m, nil
	case tea.KeyBackspace, tea.KeyCtrlH:
		m.exitInputHistoryNavigation()
		m.deleteInputBeforeCursor()
		m.refreshMentionPickerOpen()
		m.slashMenu.command = 0
		m.slashMenu.commandArg = 0
		m.slashMenu.mention = 0
		m.slashMenu.quickAction = 0
		return m, nil
	case tea.KeyDelete:
		m.exitInputHistoryNavigation()
		m.deleteInputAtCursor()
		m.refreshMentionPickerOpen()
		m.slashMenu.command = 0
		m.slashMenu.commandArg = 0
		m.slashMenu.mention = 0
		m.slashMenu.quickAction = 0
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
		m.slashMenu.command = 0
		m.slashMenu.commandArg = 0
		m.slashMenu.mention = 0
		m.slashMenu.quickAction = 0
		return m, nil
	case tea.KeyCtrlK:
		// Ctrl+K — kill to end of line. Pairs with Ctrl+U (kill whole
		// line) so editors coming from bash/emacs feel at home.
		m.exitInputHistoryNavigation()
		m.deleteInputToEndOfLine()
		m.slashMenu.command = 0
		m.slashMenu.commandArg = 0
		m.slashMenu.mention = 0
		m.slashMenu.quickAction = 0
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
			m.slashMenu.command = 0
			m.slashMenu.commandArg = 0
			m.slashMenu.mention = 0
			m.slashMenu.quickAction = 0
			m.notice = "History: previous input"
			return m, nil
		}
		return m, nil
	case tea.KeyDown:
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
			m.slashMenu.command = 0
			m.slashMenu.commandArg = 0
			m.slashMenu.mention = 0
			m.slashMenu.quickAction = 0
			m.notice = "History: next input"
			return m, nil
		}
		return m, nil
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
		m.exitInputHistoryNavigation()
		m.insertInputText("\n")
		m.slashMenu.command = 0
		m.slashMenu.commandArg = 0
		m.slashMenu.mention = 0
		m.slashMenu.quickAction = 0
		return m, nil
	case tea.KeyEnter:
		// Alt+Enter also inserts a newline rather than submitting — some
		// terminals deliver Alt+Enter as KeyEnter with Alt=true. On
		// terminals without a real Alt key this is a no-op for regular
		// Enter and submission still works.
		if msg.Alt {
			m.exitInputHistoryNavigation()
			m.insertInputText("\n")
			m.clearPasteBurst()
			m.slashMenu.command = 0
			m.slashMenu.commandArg = 0
			m.slashMenu.mention = 0
			m.slashMenu.quickAction = 0
			return m, nil
		}
		suggestions := m.buildChatSuggestionState()
		if !m.chat.sending && len(suggestions.mentionSuggestions) > 0 {
			if next, ok := autocompleteMentionSelectionFromSuggestions(m.chat.input, m.slashMenu.mention, suggestions.mentionSuggestions); ok {
				m.setChatInput(next)
				m.slashMenu.mention = 0
				m.chat.mentionPickerOpen = false
				return m, nil
			}
		}
		if !m.chat.sending && suggestions.slashMenuActive && len(suggestions.slashCommands) > 0 {
			if next, ok := m.expandSlashSelection(strings.TrimSpace(m.chat.input)); ok {
				m.setChatInput(next)
				return m, nil
			}
		}
		if !m.chat.sending && hasTrailingWhitespace(m.chat.input) && len(suggestions.slashArgSuggestions) > 0 {
			if next, ok := m.autocompleteSlashArg(); ok {
				m.setChatInput(next)
				m.slashMenu.commandArg = 0
				return m, nil
			}
		}
		now := time.Now()
		if m.pasteBurstActive(now) {
			if m.appendPasteBurstText("\n", now) {
				m.notice = "PASTE collecting..."
				return m, nil
			}
		}
		if m.shouldStartPasteBurstOnEnter(now) && m.startPasteBurstFromInput(now) {
			m.notice = "PASTE collecting..."
			return m, nil
		}
		if len(m.chat.pasteBlocks) == 0 && strings.HasPrefix(strings.TrimSpace(m.chat.input), "/") {
			return m.submitChatComposer(suggestions)
		}
		m.exitInputHistoryNavigation()
		m.insertInputText("\n")
		m.clearPasteBurst()
		m.slashMenu.command = 0
		m.slashMenu.commandArg = 0
		m.slashMenu.mention = 0
		m.slashMenu.quickAction = 0
		return m, nil
	}
	// Defensive catch-all for keys that didn't match any explicit case but
	// still carry printable runes. On Windows with non-standard keyboard
	// layouts (Turkish Q, AltGr combos, IME pass-through) bubbletea can
	// deliver a key event whose Type is something like KeyCtrlQ while
	// Runes=['@'] — the earlier code ignored Runes in that branch and the
	// '@' never reached the input buffer, which looked to the user like
	// "the @ key doesn't trigger the picker". If Runes is non-empty and
	// at least one rune is printable, insert them as text.
	if len(msg.Runes) > 0 {
		printable := false
		for _, r := range msg.Runes {
			if r >= 0x20 && r != 0x7f {
				printable = true
				break
			}
		}
		if printable {
			m.exitInputHistoryNavigation()
			m.insertInputText(string(msg.Runes))
			if strings.ContainsRune(string(msg.Runes), '@') {
				m.chat.mentionPickerOpen = true
			}
			m.slashMenu.command = 0
			m.slashMenu.commandArg = 0
			m.slashMenu.mention = 0
			m.slashMenu.quickAction = 0
			if m.ui.keyLogEnabled {
				m.notice = fmt.Sprintf("FALLBACK inserted %q → input=%q", string(msg.Runes), m.chat.input)
			}
			if strings.ContainsRune(string(msg.Runes), '@') && len(m.filesView.entries) == 0 && m.eng != nil {
				return m, loadFilesCmd(m.eng)
			}
			return m, nil
		}
	}
	return m, nil
}

func isAtMentionOpenKey(msg tea.KeyMsg) bool {
	if len(msg.Runes) > 0 {
		return false
	}
	if msg.Alt && msg.Type == tea.KeyCtrlQ {
		return true
	}
	key := strings.ToLower(strings.TrimSpace(msg.String()))
	return key == "alt+q" || key == "alt+ctrl+q"
}

func (m Model) openMentionPickerFromKey() (tea.Model, tea.Cmd) {
	if m.chat.sending {
		return m, nil
	}
	m.exitInputHistoryNavigation()
	m.syncChatCursor()
	runes := []rune(m.chat.input)
	needSpace := m.chat.cursor > 0 && m.chat.cursor <= len(runes) &&
		!unicode.IsSpace(runes[m.chat.cursor-1])
	if needSpace {
		m.insertInputText(" @")
	} else {
		m.insertInputText("@")
	}
	m.chat.mentionPickerOpen = true
	m.slashMenu.mention = 0
	m.notice = "File picker open - type to filter, tab/enter inserts, esc cancels."
	if len(m.filesView.entries) == 0 && m.eng != nil {
		return m, loadFilesCmd(m.eng)
	}
	return m, nil
}

func (m *Model) refreshMentionPickerOpen() {
	if m == nil {
		return
	}
	if _, _, ok := activeMentionQuery(m.chat.input); !ok {
		m.chat.mentionPickerOpen = false
	}
}

func (m Model) submitChatComposer(suggestions chatSuggestionState) (tea.Model, tea.Cmd) {
	m.clearPasteBurst()
	if len(m.chat.pasteBlocks) > 0 {
		full := m.composeInput()
		n := len(m.chat.pasteBlocks)
		m.clearPasteBlocks()
		m.setChatInput("")
		m.notice = fmt.Sprintf("pasted text · %d block%s", n, _s(n))
		if m.chat.sending {
			if len(m.chat.pendingQueue) >= pendingQueueCap {
				block := m.addPasteBlock(full)
				m.notice = fmt.Sprintf("Queue full (%d max) - PASTE #%d kept in input.", pendingQueueCap, block.blockNum)
				return m, nil
			}
			m.chat.pendingQueue = append(m.chat.pendingQueue, full)
			m.notice = fmt.Sprintf("Pasted text queued as one message (#%d)", len(m.chat.pendingQueue))
			m = m.appendSystemMessage(fmt.Sprintf("queued paste #%d: %s", len(m.chat.pendingQueue), truncateSingleLine(full, 80)))
			return m, nil
		}
		next, cmdOut := m.submitChatQuestion(full, nil)
		return next, cmdOut
	}

	raw := strings.TrimSpace(m.chat.input)
	if !m.chat.sending && m.ui.resumePromptActive && m.eng != nil && m.eng.HasParkedAgent() {
		m.setChatInput("")
		return m.startChatResume(raw)
	}
	if raw == "" {
		if len(m.chat.input) > 0 {
			m.notice = "input is whitespace-only - type a message or press Esc to clear"
		}
		return m, nil
	}
	if m.chat.sending {
		if strings.HasPrefix(raw, "/") {
			cmd, _, _, err := parseChatCommandInput(raw)
			if err != nil || !isKnownChatCommandToken(cmd) || isImmediateChatSlashCommand(cmd) {
				m.pushInputHistory(raw)
				m.setChatInput("")
				next, cmdOut, _ := m.executeChatCommand(raw)
				return next, cmdOut
			}
		}
		if len(m.chat.pendingQueue) >= pendingQueueCap {
			m.notice = fmt.Sprintf("Queue full (%d max) - wait for the current reply, then send again.", pendingQueueCap)
			return m, nil
		}
		m.chat.pendingQueue = append(m.chat.pendingQueue, raw)
		m.notice = fmt.Sprintf("Queued (%d/%d) - will send after the current reply finishes.", len(m.chat.pendingQueue), pendingQueueCap)
		m = m.appendSystemMessage(fmt.Sprintf("queued #%d: %s", len(m.chat.pendingQueue), raw))
		m.setChatInput("")
		return m, nil
	}
	if expanded, ok := m.expandSlashSelection(raw); ok {
		raw = expanded
	}
	m.pushInputHistory(raw)
	if next, cmd, handled := m.executeChatCommand(raw); handled {
		return next, cmd
	}
	question := m.chatPrompt()
	if question == "" {
		return m, nil
	}
	m.setChatInput("")
	return m.submitChatQuestion(question, suggestions.quickActions)
}

// _s returns "s" for plural, "" for singular.
func _s(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// Chat composer input editing (cursor, word boundaries, Ctrl+W/K,
// Home/End, history navigation) lives in input.go.
