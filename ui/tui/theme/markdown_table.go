package theme

// markdown_table.go owns markdown table detection and rendering. Tables are
// drawn with box characters and are sized to the width supplied by the caller;
// long cell content wraps inside its cell instead of forcing the chat transcript
// into horizontal overflow.

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

const (
	boxH  = "\u2500"
	boxV  = "\u2502"
	boxTL = "\u250c"
	boxTR = "\u2510"
	boxBL = "\u2514"
	boxBR = "\u2518"
	boxLT = "\u251c"
	boxRT = "\u2524"
	boxTT = "\u252c"
	boxBT = "\u2534"
	boxX  = "\u253c"
)

type tableCellLines []string

func tableDelim(line string) (rune, bool) {
	t := strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(t, "|") && strings.Count(t, "|") >= 3:
		return '|', true
	case strings.HasPrefix(t, boxV) && strings.Count(t, boxV) >= 3:
		return []rune(boxV)[0], true
	}
	return 0, false
}

func IsTableHeader(line string) bool {
	_, ok := tableDelim(line)
	return ok
}

func IsTableSeparator(line string) bool {
	t := strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(t, "|"):
		return IsMarkdownSeparator(t)
	case ContainsBoxSeparator(t):
		return true
	}
	return false
}

func IsMarkdownSeparator(t string) bool {
	body := strings.Trim(t, "|")
	for _, cell := range strings.Split(body, "|") {
		c := strings.TrimSpace(cell)
		c = strings.TrimPrefix(c, ":")
		c = strings.TrimSuffix(c, ":")
		if c == "" || strings.Trim(c, "-") != "" {
			return false
		}
	}
	return true
}

func ContainsBoxSeparator(t string) bool {
	if t == "" {
		return false
	}
	hasDash := false
	for _, r := range t {
		switch r {
		case '\u2500', '\u253c', '\u2524', '\u251c', '\u252c', '\u2534', '\u2502', '|', '-', ' ':
			if r == '\u2500' || r == '-' {
				hasDash = true
			}
		default:
			return false
		}
	}
	return hasDash
}

// RenderMarkdownTable renders a markdown table as a bordered table. The
// separator row is consumed but not displayed as data; each content row can
// expand vertically when cells wrap.
func RenderMarkdownTable(lines []string, width int) (int, []string) {
	if len(lines) < 2 {
		return 0, nil
	}
	delim, ok := tableDelim(lines[0])
	if !ok {
		return 0, nil
	}

	// Walk header + body rows, skipping the separator line. Pipe-style
	// tables capture their separator with the same | delim, but box-
	// drawing tables use ─/┼/┤ glyphs for the separator so we cannot
	// rely on the delimiter prefix to find it — treat any line that
	// IsTableSeparator(...) accepts as a non-row pass-through.
	rawRows := make([][]string, 0, 8)
	consumed := 0
	headerSeen := false
	sawSeparator := false
	for i, line := range lines {
		if IsTableSeparator(line) {
			if !headerSeen {
				// Separator before any header is suspicious — bail.
				break
			}
			sawSeparator = true
			consumed = i + 1
			continue
		}
		if !strings.HasPrefix(strings.TrimSpace(line), string(delim)) {
			break
		}
		cells := SplitTableRow(line, delim)
		if len(cells) == 0 {
			break
		}
		rawRows = append(rawRows, cells)
		headerSeen = true
		consumed = i + 1
	}
	if !sawSeparator || len(rawRows) < 1 {
		return 0, nil
	}

	numCols := len(rawRows[0])
	if numCols == 0 {
		return 0, nil
	}
	rows := make([][]string, 0, len(rawRows))
	for _, row := range rawRows {
		rows = append(rows, normalizeTableRow(row, numCols))
	}

	colW := markdownTableColumnWidths(rows, numCols, width)
	cellContent := make([][]tableCellLines, len(rows))
	for ri, row := range rows {
		cellContent[ri] = make([]tableCellLines, numCols)
		for ci := 0; ci < numCols; ci++ {
			cell := RenderMarkdownLite(row[ci])
			if ri == 0 {
				cell = BoldStyle.Render(AccentStyle.Render(cell))
			}
			cellContent[ri][ci] = wrapTableCell(cell, colW[ci])
		}
	}

	out := []string{tableBorder(colW, boxTL, boxTT, boxTR)}
	out = append(out, tableContentRows(cellContent[0], colW)...)
	out = append(out, tableBorder(colW, boxLT, boxX, boxRT))
	for ri := 1; ri < len(cellContent); ri++ {
		out = append(out, tableContentRows(cellContent[ri], colW)...)
	}
	out = append(out, tableBorder(colW, boxBL, boxBT, boxBR))

	return consumed, out
}

