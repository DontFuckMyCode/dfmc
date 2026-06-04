package tui

import (
	"strings"
	"testing"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
)

// TestTruncateRunes_RuneSafe pins the rune-safe truncation that replaced
// the byte-slice (s[:n]) truncations scattered across the TUI render
// path. A byte slice cuts a multibyte (Turkish/CJK/emoji) character
// mid-sequence and produces invalid UTF-8 / a corrupted final glyph;
// truncateRunes must never do that and must keep the result — marker
// included — within the rune budget.
func TestTruncateRunes_RuneSafe(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		maxLen int
		marker string
		want   string
	}{
		{"ascii-fits", "hello", 10, "…", "hello"},
		{"ascii-cut", "hello world", 5, "…", "hell…"},
		{"exact-fit", "hello", 5, "…", "hello"},
		{"ascii-dots-marker", "hello world", 5, "...", "he..."},
		{"zero-maxlen", "hello", 0, "…", ""},
		{"negative-maxlen", "hello", -3, "…", ""},
		{"marker-bigger-than-budget", "hello", 2, "...", ".."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := truncateRunes(tc.in, tc.maxLen, tc.marker)
			if got != tc.want {
				t.Fatalf("truncateRunes(%q,%d,%q) = %q, want %q", tc.in, tc.maxLen, tc.marker, got, tc.want)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("result %q is not valid UTF-8", got)
			}
			if tc.maxLen > 0 && utf8.RuneCountInString(got) > tc.maxLen {
				t.Fatalf("result %q is %d runes, over budget %d", got, utf8.RuneCountInString(got), tc.maxLen)
			}
		})
	}
}

// TestTruncateRunes_MultibyteNeverCorrupts is the direct regression for
// the Turkish-input hazard: a string of multibyte runes truncated at
// every length must always stay valid UTF-8 and exactly N runes. The old
// byte-slice form failed this whenever the cut landed inside a rune.
func TestTruncateRunes_MultibyteNeverCorrupts(t *testing.T) {
	inputs := []string{
		"şğıİöü çÇ ğüş", // Turkish — 2-byte runes
		"日本語のテキスト",      // CJK — 3-byte runes
		"😀🎉🚀💡🔥",         // emoji — 4-byte runes
		"café résumé naïve",
	}
	for _, in := range inputs {
		total := utf8.RuneCountInString(in)
		for n := 1; n <= total+2; n++ {
			got := truncateStr(in, n)
			if !utf8.ValidString(got) {
				t.Fatalf("truncateStr(%q, %d) produced invalid UTF-8: %q", in, n, got)
			}
			if rc := utf8.RuneCountInString(got); rc > n {
				t.Fatalf("truncateStr(%q, %d) = %q has %d runes, over budget", in, n, got, rc)
			}
		}
	}
}

// TestStatusPanel_CodeMapHintSaysF10 pins the corrected key hint: the
// Status panel's CodeMap card must point at F10 (CodeMap's real key),
// not F9 (which is the Status panel itself — pressing it never reaches
// CodeMap).
func TestStatusPanel_CodeMapHintSaysF10(t *testing.T) {
	m := newCoverageModel(t)
	out := m.renderStatusViewSized(120, 48)
	if !strings.Contains(out, "F10 CodeMap") {
		t.Errorf("Status panel should hint F10 CodeMap; got:\n%s", out)
	}
	if strings.Contains(out, "F9 CodeMap") {
		t.Errorf("Status panel still hints the wrong F9 CodeMap key:\n%s", out)
	}
}

// TestPanelNav_HiddenAliasesRemoved pins the keybinding cleanup: the
// undocumented duplicate/dead aliases no longer steal keystrokes, while
// every panel's first-class key still routes. ctrl+l in particular must
// fall through so the Status overlay can use it for grid navigation
// instead of being yanked to ProviderLog.
func TestPanelNav_HiddenAliasesRemoved(t *testing.T) {
	m := newCoverageModel(t)

	removed := []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{'i'}, Alt: true}, // alt+i (was Tools dupe)
		{Type: tea.KeyCtrlO}, // ctrl+o (was Providers dupe)
		{Type: tea.KeyCtrlL}, // ctrl+l (was ProviderLog dupe; now Status grid)
		{Type: tea.KeyRunes, Runes: []rune{'9'}, Alt: true}, // alt+9 (was redundant help)
		{Type: tea.KeyRunes, Runes: []rune{'0'}, Alt: true}, // alt+0 (was redundant help)
	}
	for _, k := range removed {
		if _, _, handled := m.handlePanelNavigationShortcut(k); handled {
			t.Errorf("removed alias %q should no longer be handled by panel nav", k.String())
		}
	}

	kept := []struct {
		key  tea.KeyMsg
		name string
	}{
		{tea.KeyMsg{Type: tea.KeyF11}, "f11"},
		{tea.KeyMsg{Type: tea.KeyF8}, "f8"},
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}, Alt: true}, "alt+r"},
	}
	for _, tc := range kept {
		if _, _, handled := m.handlePanelNavigationShortcut(tc.key); !handled {
			t.Errorf("first-class key %q should still be handled by panel nav", tc.name)
		}
	}
}
