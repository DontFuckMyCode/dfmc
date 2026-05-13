package tui

// input.go — chat composer text editing primitives. Owns the input
// buffer / cursor state and the readline-style navigation/deletion
// helpers (Home/End/word-left/right/row-up/row-down/kill-word/kill-line).
// Companion siblings:
//
//   - input_paste.go    paste-burst detection + pasteBlock placeholders
//   - input_history.go  up/down recall ring buffer
//
// All methods continue to live on `Model` — no behaviour change, no
// new abstractions. The split is purely organisational: the parent
// file's Update/handleChatKey switch still drives every keystroke,
// these helpers just stop crowding it.

// syncChatCursor reconciles the visible cursor with the current input
// buffer. The cursor is "manual" once the user has explicitly moved
// it (arrow keys, Home/End, etc.) — autocomplete / setChatInput /
// history recall reset it back to end-of-input.
func (m *Model) syncChatCursor() {
	inputLen := len([]rune(m.chat.input))
	if m.chat.cursorManual && m.chat.cursorInput != m.chat.input {
		m.chat.cursorManual = false
	}
	if !m.chat.cursorManual {
		m.chat.cursor = inputLen
		m.chat.cursorInput = m.chat.input
		return
	}
	if m.chat.cursor < 0 {
		m.chat.cursor = 0
	}
	if m.chat.cursor > inputLen {
		m.chat.cursor = inputLen
	}
	m.chat.cursorInput = m.chat.input
}

func (m *Model) setChatInput(text string) {
	m.chat.input = text
	m.prunePasteBlocksForInput()
	m.chat.cursorManual = false
	m.chat.cursor = len([]rune(m.chat.input))
	m.chat.cursorInput = m.chat.input
}

func (m *Model) insertInputText(text string) {
	m.insertInputTextRange(text)
}

func (m *Model) insertInputTextRange(text string) (start, end int) {
	if text == "" {
		m.syncChatCursor()
		return m.chat.cursor, m.chat.cursor
	}
	m.syncChatCursor()
	runes := []rune(m.chat.input)
	cursor := m.chat.cursor
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
	m.chat.input = string(updated)
	m.chat.cursor = cursor + len(insert)
	m.chat.cursorManual = true
	m.chat.cursorInput = m.chat.input
	return cursor, m.chat.cursor
}

// deleteInputBeforeCursor removes the character before the cursor. Paste
// placeholders are atomic: deleting any rune inside one removes both the
// visible placeholder and its stored backing text.
func (m *Model) deleteInputBeforeCursor() {
	m.syncChatCursor()
	runes := []rune(m.chat.input)
	if len(runes) == 0 || m.chat.cursor <= 0 {
		return
	}
	cursor := m.chat.cursor
	if cursor > len(runes) {
		cursor = len(runes)
	}
	m.deleteInputRange(cursor-1, cursor)
}

// deleteInputAtCursor removes the rune after the cursor. Paste placeholders are atomic, matching deleteInputBeforeCursor.
func (m *Model) deleteInputAtCursor() {
	m.syncChatCursor()
	runes := []rune(m.chat.input)
	if len(runes) == 0 {
		return
	}
	cursor := m.chat.cursor
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= len(runes) {
		return
	}
	m.deleteInputRange(cursor, cursor+1)
}

func (m *Model) deleteInputRange(start, end int) {
	m.syncChatCursor()
	runes := []rune(m.chat.input)
	if len(runes) == 0 {
		return
	}
	if start < 0 {
		start = 0
	}
	if end > len(runes) {
		end = len(runes)
	}
	if start >= end {
		return
	}
	for {
		hit := -1
		for _, span := range m.pastePlaceholderSpans() {
			if start < span.end && end > span.start {
				if span.start < start {
					start = span.start
				}
				if span.end > end {
					end = span.end
				}
				hit = span.blockIndex
				break
			}
		}
		if hit < 0 {
			break
		}
		m.chat.pasteBlocks = append(m.chat.pasteBlocks[:hit], m.chat.pasteBlocks[hit+1:]...)
	}
	runes = []rune(m.chat.input)
	updated := append([]rune(nil), runes[:start]...)
	updated = append(updated, runes[end:]...)
	m.chat.input = string(updated)
	m.renumberPastePlaceholders()
	m.chat.cursor = start
	m.chat.cursorManual = true
	m.chat.cursorInput = m.chat.input
}

func (m *Model) moveChatCursor(delta int) {
	m.syncChatCursor()
	cursor := m.chat.cursor + delta
	if cursor < 0 {
		cursor = 0
	}
	max := len([]rune(m.chat.input))
	if cursor > max {
		cursor = max
	}
	m.chat.cursor = cursor
	m.chat.cursorManual = true
	m.chat.cursorInput = m.chat.input
}

// Home/End/word-left/right/row-up/row-down/kill-word/kill-line
// helpers (moveChatCursorHome/End, moveChatCursorRowUp/Down,
// moveChatCursorWordLeft/Right, deleteInputWordBeforeCursor,
// deleteInputToEndOfLine + the chatInputLineHome/End and
// chatInputWordBoundaryLeft/Right + isInputWordSeparator helpers)
// live in input_navigation.go.
