package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// composer_hint_test.go pins the persistent composer hint so the
// `@ mention` affordance doesn't silently drift back out of it. The
// help-overlay chat section had advertised it for ages; the persistent
// hint omitted it, leaving the always-on surface less discoverable
// than the press-ctrl+h overlay.

func TestComposerHint_AdvertisesEssentialAffordances(t *testing.T) {
	m := NewModel(context.Background(), nil)
	// renderTimelineComposer renders the always-on composer hint —
	// any width ≥ 60 is fine, the hint isn't width-responsive.
	view := ansi.Strip(strings.Join(m.renderTimelineComposer(140), "\n"))
	for _, want := range []string{
		"enter send",        // submit key
		"alt+enter newline", // multi-line composition
		"/ commands",        // slash-command discovery
		"@ mention",         // file mention picker
	} {
		if !strings.Contains(view, want) {
			t.Errorf("composer hint missing essential affordance %q. Got:\n%s", want, view)
		}
	}
}

// TestComposerHint_StaysUnderNarrowWidthBudget locks in the
// constraint that surfaced when the hint regressed at 71 chars —
// the chat-scrollbar tests assert composer rows fit ≤70 chars at
// narrow widths. The hint line includes a 2-char leading indent.
func TestComposerHint_StaysUnderNarrowWidthBudget(t *testing.T) {
	m := NewModel(context.Background(), nil)
	rows := m.renderTimelineComposer(140)
	for _, row := range rows {
		stripped := ansi.Strip(row)
		// Find the actual hint line by its distinctive content.
		if !strings.Contains(stripped, "enter send") || !strings.Contains(stripped, "alt+enter") {
			continue
		}
		if w := ansi.StringWidth(stripped); w > 70 {
			t.Errorf("composer hint is %d chars wide, must stay ≤70 for narrow-width compatibility: %q",
				w, stripped)
		}
	}
}
