package tui

import (
	"strings"
	"testing"
)

// TestSlashCancel_IdleReportsNothingToCancel — /cancel must produce
// a clear "nothing in flight" message when typed outside an active
// turn so the user knows the command was received but a no-op,
// rather than silently appearing to do nothing.
func TestSlashCancel_IdleReportsNothingToCancel(t *testing.T) {
	m := newCoverageModel(t)
	next, _, handled := m.executeChatCommand("/cancel")
	if !handled {
		t.Fatal("/cancel should be handled")
	}
	view := stripANSI(modelTranscriptText(next.(Model)))
	if !strings.Contains(view, "nothing to cancel") {
		t.Errorf("expected 'nothing to cancel' guidance. Got:\n%s", view)
	}
}

// TestSlashAbortAndStopAreAliasesOfCancel — pin the alias surface so
// a user typing /abort or /stop gets the same routing instead of the
// "Unknown chat command" fallback.
func TestSlashAbortAndStopAreAliasesOfCancel(t *testing.T) {
	for _, alias := range []string{"/abort", "/stop"} {
		m := newCoverageModel(t)
		next, _, handled := m.executeChatCommand(alias)
		if !handled {
			t.Errorf("%s should be handled", alias)
		}
		view := stripANSI(modelTranscriptText(next.(Model)))
		if strings.Contains(view, "Unknown chat command") {
			t.Errorf("%s fell into Unknown fallback. Got:\n%s", alias, view)
		}
	}
}

// TestSlashTodosClear_HandledWithoutFallback — coverage helper has
// engine.Tools == nil, so we get the "engine not initialized" reply.
// What matters: the slash is recognized (handled=true, no Unknown
// fallback) and the response is explicit, not silent.
func TestSlashTodosClear_HandledWithoutFallback(t *testing.T) {
	m := newCoverageModel(t)
	next, _, handled := m.executeChatCommand("/todos clear")
	if !handled {
		t.Fatal("/todos clear should be handled")
	}
	view := stripANSI(modelTranscriptText(next.(Model)))
	if strings.Contains(view, "Unknown chat command") {
		t.Errorf("/todos clear fell into Unknown fallback. Got:\n%s", view)
	}
	if !strings.Contains(strings.ToLower(view), "todos clear") &&
		!strings.Contains(strings.ToLower(view), "engine") {
		t.Errorf("expected explicit /todos clear response, got:\n%s", view)
	}
}

// TestSlashTasksClear_EmptyStoreReportsClearly — analogous coverage
// for /tasks clear when the store has nothing to wipe.
func TestSlashTasksClear_EmptyStoreReportsClearly(t *testing.T) {
	m := newCoverageModel(t)
	next, _, handled := m.executeChatCommand("/tasks clear")
	if !handled {
		t.Fatal("/tasks clear should be handled")
	}
	view := stripANSI(modelTranscriptText(next.(Model)))
	// Either "already empty" (if store reachable) or "Engine unavailable"
	// / "Task store not initialized" (if not). All three are explicit
	// — the only failure mode is silent success or "Unknown command".
	if strings.Contains(view, "Unknown chat command") {
		t.Errorf("/tasks clear fell into Unknown fallback. Got:\n%s", view)
	}
}

// modelTranscriptText collects every transcript chatLine's content
// into one string so tests can grep for system-message contents
// regardless of which line they landed on.
func modelTranscriptText(m Model) string {
	parts := make([]string, 0, len(m.chat.transcript))
	for _, line := range m.chat.transcript {
		parts = append(parts, line.Content)
	}
	return strings.Join(parts, "\n")
}
