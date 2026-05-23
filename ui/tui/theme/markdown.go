package theme

// markdown.go — markdown-lite inline + block rendering: the
// RenderMarkdownLite per-token style pass (bold + code), the
// RenderMarkdownBlocks block walker (code-fence + headers +
// bullets + table dispatch + per-line inline render), and the
// HeaderLevel + BulletLine + renderInlineTokens primitives.
// Sibling: markdown_table.go owns the table renderer + the
// |/│ delimiter classifiers + Max0 padding helper.

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func RenderMarkdownLite(text string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	out := renderInlineLinks(text)
	out = renderInlineTokens(out, "**", BoldStyle)
	out = renderInlineTokens(out, "`", CodeStyle)
	out = linkifyURLs(out)
	return out
}

// inlineLinkRe matches the CommonMark inline link shape `[label](url)`
// where label cannot contain `]` and url cannot contain whitespace
// or closing parens. Title attributes (`(url "title")`) and reference
// links (`[label][1]`) are intentionally not supported — the goal is
// "make the most common link form readable", not full CommonMark.
var inlineLinkRe = regexp.MustCompile(`\[([^\]]+)\]\(([^\s\)]+)\)`)

// renderInlineLinks rewrites `[label](url)` to a styled label followed
// by a subtle `(url)` suffix when the URL adds info the label doesn't
// already contain. When label == url we drop the redundant suffix so
// links like `[https://x.test](https://x.test)` don't double-render.
func renderInlineLinks(text string) string {
	if !strings.Contains(text, "](") {
		return text
	}
	return inlineLinkRe.ReplaceAllStringFunc(text, func(m string) string {
		match := inlineLinkRe.FindStringSubmatch(m)
		if len(match) != 3 {
			return m
		}
		label, url := match[1], match[2]
		styled := LinkStyle().Render(label)
		if strings.TrimSpace(label) == strings.TrimSpace(url) {
			return styled
		}
		return styled + SubtleStyle.Render(" ("+url+")")
	})
}

// urlRe matches plain http(s):// URLs and file:// paths that occur
// inline in chat content. We intentionally keep this conservative —
// no www. fallback, no autolinker for bare domains — so prose like
// "see foo.go for context" doesn't become an underlined non-link.
// Trailing punctuation (period, comma, paren, semicolon) is stripped
// from the styled span so a sentence-ending URL doesn't drag the
// terminator into the underline.
var urlRe = regexp.MustCompile(`https?://[^\s<>"'\)\]\}]+|file://[^\s<>"'\)\]\}]+`)

func linkifyURLs(text string) string {
	if !strings.Contains(text, "://") {
		return text
	}
	return urlRe.ReplaceAllStringFunc(text, func(m string) string {
		trailing := ""
		for len(m) > 0 {
			last := m[len(m)-1]
			if last == '.' || last == ',' || last == ';' || last == ':' || last == ')' || last == ']' || last == '}' {
				trailing = string(last) + trailing
				m = m[:len(m)-1]
				continue
			}
			break
		}
		if m == "" {
			return trailing
		}
		return LinkStyle().Render(m) + trailing
	})
}

// LinkStyle styles inline URLs. Derived from existing palette colours
// (no new hex literal — palette.go remains the single sink) and adds
// underline so terminals that support OSC 8 hyperlinks render the
// expected affordance even when the renderer can't emit them.
func LinkStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(ColorInfo).Underline(true)
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
	fenceLang := ""
	for i := 0; i < len(rawLines); i++ {
		line := rawLines[i]
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			marker := SubtleStyle.Render("  ╌╌╌ code ╌╌╌")
			if inFence {
				fenceLang = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(trimmed, "```")))
				if fenceLang != "" {
					marker = SubtleStyle.Render("  ╌╌╌ " + fenceLang + " ╌╌╌")
				}
			} else {
				fenceLang = ""
			}
			out = append(out, marker)
			continue
		}
		if inFence {
			out = append(out, RenderFencedCodeLine(line, fenceLang))
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
		if quote, ok := blockquoteBody(line); ok {
			bar := AccentStyle.Render("▎")
			out = append(out, bar+" "+SubtleStyle.Italic(true).Render(RenderMarkdownLite(quote)))
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

// blockquoteBody matches `> body` (markdown blockquote, one level deep)
// and returns the body text without the leading marker. Nested quotes
// (`>> body`) and lazy continuation lines (no marker but visually
// inside a quote) are intentionally not supported — the rendered
// signal is "this line is quoted", not full CommonMark fidelity.
func blockquoteBody(line string) (string, bool) {
	indent := 0
	for indent < len(line) && line[indent] == ' ' {
		indent++
	}
	body := line[indent:]
	if !strings.HasPrefix(body, ">") {
		return "", false
	}
	// Distinguish `>foo` (quote) from `>>` (skip nested quotes for now)
	// and ensure single `>` is followed by space-or-EOL.
	rest := body[1:]
	if strings.HasPrefix(rest, ">") {
		return "", false
	}
	rest = strings.TrimPrefix(rest, " ")
	return rest, true
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
