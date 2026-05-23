package tui

// input_paste.go — paste-burst detection and pasteBlock placeholder
// management for the chat composer. The terminal can deliver a paste
// in two shapes: a single "bulk" KeyMsg with `Paste:true` (bracketed
// paste mode) or as a rapid-fire stream of single-rune KeyMsgs that
// look identical to typing. We want both to collapse into one
// pasteBlock so the composer shows `[pasted text #N +K lines]` instead
// of dumping the raw content into the input buffer.
//
// The detector runs in two stages:
//   1. As runes flow in, armPasteBurstCandidate / extendPasteBurstCandidate
//      track a rolling [start, end, runeCount] window with timing.
//   2. shouldStartPasteBurstOnEnter / shouldPromotePasteCandidateDuringInput
//      decide when the candidate has crossed the threshold to be
//      converted into a real pasteBlock; startPasteBurstFromInput then
//      replaces the in-buffer slice with the placeholder.
//
// All other Model state (input buffer, cursor, history) lives in
// input.go; input_history.go owns the up/down recall ring buffer.

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

// maxPasteRuneCount caps single paste content to prevent unbounded
// memory growth in the composer buffer. Content beyond this is
// silently truncated with an ellipsis marker.
const maxPasteRuneCount = 100_000

func (m *Model) addPasteBlock(content string) pasteBlock {
	content = normalizePastedText(content)
	if len([]rune(content)) > maxPasteRuneCount {
		runes := []rune(content)
		content = string(runes[:maxPasteRuneCount]) + "\n… (truncated)"
	}
	block := pasteBlock{
		content:   content,
		blockNum:  len(m.chat.pasteBlocks) + 1,
		lineCount: pasteLineCount(content),
	}
	m.chat.pasteBlocks = append(m.chat.pasteBlocks, block)
	m.insertInputText(block.placeholder())
	return block
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
		// Candidate range is stale (input was mutated by history recall
		// or autocomplete). Clear the candidate instead of sweeping up
		// the entire input buffer into a paste block.
		m.clearPasteBurst()
		return "", 0, 0
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
	addedLines := strings.Count(text, "\n")
	// Find the exact rune position of this block's placeholder
	// so we replace the placeholder, not user-typed text.
	oldPh := m.chat.pasteBlocks[idx].placeholder()
	m.chat.pasteBlocks[idx].content += text
	if addedLines > 0 {
		m.chat.pasteBlocks[idx].lineCount += addedLines
	}
	nextPh := m.chat.pasteBlocks[idx].placeholder()
	if oldPh != nextPh {
		byteStart := strings.Index(m.chat.input, oldPh)
		if byteStart >= 0 {
			runes := []rune(m.chat.input)
			runeStart := len([]rune(m.chat.input[:byteStart]))
			runeEnd := runeStart + len([]rune(oldPh))
			newRunes := []rune(nextPh)
			runes = append(runes[:runeStart], append(newRunes, runes[runeEnd:]...)...)
			m.chat.input = string(runes)
		}
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
	// Find the exact rune positions of each placeholder via
	// pastePlaceholderSpans (which uses byte-accurate matching then
	// converts to rune offsets) so we never accidentally replace
	// user-typed text that happens to look like a placeholder.
	spans := m.pastePlaceholderSpans()
	for i := range m.chat.pasteBlocks {
		m.chat.pasteBlocks[i].blockNum = i + 1
	}
	// Rebuild input from scratch using the new placeholder text.
	// This is more robust than incremental strings.Replace calls.
	runes := []rune(m.chat.input)
	// Process spans in reverse to preserve earlier indices.
	// Re-derive spans after renumbering to get new placeholder text.
	for si := len(spans) - 1; si >= 0; si-- {
		s := spans[si]
		if s.blockIndex < 0 || s.blockIndex >= len(m.chat.pasteBlocks) {
			continue
		}
		newPh := []rune(m.chat.pasteBlocks[s.blockIndex].placeholder())
		// Replace runes[s.start:s.end] with newPh
		runes = append(runes[:s.start], append(newPh, runes[s.end:]...)...)
	}
	m.chat.input = string(runes)
}
