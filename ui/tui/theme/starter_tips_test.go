package theme

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// starter_tips_test.go pins the empty-chat starter prompts' Tips line
// against drift. The old copy advertised `ctrl+x send · enter newline`
// — but Enter SUBMITS in the chat composer (handleChatEnterKey) and
// Alt+Enter inserts the newline. A first-run user reading "enter
// newline" would press Enter expecting a soft break and watch their
// half-finished prompt fly off to the model.

func TestStarterTips_NameTheRealEnterBehaviour(t *testing.T) {
	out := ansi.Strip(strings.Join(RenderStarterPrompts(120, true), "\n"))
	if !strings.Contains(out, "enter") || !strings.Contains(out, "send") {
		t.Errorf("starter tips must advertise enter as the send key, got:\n%s", out)
	}
	if !strings.Contains(out, "alt+enter") || !strings.Contains(out, "newline") {
		t.Errorf("starter tips must advertise alt+enter as the newline key, got:\n%s", out)
	}
	// The lie was a bare `enter newline` token (not the legitimate
	// `alt+enter newline`). Strip the legitimate occurrence first,
	// then check the dangerous one is gone.
	residual := strings.ReplaceAll(out, "alt+enter newline", "")
	if strings.Contains(residual, "enter newline") {
		t.Errorf("starter tips still claim a bare 'enter newline' — that was the lie. Got:\n%s", out)
	}
}
