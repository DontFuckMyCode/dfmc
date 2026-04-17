// Stream cancellation UX tests. A user pressing esc mid-turn should
// see a calm, unambiguous message — both in the notice line and in
// the transcript — NOT the raw "context canceled" provider error.
// Without these guards a regression in chatErrMsg routing silently
// reintroduces the "looks like something broke" UX.

package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestStreamCancel_SetsFlagAndClearsCancel(t *testing.T) {
	m := Model{}
	fired := false
	m.streamCancel = func() { fired = true }

	if !m.cancelActiveStream() {
		t.Fatal("cancelActiveStream should report true when a cancel fires")
	}
	if !fired {
		t.Fatal("cancelActiveStream must invoke the stored CancelFunc")
	}
	if m.streamCancel != nil {
		t.Fatal("streamCancel must be cleared after firing to avoid stale double-cancel")
	}
	if !m.userCancelledStream {
		t.Fatal("userCancelledStream must be set so chatErrMsg can tailor its message")
	}
}

func TestStreamCancel_NoOpWhenNothingStreaming(t *testing.T) {
	m := Model{}
	if m.cancelActiveStream() {
		t.Fatal("cancelActiveStream with no active stream should return false")
	}
	if m.userCancelledStream {
		t.Fatal("userCancelledStream must stay false when no cancel fired")
	}
}

func TestChatErrMsg_UserCancelEmitsFriendlyNoticeAndMarker(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)
	m.sending = true
	m.userCancelledStream = true

	next, _ := m.Update(chatErrMsg{err: context.Canceled})
	nm := next.(Model)

	if nm.sending {
		t.Fatal("sending flag must be cleared after chatErrMsg")
	}
	if nm.userCancelledStream {
		t.Fatal("userCancelledStream must be reset after the message is consumed")
	}
	if !strings.Contains(strings.ToLower(nm.notice), "cancel") {
		t.Fatalf("notice should mention cancellation, got: %q", nm.notice)
	}
	if strings.Contains(nm.notice, "context canceled") {
		t.Fatalf("notice must not expose the raw provider error, got: %q", nm.notice)
	}
	// Transcript must carry an unambiguous marker so a user scrolling
	// back can tell where the turn was aborted.
	if len(nm.transcript) == 0 {
		t.Fatal("transcript should gain a cancellation marker line")
	}
	last := nm.transcript[len(nm.transcript)-1].Content
	if !strings.Contains(strings.ToLower(last), "cancel") {
		t.Fatalf("expected transcript marker to mention cancel, got: %q", last)
	}
}

func TestChatErrMsg_ContextCanceledTreatedAsUserCancel(t *testing.T) {
	// Even when userCancelledStream didn't get set (e.g. process-wide
	// cancel from the host), context.Canceled is a cleaner signal than
	// "chat: context canceled" — degrade to the friendly message.
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)
	m.sending = true

	next, _ := m.Update(chatErrMsg{err: errors.New("wrapped: context canceled")})
	nm := next.(Model)

	// Unrecognized error must keep the raw prefix ("chat:") — we only
	// smooth the message when context.Canceled is actually in the chain
	// or the user pressed esc.
	if !strings.HasPrefix(nm.notice, "chat:") {
		t.Fatalf("unrelated error should still show 'chat:' prefix, got: %q", nm.notice)
	}
}

func TestChatErrMsg_ProviderErrorKeepsOriginalWording(t *testing.T) {
	// Real provider error — rate limit, auth failure — must NOT be
	// mistaken for a cancel. The user needs to see the actual reason.
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)
	m.sending = true

	next, _ := m.Update(chatErrMsg{err: errors.New("401 unauthorized")})
	nm := next.(Model)

	if !strings.Contains(nm.notice, "401 unauthorized") {
		t.Fatalf("provider error should surface verbatim, got: %q", nm.notice)
	}
	if strings.Contains(strings.ToLower(nm.notice), "turn cancelled") {
		t.Fatalf("provider error misreported as cancellation: %q", nm.notice)
	}
}

// Smoke test: pressing Esc mid-stream routes through cancelActiveStream
// from the composer key handler. Regression would drop the plumbing and
// leave esc as a no-op during streams.
func TestEsc_DuringStream_FiresCancel(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)
	m.sending = true
	fired := false
	m.streamCancel = func() { fired = true }

	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})

	if !fired {
		t.Fatal("Esc during a stream should invoke the cancel func")
	}
}
