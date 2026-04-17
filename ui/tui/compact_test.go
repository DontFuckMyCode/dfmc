package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func mkLine(role, content string) chatLine {
	return newChatLine(role, content)
}

func TestCompactTranscript_ShortTranscriptNotCompacted(t *testing.T) {
	lines := []chatLine{
		mkLine("user", "hi"),
		mkLine("assistant", "hello"),
	}
	out, collapsed, ok := compactTranscript(lines, 6)
	if ok {
		t.Fatalf("short transcript should not compact; got collapsed=%d", collapsed)
	}
	if len(out) != len(lines) {
		t.Fatalf("output should be unchanged; got %d, want %d", len(out), len(lines))
	}
}

func TestCompactTranscript_CollapsesOlderLines(t *testing.T) {
	lines := []chatLine{
		mkLine("user", "q1"),
		mkLine("assistant", "a1"),
		mkLine("user", "q2"),
		mkLine("assistant", "a2"),
		mkLine("tool", "ran read_file"),
		mkLine("user", "q3"),
		mkLine("assistant", "a3"),
		mkLine("user", "q4"),
		mkLine("assistant", "a4"),
	}
	out, collapsed, ok := compactTranscript(lines, 3)
	if !ok {
		t.Fatalf("expected compaction to fire")
	}
	if collapsed != len(lines)-3 {
		t.Fatalf("collapsed=%d, want %d", collapsed, len(lines)-3)
	}
	// Expect: summary + 3 tail lines.
	if len(out) != 4 {
		t.Fatalf("output length=%d, want 4", len(out))
	}
	if !strings.EqualFold(out[0].Role, "system") {
		t.Fatalf("first line should be a system summary, got role=%q", out[0].Role)
	}
	if !strings.Contains(out[0].Content, "user") || !strings.Contains(out[0].Content, "assistant") || !strings.Contains(out[0].Content, "tool") {
		t.Fatalf("summary should fingerprint user/assistant/tool counts, got: %s", out[0].Content)
	}
	// Tail preserved verbatim.
	for i, tail := range out[1:] {
		if tail.Content != lines[len(lines)-3+i].Content {
			t.Fatalf("tail[%d] mismatch: got %q, want %q", i, tail.Content, lines[len(lines)-3+i].Content)
		}
	}
}

func TestCompactTranscript_PureSystemHeadNoCompaction(t *testing.T) {
	// Collapsing a head of only system notes would inflate the transcript
	// instead of shrinking it — the fn must bail in that case.
	lines := []chatLine{
		mkLine("system", "sys note 1"),
		mkLine("system", "sys note 2"),
		mkLine("system", "sys note 3"),
		mkLine("user", "hi"),
		mkLine("assistant", "hello"),
	}
	out, collapsed, ok := compactTranscript(lines, 2)
	if ok {
		t.Fatalf("pure-system head should not compact; got collapsed=%d", collapsed)
	}
	if len(out) != len(lines) {
		t.Fatalf("output should be unchanged when refusing to compact")
	}
}

func TestCompactTranscript_ZeroKeepTreatedAsOne(t *testing.T) {
	// Defensive: /compact 0 shouldn't blow up the transcript; keep=1 is
	// the floor so at least the most recent turn survives.
	lines := []chatLine{
		mkLine("user", "q1"),
		mkLine("assistant", "a1"),
		mkLine("user", "q2"),
		mkLine("assistant", "a2"),
	}
	out, _, ok := compactTranscript(lines, 0)
	if !ok {
		t.Fatalf("expected compaction with keep=0→1 floor")
	}
	if len(out) != 2 {
		t.Fatalf("expected summary + 1 tail line, got %d", len(out))
	}
}

// TestSlashCompact_CollapsesTranscriptAndLeavesSummary drives the full
// slash-handler path to make sure /compact survives the parser and
// actually mutates the model.
func TestSlashCompact_CollapsesTranscriptAndLeavesSummary(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.transcript = []chatLine{
		mkLine("user", "q1"),
		mkLine("assistant", "a1"),
		mkLine("user", "q2"),
		mkLine("assistant", "a2"),
		mkLine("user", "q3"),
		mkLine("assistant", "a3"),
		mkLine("user", "q4"),
		mkLine("assistant", "a4"),
		mkLine("user", "q5"),
		mkLine("assistant", "a5"),
	}
	m.setChatInput("/compact 4")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	nm := next.(Model)
	// Expected: summary + last 4 lines + a fresh system notice appended
	// by the slash handler = 6 lines total.
	if got := len(nm.transcript); got != 6 {
		t.Fatalf("/compact 4 should leave 6 lines (summary + 4 tail + notice); got %d", got)
	}
	if !strings.EqualFold(nm.transcript[0].Role, "system") {
		t.Fatalf("first line after compact should be summary; got role=%q", nm.transcript[0].Role)
	}
	if !strings.Contains(nm.notice, "Compacted") {
		t.Fatalf("notice should confirm compaction; got %q", nm.notice)
	}
}

func TestSlashCompact_NothingToCompactOnShortTranscript(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.transcript = []chatLine{
		mkLine("user", "hi"),
		mkLine("assistant", "hello"),
	}
	m.setChatInput("/compact")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	nm := next.(Model)
	if !strings.Contains(strings.ToLower(nm.notice), "nothing to compact") {
		t.Fatalf("short transcript should surface 'nothing to compact' notice; got %q", nm.notice)
	}
}

func TestSlashCompact_BlockedWhileStreaming(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.transcript = []chatLine{
		mkLine("user", "q1"),
		mkLine("assistant", "a1"),
		mkLine("user", "q2"),
		mkLine("assistant", "a2"),
		mkLine("user", "q3"),
		mkLine("assistant", "a3"),
		mkLine("user", "q4"),
		mkLine("assistant", "a4"),
	}
	m.sending = true

	next, _, handled := m.executeChatCommand("/compact")
	if !handled {
		t.Fatalf("/compact must always be handled")
	}
	nm := next.(Model)
	last := nm.transcript[len(nm.transcript)-1].Content
	if !strings.Contains(strings.ToLower(last), "streaming") {
		t.Fatalf("guard message should mention streaming, got:\n%s", last)
	}
	// Actual compaction must not have happened — the four original user turns must still be there.
	userSeen := 0
	for _, ln := range nm.transcript {
		if strings.EqualFold(ln.Role, "user") {
			userSeen++
		}
	}
	if userSeen != 4 {
		t.Fatalf("original user turns should survive a blocked compact; saw %d", userSeen)
	}
}
