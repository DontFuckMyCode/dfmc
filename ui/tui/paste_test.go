package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestPasteBlockDetection(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0

	// Simulate pasting multi-line text via KeyRunes with \n
	pasteMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("line1\nline2\nline3")}

	next1, _ := m.Update(pasteMsg)
	m2, ok := next1.(Model)
	if !ok {
		t.Fatalf("expected Model, got %T", next1)
	}

	// Should have created a paste block
	if len(m2.chat.pasteBlocks) != 1 {
		t.Fatalf("expected 1 paste block, got %d", len(m2.chat.pasteBlocks))
	}

	// Input should contain the placeholder
	if !strings.Contains(m2.chat.input, "[Pasted #1 3 lines]") {
		t.Fatalf("expected placeholder in input, got %q", m2.chat.input)
	}

	// Notice should confirm paste
	if !strings.Contains(m2.notice, "PASTE") {
		t.Fatalf("expected paste notice, got %q", m2.notice)
	}
}

func TestLongPasteChunkBecomesPlaceholderImmediately(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0
	chunk := strings.Repeat("x", pasteChunkRuneThreshold+8)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(chunk)})
	m = next.(Model)

	if len(m.chat.pasteBlocks) != 1 {
		t.Fatalf("expected long chunk to become a paste block immediately, got %d", len(m.chat.pasteBlocks))
	}
	if strings.Contains(m.chat.input, chunk) || !strings.Contains(m.chat.input, "[Pasted #1 1 lines]") {
		t.Fatalf("long paste chunk should not render raw text, input=%q", m.chat.input)
	}
	if got := m.chat.pasteBlocks[0].content; got != chunk {
		t.Fatalf("stored paste content changed, got %q", got)
	}
}

func TestLongLinePasteCollectsFollowingLinesWithoutRawRender(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0
	first := strings.Repeat("a", pasteChunkRuneThreshold+4)
	second := strings.Repeat("b", pasteChunkRuneThreshold+4)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(first)})
	m = next.(Model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(second)})
	m = next.(Model)

	if len(m.chat.pasteBlocks) != 1 {
		t.Fatalf("expected one active paste block, got %d", len(m.chat.pasteBlocks))
	}
	if strings.Contains(m.chat.input, first) || strings.Contains(m.chat.input, second) {
		t.Fatalf("line paste should stay as placeholder, input=%q", m.chat.input)
	}
	if !strings.Contains(m.chat.input, "[Pasted #1 2 lines]") {
		t.Fatalf("placeholder should show updated line count, got %q", m.chat.input)
	}
	if got := m.chat.pasteBlocks[0].content; got != first+"\n"+second {
		t.Fatalf("expected collected paste content, got %q", got)
	}
}

func TestCharwiseLongPastePromotesToPlaceholderBeforeEnter(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0
	line := strings.Repeat("x", charwisePasteImmediateRuneThreshold+12)

	for _, r := range line {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = next.(Model)
	}

	if len(m.chat.pasteBlocks) != 1 {
		t.Fatalf("expected charwise paste to promote to one block, got %d", len(m.chat.pasteBlocks))
	}
	if strings.Contains(m.chat.input, line) || !strings.Contains(m.chat.input, "[Pasted #1 1 lines]") {
		t.Fatalf("charwise paste should render as placeholder, input=%q", m.chat.input)
	}
	if got := m.chat.pasteBlocks[0].content; got != line {
		t.Fatalf("stored charwise paste changed, got %q", got)
	}
}

