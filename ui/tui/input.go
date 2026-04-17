package tui

// input.go — chat composer input editing primitives.
//
// Lifted out of the 10K-line tui.go god file (REPORT.md C1) so the
// "what does typing do" surface lives in one obvious place. Every
// function here is either:
//   - a Model receiver mutating m.input / m.chatCursor / history fields, or
//   - a pure helper that takes a []rune and returns a new index.
//
// All methods continue to live on `Model` — no behaviour change, no
// new abstractions. The split is purely organisational: the parent
// file's Update/handleChatKey switch still drives every keystroke,
// these helpers just stop crowding it.

import "strings"

// syncChatCursor reconciles the visible cursor with the current input
// buffer. The cursor is "manual" once the user has explicitly moved
// it (arrow keys, Home/End, etc.) — autocomplete / setChatInput /
// history recall reset it back to end-of-input.
func (m *Model) syncChatCursor() {
	max := len([]rune(m.input))
	if m.chatCursorManual && m.chatCursorInput != m.input {
		m.chatCursorManual = false
	}
	if !m.chatCursorManual {
		m.chatCursor = max
		m.chatCursorInput = m.input
		return
	}
	if m.chatCursor < 0 {
		m.chatCursor = 0
	}
	if m.chatCursor > max {
		m.chatCursor = max
	}
	m.chatCursorInput = m.input
}

func (m *Model) setChatInput(text string) {
	m.input = text
	m.chatCursorManual = false
	m.chatCursor = len([]rune(text))
	m.chatCursorInput = text
}

func (m *Model) insertInputText(text string) {
	if text == "" {
		return
	}
	m.syncChatCursor()
	runes := []rune(m.input)
	cursor := m.chatCursor
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(runes) {
		cursor = len(runes)
	}
	insert := []rune(text)
	updated := make([]rune, 0, len(runes)+len(insert))
	updated = append(updated, runes[:cursor]...)
	updated = append(updated, insert...)
	updated = append(updated, runes[cursor:]...)
	m.input = string(updated)
	m.chatCursor = cursor + len(insert)
	m.chatCursorManual = true
	m.chatCursorInput = m.input
}

func (m *Model) deleteInputBeforeCursor() {
	m.syncChatCursor()
	runes := []rune(m.input)
	if len(runes) == 0 || m.chatCursor <= 0 {
		return
	}
	cursor := m.chatCursor
	if cursor > len(runes) {
		cursor = len(runes)
	}
	updated := append([]rune(nil), runes[:cursor-1]...)
	updated = append(updated, runes[cursor:]...)
	m.input = string(updated)
	m.chatCursor = cursor - 1
	m.chatCursorManual = true
	m.chatCursorInput = m.input
}

func (m *Model) deleteInputAtCursor() {
	m.syncChatCursor()
	runes := []rune(m.input)
	if len(runes) == 0 {
		return
	}
	cursor := m.chatCursor
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= len(runes) {
		return
	}
	updated := append([]rune(nil), runes[:cursor]...)
	updated = append(updated, runes[cursor+1:]...)
	m.input = string(updated)
	m.chatCursor = cursor
	m.chatCursorManual = true
	m.chatCursorInput = m.input
}

func (m *Model) moveChatCursor(delta int) {
	m.syncChatCursor()
	cursor := m.chatCursor + delta
	if cursor < 0 {
		cursor = 0
	}
	max := len([]rune(m.input))
	if cursor > max {
		cursor = max
	}
	m.chatCursor = cursor
	m.chatCursorManual = true
	m.chatCursorInput = m.input
}

// moveChatCursorHome — Home / Ctrl+A: jump to the start of the current
// logical line (not the buffer start). For single-line input this is
// indistinguishable from the old buffer-start behavior; in a multi-line
// composition it matches every text editor the user has ever used.
func (m *Model) moveChatCursorHome() {
	m.syncChatCursor()
	m.chatCursor = chatInputLineHome([]rune(m.input), m.chatCursor)
	m.chatCursorManual = true
	m.chatCursorInput = m.input
}

