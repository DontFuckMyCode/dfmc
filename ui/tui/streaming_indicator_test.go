package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// streaming_indicator_test.go pins the cancel-key claim in the
// streaming indicator. The indicator used to advertise `esc cancels`
// but esc only dismisses overlay states; the real cancel key is
// ctrl+c (and ctrl+q) per handleChatControlShortcut. Users hit by a
// runaway response would mash esc and watch nothing happen.

func TestStreamingIndicator_NamesTheRealCancelKey(t *testing.T) {
	out := ansi.Strip(renderStreamingIndicator("thinking", 0))
	if !strings.Contains(out, "ctrl+c cancels") {
		t.Errorf("streaming indicator must advertise ctrl+c as the cancel key, got %q", out)
	}
	if strings.Contains(out, "esc cancels") {
		t.Errorf("streaming indicator must NOT claim esc cancels — esc only dismisses overlay states, not the active stream. Got %q", out)
	}
}
