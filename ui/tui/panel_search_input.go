package tui

import "fmt"

// panel_search_input.go — shared affordances for the live "/" search
// input box, its companion typing hint, and the hit-count chip, used
// by every diagnostic panel that supports filtering (Activity, Memory,
// Conversations, CodeMap, Files, Tools, Security, Prompts).
//
// The box renders as `Search: <q>▏` with an accent-coloured caret;
// empty queries render a subtle italic placeholder so the user knows
// the field is editable. The typing hint reads
// `enter commit · esc stop · backspace delete` and is intentionally
// short — panels with extra state (hit chips, etc.) append onto it
// rather than building their own variants.
//
// Why extract: every panel was hand-rolling the same two strings,
// each with its own slightly-different palette / spacing. A single
// canonical helper keeps the affordance visually identical across the
// whole TUI and gives one place to evolve the look (e.g. swap the
// caret glyph or paint the placeholder differently).

// renderSearchInput returns the live `Search: <q>▏` input line. Empty
// query renders the placeholder in subtle italic so the box reads as
// editable rather than broken. Callers append a hint underneath.
func renderSearchInput(query, placeholder string) string {
	body := query
	if body == "" {
		body = subtleStyle.Italic(true).Render(placeholder)
	}
	return accentStyle.Render("Search: ") + body + accentStyle.Render("▏")
}

// searchTypingHint is the canonical hint line shown alongside the
// search input box. Returned unstyled-suffixed so callers can append
// extras (a hit chip, a separator dot, etc.) without re-rendering.
func searchTypingHint() string {
	return subtleStyle.Render("enter commit · esc stop · backspace delete")
}

// searchHitsChip is the canonical N-hits / 0-hits indicator paired
// with a panel search query. Red on zero (the eye needs to notice
// "your filter killed every row"), green otherwise. Every panel uses
// the same chip so a search at zero reads identically in Activity,
// Memory, Conversations, CodeMap, Files, Tools, Security, and Prompts.
func searchHitsChip(n int) string {
	if n == 0 {
		return failStyle.Render("0 hits")
	}
	return okStyle.Render(fmt.Sprintf("%d hits", n))
}

// panelIdleHint returns the canonical
// "↑↓ scroll · enter <verb> · / search" affordance line used by
// every single-column-list diagnostic panel with a search filter
// (CodeMap, Conversations, Prompts, ...). enterVerb is the
// panel-specific noun for what Enter does ("preview", "re-run",
// "action menu", ...) — kept short so the hint fits on one line
// under the banner. Panels with non-standard nav surfaces
// (Activity's page-keys, Status's row navigation) still build their
// own hint inline.
//
// We intentionally do NOT advertise "esc back" — esc never leaves
// the panel (tab switching is global), it only cancels the
// search-input sub-state in panels that have one. Surfacing it in
// the idle hint was a lie that the bi-directional sync test caught.
func panelIdleHint(enterVerb string) string {
	return subtleStyle.Render("↑↓ scroll · enter " + enterVerb + " · / search")
}