func TestCharwiseLongPasteCollectsNextLineAfterPromotion(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0
	first := strings.Repeat("a", charwisePasteImmediateRuneThreshold+8)
	second := "short second line"

	for _, r := range first {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = next.(Model)
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	for _, r := range second {
		msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
		if r == ' ' {
			msg = tea.KeyMsg{Type: tea.KeySpace}
		}
		next, _ = m.Update(msg)
		m = next.(Model)
	}

	if len(m.chat.pasteBlocks) != 1 {
		t.Fatalf("expected one promoted paste block, got %d", len(m.chat.pasteBlocks))
	}
	if strings.Contains(m.chat.input, first) || strings.Contains(m.chat.input, second) {
		t.Fatalf("promoted paste should keep following line off the raw input, input=%q", m.chat.input)
	}
	if !strings.Contains(m.chat.input, "[Pasted #1 2 lines]") {
		t.Fatalf("placeholder should show collected lines, got %q", m.chat.input)
	}
	if got := m.chat.pasteBlocks[0].content; got != first+"\n"+second {
		t.Fatalf("expected collected content, got %q", got)
	}
}

func TestPasteBurstAppendSuppressesRenderUntilLineCountChanges(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0
	first := strings.Repeat("a", charwisePasteImmediateRuneThreshold+4)

	for _, r := range first {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = next.(Model)
	}
	if len(m.chat.pasteBlocks) != 1 {
		t.Fatalf("expected promoted paste block, got %d", len(m.chat.pasteBlocks))
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	m = next.(Model)
	if !m.chat.suppressPasteRender {
		t.Fatalf("same-line paste append should suppress expensive redraw")
	}
	if got := m.chat.input; !strings.Contains(got, "[Pasted #1 1 lines]") {
		t.Fatalf("same-line append should not rewrite placeholder, got %q", got)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.chat.suppressPasteRender {
		t.Fatalf("line-count change must redraw the placeholder")
	}
	if got := m.chat.input; !strings.Contains(got, "[Pasted #1 2 lines]") {
		t.Fatalf("newline append should update placeholder line count, got %q", got)
	}
}

func TestContextStripCountsStoredPasteContentNotPlaceholder(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0
	content := strings.Repeat("z", 57)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(content), Paste: true})
	m = next.(Model)

	strip := m.renderContextStrip(120)
	if !strings.Contains(strip, "chars:") || !strings.Contains(strip, "57") {
		t.Fatalf("context strip should count stored paste content, got %q", strip)
	}
	if strings.Contains(strip, "19") {
		t.Fatalf("context strip appears to count placeholder text instead of paste content: %q", strip)
	}
}

func TestLinePasteWhileStreamingQueuesAsOneMessage(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0
	m.chat.sending = true
	m.chat.streamIndex = 1
	m.chat.transcript = []chatLine{
		newChatLine(chatRoleUser, "working"),
		newChatLine(chatRoleAssistant, ""),
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("first line")})
	m = next.(Model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)

	if len(m.chat.pendingQueue) != 0 {
		t.Fatalf("line-paste enter must not queue first line, got %#v", m.chat.pendingQueue)
	}
	if len(m.chat.pasteBlocks) != 1 {
		t.Fatalf("expected active paste block after first paste enter, got %d", len(m.chat.pasteBlocks))
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("second line")})
	m = next.(Model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)

	if len(m.chat.pendingQueue) != 0 {
		t.Fatalf("line-paste enter must keep collecting, got queued %#v", m.chat.pendingQueue)
	}
	if got := m.chat.pasteBlocks[0].content; !strings.Contains(got, "first line\nsecond line\n") {
		t.Fatalf("expected one collected paste block, got %q", got)
	}

	m.chat.pasteBurstUntil = time.Now().Add(-time.Second)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlX})
	m = next.(Model)

	if len(m.chat.pendingQueue) != 1 {
		t.Fatalf("expected exactly one queued paste message, got %#v", m.chat.pendingQueue)
	}
	if got := m.chat.pendingQueue[0]; !strings.Contains(got, "first line") || !strings.Contains(got, "second line") {
		t.Fatalf("queued paste should contain all lines, got %q", got)
	}
	if len(m.chat.pasteBlocks) != 0 || strings.TrimSpace(m.chat.input) != "" {
		t.Fatalf("paste state should clear after queueing, blocks=%d input=%q", len(m.chat.pasteBlocks), m.chat.input)
	}
}

