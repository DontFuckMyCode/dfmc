package theme

// markdown.go — markdown-lite inline + block rendering: the
// RenderMarkdownLite per-token style pass (bold + code), the
// RenderMarkdownBlocks block walker (code-fence + headers +
// bullets + table dispatch + per-line inline render), and the
// HeaderLevel + BulletLine + renderInlineTokens primitives.
// Sibling: markdown_table.go owns the table renderer + the
// |/│ delimiter classifiers + Max0 padding helper.

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

func RenderMarkdownBlocks(text string, width int) []string {
	if text == "" {
		return nil
	}
	if width <= 0 {
		width = 120
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
			consumed, rendered := RenderMarkdownTable(rawLines[i:], width)
			// consumed=0 means the table renderer refused (e.g. fewer
			// than 2 delimited rows, or a separator built of non-delim
			// glyphs). Fall through to the plain-line render below
			// instead of looping forever on the same index.
			if consumed > 0 {
				out = append(out, rendered...)
				i += consumed - 1
				continue
			}
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

// tableDelim + IsTableHeader + IsTableSeparator + IsMarkdownSeparator
// + ContainsBoxSeparator + RenderMarkdownTable + SplitTableRow + Max0
// live in markdown_table.go.

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
