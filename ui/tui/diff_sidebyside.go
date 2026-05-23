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
	leftLine  int // 1-based "before" line number; 0 when not applicable
	rightKind diffRowKind
	rightText string
	rightLine int // 1-based "after" line number; 0 when not applicable
}

// hunkLineCounters tracks the next line numbers to assign as the
// parser walks a hunk body. They are seeded from the `@@ -A,B +C,D @@`
// header and incremented per content line — both sides for context,
// left only for removed, right only for added.
type hunkLineCounters struct {
	left  int
	right int
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
	type bufEntry struct {
		text string
		line int
	}
	var leftBuf, rightBuf []bufEntry
	var cur hunkLineCounters

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
				row.leftText = leftBuf[i].text
				row.leftLine = leftBuf[i].line
			}
			if i < len(rightBuf) {
				row.rightKind = diffAdd
				row.rightText = rightBuf[i].text
				row.rightLine = rightBuf[i].line
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
			// Seed the line counters from the hunk header. The format is
			// `@@ -A[,B] +C[,D] @@ optional-context`; parseHunkStart
			// returns (A, C) and falls back to 0,0 on malformed headers
			// — in which case line numbers simply stop incrementing,
			// which is the safest visible degradation.
			cur.left, cur.right = parseHunkStart(line)
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
			rightBuf = append(rightBuf, bufEntry{text: line[1:], line: cur.right})
			cur.right++
		case strings.HasPrefix(line, "-"):
			leftBuf = append(leftBuf, bufEntry{text: line[1:], line: cur.left})
			cur.left++
		case strings.HasPrefix(line, " "):
			flushPair()
			text := line[1:]
			rows = append(rows, diffSideRow{
				leftKind:  diffContext,
				leftText:  text,
				leftLine:  cur.left,
				rightKind: diffContext,
				rightText: text,
				rightLine: cur.right,
			})
			cur.left++
			cur.right++
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

// parseHunkStart pulls the starting "before" and "after" line numbers
// from a unified-diff hunk header. Inputs look like:
//
//	@@ -42,7 +42,8 @@
//	@@ -1 +1 @@                  (single-line hunks omit the count)
//	@@ -10,3 +10,5 @@ optional-func-context
//
// Returns (0, 0) when the header is malformed so the caller falls back
// to "no numbers" instead of incrementing from a wrong base.
func parseHunkStart(header string) (left, right int) {
	// strip leading @@ + space; we just need the two `-A,B` `+C,D` tokens
	rest := strings.TrimPrefix(header, "@@")
	rest = strings.TrimSpace(rest)
	fields := strings.Fields(rest)
	for _, f := range fields {
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case '-':
			left = parseHunkNumberToken(f[1:])
		case '+':
			right = parseHunkNumberToken(f[1:])
		}
		if left != 0 && right != 0 {
			return
		}
	}
	return
}

