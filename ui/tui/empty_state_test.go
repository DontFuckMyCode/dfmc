package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// empty_state_test.go pins the contract that every "nothing here
// yet" empty state has three pieces of information:
//   1. a HEADLINE naming what's missing
//   2. a WHY explaining what populates this panel
//   3. an ACTION naming the concrete key / command to make it fill
//
// Without (3), the user lands on the panel, sees "No X yet.", and
// is left guessing how to make X appear — a common dead-end that
// the bi-directional hint test can't catch because empty states
// render BEFORE the hint line is even relevant.
//
// Real bugs this test would have caught when written:
//   - Memory had a 1-line "No memory entries." with no why and no
//     next-step. A user lands, sees the panel is empty, and has no
//     idea that memory fills as the agent runs or via /remember.
//   - Files claimed "Press Ctrl+R to refresh" but the handler is
//     just `r`. Following the hint did nothing.
//   - Security claimed "Press ctrl+r to scan" — same lie.

func TestEmptyStates_AllPanelsExplainAndCallToAction(t *testing.T) {
	cases := []struct {
		panel      string
		renderView func(m Model) string
		// headline that must appear (the "missing X" headline)
		headline string
		// why-fragment that must appear (the explanation of what fills
		// this panel — short substring is fine)
		why string
		// action key/command that must appear (must be one the handler
		// actually supports)
		action string
	}{
		{
			panel:      "Memory",
			renderView: func(m Model) string { return m.renderMemoryView(140) },
			headline:   "No memory entries",
			why:        "working / episodic / semantic",
			action:     "/remember",
		},
		{
			panel:      "Conversations",
			renderView: func(m Model) string { return m.renderConversationsView(140) },
			headline:   "No conversations persisted yet",
			why:        ".dfmc/conversations/",
			action:     "/chat",
		},
		{
			panel:      "Prompts",
			renderView: func(m Model) string { return m.renderPromptsView(140) },
			headline:   "No prompt templates loaded",
			why:        "system-prompt overlays",
			action:     ".dfmc/prompts",
		},
		{
			panel:      "CodeMap",
			renderView: func(m Model) string { return m.renderCodemapView(140) },
			headline:   "CodeMap is empty",
			why:        "symbol/dependency graph",
			action:     "dfmc analyze",
		},
		{
			panel:      "Activity",
			renderView: func(m Model) string { return m.renderActivityView(140) },
			headline:   "No events yet",
			why:        "live firehose",
			action:     "/chat",
		},
		{
			panel:      "Security",
			renderView: func(m Model) string { return m.renderSecurityView(140) },
			headline:   "No security scan run yet",
			why:        "hard-coded secrets",
			action:     "Press r to scan",
		},
		{
			panel:      "Files",
			renderView: func(m Model) string { return m.renderFilesView(140) },
			headline:   "No indexed project files",
			why:        "", // Files has 2-line variant — skip the why check
			action:     "Press r to refresh",
		},
	}
	for _, c := range cases {
		m := NewModel(context.Background(), nil)
		view := ansi.Strip(c.renderView(m))
		if !strings.Contains(view, c.headline) {
			t.Errorf("[%s] empty state missing headline %q", c.panel, c.headline)
		}
		if c.why != "" && !strings.Contains(view, c.why) {
			t.Errorf("[%s] empty state missing why-fragment %q (user needs to know what populates the panel)", c.panel, c.why)
		}
		if !strings.Contains(view, c.action) {
			t.Errorf("[%s] empty state missing action %q (user has no concrete next step)", c.panel, c.action)
		}
	}
}

// TestEmptyStates_DoNotLieAboutKeys regression-pins the specific
// "Ctrl+R refresh" lie that lived in Files/Security empty states —
// the handlers bind plain `r`, not `Ctrl+R`. If anyone copies a
// stale hint back in, this catches it.
func TestEmptyStates_DoNotLieAboutKeys(t *testing.T) {
	m := NewModel(context.Background(), nil)
	for _, panel := range []struct {
		name string
		view string
	}{
		{"Files", ansi.Strip(m.renderFilesView(140))},
		{"Security", ansi.Strip(m.renderSecurityView(140))},
	} {
		// Both panels handle `r`, NOT `Ctrl+R`. The original lies were
		// case-mixed ("Ctrl+R", "ctrl+r"); reject both.
		for _, lie := range []string{"Ctrl+R to refresh", "ctrl+r to refresh", "ctrl+r to scan", "Ctrl+R to scan"} {
			if strings.Contains(panel.view, lie) {
				t.Errorf("[%s] empty state lies about key — claims %q but handler is plain `r`", panel.name, lie)
			}
		}
	}
}
