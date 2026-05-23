package tui

import (
	"strings"
	"testing"
)

// help_coverage_meta_test.go pins help-overlay coverage: every panel
// with an inline affordance hint MUST have a corresponding PANEL
// section in the help overlay, otherwise the user has to grep source
// to discover non-trivial keys (`t` cycles memory tier, `S` deep-
// searches conversations, `v` cycles codemap views, etc.).
//
// Sibling of help_coverage_test.go which audits the slash-command
// catalog; this one is about diagnostic-panel keyboard discoverability.

func TestHelpOverlay_DocumentsEveryDiagnosticPanel(t *testing.T) {
	help := renderTUIHelp()
	for _, want := range []string{
		"FILES PANEL",
		"ACTIVITY PANEL",
		"TOOLS PANEL",
		"SECURITY PANEL",
		"PROMPTS PANEL",
		"MEMORY PANEL",
		"CONVERSATIONS PANEL",
		"CODEMAP PANEL",
		"PLANS PANEL",
		"CONTEXT PANEL",
		"STATUS PANEL",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("help overlay is missing section %q — every diagnostic panel must be documented", want)
		}
	}
}

// TestHelpOverlay_PanelSectionsNameNonObviousKeys asserts that each
// section documents the panel-specific keys that aren't obvious from
// the inline hint alone. The list is intentionally tight — only keys
// the user would not figure out by mashing arrow keys.
func TestHelpOverlay_PanelSectionsNameNonObviousKeys(t *testing.T) {
	help := renderTUIHelp()
	cases := []struct {
		section string
		keys    []string
	}{
		{"MEMORY PANEL", []string{"t", "d", "p"}},                    // tier cycle, delete, promote
		{"CONVERSATIONS PANEL", []string{"L", "S"}},                  // load full, deep search
		{"CODEMAP PANEL", []string{"v"}},                             // view cycle
		{"PLANS PANEL", []string{"e"}},                               // edit task
		{"CONTEXT PANEL", []string{"e", "m", "a"}},                   // edit, manager, active
		{"SECURITY PANEL", []string{"v", "i", "f"}},                  // view, ignore, fix
		{"STATUS PANEL", []string{"h", "l"}},                         // hjkl nav
		{"PROMPTS PANEL", []string{"r"}},                             // reload
		{"TOOLS PANEL", []string{"e", "x"}},                          // edit params, reset
		{"ACTIVITY PANEL", []string{"1-6", "p"}},                     // filter, follow
		{"FILES PANEL", []string{"p", "i", "e", "v"}},                // pin, insert, explain, review
	}
	for _, c := range cases {
		idx := strings.Index(help, c.section)
		if idx < 0 {
			t.Errorf("section %q missing", c.section)
			continue
		}
		// Look at the chunk between this header and the next one.
		// Sections are separated by the divider "──...──" lines, so
		// grabbing the next ~600 chars covers each panel block.
		end := idx + 600
		if end > len(help) {
			end = len(help)
		}
		chunk := help[idx:end]
		for _, key := range c.keys {
			if !strings.Contains(chunk, key) {
				t.Errorf("section %q missing key %q. Got chunk:\n%s", c.section, key, chunk)
			}
		}
	}
}
