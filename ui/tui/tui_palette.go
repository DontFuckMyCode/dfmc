package tui

// tui_palette.go — per-tab colour palette and the top "tab strip" that
// shows the active tab between its F-key neighbours. Lives outside
// theme.go so the colour map is editable in one obvious place when
// somebody wants to retune contrast, and so the strip helpers don't
// inflate the already-50KB theme file.
//
// The palette is intentionally generous (15 distinct entries) — each
// tab has its own border colour, accent colour, and short glyph so
// the user can tell at a glance which screen they're on. Without
// per-tab colour the workbench felt monotone; ten chrome panels
// looked identical except for the body content.
//
// Chat has a second entry (planChatPaletteOverride) that kicks in
// when the user is in plan mode. The colour shift is deliberately
// loud so a user who forgot they ran /plan can't miss the gate.

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/dontfuckmycode/dfmc/ui/tui/theme"
)

// tabPaletteEntry bundles every colour decision a tab needs. Border
// goes on the body's outer frame; Accent paints the active badge in
// the strip; Glyph prefixes the tab badge so blind monochrome users
// still see a per-tab marker.
type tabPaletteEntry struct {
	Border lipgloss.Color
	Accent lipgloss.Color
	Glyph  string
}

// Pre-computed entries for every tab name in the canonical order.
// The map is keyed by the tab label exactly as it appears in
// Model.tabs, case-sensitive.
// All hex literals come from theme/palette.go (P14 invariant: zero
// hex literals outside theme/). Overlapping per-tab tones (Chat=Info,
// Status=Ok, etc.) reuse the existing role/severity colours; the
// remaining 8 unique tab tones are exported as theme.ColorTab*.
var tabPalette = map[string]tabPaletteEntry{
	"Chat":          {Border: theme.ColorInfo, Accent: theme.ColorInfo, Glyph: "◆"},
	"Status":        {Border: theme.ColorOk, Accent: theme.ColorOk, Glyph: "◉"},
	"Files":         {Border: theme.ColorWarn, Accent: theme.ColorWarn, Glyph: "▦"},
	"Patch":         {Border: theme.ColorTabPatch, Accent: theme.ColorTabPatch, Glyph: "◈"},
	"Workflow":      {Border: theme.ColorAccent, Accent: theme.ColorAccent, Glyph: "⚙"},
	"Tools":         {Border: theme.ColorRoleCoach, Accent: theme.ColorRoleCoach, Glyph: "⚒"},
	"Activity":      {Border: theme.ColorRoleUser, Accent: theme.ColorRoleUser, Glyph: "✦"},
	"Memory":        {Border: theme.ColorRoleTool, Accent: theme.ColorRoleTool, Glyph: "❖"},
	"CodeMap":       {Border: theme.ColorTabCodeMap, Accent: theme.ColorTabCodeMap, Glyph: "◇"},
	"Conversations": {Border: theme.ColorTabConversations, Accent: theme.ColorTabConversations, Glyph: "❍"},
	"Prompts":       {Border: theme.ColorCode, Accent: theme.ColorCode, Glyph: "✎"},
	"Security":      {Border: theme.ColorFail, Accent: theme.ColorFail, Glyph: "⛒"},
	"Plans":         {Border: theme.ColorTabPlans, Accent: theme.ColorTabPlans, Glyph: "▣"},
	"Context":       {Border: theme.ColorTabContext, Accent: theme.ColorTabContext, Glyph: "◐"},
	"Providers":     {Border: theme.ColorTabProviders, Accent: theme.ColorTabProviders, Glyph: "◌"},
	"Orchestrate":   {Border: theme.ColorTabOrchestrate, Accent: theme.ColorTabOrchestrate, Glyph: "◬"},
	"Shortcuts":     {Border: theme.ColorTabShortcuts, Accent: theme.ColorTabShortcuts, Glyph: "?"},
}

// planChatPaletteOverride kicks in when chat is in plan mode. The
// loud yellow ties the screen frame to the existing /plan badge so
// the gate state is impossible to miss.
var planChatPaletteOverride = tabPaletteEntry{
	Border: theme.ColorWarn,
	Accent: theme.ColorWarn,
	Glyph:  "◈",
}

// fallbackPalette is used for any unknown tab name (defensive — the
// map covers everything the model can currently hold).
var fallbackPalette = tabPaletteEntry{
	Border: colorPanelBorder,
	Accent: colorTabActiveBg,
	Glyph:  "•",
}

// paletteForTab resolves a tab name to its palette entry, applying
// the plan-mode override for Chat.
func paletteForTab(tab string, planMode bool) tabPaletteEntry {
	if tab == "Chat" && planMode {
		return planChatPaletteOverride
	}
	if entry, ok := tabPalette[tab]; ok {
		return entry
	}
	return fallbackPalette
}

