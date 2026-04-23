// find_symbol_html.go — the HTML/XML/JSX-template branch of find_symbol.
// find_symbol's AST engines don't speak HTML, so when kind is "tag",
// "html_id", "html_class" (or unspecified for an HTML file) the tool
// dispatches into findInHTML here. Everything in this file is a pure
// line-scan over the file body — no AST, no engine state — and nothing
// outside find_symbol.go calls it. Keeping it in a sibling makes the
// "HTML fallback" branch easy to find and keeps the main file focused
// on the AST-driven path.

package tools

import (
	"os"
	"strings"
)

// findInHTML scans an HTML/XML/template file for the named id, class,
// or tag and returns the balanced tag block(s). Multiple matches are
// possible (e.g. multiple elements with the same class).
func findInHTML(path, name, kind, mode string, bodyMax int, includeBody bool) []symbolMatch {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(string(data), "\n")

	wantTag := kind == "tag"
	wantID := kind == "html_id" || kind == ""
	wantClass := kind == "html_class" || kind == ""

	out := []symbolMatch{}
	for i, line := range lines {
		startLine := i + 1
		var hit struct {
			matched bool
			tagName string
			kind    string
		}
		if wantTag && containsHTMLTagOpener(line, name) {
			hit.matched = true
			hit.tagName = name
			hit.kind = "tag"
		} else if wantID {
			if attrValueMatches(line, "id", name, mode) {
				hit.matched = true
				hit.tagName = htmlOpeningTagName(line)
				hit.kind = "html_id"
			}
		}
		if !hit.matched && wantClass {
			if attrValueMatches(line, "class", name, mode) {
				hit.matched = true
				hit.tagName = htmlOpeningTagName(line)
				hit.kind = "html_class"
			}
		}
		if !hit.matched {
			continue
		}
		endLine := extractHTMLTagBlock(lines, startLine, hit.tagName)
		body := ""
		truncated := false
		if includeBody {
			body, truncated = sliceBody(lines, startLine, endLine, bodyMax)
		} else if endLine-startLine+1 > bodyMax {
			truncated = true
		}
		out = append(out, symbolMatch{
			Path:      path,
			Language:  "html",
			Name:      name,
			Kind:      hit.kind,
			StartLine: startLine,
			EndLine:   endLine,
			Body:      body,
			Truncated: truncated,
		})
	}
	return out
}

// containsHTMLTagOpener reports whether `line` opens a `<tag` (not the
// closing `</tag` and not a substring of an unrelated word).
func containsHTMLTagOpener(line, tag string) bool {
	if tag == "" {
		return false
	}
	needle := "<" + strings.ToLower(tag)
	low := strings.ToLower(line)
	idx := 0
	for {
		pos := strings.Index(low[idx:], needle)
		if pos < 0 {
			return false
		}
		pos += idx
		end := pos + len(needle)
		if end >= len(low) {
			return false
		}
		next := low[end]
		// Followed by whitespace, '>', or '/' → real opener. Followed
		// by another letter (e.g. `<header` for tag=`head`) → keep
		// looking.
		if next == ' ' || next == '\t' || next == '>' || next == '/' || next == '\n' || next == '\r' {
			return true
		}
		idx = end
	}
}

// attrValueMatches checks whether `line` carries `attr="value"` (or
// single-quoted) where value matches `name` per `mode`. For class
// attrs the value is split on whitespace before comparison so
// class="foo bar" matches name="bar".
func attrValueMatches(line, attr, name, mode string) bool {
	low := strings.ToLower(line)
	wantAttr := strings.ToLower(attr) + "="
	pos := strings.Index(low, wantAttr)
	if pos < 0 {
		return false
	}
	rest := line[pos+len(wantAttr):]
	if len(rest) == 0 {
		return false
	}
	quote := rest[0]
	if quote != '"' && quote != '\'' {
		return false
	}
	end := strings.IndexByte(rest[1:], quote)
	if end < 0 {
		return false
	}
	value := rest[1 : 1+end]
	if attr == "class" {
		for _, tok := range strings.Fields(value) {
			if nameMatches(tok, name, mode) {
				return true
			}
		}
		return false
	}
	return nameMatches(value, name, mode)
}

// htmlOpeningTagName returns the tag name from the first `<TAG` opener
// on the line. Returns "" when no opener is found (rare for a hit; the
// scope walker falls back to a minimal 1-line span).
func htmlOpeningTagName(line string) string {
	idx := strings.Index(line, "<")
	if idx < 0 {
		return ""
	}
	rest := line[idx+1:]
	if len(rest) == 0 || rest[0] == '/' || rest[0] == '!' {
		return ""
	}
	end := 0
	for end < len(rest) {
		c := rest[end]
		if c == ' ' || c == '\t' || c == '>' || c == '/' || c == '\n' || c == '\r' {
			break
		}
		end++
	}
	return strings.ToLower(rest[:end])
}

// extractHTMLTagBlock walks forward from startLine maintaining a tag
// stack; returns the line that closes the opening tag. Self-closing
// tags (`<br/>`, `<img ... />`) and void elements (`<input ...>`)
// resolve to startLine. Best-effort — doesn't parse CDATA or scripts.
func extractHTMLTagBlock(lines []string, startLine int, tag string) int {
	if tag == "" || startLine < 1 || startLine > len(lines) {
		return startLine
	}
	const maxLookahead = 3000
	limit := startLine + maxLookahead
	if limit > len(lines) {
		limit = len(lines)
	}
	voidTags := map[string]bool{
		"area": true, "base": true, "br": true, "col": true, "embed": true,
		"hr": true, "img": true, "input": true, "link": true, "meta": true,
		"param": true, "source": true, "track": true, "wbr": true,
	}
	if voidTags[tag] {
		return startLine
	}
	opener := "<" + tag
	closer := "</" + tag
	depth := 0
	openedSeen := false
	for i := startLine - 1; i < limit; i++ {
		low := strings.ToLower(lines[i])
		for {
			oIdx := strings.Index(low, opener)
			cIdx := strings.Index(low, closer)
			if oIdx < 0 && cIdx < 0 {
				break
			}
			// Check if this is a self-closing opener — in that case it
			// doesn't increment the stack.
			if oIdx >= 0 && (cIdx < 0 || oIdx < cIdx) {
				end := oIdx + len(opener)
				gt := strings.Index(low[end:], ">")
				if gt < 0 {
					// Tag opener spans lines; treat as a real open and
					// move on.
					depth++
					openedSeen = true
					low = low[end:]
					continue
				}
				selfClose := gt > 0 && low[end+gt-1] == '/'
				if !selfClose {
					depth++
					openedSeen = true
				}
				low = low[end+gt+1:]
				continue
			}
			// Closer comes first.
			depth--
			if openedSeen && depth <= 0 {
				return i + 1
			}
			low = low[cIdx+len(closer):]
		}
	}
	if !openedSeen {
		return startLine
	}
	return limit
}
