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
	lowerBody := strings.ToLower(body)
	lowerQ := strings.ToLower(query)
	if !strings.Contains(lowerBody, lowerQ) {
		return body
	}
	var out strings.Builder
	cursor := 0
	for cursor < len(body) {
		idx := strings.Index(lowerBody[cursor:], lowerQ)
		if idx < 0 {
			out.WriteString(body[cursor:])
			break
		}
		hit := cursor + idx
		out.WriteString(body[cursor:hit])
		end := hit + len(query)
		if end > len(body) {
			end = len(body)
		}
		out.WriteString(searchHitStyle.Render(body[hit:end]))
		cursor = end
	}
	return out.String()
}