// tabFKeyHint returns the keyboard hint a user should see for jumping
// to a tab — F1..F12 where defined, otherwise the alt+key alias.
// Kept aligned with the switch in handleAnyKey so the strip stays
// honest about how to navigate.
func tabFKeyHint(tab string) string {
	switch tab {
	// Eight first-class tabs — F1..F8 step in tab-strip order.
	// Alt+1..Alt+8 mirror them for terminals that swallow F-keys.
	case "Chat":
		return "F1"
	case "Files":
		return "F2"
	case "Patch":
		return "F3"
	case "Workflow":
		return "F4"
	case "Activity":
		return "F5"
	case "Memory":
		return "F6"
	case "Conversations":
		return "F7"
	case "Providers":
		return "F8"
	// Demoted overlays — F9..F12 cover the four most-trafficked
	// (Status, CodeMap, Tools, Security); the remaining five live on
	// Shift+F1..Shift+F5 so every panel still has an F-key. Help is
	// reachable via Ctrl+H / Alt+H, not an F-key.
	case "Status":
		return "F9"
	case "CodeMap":
		return "F10"
	case "Tools":
		return "F11"
	case "Security":
		return "F12"
	case "Prompts":
		return "Shift+F1"
	case "Plans":
		return "Shift+F2"
	case "Context":
		return "Shift+F3"
	case "Orchestrate":
		return "Shift+F4"
	case "Shortcuts":
		return "Shift+F5"
	}
	return ""
}

// renderTopTabStrip paints the new header — a single bright bar with:
//
//	DFMC ▌ Files  ─────  ◀ F2 Status   ◆ F3 FILES ◆   F4 Patch ▶  ─────  tab/⇥ cycles
//
// The active tab badge is filled with the palette accent; previous
// and next tab names are dimmer with their F-key hints. Width-aware:
// when the terminal is narrow we drop the trailing hint, then the
// dashes, and finally collapse to just `[ACTIVE]` if necessary.
func renderTopTabStrip(tabs []string, activeIdx int, planMode bool, width int) string {
	if width < 20 {
		width = 20
	}
	if len(tabs) == 0 || activeIdx < 0 || activeIdx >= len(tabs) {
		return bannerStyle.Render("DFMC")
	}

	active := tabs[activeIdx]
	pal := paletteForTab(active, planMode)
	prevIdx := (activeIdx - 1 + len(tabs)) % len(tabs)
	nextIdx := (activeIdx + 1) % len(tabs)
	prevName := tabs[prevIdx]
	nextName := tabs[nextIdx]

	brand := bannerStyle.Render("DFMC ▌") + " " +
		lipgloss.NewStyle().Foreground(pal.Accent).Bold(true).Render(active)

	prevSeg := subtleStyle.Render("◀ "+blankFallback(tabFKeyHint(prevName), "")+" ") +
		subtleStyle.Render(prevName)
	nextSeg := subtleStyle.Render(nextName) +
		subtleStyle.Render(" "+blankFallback(tabFKeyHint(nextName), "")+" ▶")

	activeBadge := lipgloss.NewStyle().
		Foreground(colorTitleFg).
		Background(pal.Accent).
		Bold(true).
		Padding(0, 1).
		Render(pal.Glyph + " " + blankFallback(tabFKeyHint(active), "") + " " + strings.ToUpper(active) + " " + pal.Glyph)

	hint := subtleStyle.Render("tab/⇥ cycles · ctrl+p palette")

	// Layout: brand | dashes | prev | activeBadge | next | dashes | hint
	// Widths are computed from the visible width of each rendered chunk
	// (lipgloss.Width strips ANSI for measurement).
	brandW := lipgloss.Width(brand)
	prevW := lipgloss.Width(prevSeg)
	badgeW := lipgloss.Width(activeBadge)
	nextW := lipgloss.Width(nextSeg)
	hintW := lipgloss.Width(hint)

	gap := 2 // single-space padding between segments
	fixed := brandW + prevW + badgeW + nextW + hintW + gap*4
	dashBudget := width - fixed
	if dashBudget < 4 {
		// Not enough room — drop the hint, then the next/prev arrows.
		hint = ""
		fixed = brandW + prevW + badgeW + nextW + gap*3
		dashBudget = width - fixed
		if dashBudget < 4 {
			return brand + " " + activeBadge
		}
	}
	leftDashes := subtleStyle.Render(strings.Repeat("─", dashBudget/2))
	rightDashes := subtleStyle.Render(strings.Repeat("─", dashBudget-dashBudget/2))

	parts := []string{brand, leftDashes, prevSeg, activeBadge, nextSeg, rightDashes}
	if hint != "" {
		parts = append(parts, hint)
	}
	return strings.Join(parts, " ")
}
