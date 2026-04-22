package tui

// Pin the per-tab palette and the new top-strip rendering. Without
// these, somebody could quietly change a tab colour, drop a tab from
// the palette, or break the active-badge → prev/next layout and the
// only signal would be a screenshot looking "off". The strip is part
// of the workbench identity now — every tab must have a colour and
// every screen must surface its prev/next neighbour by F-key.

import (
	"strings"
	"testing"
)

func TestTabPaletteCoversEveryCanonicalTab(t *testing.T) {
	canonical := []string{
		"Chat", "Status", "Files", "Patch", "Workflow", "Tools",
		"Activity", "Memory", "CodeMap", "Conversations",
		"Prompts", "Security", "Plans", "Context", "Providers",
	}
	for _, name := range canonical {
		if _, ok := tabPalette[name]; !ok {
			t.Fatalf("palette missing entry for tab %q — every workbench tab needs its own colour", name)
		}
	}
}

func TestPaletteForTab_PlanModeOverridesChat(t *testing.T) {
	normal := paletteForTab("Chat", false)
	plan := paletteForTab("Chat", true)
	if normal.Border == plan.Border {
		t.Fatalf("plan mode must repaint the chat frame so the gate state is unmissable; got identical border colour")
	}
	if plan.Border != planChatPaletteOverride.Border {
		t.Fatalf("plan-mode chat palette must come from planChatPaletteOverride; got %#v", plan)
	}
}

func TestPaletteForTab_NonChatIgnoresPlanMode(t *testing.T) {
	a := paletteForTab("Files", false)
	b := paletteForTab("Files", true)
	if a != b {
		t.Fatalf("plan-mode override must only affect Chat; Files diverged: %#v vs %#v", a, b)
	}
}

func TestTabFKeyHintCoversNavigableTabs(t *testing.T) {
	for _, name := range []string{"Chat", "Status", "Files", "Patch", "Workflow", "Tools",
		"Activity", "Memory", "CodeMap", "Conversations", "Prompts", "Security",
		"Plans", "Context", "Providers"} {
		if hint := tabFKeyHint(name); strings.TrimSpace(hint) == "" {
			t.Fatalf("tab %q has no F-key/alt hint — strip would render an empty navigation cue", name)
		}
	}
}

func TestRenderTopTabStrip_ShowsPrevActiveNext(t *testing.T) {
	tabs := []string{"Chat", "Status", "Files", "Patch", "Workflow"}
	out := renderTopTabStrip(tabs, 2 /* Files */, false, 200)
	// The active tab must surface in upper-case as part of the badge —
	// that's what makes it scan as "the one I'm on".
	if !strings.Contains(out, "FILES") {
		t.Fatalf("active tab name (FILES) missing from strip:\n%s", out)
	}
	// Prev (Status) and next (Patch) must both be visible with their
	// F-keys so the user knows how to step.
	if !strings.Contains(out, "Status") || !strings.Contains(out, "Patch") {
		t.Fatalf("prev/next neighbours missing:\n%s", out)
	}
	if !strings.Contains(out, "Alt+I") || !strings.Contains(out, "F4") {
		t.Fatalf("prev/next F-key hints missing:\n%s", out)
	}
}

func TestRenderTopTabStrip_WrapsAroundEnds(t *testing.T) {
	tabs := []string{"Chat", "Status", "Files"}
	// Active = first tab; prev should wrap to last (Files).
	first := renderTopTabStrip(tabs, 0, false, 200)
	if !strings.Contains(first, "Files") {
		t.Fatalf("first-tab strip should show last tab as prev:\n%s", first)
	}
	// Active = last tab; next should wrap to first (Chat).
	last := renderTopTabStrip(tabs, 2, false, 200)
	if !strings.Contains(last, "Chat") {
		t.Fatalf("last-tab strip should show first tab as next:\n%s", last)
	}
}

// At a tiny terminal width the strip must degrade gracefully — drop
// the trailing hint first, then collapse — but it must still render
// a non-empty active badge.
func TestRenderTopTabStrip_DegradesAtNarrowWidth(t *testing.T) {
	tabs := []string{"Chat", "Status", "Files"}
	out := renderTopTabStrip(tabs, 1, false, 30)
	if !strings.Contains(out, "STATUS") {
		t.Fatalf("narrow strip must still surface the active tab badge:\n%s", out)
	}
	if strings.Contains(out, "tab/⇥ cycles") {
		t.Fatalf("narrow strip should drop the hint to make room:\n%s", out)
	}
}
