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
	if action != "" {
		m.notice = text + " | Suggested: " + action
	} else {
		m.notice = text
	}
	return m
}

// scrollTranscript shifts the chat head backwards by delta *lines* (negative
// = older/upward, positive = newer/downward) and clamps to a rough ceiling
// derived from the transcript size. The render layer (fitChatBody) clamps
// tighter based on actual rendered line count — scroll just tracks intent.
func (m *Model) scrollTranscript(delta int) {
	next := m.chat.scrollback - delta
	if next < 0 {
		next = 0
	}
	maxBack := estimateTranscriptLines(m.chat.transcript)
	if next > maxBack {
		next = maxBack
	}
	if next == m.chat.scrollback {
		if next == 0 {
			m.notice = "Transcript: already at latest"
		} else {
			m.notice = "Transcript: at top of history"
		}
		return
	}
	m.chat.scrollback = next
	if next == 0 {
		m.notice = "Transcript: back to latest"
	} else {
		m.notice = fmt.Sprintf("Transcript: scrolled back %d lines (PageDown / End resumes)", next)
	}
}

// estimateTranscriptLines returns a rough upper bound on the number of
// rendered lines the transcript will produce — used only as a scrollback
// ceiling so the user can't scroll into empty space indefinitely.
func estimateTranscriptLines(transcript []chatLine) int {
	total := 0
	for _, item := range transcript {
		// Header bar + content lines + spacer between messages.
		total += 2 + strings.Count(item.Content, "\n")
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
		Role:      role,
		Content:   content,
		Preview:   chatDigest(content),
		Timestamp: time.Now(),
	}
}