func TestCharwisePasteWhileStreamingQueuesAsOneMessage(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0
	m.chat.sending = true
	m.chat.streamIndex = 1
	m.chat.transcript = []chatLine{
		newChatLine(chatRoleUser, "working"),
		newChatLine(chatRoleAssistant, ""),
	}

	for _, r := range "first line" {
		msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
		if r == ' ' {
			msg = tea.KeyMsg{Type: tea.KeySpace}
		}
		next, _ := m.Update(msg)
		m = next.(Model)
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)

	if len(m.chat.pendingQueue) != 0 {
		t.Fatalf("charwise paste enter must not queue first line, got %#v", m.chat.pendingQueue)
	}
	if len(m.chat.pasteBlocks) != 1 {
		t.Fatalf("expected active paste block after first paste enter, got %d", len(m.chat.pasteBlocks))
	}

	for _, r := range "second line" {
		msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
		if r == ' ' {
			msg = tea.KeyMsg{Type: tea.KeySpace}
		}
		next, _ = m.Update(msg)
		m = next.(Model)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)

	if len(m.chat.pendingQueue) != 0 {
		t.Fatalf("second pasted line must still collect, got queued %#v", m.chat.pendingQueue)
	}
	if got := m.chat.pasteBlocks[0].content; got != "first line\nsecond line\n" {
		t.Fatalf("expected one collected paste block, got %q", got)
	}

	m.chat.pasteBurstUntil = time.Now().Add(-time.Second)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlX})
	m = next.(Model)

	if len(m.chat.pendingQueue) != 1 {
		t.Fatalf("expected exactly one queued paste message, got %#v", m.chat.pendingQueue)
	}
	if got := m.chat.pendingQueue[0]; got != "first line\nsecond line\n" {
		t.Fatalf("queued paste should preserve all pasted lines, got %q", got)
	}
}

func TestCharwisePasteWhileIdleDoesNotSubmitEachLine(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0

	for _, r := range "first line" {
		msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
		if r == ' ' {
			msg = tea.KeyMsg{Type: tea.KeySpace}
		}
		next, _ := m.Update(msg)
		m = next.(Model)
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)

	if len(m.chat.transcript) != 0 {
		t.Fatalf("charwise paste enter must not submit first line, got transcript %#v", m.chat.transcript)
	}
	if len(m.chat.pasteBlocks) != 1 {
		t.Fatalf("expected active paste block after first paste enter, got %d", len(m.chat.pasteBlocks))
	}
	if !strings.Contains(m.chat.input, "[Pasted #1 2 lines]") {
		t.Fatalf("expected paste placeholder in input, got %q", m.chat.input)
	}

	for _, r := range "second line" {
		msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
		if r == ' ' {
			msg = tea.KeyMsg{Type: tea.KeySpace}
		}
		next, _ = m.Update(msg)
		m = next.(Model)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)

	if len(m.chat.transcript) != 0 {
		t.Fatalf("second pasted line must still collect, got transcript %#v", m.chat.transcript)
	}
	if got := m.chat.pasteBlocks[0].content; got != "first line\nsecond line\n" {
		t.Fatalf("expected one collected paste block, got %q", got)
	}

	m.chat.pasteBurstUntil = time.Now().Add(-time.Second)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlX})
	m = next.(Model)

	if len(m.chat.pasteBlocks) != 0 {
		t.Fatalf("paste blocks should clear after submit, got %d", len(m.chat.pasteBlocks))
	}
	if len(m.chat.transcript) == 0 || !strings.Contains(m.chat.transcript[0].Content, "first line\nsecond line") {
		t.Fatalf("expected one submitted multiline message, got %#v", m.chat.transcript)
	}
}

func TestLinePasteWhileIdleDoesNotSubmitEachLine(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("first line")})
	m = next.(Model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)

	if len(m.chat.transcript) != 0 {
		t.Fatalf("first pasted line should not submit immediately, got transcript %#v", m.chat.transcript)
	}
	if len(m.chat.pasteBlocks) != 1 {
		t.Fatalf("expected active paste block after first paste enter, got %d", len(m.chat.pasteBlocks))
	}
	if !strings.Contains(m.chat.input, "[Pasted #1 2 lines]") {
		t.Fatalf("expected paste placeholder in input, got %q", m.chat.input)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("second line")})
	m = next.(Model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)

	if len(m.chat.transcript) != 0 {
		t.Fatalf("second pasted line should still be collected, got transcript %#v", m.chat.transcript)
	}
	if got := m.chat.pasteBlocks[0].content; got != "first line\nsecond line\n" {
		t.Fatalf("expected collected paste content, got %q", got)
	}

	m.chat.pasteBurstUntil = time.Now().Add(-time.Second)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlX})
	m = next.(Model)

	if len(m.chat.pasteBlocks) != 0 {
		t.Fatalf("paste blocks should clear after submit, got %d", len(m.chat.pasteBlocks))
	}
	if len(m.chat.transcript) == 0 || !strings.Contains(m.chat.transcript[0].Content, "first line\nsecond line") {
		t.Fatalf("expected one submitted multiline message, got %#v", m.chat.transcript)
	}
}

