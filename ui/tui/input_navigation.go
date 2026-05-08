package tui

// input_navigation.go — readline-style cursor navigation and word/line
// kill helpers for the chat composer. Sibling of input.go which keeps
// the core buffer state primitives (syncChatCursor, setChatInput,
// insertInput*, deleteInput* basics, moveChatCursor delta helper).
// Companion siblings: input_paste.go (paste burst) and input_history.go
// (up/down recall ring).
//
// All methods continue to live on `Model` — no behaviour change, no
// new abstractions. The split groups Home/End/word-left/right/row-up/
// row-down/kill-word/kill-line into one file so the navigation
// behaviour is auditable in isolation.

// moveChatCursorHome — Home / Ctrl+A: jump to the start of the current
// logical line (not the buffer start). For single-line input this is
// indistinguishable from the old buffer-start behavior; in a multi-line
// composition it matches every text editor the user has ever used.
func (m *Model) moveChatCursorHome() {
	m.syncChatCursor()
	m.chat.cursor = chatInputLineHome([]rune(m.chat.input), m.chat.cursor)
	m.chat.cursorManual = true
	m.chat.cursorInput = m.chat.input
}

// moveChatCursorEnd — End / Ctrl+E: jump to the end of the current logical
// line. Again identical to buffer-end when there are no newlines, and
// correctly stops at the next `\n` when there are.
func (m *Model) moveChatCursorEnd() {
	m.syncChatCursor()
	m.chat.cursor = chatInputLineEnd([]rune(m.chat.input), m.chat.cursor)
	m.chat.cursorManual = true
	m.chat.cursorInput = m.chat.input
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
	runes := []rune(m.chat.input)
	cursor := m.chat.cursor
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
	m.chat.cursor = prevHome + col
	m.chat.cursorManual = true
	m.chat.cursorInput = m.chat.input
	return true
}

// moveChatCursorRowDown — symmetric to moveChatCursorRowUp. Returns false
// when there's no next row.
func (m *Model) moveChatCursorRowDown() bool {
	m.syncChatCursor()
	runes := []rune(m.chat.input)
	cursor := m.chat.cursor
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
	m.chat.cursor = nextHome + col
	m.chat.cursorManual = true
	m.chat.cursorInput = m.chat.input
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
	m.chat.cursor = chatInputWordBoundaryLeft([]rune(m.chat.input), m.chat.cursor)
	m.chat.cursorManual = true
	m.chat.cursorInput = m.chat.input
}

func (m *Model) moveChatCursorWordRight() {
	m.syncChatCursor()
	m.chat.cursor = chatInputWordBoundaryRight([]rune(m.chat.input), m.chat.cursor)
	m.chat.cursorManual = true
	m.chat.cursorInput = m.chat.input
}

// deleteInputWordBeforeCursor implements Ctrl+W: kill the word to the
// left of the cursor. Idempotent at the start of the line.
func (m *Model) deleteInputWordBeforeCursor() {
	m.syncChatCursor()
	runes := []rune(m.chat.input)
	if m.chat.cursor <= 0 || len(runes) == 0 {
		return
	}
	cursor := m.chat.cursor
	if cursor > len(runes) {
		cursor = len(runes)
	}
	start := chatInputWordBoundaryLeft(runes, cursor)
	m.deleteInputRange(start, cursor)
}

// deleteInputToEndOfLine implements Ctrl+K: kill text from the cursor to
// the end of the input. Idempotent when already at the end.
func (m *Model) deleteInputToEndOfLine() {
	m.syncChatCursor()
	runes := []rune(m.chat.input)
	cursor := m.chat.cursor
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= len(runes) {
		return
	}
	m.deleteInputRange(cursor, len(runes))
}
