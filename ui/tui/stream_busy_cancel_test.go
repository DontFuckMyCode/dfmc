package tui

import (
	"context"
	"strings"
	"testing"
)

// stream_busy_cancel_test.go pins the cancel-key claim in the
// "stream busy" refusal messages emitted by /retry, /edit, and
// /compact when chat.sending is true. The old copy said "press esc
// to cancel it first" — but handleChatEscapeKey only dismisses
// overlay states (resume prompt, mention picker, next-actions
// strip); it does NOT cancel an in-flight stream. The real cancel
// keys are ctrl+c / ctrl+q per handleChatControlShortcut. Users who
// tried /retry mid-stream were told to mash esc and watched nothing
// happen.

func TestStreamBusyMessages_NameTheRealCancelKey(t *testing.T) {
	t.Helper()
	cases := []struct {
		name    string
		command string
	}{
		{"retry", "/retry"},
		{"edit", "/edit"},
		{"compact", "/compact"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewModel(context.Background(), nil)
			m.chat.sending = true
			next, _, handled := m.executeChatCommand(tc.command)
			if !handled {
				t.Fatalf("%s should be handled by the slash dispatcher", tc.command)
			}
			model := next.(Model)
			if len(model.chat.transcript) == 0 {
				t.Fatalf("%s should append a system message while sending", tc.command)
			}
			last := model.chat.transcript[len(model.chat.transcript)-1].Content
			if !strings.Contains(last, "ctrl+c") {
				t.Errorf("%s busy refusal must advertise ctrl+c as the cancel key, got: %q", tc.command, last)
			}
			if strings.Contains(last, "press esc to cancel") {
				t.Errorf("%s busy refusal still claims esc cancels the stream — that was the lie. Got: %q", tc.command, last)
			}
		})
	}
}
