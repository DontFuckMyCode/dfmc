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
var tabPalette = map[string]tabPaletteEntry{
	"Chat":          {Border: lipgloss.Color("#67E8F9"), Accent: lipgloss.Color("#67E8F9"), Glyph: "◆"},
	"Status":        {Border: lipgloss.Color("#6EE7A7"), Accent: lipgloss.Color("#6EE7A7"), Glyph: "◉"},
	"Files":         {Border: lipgloss.Color("#F6D38A"), Accent: lipgloss.Color("#F6D38A"), Glyph: "▦"},
	"Patch":         {Border: lipgloss.Color("#FF9F6A"), Accent: lipgloss.Color("#FF9F6A"), Glyph: "◈"},
	"Workflow":      {Border: lipgloss.Color("#BFA9FF"), Accent: lipgloss.Color("#BFA9FF"), Glyph: "⚙"},
	"Tools":         {Border: lipgloss.Color("#F4B8D6"), Accent: lipgloss.Color("#F4B8D6"), Glyph: "⚒"},
	"Activity":      {Border: lipgloss.Color("#8BC7FF"), Accent: lipgloss.Color("#8BC7FF"), Glyph: "✦"},
	"Memory":        {Border: lipgloss.Color("#C4A7FF"), Accent: lipgloss.Color("#C4A7FF"), Glyph: "❖"},
	"CodeMap":       {Border: lipgloss.Color("#5EEAD4"), Accent: lipgloss.Color("#5EEAD4"), Glyph: "◇"},
	"Conversations": {Border: lipgloss.Color("#FFB4B4"), Accent: lipgloss.Color("#FFB4B4"), Glyph: "❍"},
	"Prompts":       {Border: lipgloss.Color("#F2E5A1"), Accent: lipgloss.Color("#F2E5A1"), Glyph: "✎"},
	"Security":      {Border: lipgloss.Color("#FF8A8A"), Accent: lipgloss.Color("#FF8A8A"), Glyph: "⛒"},
	"Plans":         {Border: lipgloss.Color("#A5B4FC"), Accent: lipgloss.Color("#A5B4FC"), Glyph: "▣"},
	"Context":       {Border: lipgloss.Color("#BEF264"), Accent: lipgloss.Color("#BEF264"), Glyph: "◐"},
	"Providers":     {Border: lipgloss.Color("#F0ABFC"), Accent: lipgloss.Color("#F0ABFC"), Glyph: "◌"},
}

// planChatPaletteOverride kicks in when chat is in plan mode. The
// loud yellow ties the screen frame to the existing /plan badge so
// the gate state is impossible to miss.
var planChatPaletteOverride = tabPaletteEntry{
	Border: lipgloss.Color("#F6D38A"),
	Accent: lipgloss.Color("#F6D38A"),
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
	case "Chat":
		return "F1"
	case "Providers":
		return "F2"
	case "Files":
		return "F3"
	case "Patch":
		return "F4"
	case "Workflow":
		return "F5"
	case "Tools":
		return "F6"
	case "Activity":
		return "F7"
	case "Memory":
		return "F8"
	case "CodeMap":
		return "F9"
	case "Conversations":
		return "F10"
	case "Prompts":
		return "F11"
	case "Security":
		return "F12"
	case "Status":
		return "Alt+I"
	case "Plans":
		return "Alt+Y"
	case "Context":
		return "Alt+W"
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
		hintW = 0
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
