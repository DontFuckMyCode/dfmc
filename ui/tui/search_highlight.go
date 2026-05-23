package tui

// search_highlight.go — paints every case-insensitive substring match
// of m.chat.lastSearchQuery with an accent background so the user can
// spot the term inside a bubble after `/history search` returns its
// hit list. The query is set by runHistorySearch (zero-hits clears
// it again) and dropped on /clear.
//
// We deliberately do the highlight on raw text BEFORE the markdown +
// linkify passes run inside the bubble render. That means the rest
// of the styling (bold, code, link underline, syntax HL) still wins
// for the non-matched portions of the line — the highlight is the
// only style applied to the matched span. Nested-ANSI shenanigans
// inside an already-styled fragment are avoided because the match
// pass runs on plaintext.

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/dontfuckmycode/dfmc/ui/tui/theme"
)

// searchHitStyle paints the matched span. Background-accent + bold so
// the eye finds it inside a wall of prose without losing legibility.
var searchHitStyle = lipgloss.NewStyle().
	Foreground(theme.ColorTitleFg).
	Background(theme.ColorWarn).
	Bold(true)

func highlightSearchHits(body, query string) string {
	query = strings.TrimSpace(query)
	if query == "" || body == "" {
		return body
	}
	// Work in runes to avoid byte-offset desync when
	// strings.ToLower changes byte length (e.g. İ → i\u0307).
	bodyRunes := []rune(body)
	lowerBodyRunes := []rune(strings.ToLower(body))
	lowerQRunes := []rune(strings.ToLower(query))
	if len(lowerQRunes) == 0 {
		return body
	}
	var out strings.Builder
	cursor := 0
	for cursor < len(bodyRunes) {
		idx := runeIndex(lowerBodyRunes[cursor:], lowerQRunes)
		if idx < 0 {
			out.WriteString(string(bodyRunes[cursor:]))
			break
		}
		hit := cursor + idx
		out.WriteString(string(bodyRunes[cursor:hit]))
		end := hit + len(lowerQRunes)
		if end > len(bodyRunes) {
			end = len(bodyRunes)
		}
		out.WriteString(searchHitStyle.Render(string(bodyRunes[hit:end])))
		cursor = end
	}
	return out.String()
}

// runeIndex finds the first occurrence of pattern in text, returning
// the rune index or -1 if not found.
func runeIndex(text, pattern []rune) int {
	if len(pattern) == 0 || len(pattern) > len(text) {
		return -1
	}
	for i := 0; i <= len(text)-len(pattern); i++ {
		match := true
		for j := range pattern {
			if text[i+j] != pattern[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