func TestLinePastePreservesSurroundingTypedInput(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0
	m.setChatInput("before ")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("first line")})
	m = next.(Model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)

	if len(m.chat.pasteBlocks) != 1 {
		t.Fatalf("expected paste block, got %d", len(m.chat.pasteBlocks))
	}
	if got := m.chat.input; got != "before [Pasted #1 2 lines]" {
		t.Fatalf("typed prefix must remain outside paste placeholder, got %q", got)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("second line")})
	m = next.(Model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)

	m.insertInputText(" after")
	if got := m.composeInput(); got != "before first line\nsecond line\n after" {
		t.Fatalf("composeInput must preserve typed text around paste, got %q", got)
	}
}

func TestBracketedPasteWhileStreamingStaysInInputUntilSend(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0
	m.chat.sending = true
	m.chat.streamIndex = 1
	m.chat.transcript = []chatLine{
		newChatLine(chatRoleUser, "working"),
		newChatLine(chatRoleAssistant, ""),
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("alpha\nbeta\ngamma"), Paste: true})
	m = next.(Model)

	if len(m.chat.pendingQueue) != 0 {
		t.Fatalf("paste should stay in input until explicit send, got queue %#v", m.chat.pendingQueue)
	}
	if len(m.chat.pasteBlocks) != 1 {
		t.Fatalf("expected one stored paste block, got %d", len(m.chat.pasteBlocks))
	}
	if !strings.Contains(m.chat.input, "[Pasted #1 3 lines]") {
		t.Fatalf("expected visible atomic placeholder, got %q", m.chat.input)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlX})
	m = next.(Model)

	if len(m.chat.pendingQueue) != 1 {
		t.Fatalf("explicit send should queue exactly one paste message, got %#v", m.chat.pendingQueue)
	}
	if got := m.chat.pendingQueue[0]; got != "alpha\nbeta\ngamma" {
		t.Fatalf("queued paste should preserve original content, got %q", got)
	}
	if len(m.chat.pasteBlocks) != 0 || m.chat.input != "" {
		t.Fatalf("paste input should clear after queueing, blocks=%d input=%q", len(m.chat.pasteBlocks), m.chat.input)
	}
}

func TestPasteBlockSendSubmitsOneMessage(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0

	// Paste multi-line
	pasteMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("line1\nline2\nline3")}
	next1, _ := m.Update(pasteMsg)
	m2, ok := next1.(Model)
	if !ok {
		t.Fatalf("expected Model, got %T", next1)
	}

	// Ctrl+X should submit immediately because a complete multi-line paste
	// does not extend the window.
	enterMsg := tea.KeyMsg{Type: tea.KeyCtrlX}
	next2, _ := m2.Update(enterMsg)
	m3, ok := next2.(Model)
	if !ok {
		t.Fatalf("expected Model, got %T", next2)
	}

	// pasteBlocks should be cleared
	if len(m3.chat.pasteBlocks) != 0 {
		t.Fatalf("expected paste blocks cleared, got %d", len(m3.chat.pasteBlocks))
	}

	// Transcript should have exactly ONE user message (not multiple)
	userCount := 0
	for _, line := range m3.chat.transcript {
		if line.Role == "user" {
			userCount++
		}
	}
	if userCount != 1 {
		t.Fatalf("expected exactly 1 user message, got %d", userCount)
	}

	// That message should contain all three lines
	found := false
	for _, line := range m3.chat.transcript {
		if line.Role == "user" && strings.Contains(line.Content, "line1") &&
			strings.Contains(line.Content, "line2") && strings.Contains(line.Content, "line3") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected user message with all three lines, got transcript: %v", m3.chat.transcript)
	}
}

