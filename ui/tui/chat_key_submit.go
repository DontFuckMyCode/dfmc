package tui

// chat_key_submit.go — submitChatComposer + the small paste-notice
// helpers it shares with the rest of the composer surface.
// Companion siblings:
//
//   - chat_key.go             handleChatKey dispatcher + small helpers
//                             (isAtMentionOpenKey, openMentionPickerFromKey,
//                             refreshMentionPickerOpen)
//   - chat_key_navigation.go  Up/Down arrow handlers (autocomplete walk +
//                             multi-line row nav + history recall)

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) submitChatComposer(suggestions chatSuggestionState) (tea.Model, tea.Cmd) {
	m.clearPasteBurst()
	// Once the user is sending a new turn, the previous answer's
	// next-actions strip is stale — clear it so we don't render a
	// confusing strip whose suggestions belong to the *prior*
	// situation. The new turn either gets its own strip from the
	// engine or none.
	m.assistantNextActions.actions = nil

	// If any agent is waiting for user input, route there instead of root.
	if m.session != nil && m.session.HasWaitingAgents() && !m.chat.sending {
		raw := strings.TrimSpace(m.chat.input)
		if raw == "" {
			return m, nil
		}
		m.setChatInput("")
		if m.sendToWaitingAgent(raw) {
			return m, nil
		}
		// Fall through if sendToWaitingAgent didn't find anyone
	}

	if len(m.chat.pasteBlocks) > 0 {
		full := m.composeInput()
		n := len(m.chat.pasteBlocks)
		m.clearPasteBlocks()
		m.setChatInput("")
		m.notice = fmt.Sprintf("pasted text · %d block%s", n, _s(n))
		if m.chat.sending {
			if len(m.chat.pendingQueue) >= pendingQueueCap {
				block := m.addPasteBlock(full)
				m.notice = fmt.Sprintf("Queue full (%d max) — paste #%d kept in input.", pendingQueueCap, block.blockNum)
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

// pasteCollectingNotice is the single quiet message shown while a multi-chunk
// paste is still being assembled. We don't broadcast per-chunk byte/line
// counts — those are debug-grade signals that flooded the notice line and
// hid genuinely-actionable feedback.
const pasteCollectingNotice = "Collecting paste…"

// formatPasteNotice renders the one-line confirmation shown after a paste
// block lands in the input. The placeholder in the input box already carries
// the line count, so the notice stays terse: id + size hint only.
func formatPasteNotice(b pasteBlock) string {
	return fmt.Sprintf("Pasted #%d · %d line%s · %d bytes", b.blockNum, b.lineCount, _s(b.lineCount), len(b.content))
}
