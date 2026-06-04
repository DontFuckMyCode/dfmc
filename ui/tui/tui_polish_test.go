package tui

import (
	"strings"
	"testing"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func ansiStripForTest(s string) string { return ansi.Strip(s) }

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

// TestContextPanel_ManagerToggleViaM pins the fix for the dead `ctrl+m`
// binding: the Context Manager sub-view now toggles on plain `m` (the
// key that bubbletea can actually deliver — Ctrl+M is indistinguishable
// from Enter), matching the action-menu Accel, and the toggle works both
// directions.
func TestContextPanel_ManagerToggleViaM(t *testing.T) {
	m := newCoverageModel(t)
	m.contextPanel.inputActive = false
	if m.contextPanel.manager.active {
		t.Fatal("Context Manager should start inactive")
	}

	mKey := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}}

	on, _ := m.handleContextKey(mKey)
	m = on.(Model)
	if !m.contextPanel.manager.active {
		t.Fatal("`m` should activate the Context Manager")
	}

	off, _ := m.handleContextKey(mKey)
	m = off.(Model)
	if m.contextPanel.manager.active {
		t.Fatal("`m` again should deactivate the Context Manager")
	}
}

// TestStatsPaging_PgDownWorksAndYieldsToOverlay pins two fixes: (1) the
// stats panel scrolls on PageDown (the old "pgdn" label never matched
// bubbletea's "pgdown", so it was dead), and (2) when a panel overlay is
// open over the Chat tab, stats paging yields so the overlay — which
// keeps activeTab==0 — can scroll its own (often overflowing) body.
func TestStatsPaging_PgDownWorksAndYieldsToOverlay(t *testing.T) {
	m := newCoverageModel(t)
	m.activeTab = 0
	m.ui.showStatsPanel = true
	m.ui.panelOverlayKind = ""

	pgdn := tea.KeyMsg{Type: tea.KeyPgDown}
	pgup := tea.KeyMsg{Type: tea.KeyPgUp}

	if _, _, handled := m.handleStatsPanelShortcut(pgdn); !handled {
		t.Fatal("PageDown should scroll the stats panel (pgdown label fix)")
	}
	if _, _, handled := m.handleStatsPanelShortcut(pgup); !handled {
		t.Fatal("PageUp should scroll the stats panel")
	}

	m.ui.panelOverlayKind = "shortcuts"
	if _, _, handled := m.handleStatsPanelShortcut(pgdn); handled {
		t.Error("with an overlay open, stats paging must yield PageDown to the overlay")
	}
	if _, _, handled := m.handleStatsPanelShortcut(pgup); handled {
		t.Error("with an overlay open, stats paging must yield PageUp to the overlay")
	}
}

// TestNoStaleRemovedKeyHints guards against the panel switcher, Status
// panel, and runtime hints re-introducing a key that the keymap cleanup
// removed (ctrl+i dead, alt+i / ctrl+o / alt+9 / alt+0 hidden dupes, and
// ctrl+l reassigned from ProviderLog to the Status grid). A hint that
// points at a key which no longer does anything is exactly the wrong-
// shortcut defect the cleanup set out to remove.
func TestNoStaleRemovedKeyHints(t *testing.T) {
	m := newCoverageModel(t)
	surfaces := map[string]string{
		"panel switcher": ansiStripForTest(m.renderPanelSwitcher(100)),
		"status panel":   ansiStripForTest(m.renderStatusViewSized(120, 48)),
		"global help":    ansiStripForTest(m.renderHelpOverlay(100)),
	}
	// Whole-word-ish checks for the dead/removed bindings. ctrl+l is
	// allowed in the Status panel (its grid key), so it is not screened.
	banned := []string{"ctrl+o", "alt+i", "ctrl+i", "alt+9", "alt+0"}
	for name, body := range surfaces {
		low := strings.ToLower(body)
		for _, b := range banned {
			if strings.Contains(low, b) {
				t.Errorf("%s still references removed key %q", name, b)
			}
		}
	}
}

// TestRenderActiveView_NoPanicAcrossPanelsAndSizes drives the central
// body dispatcher for every tab, every demoted overlay, the panel
// switcher, and the help overlay across a grid of extreme widths and
// heights (down to 1×1). A panic here is the crash class of "broken
// visual" — a negative strings.Repeat / sub-slice from unguarded width
// math. Output is intentionally not asserted; the guarantee is "renders
// without crashing at any terminal size".
func TestRenderActiveView_NoPanicAcrossPanelsAndSizes(t *testing.T) {
	overlayKinds := []string{
		"status", "tools", "codemap", "prompts", "security", "plans",
		"context", "orchestrate", "shortcuts", "contexts", "providerlog",
		"toolstatus", "telegram",
	}
	widths := []int{1, 5, 20, 40, 60, 80, 120, 200}
	heights := []int{1, 3, 4, 8, 24, 60}
	pal := paletteForTab("Chat", false)

	safeRender := func(label string, m Model, w, h int) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("panic rendering %s at %dx%d: %v", label, w, h, r)
			}
		}()
		_ = m.renderActiveView(w, h, pal)
	}

	base := newCoverageModel(t)

	for ti := range base.tabs {
		for _, w := range widths {
			for _, h := range heights {
				m := base
				m.activeTab = ti
				m.ui.panelOverlayKind = ""
				safeRender("tab:"+base.tabs[ti], m, w, h)
			}
		}
	}

	for _, kind := range overlayKinds {
		for _, w := range widths {
			for _, h := range heights {
				m := base
				m.ui.panelOverlayKind = kind
				safeRender("overlay:"+kind, m, w, h)
			}
		}
	}

	for _, w := range widths {
		for _, h := range heights {
			ms := base
			ms.ui.panelOverlayKind = ""
			ms.panelSwitcher.active = true
			safeRender("panelSwitcher", ms, w, h)

			mh := base
			mh.panelSwitcher.active = false
			mh.ui.panelOverlayKind = ""
			if len(mh.tabs) > 1 {
				mh.activeTab = 1 // non-Chat so the help overlay takes the body
			}
			mh.ui.showHelpOverlay = true
			safeRender("helpOverlay", mh, w, h)
		}
	}
}