// TestPasteBlockMultiplePastes verifies that two separate paste blocks can
// exist and that composeInput reconstructs them in the right order.
func TestPasteBlockMultiplePastes(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0

	// Paste first block
	next1, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("block1\nblock1b")})
	m2, ok := next1.(Model)
	if !ok {
		t.Fatalf("expected Model, got %T", next1)
	}
	if len(m2.chat.pasteBlocks) != 1 {
		t.Fatalf("expected 1 paste block, got %d", len(m2.chat.pasteBlocks))
	}
	// Paste a second independent block.
	next2, _ := m2.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("block2\nblock2b")})
	m3, ok := next2.(Model)
	if !ok {
		t.Fatalf("expected Model, got %T", next2)
	}

	if len(m3.chat.pasteBlocks) != 2 {
		t.Fatalf("expected 2 paste blocks, got %d", len(m3.chat.pasteBlocks))
	}

	// composeInput should reconstruct correctly
	full := m3.composeInput()
	if !strings.Contains(full, "block1") || !strings.Contains(full, "block2") {
		t.Fatalf("composeInput failed, got: %q", full)
	}
}

func TestMultiplePasteBlocksInterleavedWithTypedText(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("alpha\nbeta"), Paste: true})
	m = next.(Model)
	m.insertInputText(" middle ")
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("gamma\ndelta"), Paste: true})
	m = next.(Model)

	if len(m.chat.pasteBlocks) != 2 {
		t.Fatalf("expected two paste blocks, got %d", len(m.chat.pasteBlocks))
	}
	if got := m.chat.input; got != "[Pasted #1 2 lines] middle [Pasted #2 2 lines]" {
		t.Fatalf("visible input should show both atomic placeholders and typed text, got %q", got)
	}
	if got := m.composeInput(); got != "alpha\nbeta middle gamma\ndelta" {
		t.Fatalf("composeInput should rebuild interleaved content, got %q", got)
	}
}

func TestPastePlaceholderBackspaceDeletesStoredBlock(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("one\ntwo\nthree"), Paste: true})
	m2 := next.(Model)
	if len(m2.chat.pasteBlocks) != 1 {
		t.Fatalf("expected paste block, got %d", len(m2.chat.pasteBlocks))
	}

	// Move into the middle of the compact placeholder and delete one rune.
	m2.chat.cursor = len([]rune("[Pasted #1"))
	m2.chat.cursorManual = true
	m2.chat.cursorInput = m2.chat.input
	m2.deleteInputBeforeCursor()

	if len(m2.chat.pasteBlocks) != 0 {
		t.Fatalf("stored paste block should be removed after placeholder edit, got %d", len(m2.chat.pasteBlocks))
	}
	if strings.Contains(m2.chat.input, "Pasted") {
		t.Fatalf("placeholder should be removed from input, got %q", m2.chat.input)
	}
}

func TestPastePlaceholderRenumbersAfterDelete(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0

	next1, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("first\nblock"), Paste: true})
	m2 := next1.(Model)
	m2.insertInputText(" ")
	next2, _ := m2.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("second\nblock"), Paste: true})
	m3 := next2.(Model)
	if len(m3.chat.pasteBlocks) != 2 {
		t.Fatalf("expected 2 paste blocks, got %d", len(m3.chat.pasteBlocks))
	}

	m3.chat.cursor = len([]rune(m3.chat.pasteBlocks[0].placeholder()))
	m3.chat.cursorManual = true
	m3.chat.cursorInput = m3.chat.input
	m3.deleteInputBeforeCursor()

	if len(m3.chat.pasteBlocks) != 1 {
		t.Fatalf("expected one remaining block, got %d", len(m3.chat.pasteBlocks))
	}
	if !strings.Contains(m3.chat.input, "[Pasted #1 2 lines]") {
		t.Fatalf("remaining placeholder should be renumbered, got %q", m3.chat.input)
	}
	full := m3.composeInput()
	if strings.Contains(full, "first") || !strings.Contains(full, "second") {
		t.Fatalf("composeInput kept wrong paste content: %q", full)
	}
}

func TestPasteLongSingleLineTypingDoesNotBecomePaste(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("this is the first line")})
	m2 := next.(Model)
	if len(m2.chat.pasteBlocks) != 0 {
		t.Fatalf("plain long typing must not create paste blocks, got %d", len(m2.chat.pasteBlocks))
	}
	if m2.chat.input != "this is the first line" {
		t.Fatalf("expected text inserted normally, got %q", m2.chat.input)
	}
}

