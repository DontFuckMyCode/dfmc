package tui

import (
	"strings"
	"testing"
)

// shortcuts_slash_test.go pins the `/shortcuts` documentation against
// drift. Previously help.go said it "Open[s] the Shortcuts cheat
// sheet (alt+h tab)" — wording that implied alt+h takes you to a
// "Shortcuts tab". There IS a Shortcuts panel (Shift+F5), but
// `/shortcuts` does NOT take you there; it toggles the help overlay
// (handleShortcutsSlash sets showHelpOverlay=true). The misleading
// copy was a documentation bug.

func TestShortcutsSlash_HelpDescribesTheRealEffect(t *testing.T) {
	help := renderTUIHelp()
	// The corrected line must be present.
	if !strings.Contains(help, "/shortcuts") {
		t.Fatal("help overlay must document /shortcuts")
	}
	// It must mention "help overlay" — the real effect of the slash.
	if !strings.Contains(help, "help overlay") {
		t.Errorf("/shortcuts help line must name the real effect (help overlay). Got context:\n%s",
			contextAround(help, "/shortcuts", 200))
	}
	// And it must NOT claim the slash "opens the Shortcuts cheat sheet (alt+h tab)" — that was the lie.
	if strings.Contains(help, "Shortcuts cheat sheet (alt+h tab)") {
		t.Errorf("/shortcuts help line still claims the old misleading shape. Got context:\n%s",
			contextAround(help, "/shortcuts", 200))
	}
}

// contextAround returns ±n bytes around the first occurrence of needle
// in haystack. Used for descriptive test errors when a substring is
// missing from a large blob.
func contextAround(haystack, needle string, span int) string {
	idx := strings.Index(haystack, needle)
	if idx < 0 {
		return "(needle not found)"
	}
	start := idx - span
	if start < 0 {
		start = 0
	}
	end := idx + len(needle) + span
	if end > len(haystack) {
		end = len(haystack)
	}
	return haystack[start:end]
}
