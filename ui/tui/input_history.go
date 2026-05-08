package tui

// input_history.go — chat composer up-arrow / down-arrow recall.
// Keeps a bounded ring of submitted prompts (cap 80) plus a single
// "draft" snapshot so navigating away and back to the live composer
// doesn't lose half-typed text. The handler in chat_key.go calls
// recallInputHistoryPrev / recallInputHistoryNext / exitInputHistoryNavigation;
// each new submission appends via pushInputHistory.

import "strings"

func (m *Model) pushInputHistory(raw string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return
	}
	if n := len(m.inputHistory.history); n > 0 && strings.EqualFold(strings.TrimSpace(m.inputHistory.history[n-1]), raw) {
		m.inputHistory.index = -1
		m.inputHistory.draft = ""
		return
	}
	m.inputHistory.history = append(m.inputHistory.history, raw)
	if len(m.inputHistory.history) > 80 {
		drop := len(m.inputHistory.history) - 80
		m.inputHistory.history = m.inputHistory.history[drop:]
	}
	m.inputHistory.index = -1
	m.inputHistory.draft = ""
}

func (m *Model) recallInputHistoryPrev() bool {
	if len(m.inputHistory.history) == 0 {
		return false
	}
	if m.inputHistory.index < 0 {
		m.inputHistory.draft = m.chat.input
		m.inputHistory.index = len(m.inputHistory.history) - 1
	} else if m.inputHistory.index > 0 {
		m.inputHistory.index--
	}
	m.setChatInput(m.inputHistory.history[m.inputHistory.index])
	return true
}

func (m *Model) recallInputHistoryNext() bool {
	if len(m.inputHistory.history) == 0 || m.inputHistory.index < 0 {
		return false
	}
	if m.inputHistory.index < len(m.inputHistory.history)-1 {
		m.inputHistory.index++
		m.setChatInput(m.inputHistory.history[m.inputHistory.index])
		return true
	}
	draft := m.inputHistory.draft
	m.inputHistory.index = -1
	m.inputHistory.draft = ""
	m.setChatInput(draft)
	return true
}

func (m *Model) exitInputHistoryNavigation() {
	if m.inputHistory.index < 0 {
		return
	}
	m.inputHistory.index = -1
	m.inputHistory.draft = ""
}