func TestPasteBlockCtrlCCancels(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0

	// Paste something
	next1, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("to cancel\nmore lines")})
	m2, ok := next1.(Model)
	if !ok {
		t.Fatalf("expected Model, got %T", next1)
	}

	if len(m2.chat.pasteBlocks) != 1 {
		t.Fatalf("expected 1 paste block, got %d", len(m2.chat.pasteBlocks))
	}

	// Ctrl+C should cancel
	next2, _ := m2.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m3, ok := next2.(Model)
	if !ok {
		t.Fatalf("expected Model, got %T", next2)
	}

	if len(m3.chat.pasteBlocks) != 0 {
		t.Fatalf("expected paste blocks cleared after ctrl+c, got %d", len(m3.chat.pasteBlocks))
	}
	if m3.chat.input != "" {
		t.Fatalf("expected input cleared, got %q", m3.chat.input)
	}
}

func TestPasteSendSubmitsInsteadOfWaitingOnWindow(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0

	for _, r := range "this is the first line" {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = next.(Model)
	}
	m2 := m
	if len(m2.chat.pasteBlocks) != 0 {
		t.Fatalf("plain long input should not open paste mode, got %d blocks", len(m2.chat.pasteBlocks))
	}
	m2.chat.pasteBurstUntil = time.Now().Add(-time.Second)
	next2, _ := m2.Update(tea.KeyMsg{Type: tea.KeyCtrlX})
	m3 := next2.(Model)
	if len(m3.chat.transcript) == 0 || !strings.Contains(m3.chat.transcript[0].Content, "first line") {
		t.Fatalf("expected Ctrl+X to submit normal long input, got %#v", m3.chat.transcript)
	}
}

// TestEmptyWhitespaceInputShowsNotice asserts the send handler tells the
// user why a whitespace-only message didn't submit instead of returning
// silently (which previously read as "send is broken").
func TestEmptyWhitespaceInputShowsNotice(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0
	m.setChatInput("   \t  ")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlX})
	m2, _ := next.(Model)

	if !strings.Contains(strings.ToLower(m2.notice), "whitespace") {
		t.Fatalf("expected whitespace-only notice, got %q", m2.notice)
	}
}

// TestPasteBracketedPaste simulates a terminal with bracketed paste mode
// enabled. The entire multi-line paste arrives as a single KeyMsg with
// Paste=true, newlines intact, and submits as ONE message.
func TestPasteBracketedPaste(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0

	// Bracketed paste delivers everything in one message.
	pasteMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("line1\nline2\nline3"), Paste: true}
	next1, _ := m.Update(pasteMsg)
	m2, ok := next1.(Model)
	if !ok {
		t.Fatalf("expected Model, got %T", next1)
	}

	if len(m2.chat.pasteBlocks) != 1 {
		t.Fatalf("expected 1 paste block, got %d", len(m2.chat.pasteBlocks))
	}
	if !strings.Contains(m2.chat.input, "[Pasted #1 3 lines]") {
		t.Fatalf("expected placeholder in input, got %q", m2.chat.input)
	}

	// Manual Ctrl+X submits everything as one message.
	next2, _ := m2.Update(tea.KeyMsg{Type: tea.KeyCtrlX})
	m3, ok := next2.(Model)
	if !ok {
		t.Fatalf("expected Model, got %T", next2)
	}

	if len(m3.chat.pasteBlocks) != 0 {
		t.Fatalf("expected paste blocks cleared after submit, got %d", len(m3.chat.pasteBlocks))
	}

	userCount := 0
	for _, line := range m3.chat.transcript {
		if line.Role == "user" {
			userCount++
		}
	}
	if userCount != 1 {
		t.Fatalf("expected exactly 1 user message, got %d", userCount)
	}

	found := false
	for _, line := range m3.chat.transcript {
		if line.Role == "user" && strings.Contains(line.Content, "line1") &&
			strings.Contains(line.Content, "line2") && strings.Contains(line.Content, "line3") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected user message with all three lines, got transcript: %v", m3.chat.transcript)
	}
}
