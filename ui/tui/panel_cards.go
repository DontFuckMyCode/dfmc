// panel_cards.go — shared card primitive for the rebuilt panel UIs.
//
// A "card" is a bordered box with a title bar (icon + label + status
// chip), a body of key-value rows, and an optional footer (action
// hint). Cards arrange in 1-, 2-, or 3-column grids depending on
// available width. The selected card has its border + title bar
// rendered in the active palette so arrow-key navigation reads at
// a glance.
//
// Used by render_status.go (rebuilt F2 panel) as a template for the
// rest of the panel rebuild — Files, Activity, Memory, etc. all
// land on the same primitive so they share the same look.

package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// panelCard is the data the renderer needs to draw one card.
type panelCard struct {
	// Icon is a single-glyph badge (e.g. "◉", "⎈", "≡") rendered to
	// the left of the title.
	Icon string

	// Title is the card's section name in TITLE CASE — uppercased
	// at render time so callers don't have to remember.
	Title string

	// StatusChip is an optional one-word status badge ("OK", "DEGRADED",
	// "OFFLINE") drawn to the right of the title in a styled pill.
	// Empty hides the chip.
	StatusChip string
	// StatusChipStyle styles the chip pill. lipgloss.Style isn't
	// comparable so we use a pointer; nil → subtleStyle default.
	StatusChipStyle *lipgloss.Style

	// Rows are key-value pairs. Keys are right-padded to the same
	// column width within a single card. An empty Value renders the
	// key alone (used for free-form lines).
	Rows []panelCardRow

	// FooterHint is optional one-line guidance (e.g. "press → for
	// detail" or "F12 to scan"). Renders dim under the body.
	FooterHint string
}

type panelCardRow struct {
	Key   string
	Value string
	// Style applies to the value half. nil defaults to plain (no
	// styling on the value half so it reads neutral).
	Style *lipgloss.Style
}

// renderPanelCard draws one card at the given inner width (excludes
// border padding). selected=true paints the border and title bar in
// the active palette accent so arrow-key nav reads visibly.
func renderPanelCard(card panelCard, innerWidth int, selected bool, accent lipgloss.Color) string {
	if innerWidth < 18 {
		innerWidth = 18
	}

	// --- title bar ---------------------------------------------------
	icon := strings.TrimSpace(card.Icon)
	if icon == "" {
		icon = "▣"
	}
	title := strings.ToUpper(strings.TrimSpace(card.Title))
	if title == "" {
		title = "CARD"
	}
	titleParts := []string{icon, title}
	chipText := strings.TrimSpace(card.StatusChip)
	chipRendered := ""
	if chipText != "" {
		style := subtleStyle
		if card.StatusChipStyle != nil {
			style = *card.StatusChipStyle
		}
		chipRendered = style.Render(" " + chipText + " ")
	}

	titleStr := strings.Join(titleParts, " ")
	if selected {
		titleStr = lipgloss.NewStyle().Foreground(accent).Bold(true).Render(titleStr)
	} else {
		titleStr = titleStyle.Render(titleStr)
	}

	// title bar with left-justified title + right-justified chip
	titleBarInner := titleStr
	if chipRendered != "" {
		// Compute padding so chip lands at the right edge of the card.
		titleVisible := lipgloss.Width(titleStr)
		chipVisible := lipgloss.Width(chipRendered)
		gap := innerWidth - titleVisible - chipVisible
		if gap < 1 {
			gap = 1
		}
		titleBarInner = titleStr + strings.Repeat(" ", gap) + chipRendered
	}

	// --- body --------------------------------------------------------
	keyWidth := 0
	for _, r := range card.Rows {
		k := strings.TrimSpace(r.Key)
		if w := lipgloss.Width(k); w > keyWidth {
			keyWidth = w
		}
	}
	if keyWidth > innerWidth/2 {
		keyWidth = innerWidth / 2
	}
	bodyLines := make([]string, 0, len(card.Rows))
	for _, r := range card.Rows {
		key := strings.TrimSpace(r.Key)
		val := r.Value
		// Right-pad key + append ":" so all values align in the card
		// and the keys read like a natural-language label.
		if key != "" {
			padding := keyWidth - lipgloss.Width(key)
			if padding < 0 {
				padding = 0
			}
			key = subtleStyle.Render(key + ":" + strings.Repeat(" ", padding))
		}
		valRendered := val
		if r.Style != nil {
			valRendered = r.Style.Render(val)
		}
		var line string
		if key == "" {
			line = valRendered
		} else {
			line = key + "  " + valRendered
		}
		// Hard-clip to inner width so a long value doesn't wrap and
		// burst the card frame.
		bodyLines = append(bodyLines, truncateSingleLine(line, innerWidth))
	}
	if len(bodyLines) == 0 {
		bodyLines = []string{subtleStyle.Render("(no data)")}
	}

	// --- footer hint -------------------------------------------------
	var footer string
	if hint := strings.TrimSpace(card.FooterHint); hint != "" {
		footer = subtleStyle.Render(truncateSingleLine(hint, innerWidth))
	}

	// --- assemble ----------------------------------------------------
	contentLines := []string{titleBarInner, ""}
	contentLines = append(contentLines, bodyLines...)
	if footer != "" {
		contentLines = append(contentLines, "", footer)
	}
	content := strings.Join(contentLines, "\n")

	border := lipgloss.RoundedBorder()
	borderColor := colorPanelBorder
	if selected {
		borderColor = accent
	}
	frame := lipgloss.NewStyle().
		Border(border).
		BorderForeground(borderColor).
		Padding(0, 1).
		Width(innerWidth + 2)
	return frame.Render(content)
}

// renderPanelCardGrid arranges cards in N columns. Returns a single
// rendered block. Width is the total available width; the grid
// distributes it among columns minus a 1-cell gap.
func renderPanelCardGrid(cards []panelCard, totalWidth, columns int, selectedIdx int, accent lipgloss.Color) string {
	if len(cards) == 0 {
		return ""
	}
	if columns < 1 {
		columns = 1
	}
	if columns > len(cards) {
		columns = len(cards)
	}
	gap := 1
	colWidth := (totalWidth - (columns-1)*(gap+2)) / columns
	if colWidth < 22 {
		colWidth = 22
	}

	// Render each card to its column-width string.
	rendered := make([]string, len(cards))
	for i, c := range cards {
		rendered[i] = renderPanelCard(c, colWidth-2, i == selectedIdx, accent)
	}

	// Pack into rows of `columns` cells.
	var blocks []string
	for i := 0; i < len(rendered); i += columns {
		end := i + columns
		if end > len(rendered) {
			end = len(rendered)
		}
		row := rendered[i:end]
		// JoinHorizontal aligns to top so cells of unequal height stack
		// without the shorter ones drifting middle.
		blocks = append(blocks,
			lipgloss.JoinHorizontal(lipgloss.Top, joinWithSpacer(row, gap)...))
	}
	return strings.Join(blocks, "\n")
}

// joinWithSpacer interleaves cells with a `gap`-wide space column so
// JoinHorizontal renders gutters consistently.
func joinWithSpacer(cells []string, gap int) []string {
	if gap <= 0 || len(cells) <= 1 {
		return cells
	}
	out := make([]string, 0, len(cells)*2-1)
	spacer := strings.Repeat(" ", gap)
	for i, c := range cells {
		if i > 0 {
			out = append(out, spacer)
		}
		out = append(out, c)
	}
	return out
}
