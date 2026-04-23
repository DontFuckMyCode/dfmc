// find_symbol_scope.go — per-language scope walkers used by find_symbol
// to turn an AST-reported start line into a (start, end) span covering
// the symbol's full body. Five helpers, one dispatcher:
//
//   - extractScopeEnd: picks a strategy by language name; everything not
//     python/yaml/ruby/shell falls back to the brace walker.
//   - extractByBraces: best-effort `{`/`}` depth tracker with string and
//     comment state; stops at the line that closes the first scope.
//   - extractByIndent: Python/YAML/shell-style walker that stops at the
//     first non-empty line with indent ≤ the header's.
//   - leadingIndent: tab-aware column count for the indent walkers.
//   - extractRubyScope: walks until the matching `end` keyword at header
//     indent.
//
// All callers live in find_symbol.go (buildScopeMatch + Execute's HTML
// branch goes through find_symbol_html.go's own walker). Keeping these
// here keeps the main file focused on the AST-driven discovery path.

package tools

import "strings"

// extractScopeEnd picks a per-language scope strategy. Falls back to
// brace-balanced (most C-family languages) when the language is unknown.
func extractScopeEnd(language string, lines []string, startLine int) int {
	switch language {
	case "python", "yaml":
		return extractByIndent(lines, startLine)
	case "ruby":
		return extractRubyScope(lines, startLine)
	case "bash", "shell":
		return extractByIndent(lines, startLine)
	default:
		return extractByBraces(lines, startLine)
	}
}

// extractByBraces walks forward from startLine counting `{` vs `}`,
// best-effort skipping over chars inside string literals and comments.
// Returns the line that closes the first scope opened at-or-after
// startLine; returns startLine when no `{` is found within a reasonable
// look-ahead (3000 lines) so we don't run away on a malformed file.
func extractByBraces(lines []string, startLine int) int {
	depth := 0
	opened := false
	const maxLookahead = 3000
	limit := startLine + maxLookahead
	if limit > len(lines) {
		limit = len(lines)
	}
	inBlockComment := false
	for i := startLine - 1; i < limit; i++ {
		line := lines[i]
		j := 0
		inString := byte(0) // 0 = not in string, '"' / '\'' / '`' = in string of that quote
		for j < len(line) {
			c := line[j]
			if inBlockComment {
				if c == '*' && j+1 < len(line) && line[j+1] == '/' {
					inBlockComment = false
					j += 2
					continue
				}
				j++
				continue
			}
			if inString != 0 {
				if c == '\\' && j+1 < len(line) {
					j += 2
					continue
				}
				if c == inString {
					inString = 0
				}
				j++
				continue
			}
			// Not in a string or block comment.
			if c == '/' && j+1 < len(line) {
				if line[j+1] == '/' {
					break // line comment — skip rest of line
				}
				if line[j+1] == '*' {
					inBlockComment = true
					j += 2
					continue
				}
			}
			if c == '"' || c == '\'' || c == '`' {
				inString = c
				j++
				continue
			}
			if c == '{' {
				depth++
				opened = true
			} else if c == '}' {
				depth--
				if opened && depth <= 0 {
					return i + 1
				}
			}
			j++
		}
	}
	// No closing brace found within look-ahead — return the start line so
	// the caller doesn't dump 3000 lines of unrelated code.
	if !opened {
		return startLine
	}
	return limit
}

// extractByIndent walks forward from startLine; the scope ends at the
// first non-empty line whose indent is ≤ the header's indent. Used for
// Python / YAML / shell heredocs / similar.
func extractByIndent(lines []string, startLine int) int {
	if startLine < 1 || startLine > len(lines) {
		return startLine
	}
	headerIndent := leadingIndent(lines[startLine-1])
	const maxLookahead = 3000
	limit := startLine + maxLookahead
	if limit > len(lines) {
		limit = len(lines)
	}
	end := startLine
	for i := startLine; i < limit; i++ { // i is 0-based line just AFTER the header
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			end = i + 1
			continue
		}
		if leadingIndent(line) <= headerIndent {
			break
		}
		end = i + 1
	}
	return end
}

func leadingIndent(s string) int {
	n := 0
	for _, r := range s {
		switch r {
		case ' ':
			n++
		case '\t':
			n += 4 // count a tab as 4 columns for comparison purposes
		default:
			return n
		}
	}
	return n
}

// extractRubyScope walks until the matching `end` keyword at the
// header's indent. Best-effort — doesn't track strings/heredocs.
func extractRubyScope(lines []string, startLine int) int {
	if startLine < 1 || startLine > len(lines) {
		return startLine
	}
	headerIndent := leadingIndent(lines[startLine-1])
	const maxLookahead = 3000
	limit := startLine + maxLookahead
	if limit > len(lines) {
		limit = len(lines)
	}
	for i := startLine; i < limit; i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "end" && leadingIndent(line) == headerIndent {
			return i + 1
		}
	}
	return startLine
}