func parseHunkNumberToken(tok string) int {
	if i := strings.IndexByte(tok, ','); i >= 0 {
		tok = tok[:i]
	}
	n := 0
	for _, r := range tok {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
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
	// Determine the line-number column width once per render so cells
	// align. We size to the largest known number on either side; 0 means
	// "no numbers parsed" in which case the cell falls back to the old
	// gutter-only layout (lineCol=0 disables the prefix).
	maxLineNo := 0
	for _, r := range rows {
		if r.leftLine > maxLineNo {
			maxLineNo = r.leftLine
		}
		if r.rightLine > maxLineNo {
			maxLineNo = r.rightLine
		}
	}
	lineCol := 0
	if maxLineNo > 0 {
		lineCol = len(fmt.Sprintf("%d", maxLineNo))
	}

	// 4 chars of chrome per row: gutter (1) + space (1) per side, then
	// the ` │ ` divider in the middle (3). Split the rest evenly. When
	// line numbers are enabled, each side also reserves `lineCol + 1`
	// for the number + separator space.
	const dividerLit = " │ "
	chrome := len(dividerLit) + 4
	if lineCol > 0 {
		chrome += 2 * (lineCol + 1)
	}
	col := (width - chrome) / 2
	if col < 12 {
		col = 12
	}

	truncated := false
	if len(rows) > maxRows {
		rows = rows[:maxRows]
		truncated = true
	}

	out := make([]string, 0, len(rows)+1)
	out = append(out, renderDiffSideHeader(col+ifPositive(lineCol)+1, dividerLit))
	for _, r := range rows {
		left, right := renderDiffPairCells(r, col, lineCol)
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

// renderDiffPairCells routes a paired remove/add row through the
// word-diff highlighter (so the changed middle inside each cell pops)
// and falls back to the plain cell renderer for everything else
// (context rows, header/hunk markers, surplus add-only or remove-only
// rows where there is no peer to compare against). The two paths are
// kept structurally identical from the outside — both return the
// (left, right) cell strings already padded and line-number prefixed.
func renderDiffPairCells(r diffSideRow, contentCol, lineCol int) (string, string) {
	if r.leftKind == diffRemove && r.rightKind == diffAdd {
		left, right := renderDiffPairWordCells(r.leftText, r.rightText, contentCol)
		return wrapDiffCellWithLineNo(left, r.leftLine, lineCol, r.leftKind),
			wrapDiffCellWithLineNo(right, r.rightLine, lineCol, r.rightKind)
	}
	left := renderDiffCellWithLineNo(r.leftKind, r.leftText, r.leftLine, contentCol, lineCol)
	right := renderDiffCellWithLineNo(r.rightKind, r.rightText, r.rightLine, contentCol, lineCol)
	return left, right
}

// renderDiffPairWordCells paints a paired remove/add row with the
// changed middle highlighted on each side. The common prefix +
// suffix render in the standard remove/add foreground; the diverging
// middle gets bold + a dim bg so the eye lands on the actual change.
// Falls back to the plain remove/add cells when the two lines have
// no common bytes (e.g. wholly different rewrites) — the highlight
// would be redundant there since the whole line is already coloured.
func renderDiffPairWordCells(leftText, rightText string, col int) (string, string) {
	if col <= 0 {
		return "", ""
	}
	lp, rp := computeWordDiff(leftText, rightText)
	leftBase := lipgloss.NewStyle().Foreground(colorFail)
	rightBase := lipgloss.NewStyle().Foreground(colorOk)

	leftBody := applyWordDiffStyling(lp, leftBase, wordDiffBgRemove())
	rightBody := applyWordDiffStyling(rp, rightBase, wordDiffBgAdd())

	leftCell := composeDiffWordCell("-", leftBody, col, leftBase)
	rightCell := composeDiffWordCell("+", rightBody, col, rightBase)
	return leftCell, rightCell
}

// composeDiffWordCell wraps a pre-styled body in the gutter glyph +
// column padding the plain renderer applies. The body is already
// ANSI-styled so we cannot just %-format it through padRight (rune
// counting would treat escapes as content); instead we pad by the
// visible width using lipgloss.Width which strips ANSI for length.
func composeDiffWordCell(gutter, body string, col int, base lipgloss.Style) string {
	prefix := base.Render(gutter + " ")
	full := prefix + body
	pad := col - lipgloss.Width(full)
	if pad > 0 {
		full += strings.Repeat(" ", pad)
	}
	if w := lipgloss.Width(full); w > col {
		// Body overran (very long line); truncate with an ellipsis.
		// Trim 3 cells off the budget for the marker.
		trim := col - 3
		if trim < 0 {
			trim = 0
		}
		full = ansi_truncate(full, trim) + base.Render("...")
	}
	return full
}

// ansi_truncate cuts an ANSI-styled string to `width` visible cells.
// lipgloss has no public truncate helper, but its own Width function
// strips escapes for length. We walk byte-by-byte tracking whether
// we're inside an ESC sequence; only "outside" bytes count toward
// the visible budget. Cheap and good enough for our cell capping —
// not a full ANSI parser.
func ansi_truncate(s string, width int) string {
	if width <= 0 || lipgloss.Width(s) <= width {
		return s
	}
	var b strings.Builder
	seen := 0
	inEscape := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == 0x1b {
			inEscape = true
			b.WriteByte(c)
			continue
		}
		if inEscape {
			b.WriteByte(c)
			if (c >= '@' && c <= '~') || c == 'm' {
				inEscape = false
			}
			continue
		}
		if seen >= width {
			break
		}
		b.WriteByte(c)
		seen++
	}
	return b.String()
}

// wrapDiffCellWithLineNo is the line-number prefix wrapper used by
// renderDiffPairCells. Mirrors renderDiffCellWithLineNo's contract but
// takes an already-rendered cell body (so the word-diff path doesn't
// pay for re-rendering the whole cell from raw text just to slap on
// the number column).
func wrapDiffCellWithLineNo(body string, lineNo, lineCol int, kind diffRowKind) string {
	if lineCol <= 0 {
		return body
	}
	prefix := strings.Repeat(" ", lineCol+1)
	if lineNo > 0 && kind != diffEmpty && kind != diffHeader && kind != diffHunk {
		prefix = subtleStyle.Render(fmt.Sprintf("%*d ", lineCol, lineNo))
	}
	return prefix + body
}

// renderDiffCellWithLineNo prepends a subtle line number column (when
// lineCol > 0) before the existing gutter+content rendering. The
// number renders in subtleStyle so it sits visually behind the diff
// state colour. Zero lineNo (e.g. header/hunk rows that span both
// sides without a real source line) prints a blank-padded gap so
// the column stays aligned without claiming a fake number.
func renderDiffCellWithLineNo(kind diffRowKind, text string, lineNo, contentCol, lineCol int) string {
	body := renderDiffCell(kind, text, contentCol)
	if lineCol <= 0 {
		return body
	}
	prefix := strings.Repeat(" ", lineCol+1)
	if lineNo > 0 && kind != diffEmpty && kind != diffHeader && kind != diffHunk {
		prefix = subtleStyle.Render(fmt.Sprintf("%*d ", lineCol, lineNo))
	}
	return prefix + body
}

func ifPositive(n int) int {
	if n > 0 {
		return n
	}
	return 0
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
