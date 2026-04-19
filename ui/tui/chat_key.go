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
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) handleChatKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.commandPicker.active {
		return m.handleCommandPickerKey(msg)
	}
	// Dump the incoming key so we can see what bubbletea delivered. We
	// intentionally dump BEFORE the switch: the notice reflects the
	// arrival, then the render re-runs and shows the picker/input state
	// the user should compare against. Combined with m.chat.input always being
	// rendered in the input box, this tells us both the event and its
	// effect.
	if m.ui.keyLogEnabled {
		m.notice = fmt.Sprintf("key: %s · type=%d · runes=%q · alt=%t · input-before=%q",
			msg.String(), msg.Type, string(msg.Runes), msg.Alt, m.chat.input)
	}
	m.syncChatCursor()
	switch msg.Type {
	case tea.KeyRunes:
		if len(msg.Runes) == 1 && strings.TrimSpace(m.chat.input) == "" && len(m.chat.transcript) == 0 && !m.chat.sending {
			if template, ok := starterTemplateForDigit(msg.Runes[0]); ok {
				m.exitInputHistoryNavigation()
				m.chat.input = template
				m.chat.cursor = len([]rune(template))
				return m, nil
			}
		}
		m.exitInputHistoryNavigation()
		m.insertInputText(string(msg.Runes))
		m.slashMenu.command = 0
		m.slashMenu.commandArg = 0
		m.slashMenu.mention = 0
		m.slashMenu.quickAction = 0
		if m.ui.keyLogEnabled {
			m.notice = fmt.Sprintf("KeyRunes inserted %q → input=%q", string(msg.Runes), m.chat.input)
		}
		// When the user starts an @-mention but the project file index
		// hasn't landed yet (startup race, or the walk failed silently),
		// kick a refresh so the picker populates on the next frame
		// instead of leaving a dead empty-state.
		if strings.ContainsRune(string(msg.Runes), '@') && len(m.filesView.entries) == 0 && m.eng != nil {
			return m, loadFilesCmd(m.eng)
		}
		return m, nil
	case tea.KeySpace:
		m.exitInputHistoryNavigation()
		m.insertInputText(" ")
		m.slashMenu.command = 0
		m.slashMenu.commandArg = 0
		m.slashMenu.mention = 0
		m.slashMenu.quickAction = 0
		return m, nil
	case tea.KeyBackspace, tea.KeyCtrlH:
		m.exitInputHistoryNavigation()
		m.deleteInputBeforeCursor()
		m.slashMenu.command = 0
		m.slashMenu.commandArg = 0
		m.slashMenu.mention = 0
		m.slashMenu.quickAction = 0
		return m, nil
	case tea.KeyDelete:
		m.exitInputHistoryNavigation()
		m.deleteInputAtCursor()
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
		// Ctrl+T — open the file mention picker without typing '@'.
		// Turkish keyboards (Q layout) + MinTTY deliver '@' as alt+q
		// which can silently drop the '@' rune; users couldn't reach the
		// picker via @ at all. Ctrl+T is the guaranteed-deliverable
		// alternative — identical to typing '@' mid-composer except it
		// inserts a leading space when needed so the trailing token
		// becomes exactly '@', which is what activeMentionQuery checks.
		if !m.chat.sending {
			m.exitInputHistoryNavigation()
			// Ensure the '@' we insert is the start of a fresh mention
			// token. If the cursor is mid-word (e.g. "helloX|") prepend
			// a space so we get "helloX @|" rather than "helloX@|"
			// (which would treat the whole word as the mention).
			m.syncChatCursor()
			runes := []rune(m.chat.input)
			needSpace := m.chat.cursor > 0 && m.chat.cursor <= len(runes) &&
				!unicode.IsSpace(runes[m.chat.cursor-1])
			if needSpace {
				m.insertInputText(" @")
			} else {
				m.insertInputText("@")
			}
			m.slashMenu.mention = 0
			m.notice = "File picker open — type to filter, tab/enter inserts, esc cancels."
			// Kick a refresh if the index is empty, same as the typed-@
			// path does, so the picker isn't stuck on "Indexing…".
			if len(m.filesView.entries) == 0 && m.eng != nil {
				return m, loadFilesCmd(m.eng)
			}
			return m, nil
		}
		return m, nil
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
	case tea.KeyPgUp:
		m.scrollTranscript(-8)
		return m, nil
	case tea.KeyPgDown:
		m.scrollTranscript(8)
		return m, nil
	case tea.KeyEsc:
		// Streaming turn? Esc cancels the per-stream context. The goroutine
		// in startChatStream races ctx.Done against the next token and
		// emits chatDoneMsg/chatErrMsg, which clears sending state; we just
		// fire the cancel and surface an immediate notice.
		if m.chat.sending && m.cancelActiveStream() {
			m.notice = "Cancelling current turn… (provider may still finish the in-flight tool before stopping)"
			return m, nil
		}
		// Esc dismisses the parked-resume banner without tearing down the
		// parked state in the engine — the user can still /continue later.
		if m.ui.resumePromptActive {
			m.ui.resumePromptActive = false
			m.notice = "Resume prompt dismissed — parked loop kept; /continue re-opens it."
			return m, nil
		}
		return m, nil
	case tea.KeyShiftUp, tea.KeyCtrlUp:
		// Finer transcript scroll — Up/Down alone are taken by input
		// history + picker navigation, so we reserve the modifier variants
		// for chat scrolling. Three-message step matches the mouse wheel.
		m.scrollTranscript(-3)
		return m, nil
	case tea.KeyShiftDown, tea.KeyCtrlDown:
		m.scrollTranscript(3)
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
			if len(suggestions.mentionSuggestions) > 0 {
				idx := clampIndex(m.slashMenu.mention, len(suggestions.mentionSuggestions))
				if idx > 0 {
					idx--
				}
				m.slashMenu.mention = idx
				m.notice = "Mention: " + suggestions.mentionSuggestions[m.slashMenu.mention].Path
			}
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
			if len(suggestions.mentionSuggestions) > 0 {
				idx := clampIndex(m.slashMenu.mention, len(suggestions.mentionSuggestions))
				if idx < len(suggestions.mentionSuggestions)-1 {
					idx++
				}
				m.slashMenu.mention = idx
				m.notice = "Mention: " + suggestions.mentionSuggestions[m.slashMenu.mention].Path
			}
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
				m.setChatInput(selected.PreparedInput)
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
				return m, nil
			}
		}
		raw := strings.TrimSpace(m.chat.input)
		// Parked-resume affordance. When the loop is parked, a bare Enter
		// resumes; any typed text is forwarded to the resumed loop as a
		// /btw-style note so the user can redirect the continuation.
		if !m.chat.sending && m.ui.resumePromptActive && m.eng != nil && m.eng.HasParkedAgent() {
			m.setChatInput("")
			return m.startChatResume(raw)
		}
		if raw == "" {
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
			// Cap the queue so a user spamming Enter while a long stream
			// is in flight can't grow unbounded memory. 64 is enough
			// headroom for normal "ask three follow-ups in a row" flow
			// without becoming a DOS vector.
			if len(m.chat.pendingQueue) >= pendingQueueCap {
				m.notice = fmt.Sprintf("Queue full (%d max) — wait for the current reply, then send again.", pendingQueueCap)
				m.setChatInput("")
				return m, nil
			}
			m.chat.pendingQueue = append(m.chat.pendingQueue, raw)
			m.setChatInput("")
			m.notice = fmt.Sprintf("Queued (%d/%d) — will send after the current reply finishes.", len(m.chat.pendingQueue), pendingQueueCap)
			m = m.appendSystemMessage(fmt.Sprintf("▸ queued #%d: %s", len(m.chat.pendingQueue), raw))
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

// Chat composer input editing (cursor, word boundaries, Ctrl+W/K,
// Home/End, history navigation) lives in input.go.
