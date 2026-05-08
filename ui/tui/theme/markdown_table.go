package theme

// markdown_table.go — markdown table renderer plus the small
// classifiers that the block-rendering pass uses to decide whether
// a given line pair is a table at all. Sibling of markdown.go which
// keeps the inline-token renderer (RenderMarkdownLite), the
// block-rendering walk (RenderMarkdownBlocks), and the small
// shared classifiers (HeaderLevel for # / ## / ### detection +
// BulletLine for unordered/ordered list detection).
//
// Splitting the table side out keeps markdown.go scoped to "what
// does inline + block rendering look like for prose" while this
// file owns the column-width math, the per-cell wrap-into-multiple-
// lines logic, the box-drawing-aware separator detection, and the
// |-or-│ delimiter dispatch that lets the renderer accept both
// classic markdown pipes and box-drawing variants.

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
)

func tableDelim(line string) (rune, bool) {
	t := strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(t, "|") && strings.Count(t, "|") >= 3:
		return '|', true
	case strings.HasPrefix(t, "│") && strings.Count(t, "│") >= 3:
		return '│', true
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
		case '─', '┼', '┤', '├', '┬', '┴', '│', '|', '-', ' ':
			if r == '─' || r == '-' {
				hasDash = true
			}
		default:
			return false
		}
	}
	return hasDash
}

func RenderMarkdownTable(lines []string, width int) (int, []string) {
	if len(lines) < 2 {
		return 0, nil
	}
	delim, ok := tableDelim(lines[0])
	if !ok {
		return 0, nil
	}

	rows := make([][]string, 0, 8)
	consumed := 0
	headerParsed := false
	for i, line := range lines {
		if i == 1 {
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
		rows = append(rows, cells)
		consumed = i + 1
		if i == 0 {
			headerParsed = true
		}
	}
	if !headerParsed || len(rows) == 0 {
		return 0, nil
	}

	// Calculate raw column widths from content (in visual cells)
	rendered := make([][]string, len(rows))
	cellWidth := make([][]int, len(rows))
	colWidths := make([]int, 0, len(rows[0]))
	for ri, row := range rows {
		rendered[ri] = make([]string, len(row))
		cellWidth[ri] = make([]int, len(row))
		for ci, cell := range row {
			styled := RenderMarkdownLite(cell)
			if ri == 0 {
				styled = BoldStyle.Render(AccentStyle.Render(styled))
			}
			w := runewidth.StringWidth(styled)
			rendered[ri][ci] = styled
			cellWidth[ri][ci] = w
			if ci >= len(colWidths) {
				colWidths = append(colWidths, w)
				continue
			}
			if w > colWidths[ci] {
				colWidths[ci] = w
			}
		}
	}

	// Clamp column widths so the full table fits within terminal width
	numCols := len(colWidths)
	if numCols == 0 {
		return 0, nil
	}
	// "  │  " = 3 runes, plus leading "  " = 2 runes
	separatorBudget := 2 + 3*(numCols-1) // "  " + (numCols-1) * "│"
	minColWidth := 4
	available := width - separatorBudget
	if available < minColWidth*numCols {
		available = minColWidth * numCols
	}
	totalRaw := 0
	for _, cw := range colWidths {
		totalRaw += cw
	}
	scale := 1.0
	if totalRaw > available {
		scale = float64(available) / float64(totalRaw)
	}
	colW := make([]int, numCols)
	for ci, cw := range colWidths {
		scaled := int(float64(cw) * scale)
		if scaled < minColWidth {
			scaled = minColWidth
		}
		colW[ci] = scaled
	}

	// Wrap overflowing cells into multiple lines
	type cellLines []string
	cellContent := make([][]cellLines, len(rows))
	for ri := range rendered {
		cellContent[ri] = make([]cellLines, numCols)
		for ci := 0; ci < numCols; ci++ {
			cell := ""
			origW := 0
			if ci < len(rendered[ri]) {
				cell = rendered[ri][ci]
				origW = cellWidth[ri][ci]
			}
			limit := colW[ci]
			if origW > limit && limit > 0 {
				wrapped := ansi.Wrap(cell, limit, " 	,;:.!?/\\_-")
				parts := strings.Split(wrapped, "\n")
				cellContent[ri][ci] = parts
			} else {
				cellContent[ri][ci] = cellLines{cell}
			}
		}
	}

	// Compute max lines per column for vertical alignment
	maxLines := make([]int, numCols)
	for ri := range cellContent {
		for ci := range cellContent[ri] {
			if len(cellContent[ri][ci]) > maxLines[ci] {
				maxLines[ci] = len(cellContent[ri][ci])
			}
		}
	}

	sep := SubtleStyle.Render("  │  ")
	out := make([]string, 0, len(rows)+len(rows)*3) // rough over-alloc
	for ri := range rendered {
		rowOut := make([][]string, numCols)
		for ci := range rowOut {
			rowOut[ci] = make([]string, maxLines[ci])
			for li := range rowOut[ci] {
				parts := cellContent[ri][ci]
				if li < len(parts) {
					padded := parts[li] + strings.Repeat(" ", Max0(colW[ci]-runewidth.StringWidth(parts[li])))
					rowOut[ci][li] = padded
				} else {
					rowOut[ci][li] = strings.Repeat(" ", colW[ci])
				}
			}
		}
		totalRowLines := 1
		for _, cl := range rowOut {
			if len(cl) > totalRowLines {
				totalRowLines = len(cl)
			}
		}
		for lineIdx := 0; lineIdx < totalRowLines; lineIdx++ {
			parts := make([]string, 0, numCols)
			for ci := range rowOut {
				if lineIdx < len(rowOut[ci]) {
					parts = append(parts, rowOut[ci][lineIdx])
				} else {
					parts = append(parts, strings.Repeat(" ", colW[ci]))
				}
			}
			out = append(out, "  "+strings.Join(parts, sep))
		}
		if ri == 0 {
			sepParts := make([]string, 0, numCols)
			for _, w := range colW {
				sepParts = append(sepParts, strings.Repeat("─", w))
			}
			out = append(out, SubtleStyle.Render("  "+strings.Join(sepParts, "──┼──")))
		}
	}
	return consumed, out
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

func Max0(v int) int {
	if v < 0 {
		return 0
	}
	return v
}
