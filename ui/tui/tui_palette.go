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
	FKey   string
}

// Pre-computed entries for every tab name in the canonical order.
// The map is keyed by the tab label exactly as it appears in
// Model.tabs, case-sensitive.
// All hex literals come from theme/palette.go (P14 invariant: zero
// hex literals outside theme/). Overlapping per-tab tones (Chat=Info,
// Status=Ok, etc.) reuse the existing role/severity colours; the
// remaining 8 unique tab tones are exported as theme.ColorTab*.
var tabPalette = map[string]tabPaletteEntry{
	"Chat":          {Border: theme.ColorInfo, Accent: theme.ColorInfo, Glyph: "◆", FKey: "F1"},
	"Status":        {Border: theme.ColorOk, Accent: theme.ColorOk, Glyph: "◉", FKey: "F9"},
	"Files":         {Border: theme.ColorWarn, Accent: theme.ColorWarn, Glyph: "▦", FKey: "F2"},
	"Patch":         {Border: theme.ColorTabPatch, Accent: theme.ColorTabPatch, Glyph: "◈", FKey: "F3"},
	"Workflow":      {Border: theme.ColorAccent, Accent: theme.ColorAccent, Glyph: "⚙", FKey: "F4"},
	"Tools":         {Border: theme.ColorRoleCoach, Accent: theme.ColorRoleCoach, Glyph: "⚒", FKey: "F11"},
	"Activity":      {Border: theme.ColorRoleUser, Accent: theme.ColorRoleUser, Glyph: "✦", FKey: "F5"},
	"Memory":        {Border: theme.ColorRoleTool, Accent: theme.ColorRoleTool, Glyph: "❖", FKey: "F6"},
	"CodeMap":       {Border: theme.ColorTabCodeMap, Accent: theme.ColorTabCodeMap, Glyph: "◇", FKey: "F10"},
	"Conversations": {Border: theme.ColorTabConversations, Accent: theme.ColorTabConversations, Glyph: "❍", FKey: "F7"},
	"Prompts":       {Border: theme.ColorCode, Accent: theme.ColorCode, Glyph: "✎", FKey: "Shift+F1"},
	"Security":      {Border: theme.ColorFail, Accent: theme.ColorFail, Glyph: "⛒", FKey: "F12"},
	"Plans":         {Border: theme.ColorTabPlans, Accent: theme.ColorTabPlans, Glyph: "▣", FKey: "Shift+F2"},
	"Context":       {Border: theme.ColorTabContext, Accent: theme.ColorTabContext, Glyph: "◐", FKey: "Shift+F3"},
	"Providers":     {Border: theme.ColorTabProviders, Accent: theme.ColorTabProviders, Glyph: "◌", FKey: "F8"},
	"Orchestrate":   {Border: theme.ColorTabOrchestrate, Accent: theme.ColorTabOrchestrate, Glyph: "◬", FKey: "Shift+F4"},
	"Shortcuts":     {Border: theme.ColorTabShortcuts, Accent: theme.ColorTabShortcuts, Glyph: "?", FKey: "Shift+F5"},
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

// tabFKeyHint returns the keyboard hint for jumping to a tab.
// Backed by tabPalette so adding a new tab only requires one entry.
func tabFKeyHint(tab string) string {
	if entry, ok := tabPalette[tab]; ok {
		return entry.FKey
	}
	return ""
}

// overlayStripMeta maps a panelOverlayKind to the canonical panel name
// (the tabPalette key, for accent + glyph) and its F-key hint. The F9+
// panels demoted out of the 8-tab strip live here so the strip can still
// announce them as the active panel when one is open over a tab.
var overlayStripMeta = map[string]struct{ name, fkey string }{
	"status":      {"Status", "F9"},
	"codemap":     {"CodeMap", "F10"},
	"tools":       {"Tools", "F11"},
	"security":    {"Security", "F12"},
	"prompts":     {"Prompts", "Shift+F1"},
	"plans":       {"Plans", "Shift+F2"},
	"context":     {"Context", "Shift+F3"},
	"orchestrate": {"Orchestrate", "Shift+F4"},
	"shortcuts":   {"Shortcuts", "Shift+F5"},
	"contexts":    {"Contexts", "Shift+F6"},
	"providerlog": {"ProviderLog", "Shift+F7"},
	"telegram":    {"Telegram", "Shift+F8"},
	"toolstatus":  {"ToolStatus", "Ctrl+Shift+T"},
}

// renderTopTabStrip paints the new header — a single bright bar with:
//
//	DFMC ▌ Files  ─────  ◀ F1 Chat   ◆ F2 FILES ◆   F3 Patch ▶  ─────  tab/⇥ cycles
//
// The active tab badge is filled with the palette accent; previous
// and next tab names are dimmer with their F-key hints. Width-aware:
// when the terminal is narrow we drop the trailing hint, then the
// dashes, and finally collapse to just `[ACTIVE]` if necessary.
func renderTopTabStrip(tabs []string, activeIdx int, planMode bool, width int, overlayKind string) string {
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

	// Defaults: the normal 8-tab carousel (prev ◀ ACTIVE ▶ next).
	brandName := active
	badgeAccent := pal.Accent
	badgeGlyph := pal.Glyph
	badgeFKey := tabFKeyHint(active)
	badgeLabel := strings.ToUpper(active)
	prevSeg := subtleStyle.Render("◀ "+blankFallback(tabFKeyHint(prevName), "")+" ") +
		subtleStyle.Render(prevName)
	nextSeg := subtleStyle.Render(nextName) +
		subtleStyle.Render(" "+blankFallback(tabFKeyHint(nextName), "")+" ▶")
	hint := subtleStyle.Render("tab/⇥ cycles · ctrl+p palette")

	// When an F9+ panel overlay is open it isn't in the carousel, so the
	// active badge would otherwise keep showing the underlying tab. Swap the
	// badge to the overlay panel (its own glyph + F-key + name) and turn the
	// neighbour segments into the esc-return breadcrumb so the strip names
	// what's actually on screen.
	if overlayKind != "" {
		meta := overlayStripMeta[overlayKind]
		opal := paletteForTab(meta.name, false)
		label := panelOverlayLabel(overlayKind)
		brandName = label
		badgeAccent = opal.Accent
		badgeGlyph = opal.Glyph
		badgeFKey = meta.fkey
		badgeLabel = label
		prevSeg = subtleStyle.Render("◀ esc " + active)
		nextSeg = subtleStyle.Render("close esc")
		hint = subtleStyle.Render("esc closes · tab/⇥ tabs")
	}

	brand := bannerStyle.Render("DFMC ▌") + " " +
		lipgloss.NewStyle().Foreground(badgeAccent).Bold(true).Render(brandName)

	activeBadge := lipgloss.NewStyle().
		Foreground(colorTitleFg).
		Background(badgeAccent).
		Bold(true).
		Padding(0, 1).
		Render(badgeGlyph + " " + blankFallback(badgeFKey, "") + " " + badgeLabel + " " + badgeGlyph)

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
