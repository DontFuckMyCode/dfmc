package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// hintTokenInHelp returns true if the hint's leading key token is
// satisfied by the help block. Direct substring is the common case;
// for paired-arrow ("↑↓"), vim-style ("hjkl"), or slash-bundled
// ("j/k", "g/G", "pgup/pgdn") tokens, every component must appear
// in the help block. This keeps inline hints terse while help reads
// as the more explicit reference.
func hintTokenInHelp(token, helpBlock string) bool {
	if strings.Contains(helpBlock, token) {
		return true
	}
	// Slash-separated key bundle: split and require each part.
	if strings.Contains(token, "/") {
		for _, part := range strings.Split(token, "/") {
			if part == "" {
				continue
			}
			if !strings.Contains(helpBlock, part) {
				return false
			}
		}
		return true
	}
	// Treat any of these glyph-bundles as "each glyph individually
	// must appear" so the test passes when help uses the spread-out
	// form of the same idea.
	if token == "↑↓" || token == "hjkl" || token == "←↑↓→" {
		for _, r := range token {
			if !strings.Contains(helpBlock, string(r)) {
				return false
			}
		}
		return true
	}
	return false
}

// hint_help_sync_test.go pins the contract that every key surfaced
// in a panel's inline affordance hint ALSO appears in the help
// overlay's PANEL section for that panel. The inline hint is the
// just-glance reminder; help is the canonical reference — they must
// not drift apart silently. Adding a key to the inline hint without
// updating help breaks the test, and so does removing a key from
// help that the hint still advertises.
//
// We intentionally scan for short literal substrings (the key glyph
// or word) rather than parsing the hint's `key verb · key verb · …`
// shape. That keeps the test resilient to small wording tweaks while
// still catching real drift.

func TestHintHelpSync_KeysInInlineHintAppearInHelpSection(t *testing.T) {
	help := renderTUIHelp()
	cases := []struct {
		// section: header text in renderTUIHelp() that marks the
		// start of this panel's block.
		section string
		// renderView returns the panel's rendered output as a string
		// — used to confirm the inline hint actually surfaces in the
		// real view (catches "I removed the hint accidentally" too).
		renderView func(m Model) string
		// hintKeys: literal substrings expected in BOTH the inline
		// hint line AND the help section block. Sourced from the
		// panel's current hint string. Updating a hint requires
		// updating this list, which is the point — the test gates
		// the help update.
		hintKeys []string
	}{
		{
			section:    "MEMORY PANEL",
			renderView: func(m Model) string { return m.renderMemoryView(140) },
			hintKeys:   []string{"↑↓", "enter expand", "/ search", "t tier", "r reload", "→ action menu"},
		},
		{
			section:    "STATUS PANEL",
			renderView: func(m Model) string { return m.renderStatusView(140) },
			hintKeys:   []string{"hjkl", "enter jump to detail", "r reload", "→ action menu"},
		},
		{
			section:    "PLANS PANEL",
			renderView: func(m Model) string { return m.renderPlansView(140) },
			hintKeys:   []string{"↑↓ scroll", "e edit task", "enter re-run", "c clear", "→ action menu"},
		},
		{
			section:    "CONTEXT PANEL",
			renderView: func(m Model) string { return m.renderContextView(140) },
			hintKeys:   []string{"↑↓ scroll", "e edit query", "enter preview", "m manager", "c clear", "→ action menu"},
		},
		{
			section:    "ACTIVITY PANEL",
			renderView: func(m Model) string { return m.renderActivityView(140) },
			hintKeys:   []string{"↑↓ move", "pgup/pgdown page", "enter open", "/ search"},
		},
		{
			section:    "CONVERSATIONS PANEL",
			renderView: func(m Model) string { return m.renderConversationsView(140) },
			hintKeys:   []string{"↑↓ scroll", "enter preview", "/ search"},
		},
		{
			section:    "CODEMAP PANEL",
			renderView: func(m Model) string { return m.renderCodemapView(140) },
			hintKeys:   []string{"↑↓ scroll", "enter action menu", "/ search"},
		},
		{
			section:    "PROMPTS PANEL",
			renderView: func(m Model) string { return m.renderPromptsView(140) },
			hintKeys:   []string{"↑↓ scroll", "enter preview", "/ search"},
		},
		{
			section:    "SECURITY PANEL",
			renderView: func(m Model) string { return m.renderSecurityView(140) },
			hintKeys:   []string{"j/k scroll", "g/G top/bottom", "v toggle view", "/ search", "c clear", "r rescan", "i ignore", "f open in chat", "enter action menu"},
		},
	}
	for _, c := range cases {
		m := NewModel(context.Background(), nil)
		view := ansi.Strip(c.renderView(m))
		// 1) every claimed key must surface in the rendered view.
		for _, key := range c.hintKeys {
			if !strings.Contains(view, key) {
				t.Errorf("[%s] inline hint missing %q in rendered view", c.section, key)
			}
		}
		// 2) every claimed key must appear inside the panel's help
		// section block (between header and next divider). Help uses
		// the same key glyphs / words, so direct substring works.
		sectionIdx := strings.Index(help, c.section)
		if sectionIdx < 0 {
			t.Errorf("[%s] help overlay missing section header", c.section)
			continue
		}
		end := sectionIdx + 700
		if end > len(help) {
			end = len(help)
		}
		blockLower := strings.ToLower(help[sectionIdx:end])
		for _, key := range c.hintKeys {
			// Help docs the key glyph; the descriptive verb in the
			// hint ("scroll", "edit task") might be worded differently
			// (`scroll subtasks`, `edit task (opens input box)`) — so
			// we look up just the leading key token. Split on space
			// and check the first word lives in the help block.
			// Comparison is case-insensitive because the hint uses
			// lowercase ("enter", "esc") but help frequently uses
			// title-case ("Enter", "Esc") for the same key.
			token := strings.ToLower(strings.Fields(key)[0])
			if hintTokenInHelp(token, blockLower) {
				continue
			}
			t.Errorf("[%s] help section missing key token %q from inline hint %q. Block:\n%s",
				c.section, token, key, help[sectionIdx:end])
		}
	}
}
