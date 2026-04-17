package tui

import (
	"context"
	"strings"
	"testing"
)

// TestDebugMentionPicker_LiteralDump renders exactly what a user would see
// when they type a single '@' in a fresh session and dumps it to the test
// log. Run with:
//
//	CGO_ENABLED=0 go test ./ui/tui/ -run TestDebugMentionPicker_LiteralDump -v
//
// This helps confirm the modal is being rendered — if the dump looks wrong
// the problem is in the renderer; if it looks right the problem is a stale
// binary, a tiny terminal clipping the tail, or terminal-specific.
func TestDebugMentionPicker_LiteralDump(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0
	m.files = []string{
		"internal/auth/token.go",
		"internal/auth/session.go",
		"internal/engine/engine.go",
		"ui/tui/tui.go",
	}
	// Simulate exactly what a user sees when they just typed '@' on an
	// empty composer: trailing token is '@', mentionActive should be true.
	m.input = "@"
	m.chatCursor = 1
	m.chatCursorManual = true
	m.chatCursorInput = m.input

	view := m.renderChatView(120)
	t.Logf("\n================= LITERAL VIEW =================\n%s\n================= END VIEW =====================", view)

	if !strings.Contains(view, "◆ File Picker") {
		t.Fatalf("FILE PICKER NOT IN RENDER — modal is genuinely missing, not just hidden by a short viewport")
	}
	// Confirm the bordered modal top-left corner is present.
	if !strings.ContainsAny(view, "╭") {
		t.Fatalf("bordered modal corner not in render — modal rendering itself is broken")
	}
	// Confirm the title immediately follows the Input header (no other
	// competing tail block sneaked in after the picker-priority refactor).
	inputIdx := strings.Index(view, "› Input")
	pickerIdx := strings.Index(view, "◆ File Picker")
	if inputIdx < 0 || pickerIdx < 0 {
		t.Fatalf("markers missing: inputIdx=%d pickerIdx=%d", inputIdx, pickerIdx)
	}
	between := view[inputIdx:pickerIdx]
	for _, bad := range []string{"Slash Assist", "Quick actions", "Command args", "📎 context"} {
		if strings.Contains(between, bad) {
			t.Fatalf("found competing decoration %q between Input and File Picker — picker isn't dominant:\n%s", bad, between)
		}
	}
}
