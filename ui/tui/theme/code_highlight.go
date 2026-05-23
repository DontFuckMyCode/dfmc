package theme

// code_highlight.go — light, per-language colorization for fenced code
// blocks. Goal is "the eye finds the structure faster", not "Pygments
// in your terminal" — we touch only keywords, string literals, line
// comments, and unified-diff +/- markers. Single regex pass per line
// so the cost stays in the same league as plain CodeStyle.Render.
//
// Recognized languages: go, js/ts (tsx/jsx aliases), py/python, json,
// yaml, sh/bash, sql, diff. Anything else falls through to the plain
// CodeStyle render, exactly like before.
//
// Sibling: markdown.go's RenderMarkdownBlocks calls into us via
// RenderFencedCodeLine which prepends the `│ ` gutter and dispatches
// on fence language.

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Style accessors are functions (not vars) so they pick up palette
// changes if a theme retune ever swaps the underlying colour. They
// are intentionally derived from existing palette colours rather than
// introducing new hex literals (palette.go is the single hex sink).
func KeywordStyle() lipgloss.Style { return lipgloss.NewStyle().Foreground(ColorAccent) }
func StringStyle() lipgloss.Style  { return lipgloss.NewStyle().Foreground(ColorOk) }
func NumberStyle() lipgloss.Style  { return lipgloss.NewStyle().Foreground(ColorWarn) }

// RenderFencedCodeLine is the entry point used by the markdown renderer
// for a single line inside a ```fence```. It adds the standard `│ `
// gutter, then colorizes based on `lang`. The full raw line (no left
// trim) is preserved so indentation reads correctly.
func RenderFencedCodeLine(line, lang string) string {
	gutter := CodeStyle.Render("  │ ")
	if lang == "diff" || isLikelyDiffLine(line) {
		return gutter + renderDiffLine(line)
	}
	body := highlightCodeLine(line, lang)
	if body == "" {
		body = CodeStyle.Render(line)
	}
	return gutter + body
}

func isLikelyDiffLine(line string) bool {
	if len(line) == 0 {
		return false
	}
	switch line[0] {
	case '+', '-':
		// "+++ " / "--- " are the file headers, "+x" / "-y" are content.
		// Both deserve coloring. A leading space is ambiguous so skip.
		return true
	case '@':
		return strings.HasPrefix(line, "@@")
	}
	return false
}

func renderDiffLine(line string) string {
	switch {
	case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---"):
		return SubtleStyle.Bold(true).Render(line)
	case strings.HasPrefix(line, "@@"):
		return InfoStyle.Render(line)
	case strings.HasPrefix(line, "+"):
		return OkStyle.Render(line)
	case strings.HasPrefix(line, "-"):
		return FailStyle.Render(line)
	}
	return CodeStyle.Render(line)
}

// highlightCodeLine returns the styled line, or "" if no rules apply
// (caller will fall back to plain CodeStyle). Highlights are applied
// in a non-overlapping pass: comments shield the rest of the line,
// strings shield keywords inside them, keywords + numbers come last.
func highlightCodeLine(line, lang string) string {
	kw, commentPrefixes, stringDelims := codeRules(lang)
	if len(kw) == 0 && len(commentPrefixes) == 0 && len(stringDelims) == 0 {
		return ""
	}

	// Comment shield: if a comment starts mid-line and we are not
	// inside a string, paint the suffix subtle and stop.
	if idx := findCommentStart(line, commentPrefixes, stringDelims); idx >= 0 {
		prefix := highlightCodeBody(line[:idx], kw, stringDelims)
		comment := SubtleStyle.Italic(true).Render(line[idx:])
		return prefix + comment
	}
	return highlightCodeBody(line, kw, stringDelims)
}

