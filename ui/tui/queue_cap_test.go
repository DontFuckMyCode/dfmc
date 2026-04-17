package tui

// Pin two safety properties that REPORT.md called out:
//   - pendingQueue must not grow without bound (a user who holds Enter
//     while a long stream is in flight could otherwise OOM the TUI).
//   - cancelActiveStream must nil-guard streamCancel so a race between
//     "stream finished" and "user pressed Esc" doesn't panic.
//
// Both properties are easy to silently regress while refactoring the
// chat loop, so we lock them down.

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestPendingQueueIsBoundedAtCap(t *testing.T) {
	m := NewModel(context.Background(), nil)
	// Force the "queue, don't send" branch by lying that a stream is
	// already in flight.
	m.sending = true

	// Push way past the cap. Each Enter feeds one entry through the
	// chat-key handler.
	pushes := pendingQueueCap + 50
	for i := 0; i < pushes; i++ {
		m.setChatInput("flooded entry " + itoaSmall(i))
		next, _ := m.handleChatKey(tea.KeyMsg{Type: tea.KeyEnter})
		nm, ok := next.(Model)
		if !ok {
			t.Fatalf("handleChatKey must return Model, got %T", next)
		}
		m = nm
	}
	if got := len(m.pendingQueue); got != pendingQueueCap {
		t.Fatalf("pendingQueue must cap at %d entries, got %d (memory leak vector)", pendingQueueCap, got)
	}
	// Excess pushes must surface a notice so the user knows their
	// most recent input was dropped, not silently buffered.
	if m.notice == "" {
		t.Fatalf("hitting the cap must set a user-visible notice; got empty")
	}
}

func TestCancelActiveStream_NoPanicWhenAlreadyNil(t *testing.T) {
	m := NewModel(context.Background(), nil)
	// streamCancel is nil after construction — simulate the race where
	// the stream-done callback already cleared the cancel func and
	// then Esc fires.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("cancelActiveStream panicked on nil cancel func: %v", r)
		}
	}()
	if m.cancelActiveStream() {
		t.Fatal("cancelActiveStream must return false when no stream is active")
	}
}

func TestCancelActiveStream_FiresAndClears(t *testing.T) {
	m := NewModel(context.Background(), nil)
	called := false
	m.streamCancel = func() { called = true }

	if !m.cancelActiveStream() {
		t.Fatal("cancelActiveStream must return true when a cancel was stored")
	}
	if !called {
		t.Fatal("the stored cancel func must actually fire")
	}
	if m.streamCancel != nil {
		t.Fatal("streamCancel must be cleared after firing so a second Esc is a no-op")
	}
	if !m.userCancelledStream {
		t.Fatal("userCancelledStream flag must be set so the err handler tailors the notice")
	}
	// Calling again must be safe — the race between two Esc presses
	// would otherwise re-fire the now-stale cancel.
	if m.cancelActiveStream() {
		t.Fatal("second cancelActiveStream call must return false (cancel was cleared)")
	}
}
