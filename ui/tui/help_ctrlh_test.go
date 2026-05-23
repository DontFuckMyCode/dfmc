package tui

import (
	"strings"
	"testing"
)

// help_ctrlh_test.go pins the help-overlay copy for Ctrl+H against
// drift. The old line read "Ctrl+H / Backspace delete char before
// cursor", but the global shortcut layer (handleGlobalShortcuts)
// runs BEFORE the per-tab chat handler and claims "ctrl+h" to
// toggle the help overlay. So on every modern terminal Ctrl+H
// opens help — it never reaches the chat composer's
// KeyCtrlH→backspace fallback. Documenting it as a delete key
// teaches users a binding the runtime won't honour.

func TestHelp_DoesNotClaimCtrlHDeletesChar(t *testing.T) {
	help := renderTUIHelp()
	if strings.Contains(help, "Ctrl+H / Backspace") {
		t.Errorf("help overlay still pairs Ctrl+H with Backspace as a delete key — Ctrl+H opens the help overlay (global). Got context:\n%s",
			helpContextAround(help, "Ctrl+H", 120))
	}
	// And the help-overlay opener line for Ctrl+H must still be present.
	if !strings.Contains(help, "Ctrl+H") {
		t.Errorf("help overlay must still document Ctrl+H as the overlay toggle key")
	}
	// Backspace alone must remain as the delete-char binding.
	if !strings.Contains(help, "Backspace") || !strings.Contains(help, "delete char before cursor") {
		t.Errorf("help overlay must still document Backspace as delete-char-before-cursor")
	}
}

func helpContextAround(haystack, needle string, span int) string {
	i := strings.Index(haystack, needle)
	if i < 0 {
		return "(needle not found)"
	}
	start := i - span
	if start < 0 {
		start = 0
	}
	end := i + len(needle) + span
	if end > len(haystack) {
		end = len(haystack)
	}
	return haystack[start:end]
}
