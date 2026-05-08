package tui

// chat_key_text.go — text-insertion branches of the chat key router
// (KeyRunes / KeySpace / KeyEnter / KeyCtrlJ) plus the @-mention
// picker open/refresh helpers. Split out of chat_key.go so the
// dispatcher in handleChatKey stays a thin switch and the long
// paste-burst + suggestion-driven submit/newline logic each have a
// named home.
//
// The KeyEnter branch in particular bundles three orthogonal jobs —
// suggestion expansion, paste-burst capture, and slash-vs-newline
// arbitration — into one method on purpose: callers (Alt+Enter, plain
// Enter) need them ordered together and pulling them apart would mean
// shipping a state struct between siblings.

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) handleChatRunesKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// 1-9 with an empty composer picks from the LLM's most recent
	// `[next: ...]` strip — pressing the number drops the
	// suggestion text into the input field so the user can edit
	// it or send it as-is. Takes precedence over starter prompts
	// because next-actions are scoped to the live conversation
	// and starter prompts only show on a fresh transcript.
	if len(msg.Runes) == 1 && strings.TrimSpace(m.chat.input) == "" && !m.chat.sending && len(m.assistantNextActions.actions) > 0 {
		r := msg.Runes[0]
		if r >= '1' && r <= '9' {
			idx := int(r - '1')
			if idx < len(m.assistantNextActions.actions) {
				m.exitInputHistoryNavigation()
				m.setChatInput(m.assistantNextActions.actions[idx])
				m.notice = fmt.Sprintf("Next-action %d loaded into composer", idx+1)
				return m, nil
			}
		}
	}
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
		m.notice = formatPasteNotice(block)
		return m, nil
	}
	now := time.Now()
	if m.appendPasteBurstText(inserted, now) {
		m.notice = pasteCollectingNotice
		return m, nil
	}
	insertedRunes := len([]rune(inserted))
	if insertedRunes >= pasteChunkRuneThreshold {
		m.clearPasteBurst()
		block := m.addPasteBlock(inserted)
		m.activatePasteBurstBlock(block, now)
		m.notice = formatPasteNotice(block)
		return m, nil
	}
	start, end := m.insertInputTextRange(inserted)
	if insertedRunes > 1 {
		m.armPasteBurstCandidateMode(start, end, insertedRunes, true, now)
	} else {
		m.extendPasteBurstCandidate(start, end, insertedRunes, false, now)
	}
	if m.shouldPromotePasteCandidateDuringInput(now) && m.promotePasteCandidateDuringInput(now) {
		m.notice = pasteCollectingNotice
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
}

func (m Model) handleChatSpaceKey() (tea.Model, tea.Cmd) {
	m.exitInputHistoryNavigation()
	now := time.Now()
	if m.appendPasteBurstText(" ", now) {
		m.notice = pasteCollectingNotice
		return m, nil
	}
	start, end := m.insertInputTextRange(" ")
	m.extendPasteBurstCandidate(start, end, 1, false, now)
	if m.shouldPromotePasteCandidateDuringInput(now) && m.promotePasteCandidateDuringInput(now) {
		m.notice = pasteCollectingNotice
		return m, nil
	}
	m.slashMenu.command = 0
	m.slashMenu.commandArg = 0
	m.slashMenu.mention = 0
	m.slashMenu.quickAction = 0
	return m, nil
}

func (m Model) handleChatNewlineInsert() (tea.Model, tea.Cmd) {
	m.exitInputHistoryNavigation()
	m.insertInputText("\n")
	m.slashMenu.command = 0
	m.slashMenu.commandArg = 0
	m.slashMenu.mention = 0
	m.slashMenu.quickAction = 0
	return m, nil
}

func (m Model) handleChatEnterKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
			m.notice = pasteCollectingNotice
			return m, nil
		}
	}
	if m.shouldStartPasteBurstOnEnter(now) && m.startPasteBurstFromInput(now) {
		m.notice = pasteCollectingNotice
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

// handleChatPrintableFallback handles key events that didn't match any
// explicit case but still carry printable runes. On Windows with non-
// standard keyboard layouts (Turkish Q, AltGr combos, IME pass-through)
// bubbletea can deliver a key event whose Type is something like
// KeyCtrlQ while Runes=['@'] — the earlier code ignored Runes in that
// branch and the '@' never reached the input buffer, which looked to
// the user like "the @ key doesn't trigger the picker". If Runes is
// non-empty and at least one rune is printable, insert them as text.
func (m Model) handleChatPrintableFallback(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if len(msg.Runes) == 0 {
		return m, nil
	}
	printable := false
	for _, r := range msg.Runes {
		if r >= 0x20 && r != 0x7f {
			printable = true
			break
		}
	}
	if !printable {
		return m, nil
	}
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
		m.notice = fmt.Sprintf("[keylog] inserted %q", string(msg.Runes))
	}
	if strings.ContainsRune(string(msg.Runes), '@') && len(m.filesView.entries) == 0 && m.eng != nil {
		return m, loadFilesCmd(m.eng)
	}
	return m, nil
}

// @-mention picker helpers — opening from a key (Alt+Q / Ctrl+T /
// AltGr-mapped layouts), checking whether a key event should open the
// picker, and refreshing the open state after delete/backspace.

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
	m.notice = "File picker — type to filter · tab inserts · esc closes."
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
