package tui

// diff_sidebyside.go — render a unified-diff string as a paired
// left (before) / right (after) view with explicit `-`/`+` prefixes.
// The Patch panel previously stacked the unified text in one column,
// so a removed line and the line that replaced it sat 30 rows apart
// and the eye had to chase. Side-by-side puts them on the same row,
// gutter-coloured red/green, with a vertical divider between halves.
//
// The parser is intentionally tolerant: it accepts vanilla `git diff`
// output, `diff -u`, and the assistant's apply-patch envelope. Lines
// it doesn't understand (`diff `, `index `, mode bits, `\ No newline`)
// are echoed across both columns as headers so the structure stays
// visible.

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// diffRowKind tags how a side cell should render. Empty means "no cell
// on this side this row" — rendered as blank padding so the columns
// stay vertically aligned.
type diffRowKind rune

const (
	diffEmpty   diffRowKind = 0
	diffContext diffRowKind = ' '
	diffRemove  diffRowKind = '-'
	diffAdd     diffRowKind = '+'
	diffHunk    diffRowKind = '@'
	diffHeader  diffRowKind = '~'
)

type diffSideRow struct {
	leftKind  diffRowKind
	leftText  string
	rightKind diffRowKind
	rightText string
}

// parseUnifiedDiffSideBySide turns a unified-diff string into a slice
// of (left, right) rows. Removed/added lines from the same hunk are
// paired 1:1; surplus on either side is padded with blank cells.
// Context lines, hunk markers (@@ ... @@), and unrecognised file
// headers (`diff --git`, `+++`, `---`, `index`) appear in both columns
// so the user always knows which file they're looking at.
func parseUnifiedDiffSideBySide(diff string) []diffSideRow {
	if diff == "" {
		return nil
	}
	rows := make([]diffSideRow, 0, 64)
	var leftBuf, rightBuf []string

	flushPair := func() {
		// Pair removed and added lines positionally. Surplus on either
		// side becomes a blank cell on the other so columns line up.
		n := len(leftBuf)
		if len(rightBuf) > n {
			n = len(rightBuf)
		}
		for i := 0; i < n; i++ {
			row := diffSideRow{}
			if i < len(leftBuf) {
				row.leftKind = diffRemove
				row.leftText = leftBuf[i]
			}
			if i < len(rightBuf) {
				row.rightKind = diffAdd
				row.rightText = rightBuf[i]
			}
			rows = append(rows, row)
		}
		leftBuf = leftBuf[:0]
		rightBuf = rightBuf[:0]
	}

	pushBoth := func(kind diffRowKind, text string) {
		flushPair()
		rows = append(rows, diffSideRow{
			leftKind:  kind,
			leftText:  text,
			rightKind: kind,
			rightText: text,
		})
	}

	for _, line := range strings.Split(strings.ReplaceAll(diff, "\r\n", "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"):
			// File markers — show in both columns as headers.
			pushBoth(diffHeader, line)
		case strings.HasPrefix(line, "@@"):
			pushBoth(diffHunk, line)
		case strings.HasPrefix(line, "diff "),
			strings.HasPrefix(line, "index "),
			strings.HasPrefix(line, "old mode"),
			strings.HasPrefix(line, "new mode"),
			strings.HasPrefix(line, "similarity "),
			strings.HasPrefix(line, "rename "),
			strings.HasPrefix(line, "deleted file"),
			strings.HasPrefix(line, "new file"):
			pushBoth(diffHeader, line)
		case strings.HasPrefix(line, `\ `): // "\ No newline at end of file"
			pushBoth(diffHeader, line)
		case strings.HasPrefix(line, "+"):
			rightBuf = append(rightBuf, line[1:])
		case strings.HasPrefix(line, "-"):
			leftBuf = append(leftBuf, line[1:])
		case strings.HasPrefix(line, " "):
			flushPair()
			text := line[1:]
			rows = append(rows, diffSideRow{
				leftKind:  diffContext,
				leftText:  text,
				rightKind: diffContext,
				rightText: text,
			})
		default:
			if line == "" {
				continue
			}
			pushBoth(diffHeader, line)
		}
	}
	flushPair()
	return rows
}

// renderDiffSideBySide produces the panel-ready string. width is the
// total width budget for both columns + the gutter divider; maxRows
// caps vertical output so a 5000-line diff doesn't blow the panel.
func renderDiffSideBySide(diff string, width, maxRows int) string {
	rows := parseUnifiedDiffSideBySide(diff)
	if len(rows) == 0 {
		return subtleStyle.Render("Working tree is clean — nothing to review.")
	}
	if maxRows <= 0 {
		maxRows = 18
	}
	if width < 40 {
		width = 40
	}
	// 4 chars of chrome per row: gutter (1) + space (1) per side, then
	// the ` │ ` divider in the middle (3). Split the rest evenly.
	const dividerLit = " │ "
	col := (width - len(dividerLit) - 4) / 2
	if col < 12 {
		col = 12
	}

	truncated := false
	if len(rows) > maxRows {
		rows = rows[:maxRows]
		truncated = true
	}

	out := make([]string, 0, len(rows)+1)
	out = append(out, renderDiffSideHeader(col, dividerLit))
	for _, r := range rows {
		left := renderDiffCell(r.leftKind, r.leftText, col)
		right := renderDiffCell(r.rightKind, r.rightText, col)
		out = append(out, left+subtleStyle.Render(dividerLit)+right)
	}
	if truncated {
		out = append(out, subtleStyle.Render(fmt.Sprintf("  ... [%d more rows truncated; press F4 to focus the panel]", maxRows)))
	}
	return strings.Join(out, "\n")
}

func renderDiffSideHeader(col int, divider string) string {
	header := lipgloss.NewStyle().Bold(true).Foreground(colorMuted)
	left := header.Render(padRight("─ before "+strings.Repeat("─", maxInt(col-9, 0)), col))
	right := header.Render(padRight("─ after "+strings.Repeat("─", maxInt(col-8, 0)), col))
	return left + subtleStyle.Render(divider) + right
}

// renderDiffCell paints one cell with its prefix glyph and color. The
// 1-char gutter glyph (`-`, `+`, ` `, `@`, `~`) sits inside the column
// budget so wrap math stays simple.
func renderDiffCell(kind diffRowKind, text string, col int) string {
	if col <= 0 {
		return ""
	}
	gutter := " "
	style := lipgloss.NewStyle()
	switch kind {
	case diffRemove:
		gutter = "-"
		style = style.Foreground(colorFail)
	case diffAdd:
		gutter = "+"
		style = style.Foreground(colorOk)
	case diffContext:
		gutter = " "
		style = style.Foreground(colorMuted)
	case diffHunk:
		gutter = "@"
		style = style.Foreground(colorAccent).Bold(true)
	case diffHeader:
		gutter = "~"
		style = style.Foreground(colorInfo)
	case diffEmpty:
		// Blank cell — keep alignment, no styling.
		return strings.Repeat(" ", col)
	}
	body := text
	avail := col - 2 // gutter + space
	if avail < 1 {
		avail = 1
	}
	if len([]rune(body)) > avail {
		runes := []rune(body)
		body = string(runes[:maxInt(avail-3, 0)]) + "..."
	}
	cell := gutter + " " + body
	cell = padRight(cell, col)
	return style.Render(cell)
}

func padRight(s string, width int) string {
	r := []rune(s)
	if len(r) >= width {
		return string(r[:width])
	}
	return s + strings.Repeat(" ", width-len(r))
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