// moveChatCursorEnd — End / Ctrl+E: jump to the end of the current logical
// line. Again identical to buffer-end when there are no newlines, and
// correctly stops at the next `\n` when there are.
func (m *Model) moveChatCursorEnd() {
	m.syncChatCursor()
	m.chatCursor = chatInputLineEnd([]rune(m.input), m.chatCursor)
	m.chatCursorManual = true
	m.chatCursorInput = m.input
}

// chatInputLineHome returns the rune index of the start of the logical
// line containing cursor. That's either 0 or the index just after the
// preceding '\n'.
func chatInputLineHome(runes []rune, cursor int) int {
	if cursor <= 0 {
		return 0
	}
	if cursor > len(runes) {
		cursor = len(runes)
	}
	for i := cursor - 1; i >= 0; i-- {
		if runes[i] == '\n' {
			return i + 1
		}
	}
	return 0
}

// chatInputLineEnd returns the rune index of the end of the logical line
// containing cursor (the index of the next '\n', or len(runes)).
func chatInputLineEnd(runes []rune, cursor int) int {
	if cursor < 0 {
		cursor = 0
	}
	for i := cursor; i < len(runes); i++ {
		if runes[i] == '\n' {
			return i
		}
	}
	return len(runes)
}

// moveChatCursorRowUp drops the cursor onto the previous logical row at
// the same column offset (clamped to that row's length). Returns false
// when there's no previous row — the caller then falls back to whatever
// Up normally does (history navigation, picker move).
func (m *Model) moveChatCursorRowUp() bool {
	m.syncChatCursor()
	runes := []rune(m.input)
	cursor := m.chatCursor
	home := chatInputLineHome(runes, cursor)
	if home == 0 {
		return false
	}
	col := cursor - home
	prevEnd := home - 1                           // index of the '\n' separating the rows
	prevHome := chatInputLineHome(runes, prevEnd) // start of the previous row
	prevLen := prevEnd - prevHome
	if col > prevLen {
		col = prevLen
	}
	m.chatCursor = prevHome + col
	m.chatCursorManual = true
	m.chatCursorInput = m.input
	return true
}

// moveChatCursorRowDown — symmetric to moveChatCursorRowUp. Returns false
// when there's no next row.
func (m *Model) moveChatCursorRowDown() bool {
	m.syncChatCursor()
	runes := []rune(m.input)
	cursor := m.chatCursor
	home := chatInputLineHome(runes, cursor)
	end := chatInputLineEnd(runes, cursor)
	if end >= len(runes) {
		return false
	}
	col := cursor - home
	nextHome := end + 1
	nextEnd := chatInputLineEnd(runes, nextHome)
	nextLen := nextEnd - nextHome
	if col > nextLen {
		col = nextLen
	}
	m.chatCursor = nextHome + col
	m.chatCursorManual = true
	m.chatCursorInput = m.input
	return true
}

// chatInputWordBoundaryLeft returns the rune index of the start of the
// previous word, readline-style: skip any whitespace immediately behind
// the cursor, then skip the run of non-whitespace before it. Returns 0
// if the cursor is already at the start.
func chatInputWordBoundaryLeft(runes []rune, cursor int) int {
	if cursor <= 0 {
		return 0
	}
	if cursor > len(runes) {
		cursor = len(runes)
	}
	i := cursor
	for i > 0 && isInputWordSeparator(runes[i-1]) {
		i--
	}
	for i > 0 && !isInputWordSeparator(runes[i-1]) {
		i--
	}
	return i
}

// chatInputWordBoundaryRight returns the rune index at the END of the
// current or next word from cursor — readline convention: skip any
// leading whitespace under the cursor, then skip the following word.
// This is symmetric with chatInputWordBoundaryLeft (which lands on the
// START of a word), so Ctrl+Left and Ctrl+Right both walk across word
// boundaries rather than stalling on a word they're already inside.
func chatInputWordBoundaryRight(runes []rune, cursor int) int {
	if cursor < 0 {
		cursor = 0
	}
	i := cursor
	for i < len(runes) && isInputWordSeparator(runes[i]) {
		i++
	}
	for i < len(runes) && !isInputWordSeparator(runes[i]) {
		i++
	}
	return i
}