func highlightCodeBody(line string, kw map[string]struct{}, stringDelims []byte) string {
	if line == "" {
		return ""
	}
	// First pass: locate string literals and replace them with sentinels
	// so the keyword regex doesn't see their interior.
	type span struct{ start, end int }
	var spans []span
	if len(stringDelims) > 0 {
		i := 0
		for i < len(line) {
			c := line[i]
			if !isStringDelim(c, stringDelims) {
				i++
				continue
			}
			// find matching closing delim, honour simple backslash escapes
			j := i + 1
			for j < len(line) {
				if line[j] == '\\' && j+1 < len(line) {
					j += 2
					continue
				}
				if line[j] == c {
					j++
					break
				}
				j++
			}
			spans = append(spans, span{i, j})
			i = j
		}
	}

	// Build output by walking line, swapping strings + keywords.
	out := strings.Builder{}
	cur := 0
	for _, s := range spans {
		if s.start > cur {
			out.WriteString(applyKeywordsAndNumbers(line[cur:s.start], kw))
		}
		out.WriteString(StringStyle().Render(line[s.start:s.end]))
		cur = s.end
	}
	if cur < len(line) {
		out.WriteString(applyKeywordsAndNumbers(line[cur:], kw))
	}
	return out.String()
}

var (
	wordRe   = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
	numberRe = regexp.MustCompile(`\b[0-9]+(?:\.[0-9]+)?\b`)
)

func applyKeywordsAndNumbers(seg string, kw map[string]struct{}) string {
	if seg == "" {
		return seg
	}
	// Replace numbers first (does not overlap with keywords by definition).
	seg = numberRe.ReplaceAllStringFunc(seg, func(m string) string {
		return NumberStyle().Render(m)
	})
	if len(kw) == 0 {
		return CodeStyle.Render(seg)
	}
	// Replace keywords. Wrap non-keyword runs in CodeStyle so the line
	// still gets the muted base color.
	out := strings.Builder{}
	last := 0
	for _, m := range wordRe.FindAllStringIndex(seg, -1) {
		if m[0] > last {
			out.WriteString(CodeStyle.Render(seg[last:m[0]]))
		}
		word := seg[m[0]:m[1]]
		if _, ok := kw[word]; ok {
			out.WriteString(KeywordStyle().Render(word))
		} else {
			out.WriteString(CodeStyle.Render(word))
		}
		last = m[1]
	}
	if last < len(seg) {
		out.WriteString(CodeStyle.Render(seg[last:]))
	}
	return out.String()
}

func isStringDelim(c byte, delims []byte) bool {
	for _, d := range delims {
		if c == d {
			return true
		}
	}
	return false
}

// findCommentStart returns the byte index where a line comment begins,
// or -1. Skips over string literals so `"// not a comment"` stays a
// string. Block comments are intentionally ignored — too rare on a
// single rendered line to be worth the state machine.
func findCommentStart(line string, prefixes [][]byte, delims []byte) int {
	if len(prefixes) == 0 {
		return -1
	}
	i := 0
	for i < len(line) {
		c := line[i]
		if isStringDelim(c, delims) {
			j := i + 1
			for j < len(line) {
				if line[j] == '\\' && j+1 < len(line) {
					j += 2
					continue
				}
				if line[j] == c {
					j++
					break
				}
				j++
			}
			i = j
			continue
		}
		for _, p := range prefixes {
			if i+len(p) <= len(line) && bytesEqual(line[i:i+len(p)], p) {
				return i
			}
		}
		i++
	}
	return -1
}

func bytesEqual(s string, b []byte) bool {
	if len(s) != len(b) {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] != b[i] {
			return false
		}
	}
	return true
}