func markdownTableColumnWidths(rows [][]string, numCols, width int) []int {
	borderAndPadding := numCols + 1 + numCols*2
	available := width - borderAndPadding
	if available < numCols {
		available = numCols
	}

	raw := make([]int, numCols)
	for ri, row := range rows {
		for ci, cell := range row {
			styled := RenderMarkdownLite(cell)
			if ri == 0 {
				styled = BoldStyle.Render(AccentStyle.Render(styled))
			}
			if w := ansi.StringWidth(styled); w > raw[ci] {
				raw[ci] = w
			}
		}
	}
	for ci := range raw {
		if raw[ci] < 1 {
			raw[ci] = 1
		}
	}

	totalRaw := 0
	for _, w := range raw {
		totalRaw += w
	}
	if totalRaw <= available {
		return raw
	}

	out := make([]int, numCols)
	assigned := 0
	for ci, w := range raw {
		scaled := int(float64(w) * float64(available) / float64(totalRaw))
		if scaled < 1 {
			scaled = 1
		}
		out[ci] = scaled
		assigned += scaled
	}
	for assigned > available {
		ci := widestColumn(out)
		if ci < 0 || out[ci] <= 1 {
			break
		}
		out[ci]--
		assigned--
	}
	for assigned < available {
		ci := widestRawGapColumn(raw, out)
		if ci < 0 {
			break
		}
		out[ci]++
		assigned++
	}
	return out
}

func widestColumn(widths []int) int {
	idx := -1
	for i, w := range widths {
		if w <= 1 {
			continue
		}
		if idx == -1 || w > widths[idx] {
			idx = i
		}
	}
	return idx
}

func widestRawGapColumn(raw, current []int) int {
	idx := -1
	for i := range raw {
		gap := raw[i] - current[i]
		if gap <= 0 {
			continue
		}
		if idx == -1 || gap > raw[idx]-current[idx] {
			idx = i
		}
	}
	return idx
}

func normalizeTableRow(row []string, numCols int) []string {
	out := make([]string, numCols)
	copy(out, row)
	return out
}

func tableBorder(colW []int, left, mid, right string) string {
	var b strings.Builder
	b.WriteString(SubtleStyle.Render(left))
	for ci, w := range colW {
		b.WriteString(strings.Repeat(boxH, w+2))
		if ci < len(colW)-1 {
			b.WriteString(SubtleStyle.Render(mid))
		}
	}
	b.WriteString(SubtleStyle.Render(right))
	return b.String()
}

func tableContentRows(row []tableCellLines, colW []int) []string {
	rowMax := 1
	for ci := range colW {
		if len(row[ci]) > rowMax {
			rowMax = len(row[ci])
		}
	}
	out := make([]string, 0, rowMax)
	for lineIdx := 0; lineIdx < rowMax; lineIdx++ {
		line := SubtleStyle.Render(boxV)
		for ci, width := range colW {
			parts := row[ci]
			cellStr := ""
			if lineIdx < len(parts) {
				cellStr = parts[lineIdx]
			}
			line += " " + cellStr + strings.Repeat(" ", max(0, width-ansi.StringWidth(cellStr))) + " "
			line += SubtleStyle.Render(boxV)
		}
		out = append(out, line)
	}
	return out
}

func wrapTableCell(cell string, limit int) tableCellLines {
	if limit < 1 {
		limit = 1
	}
	wrapped := ansi.Wrap(cell, limit, " \t,;:.!?/\\_-")
	if wrapped == "" {
		return tableCellLines{""}
	}
	parts := strings.Split(wrapped, "\n")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if ansi.StringWidth(part) <= limit {
			out = append(out, part)
			continue
		}
		out = append(out, forceHardWrap(part, limit)...)
	}
	if len(out) == 0 {
		out = append(out, "")
	}
	return out
}

// max returns the larger of two integers.
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Max0 returns the larger of 0 and n.
func Max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

func SplitTableRow(line string, delim rune) []string {
	t := strings.TrimSpace(line)
	ds := string(delim)
	if !strings.HasPrefix(t, ds) {
		return nil
	}
	t = strings.Trim(t, ds)
	cells := strings.Split(t, ds)
	out := make([]string, 0, len(cells))
	for _, c := range cells {
		out = append(out, strings.TrimSpace(c))
	}
	return out
}

// forceHardWrap breaks a string into chunks of at most limit cells wide,
// always fitting within the limit regardless of content.
func forceHardWrap(s string, limit int) []string {
	if limit <= 0 {
		return []string{s}
	}
	out := make([]string, 0, (len(s)/limit)+1)
	runes := []rune(s)
	for len(runes) > 0 {
		if ansi.StringWidth(string(runes)) <= limit {
			out = append(out, string(runes))
			break
		}
		n := 0
		for n < len(runes) {
			cw := ansi.StringWidth(string(runes[:n+1]))
			if cw > limit {
				break
			}
			n++
		}
		if n == 0 {
			n = 1
		}
		out = append(out, string(runes[:n]))
		runes = runes[n:]
	}
	return out
}