// isInputWordSeparator — whitespace is the only word boundary. This
// matches bash/readline and keeps [[file:path]] markers, @mentions, and
// paths like internal/auth/token.go intact as a single "word" so Ctrl+W
// nukes the whole reference in one keystroke instead of fragmenting it.
func isInputWordSeparator(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r':
		return true
	}
	return false
}

func (m *Model) moveChatCursorWordLeft() {
	m.syncChatCursor()
	m.chatCursor = chatInputWordBoundaryLeft([]rune(m.input), m.chatCursor)
	m.chatCursorManual = true
	m.chatCursorInput = m.input
}

func (m *Model) moveChatCursorWordRight() {
	m.syncChatCursor()
	m.chatCursor = chatInputWordBoundaryRight([]rune(m.input), m.chatCursor)
	m.chatCursorManual = true
	m.chatCursorInput = m.input
}

// deleteInputWordBeforeCursor implements Ctrl+W: kill the word to the
// left of the cursor. Idempotent at the start of the line.
func (m *Model) deleteInputWordBeforeCursor() {
	m.syncChatCursor()
	runes := []rune(m.input)
	if m.chatCursor <= 0 || len(runes) == 0 {
		return
	}
	cursor := m.chatCursor
	if cursor > len(runes) {
		cursor = len(runes)
	}
	start := chatInputWordBoundaryLeft(runes, cursor)
	updated := append([]rune(nil), runes[:start]...)
	updated = append(updated, runes[cursor:]...)
	m.input = string(updated)
	m.chatCursor = start
	m.chatCursorManual = true
	m.chatCursorInput = m.input
}

// deleteInputToEndOfLine implements Ctrl+K: kill text from the cursor to
// the end of the input. Idempotent when already at the end.
func (m *Model) deleteInputToEndOfLine() {
	m.syncChatCursor()
	runes := []rune(m.input)
	cursor := m.chatCursor
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= len(runes) {
		return
	}
	m.input = string(runes[:cursor])
	m.chatCursor = cursor
	m.chatCursorManual = true
	m.chatCursorInput = m.input
}

func (m *Model) pushInputHistory(raw string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return
	}
	if n := len(m.inputHistory); n > 0 && strings.EqualFold(strings.TrimSpace(m.inputHistory[n-1]), raw) {
		m.inputHistoryIndex = -1
		m.inputHistoryDraft = ""
		return
	}
	m.inputHistory = append(m.inputHistory, raw)
	if len(m.inputHistory) > 80 {
		drop := len(m.inputHistory) - 80
		m.inputHistory = m.inputHistory[drop:]
	}
	m.inputHistoryIndex = -1
	m.inputHistoryDraft = ""
}

func (m *Model) recallInputHistoryPrev() bool {
	if len(m.inputHistory) == 0 {
		return false
	}
	if m.inputHistoryIndex < 0 {
		m.inputHistoryDraft = m.input
		m.inputHistoryIndex = len(m.inputHistory) - 1
	} else if m.inputHistoryIndex > 0 {
		m.inputHistoryIndex--
	}
	m.setChatInput(m.inputHistory[m.inputHistoryIndex])
	return true
}

func (m *Model) recallInputHistoryNext() bool {
	if len(m.inputHistory) == 0 || m.inputHistoryIndex < 0 {
		return false
	}
	if m.inputHistoryIndex < len(m.inputHistory)-1 {
		m.inputHistoryIndex++
		m.setChatInput(m.inputHistory[m.inputHistoryIndex])
		return true
	}
	draft := m.inputHistoryDraft
	m.inputHistoryIndex = -1
	m.inputHistoryDraft = ""
	m.setChatInput(draft)
	return true
}

func (m *Model) exitInputHistoryNavigation() {
	if m.inputHistoryIndex < 0 {
		return
	}
	m.inputHistoryIndex = -1
	m.inputHistoryDraft = ""
}