// codeRules returns the keyword set, the line-comment markers, and the
// string-literal delimiters for the given language. Empty values mean
// "skip this rule".
func codeRules(lang string) (map[string]struct{}, [][]byte, []byte) {
	switch strings.ToLower(lang) {
	case "go", "golang":
		return goKeywords, [][]byte{[]byte("//")}, []byte{'"', '`'}
	case "js", "javascript", "jsx", "ts", "typescript", "tsx":
		return jsKeywords, [][]byte{[]byte("//")}, []byte{'"', '\'', '`'}
	case "py", "python":
		return pyKeywords, [][]byte{[]byte("#")}, []byte{'"', '\''}
	case "json":
		return nil, nil, []byte{'"'}
	case "yaml", "yml":
		return nil, [][]byte{[]byte("#")}, []byte{'"', '\''}
	case "sh", "bash", "shell", "zsh":
		return shKeywords, [][]byte{[]byte("#")}, []byte{'"', '\''}
	case "sql":
		return sqlKeywords, [][]byte{[]byte("--")}, []byte{'\''}
	case "rust", "rs":
		return rsKeywords, [][]byte{[]byte("//")}, []byte{'"'}
	}
	return nil, nil, nil
}

func toSet(words ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(words))
	for _, w := range words {
		out[w] = struct{}{}
	}
	return out
}

var (
	goKeywords = toSet(
		"break", "case", "chan", "const", "continue", "default", "defer",
		"else", "fallthrough", "for", "func", "go", "goto", "if", "import",
		"interface", "map", "package", "range", "return", "select", "struct",
		"switch", "type", "var", "nil", "true", "false", "iota",
	)
	jsKeywords = toSet(
		"async", "await", "break", "case", "catch", "class", "const",
		"continue", "debugger", "default", "delete", "do", "else", "export",
		"extends", "finally", "for", "from", "function", "if", "import",
		"in", "instanceof", "let", "new", "null", "of", "return", "static",
		"super", "switch", "this", "throw", "try", "typeof", "undefined",
		"var", "void", "while", "yield", "true", "false",
		"interface", "type", "enum", "implements", "readonly", "public",
		"private", "protected", "as", "satisfies", "namespace", "declare",
	)
	pyKeywords = toSet(
		"and", "as", "assert", "async", "await", "break", "class", "continue",
		"def", "del", "elif", "else", "except", "False", "finally", "for",
		"from", "global", "if", "import", "in", "is", "lambda", "None",
		"nonlocal", "not", "or", "pass", "raise", "return", "True", "try",
		"while", "with", "yield",
	)
	shKeywords = toSet(
		"if", "then", "else", "elif", "fi", "for", "in", "do", "done",
		"while", "until", "case", "esac", "function", "return", "exit",
		"break", "continue", "local", "export", "readonly", "declare",
	)
	sqlKeywords = toSet(
		"SELECT", "FROM", "WHERE", "INSERT", "INTO", "VALUES", "UPDATE",
		"SET", "DELETE", "JOIN", "INNER", "LEFT", "RIGHT", "OUTER", "ON",
		"GROUP", "BY", "ORDER", "LIMIT", "OFFSET", "AS", "AND", "OR",
		"NOT", "NULL", "IS", "IN", "LIKE", "CREATE", "TABLE", "INDEX",
		"DROP", "ALTER", "ADD", "PRIMARY", "KEY", "FOREIGN", "REFERENCES",
		"DISTINCT", "HAVING", "UNION", "ALL", "CASE", "WHEN", "THEN", "END",
		"select", "from", "where", "insert", "into", "values", "update",
		"set", "delete", "join", "inner", "left", "right", "outer", "on",
		"group", "by", "order", "limit", "offset", "as", "and", "or",
		"not", "null", "is", "in", "like", "create", "table", "index",
		"drop", "alter", "add", "primary", "key", "foreign", "references",
		"distinct", "having", "union", "all", "case", "when", "then", "end",
	)
	rsKeywords = toSet(
		"as", "async", "await", "break", "const", "continue", "crate",
		"dyn", "else", "enum", "extern", "false", "fn", "for", "if",
		"impl", "in", "let", "loop", "match", "mod", "move", "mut", "pub",
		"ref", "return", "self", "Self", "static", "struct", "super",
		"trait", "true", "type", "unsafe", "use", "where", "while", "Box",
		"Vec", "String", "Option", "Result", "None", "Some", "Ok", "Err",
	)
)
