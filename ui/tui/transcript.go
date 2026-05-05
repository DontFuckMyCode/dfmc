// Transcript helpers: appenders for each chat role (system/tool/coach),
// transcript scroll clamp, the newChatLine constructor, and the
// estimated-line-count used as a scrollback ceiling. Extracted from
// tui.go — everything here mutates or reads m.chat.transcript and
// lives in one place for focused review.

package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/tokens"
)

func (m Model) appendSystemMessage(text string) Model {
	m.chat.transcript = append(m.chat.transcript, newChatLine(chatRoleSystem, strings.TrimSpace(text)))
	m.chat.scrollback = 0
	return m
}

// appendToolEventMessage inserts a tool-tagged transcript line so tool calls
// and results render with the TOOL badge rather than SYS. This is what makes
// the chat feel like a unified conversation — the events sit where they
// actually fired instead of being relegated to a separate side panel.
func (m Model) appendToolEventMessage(text string) Model {
	m.chat.transcript = append(m.chat.transcript, newChatLine(chatRoleTool, strings.TrimSpace(text)))
	m.chat.scrollback = 0
	return m
}

// appendCoachMessage inserts a coach-tagged transcript line carrying the
// background observer's commentary. Severity decides the subtle leading
// marker so warn/celebrate notes stand apart from plain info nudges without
// shouting; origin is appended as a muted tag so users can learn which rule
// fired (useful for giving feedback like "quiet the mutation_unvalidated
// rule"). Notes always land in the transcript — they're the user-facing
// surface of the tiny-touches coach, not ephemeral chatter.
func (m Model) appendCoachMessage(text string, severity coachSeverity, origin string, action string) Model {
	text = strings.TrimSpace(text)
	if text == "" {
		return m
	}
	marker := ""
	switch severity {
	case coachSeverityWarn:
		marker = warnStyle.Render("⚠") + " "
	case coachSeverityCelebrate:
		marker = okStyle.Render("✓") + " "
	}
	body := marker + text
	if action = strings.TrimSpace(action); action != "" {
		body += "\n" + subtleStyle.Render("Suggested: ") + action
	}
	if origin = strings.TrimSpace(origin); origin != "" {
		body += " " + subtleStyle.Render("["+origin+"]")
	}
	m.chat.transcript = append(m.chat.transcript, newChatLine(chatRoleCoach, body))
	m.chat.scrollback = 0
	m.appendActivity("coach: " + text)
	// Also accumulate for session-end summary — appendCoachMessage is the
	// single source of truth; handleEngineEvent no longer duplicates this.
	m.agentLoop.sessionCoachNotes = append(m.agentLoop.sessionCoachNotes, text)
	if action != "" {
		m.notice = text + " | Suggested: " + action
	} else {
		m.notice = text
	}
	return m
}

func (m *Model) moveStreamingAssistantToTranscriptEnd() {
	idx := m.chat.streamIndex
	if idx < 0 || idx >= len(m.chat.transcript) || idx == len(m.chat.transcript)-1 {
		return
	}
	line := m.chat.transcript[idx]
	m.chat.transcript = append(m.chat.transcript[:idx], m.chat.transcript[idx+1:]...)
	m.chat.transcript = append(m.chat.transcript, line)
	m.chat.streamIndex = len(m.chat.transcript) - 1
}

// scrollTranscript shifts the chat head. delta < 0 scrolls UP toward older
// content ( PgUp / shift+up / wheel up). delta > 0 scrolls DOWN toward newer
// content (PgDown / shift+down / wheel down). Clamps to the ceiling derived
// from user-turn count above the anchor.
func (m *Model) scrollTranscript(delta int) {
	next := m.chat.scrollback - delta
	if next < 0 {
		next = 0
	}
	// Ceiling is maxScrollbackSteps — each unit = one prior user turn.
	maxBack := estimateTranscriptLines(m.chat.transcript)
	if next > maxBack {
		next = maxBack
	}
	m.chat.scrollback = next
	if next == 0 {
		m.notice = "Transcript: back to latest"
	} else if next >= maxBack {
		m.notice = "Transcript: at top of history"
	} else {
		m.notice = fmt.Sprintf("Transcript: %d lines back (PgDown/End = forward)", next)
	}
}

// estimateTranscriptLines returns a rough upper bound on the number of
// rendered lines the transcript will produce — used only as a scrollback
// ceiling so the user can't scroll into empty space indefinitely.
func estimateTranscriptLines(transcript []chatLine) int {
	total := 0
	for _, item := range transcript {
		// Console header + content + spacer. This is deliberately generous:
		// it is only a scroll ceiling, while fitChatBody clamps against the
		// actual rendered feed window.
		total += 6 + strings.Count(item.Content, "\n")
		if len(item.EventLines) > 0 {
			total += 1 + min(len(item.EventLines), 10)
		}
		total += len(item.ToolChips) * 3
	}
	return total
}

// newChatLine constructs a chatLine with a typed role. Pre-fix the role
// was a bare string and call sites used literals like "system" / "user"
// — a typo ("asistant") compiled clean and silently routed to the wrong
// renderer branch. Forcing chatRole here means every call site goes
// through one of the chatRole* constants and the compiler catches typos.
func newChatLine(role chatRole, content string) chatLine {
	return chatLine{
		Role:       role,
		Content:    content,
		Preview:    chatDigest(content),
		TokenCount: estimatedChatTokens(content),
		Timestamp:  time.Now(),
	}
}

func estimatedChatTokens(content string) int {
	if strings.TrimSpace(content) == "" {
		return 0
	}
	return tokens.Estimate(content)
}

func (m *Model) refreshChatLineTokenCount(index int) {
	if index < 0 || index >= len(m.chat.transcript) {
		return
	}
	m.chat.transcript[index].TokenCount = estimatedChatTokens(m.chat.transcript[index].Content)
}
