package tui

// input.go — chat composer input editing primitives.
//
// Lifted out of the 10K-line tui.go god file (REPORT.md C1) so the
// "what does typing do" surface lives in one obvious place. Every
// function here is either:
//   - a Model receiver mutating m.chat.input / m.chat.cursor / history fields, or
//   - a pure helper that takes a []rune and returns a new index.
//
// All methods continue to live on `Model` — no behaviour change, no
// new abstractions. The split is purely organisational: the parent
// file's Update/handleChatKey switch still drives every keystroke,
// these helpers just stop crowding it.

import (
	"strings"
	"time"
)

const pasteBurstWindow = 250 * time.Millisecond
const pasteLineEnterWindow = 100 * time.Millisecond
const pasteChunkRuneThreshold = 24
const charwisePasteImmediateRuneThreshold = 24
const charwisePasteLineBaseWindow = 140 * time.Millisecond
const charwisePasteLinePerRuneWindow = 12 * time.Millisecond
const charwisePasteLineMaxWindow = 900 * time.Millisecond

// syncChatCursor reconciles the visible cursor with the current input
// buffer. The cursor is "manual" once the user has explicitly moved
// it (arrow keys, Home/End, etc.) — autocomplete / setChatInput /
// history recall reset it back to end-of-input.
func (m *Model) syncChatCursor() {
	max := len([]rune(m.chat.input))
	if m.chat.cursorManual && m.chat.cursorInput != m.chat.input {
		m.chat.cursorManual = false
	}
	if !m.chat.cursorManual {
		m.chat.cursor = max
		m.chat.cursorInput = m.chat.input
		return
	}
	if m.chat.cursor < 0 {
		m.chat.cursor = 0
	}
	if m.chat.cursor > max {
		m.chat.cursor = max
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

func (m *Model) addPasteBlock(content string) pasteBlock {
	content = normalizePastedText(content)
	block := pasteBlock{
		content:   content,
		blockNum:  len(m.chat.pasteBlocks) + 1,
		lineCount: pasteLineCount(content),
	}
	m.chat.pasteBlocks = append(m.chat.pasteBlocks, block)
	m.insertInputText(block.placeholder())
	return block
}

func (m *Model) armPasteBurstCandidate(start, end, runeCount int, now time.Time) {
	m.armPasteBurstCandidateMode(start, end, runeCount, runeCount > 1, now)
}

func (m *Model) armPasteBurstCandidateMode(start, end, runeCount int, bulk bool, now time.Time) {
	if start < 0 {
		start = 0
	}
	if end < start {
		end = start
	}
	m.chat.pasteBurstUntil = now.Add(pasteBurstWindow)
	m.chat.pasteBurstBlock = 0
	m.chat.pasteCandidateStart = start
	m.chat.pasteCandidateEnd = end
	m.chat.pasteCandidateRunes = runeCount
	m.chat.pasteCandidateBulk = bulk
	m.chat.pasteCandidateSince = now
	m.chat.pasteCandidateLast = now
}

func (m *Model) extendPasteBurstCandidate(start, end, runeCount int, bulk bool, now time.Time) {
	if m.pasteBurstCandidateActive(now) && start == m.chat.pasteCandidateEnd {
		m.chat.pasteCandidateEnd = end
		m.chat.pasteCandidateRunes += runeCount
		m.chat.pasteCandidateBulk = m.chat.pasteCandidateBulk || bulk
		m.chat.pasteCandidateLast = now
		m.chat.pasteBurstUntil = now.Add(pasteBurstWindow)
		return
	}
	m.armPasteBurstCandidateMode(start, end, runeCount, bulk, now)
}

func (m *Model) pasteBurstActive(now time.Time) bool {
	return m.chat.pasteBurstBlock > 0 && !m.chat.pasteBurstUntil.IsZero() && now.Before(m.chat.pasteBurstUntil)
}

func (m *Model) pasteBurstCandidateActive(now time.Time) bool {
	return m.chat.pasteBurstBlock == 0 && !m.chat.pasteBurstUntil.IsZero() && now.Before(m.chat.pasteBurstUntil)
}

func (m *Model) clearPasteBurst() {
	m.chat.pasteBurstUntil = time.Time{}
	m.chat.pasteBurstBlock = 0
	m.chat.pasteCandidateStart = 0
	m.chat.pasteCandidateEnd = 0
	m.chat.pasteCandidateRunes = 0
	m.chat.pasteCandidateBulk = false
	m.chat.pasteCandidateSince = time.Time{}
	m.chat.pasteCandidateLast = time.Time{}
}

func (m *Model) startPasteBurstFromInput(now time.Time) bool {
	return m.startPasteBurstFromInputWithSuffix(now, "\n")
}

func (m *Model) promotePasteCandidateDuringInput(now time.Time) bool {
	return m.startPasteBurstFromInputWithSuffix(now, "")
}

func (m *Model) startPasteBurstFromInputWithSuffix(now time.Time, suffix string) bool {
	content, start, end := m.pasteCandidateText()
	if strings.TrimSpace(content) == "" {
		m.clearPasteBurst()
		return false
	}
	block := m.replaceInputRangeWithPasteBlock(start, end, content+suffix)
	m.chat.pasteBurstBlock = block.blockNum
	m.chat.pasteBurstUntil = now.Add(pasteBurstWindow)
	m.chat.pasteCandidateStart = 0
	m.chat.pasteCandidateEnd = 0
	m.chat.pasteCandidateRunes = 0
	m.chat.pasteCandidateBulk = false
	m.chat.pasteCandidateSince = time.Time{}
	m.chat.pasteCandidateLast = time.Time{}
	return true
}

func (m *Model) activatePasteBurstBlock(block pasteBlock, now time.Time) {
	m.chat.pasteBurstBlock = block.blockNum
	m.chat.pasteBurstUntil = now.Add(pasteBurstWindow)
	m.chat.pasteCandidateStart = 0
	m.chat.pasteCandidateEnd = 0
	m.chat.pasteCandidateRunes = 0
	m.chat.pasteCandidateBulk = false
	m.chat.pasteCandidateSince = time.Time{}
	m.chat.pasteCandidateLast = time.Time{}
}

func (m *Model) pasteCandidateText() (content string, start int, end int) {
	runes := []rune(m.chat.input)
	start = m.chat.pasteCandidateStart
	end = m.chat.pasteCandidateEnd
	if start < 0 || end > len(runes) || start >= end {
		return normalizePastedText(m.chat.input), 0, len(runes)
	}
	return normalizePastedText(string(runes[start:end])), start, end
}

func (m *Model) shouldStartPasteBurstOnEnter(now time.Time) bool {
	if !m.pasteBurstCandidateActive(now) {
		return false
	}
	if m.chat.pasteCandidateBulk {
		return true
	}
	return m.chat.pasteCandidateRunes >= 3 &&
		!m.chat.pasteCandidateSince.IsZero() &&
		!m.chat.pasteCandidateLast.IsZero() &&
		now.Sub(m.chat.pasteCandidateSince) <= charwisePasteWindow(m.chat.pasteCandidateRunes) &&
		now.Sub(m.chat.pasteCandidateLast) <= pasteLineEnterWindow
}

func (m *Model) shouldPromotePasteCandidateDuringInput(now time.Time) bool {
	if !m.pasteBurstCandidateActive(now) {
		return false
	}
	if m.chat.pasteCandidateRunes < charwisePasteImmediateRuneThreshold {
		return false
	}
	if m.chat.pasteCandidateBulk {
		return true
	}
	return !m.chat.pasteCandidateSince.IsZero() &&
		!m.chat.pasteCandidateLast.IsZero() &&
		now.Sub(m.chat.pasteCandidateSince) <= charwisePasteWindow(m.chat.pasteCandidateRunes)
}

func charwisePasteWindow(runeCount int) time.Duration {
	if runeCount < 0 {
		runeCount = 0
	}
	window := charwisePasteLineBaseWindow + time.Duration(runeCount)*charwisePasteLinePerRuneWindow
	if window > charwisePasteLineMaxWindow {
		return charwisePasteLineMaxWindow
	}
	return window
}

func (m *Model) replaceInputRangeWithPasteBlock(start, end int, content string) pasteBlock {
	runes := []rune(m.chat.input)
	if start < 0 {
		start = 0
	}
	if end > len(runes) {
		end = len(runes)
	}
	if start > end {
		start = end
	}
	content = normalizePastedText(content)
	block := pasteBlock{
		content:   content,
		blockNum:  len(m.chat.pasteBlocks) + 1,
		lineCount: pasteLineCount(content),
	}
	m.chat.pasteBlocks = append(m.chat.pasteBlocks, block)
	placeholder := []rune(block.placeholder())
	updated := make([]rune, 0, len(runes)-(end-start)+len(placeholder))
	updated = append(updated, runes[:start]...)
	updated = append(updated, placeholder...)
	updated = append(updated, runes[end:]...)
	m.chat.input = string(updated)
	m.chat.cursor = start + len(placeholder)
	m.chat.cursorManual = true
	m.chat.cursorInput = m.chat.input
	return block
}

func (m *Model) appendPasteBurstText(text string, now time.Time) bool {
	if !m.pasteBurstActive(now) {
		return false
	}
	idx := m.chat.pasteBurstBlock - 1
	if idx < 0 || idx >= len(m.chat.pasteBlocks) {
		m.clearPasteBurst()
		return false
	}
	text = normalizePastedText(text)
	old := m.chat.pasteBlocks[idx].placeholder()
	addedLines := strings.Count(text, "\n")
	m.chat.pasteBlocks[idx].content += text
	if addedLines > 0 {
		m.chat.pasteBlocks[idx].lineCount += addedLines
	}
	next := m.chat.pasteBlocks[idx].placeholder()
	if old != next {
		m.chat.input = strings.Replace(m.chat.input, old, next, 1)
		m.syncChatCursor()
	} else {
		m.chat.suppressPasteRender = true
	}
	m.chat.pasteBurstUntil = now.Add(pasteBurstWindow)
	return true
}

func normalizePastedText(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	return content
}

func (m *Model) clearPasteBlocks() {
	m.chat.pasteBlocks = nil
	m.clearPasteBurst()
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

type pastePlaceholderSpan struct {
	blockIndex int
	start      int
	end        int
}

func (m *Model) pastePlaceholderSpans() []pastePlaceholderSpan {
	if len(m.chat.pasteBlocks) == 0 || m.chat.input == "" {
		return nil
	}
	spans := make([]pastePlaceholderSpan, 0, len(m.chat.pasteBlocks))
	for i, b := range m.chat.pasteBlocks {
		ph := b.placeholder()
		byteStart := strings.Index(m.chat.input, ph)
		if byteStart < 0 {
			continue
		}
		start := len([]rune(m.chat.input[:byteStart]))
		spans = append(spans, pastePlaceholderSpan{
			blockIndex: i,
			start:      start,
			end:        start + len([]rune(ph)),
		})
	}
	return spans
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

func (m *Model) prunePasteBlocksForInput() {
	if len(m.chat.pasteBlocks) == 0 {
		return
	}
	kept := m.chat.pasteBlocks[:0]
	for _, b := range m.chat.pasteBlocks {
		if strings.Contains(m.chat.input, b.placeholder()) {
			kept = append(kept, b)
		}
	}
	m.chat.pasteBlocks = kept
	m.renumberPastePlaceholders()
}

func (m *Model) renumberPastePlaceholders() {
	if len(m.chat.pasteBlocks) == 0 {
		return
	}
	for i := range m.chat.pasteBlocks {
		old := m.chat.pasteBlocks[i].placeholder()
		m.chat.pasteBlocks[i].blockNum = i + 1
		next := m.chat.pasteBlocks[i].placeholder()
		if old != next {
			m.chat.input = strings.Replace(m.chat.input, old, next, 1)
		}
	}
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
