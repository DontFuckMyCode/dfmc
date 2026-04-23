package theme

// markdown.go — markdown-lite inline + block rendering plus the helpers
// that classify table headers, separators, bullets, and header levels.
// Split out of render.go for size. Pure string transforms with no
// dependency on any view-model type.

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func RenderMarkdownLite(text string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	out := renderInlineTokens(text, "**", BoldStyle)
	out = renderInlineTokens(out, "`", CodeStyle)
	return out
}

func RenderMarkdownBlocks(text string) []string {
	if text == "" {
		return nil
	}
	rawLines := strings.Split(text, "\n")
	out := make([]string, 0, len(rawLines))
	inFence := false
	for i := 0; i < len(rawLines); i++ {
		line := rawLines[i]
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			marker := SubtleStyle.Render("  ╌╌╌ code ╌╌╌")
			if inFence {
				if lang := strings.TrimSpace(strings.TrimPrefix(trimmed, "```")); lang != "" {
					marker = SubtleStyle.Render("  ╌╌╌ " + lang + " ╌╌╌")
				}
			}
			out = append(out, marker)
			continue
		}
		if inFence {
			out = append(out, CodeStyle.Render("  │ "+line))
			continue
		}
		if IsTableHeader(line) && i+1 < len(rawLines) && IsTableSeparator(rawLines[i+1]) {
			consumed, rendered := renderMarkdownTable(rawLines[i:])
			out = append(out, rendered...)
			i += consumed - 1
			continue
		}
		if h := HeaderLevel(trimmed); h > 0 {
			label := strings.TrimSpace(trimmed[h:])
			out = append(out, BoldStyle.Render(AccentStyle.Render(strings.Repeat("#", h)+" "+label)))
			continue
		}
		if bullet, rest, ok := BulletLine(line); ok {
			out = append(out, AccentStyle.Render(bullet)+" "+RenderMarkdownLite(rest))
			continue
		}
		out = append(out, RenderMarkdownLite(line))
	}
	return out
}

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

func renderMarkdownTable(lines []string) (int, []string) {
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

	rendered := make([][]string, len(rows))
	visibleWidth := make([][]int, len(rows))
	colWidths := make([]int, 0, len(rows[0]))
	for ri, row := range rows {
		rendered[ri] = make([]string, len(row))
		visibleWidth[ri] = make([]int, len(row))
		for ci, cell := range row {
			styled := RenderMarkdownLite(cell)
			if ri == 0 {
				styled = BoldStyle.Render(AccentStyle.Render(styled))
			}
			w := lipgloss.Width(styled)
			rendered[ri][ci] = styled
			visibleWidth[ri][ci] = w
			if ci >= len(colWidths) {
				colWidths = append(colWidths, w)
				continue
			}
			if w > colWidths[ci] {
				colWidths[ci] = w
			}
		}
	}

	out := make([]string, 0, len(rows)+1)
	for ri := range rendered {
		parts := make([]string, 0, len(rendered[ri]))
		for ci, styled := range rendered[ri] {
			pad := 0
			if ci < len(colWidths) {
				pad = colWidths[ci] - visibleWidth[ri][ci]
			}
			padded := styled + strings.Repeat(" ", Max0(pad))
			parts = append(parts, padded)
		}
		joined := "  " + strings.Join(parts, SubtleStyle.Render("  │  "))
		out = append(out, joined)
		if ri == 0 {
			sepParts := make([]string, 0, len(colWidths))
			for _, w := range colWidths {
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

func HeaderLevel(trimmed string) int {
	switch {
	case strings.HasPrefix(trimmed, "### "):
		return 3
	case strings.HasPrefix(trimmed, "## "):
		return 2
	case strings.HasPrefix(trimmed, "# "):
		return 1
	}
	return 0
}

func BulletLine(line string) (bullet string, rest string, ok bool) {
	indent := 0
	for indent < len(line) && line[indent] == ' ' {
		indent++
	}
	body := line[indent:]
	if len(body) < 2 {
		return "", "", false
	}
	marker := body[0]
	switch marker {
	case '-', '*', '+':
		if body[1] != ' ' {
			return "", "", false
		}
		return strings.Repeat(" ", indent) + "•", strings.TrimSpace(body[2:]), true
	}
	digits := 0
	for digits < len(body) && body[digits] >= '0' && body[digits] <= '9' {
		digits++
	}
	if digits > 0 && digits < len(body)-1 && body[digits] == '.' && body[digits+1] == ' ' {
		return strings.Repeat(" ", indent) + body[:digits+1], strings.TrimSpace(body[digits+2:]), true
	}
	return "", "", false
}

func renderInlineTokens(text, delim string, style lipgloss.Style) string {
	if !strings.Contains(text, delim) {
		return text
	}
	var b strings.Builder
	i := 0
	for i < len(text) {
		idx := strings.Index(text[i:], delim)
		if idx < 0 {
			b.WriteString(text[i:])
			break
		}
		b.WriteString(text[i : i+idx])
		start := i + idx + len(delim)
		end := strings.Index(text[start:], delim)
		if end < 0 {
			b.WriteString(text[i+idx:])
			break
		}
		token := text[start : start+end]
		b.WriteString(style.Render(token))
		i = start + end + len(delim)
	}
	return b.String()
}
