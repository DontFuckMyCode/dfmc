// engine_analyze_complexity_body.go — function-body slicing helpers
// for the complexity scorer. Sibling of engine_analyze_complexity.go
// which keeps the public-ish surface (computeComplexity orchestrator,
// complexityScore McCabe-ish counter, complexityBranchRegexes
// compile-once cache, looksEntrypoint role-classifier).
//
// Splitting the body-walk helpers out keeps the orchestrator file
// scoped to "what does the analyze pass do" while this file owns
// the per-language end-of-body detection: brace-depth tracking
// (Go/C/JS/TS/Java) that respects string/rune/backtick literals and
// line/block comments, indent-based walk for Python, and the small
// shared primitives (skipStringLiteral, leadingWhitespaceLen).

package engine

import (
	"strings"
)

// endOfFunctionBody returns the (0-indexed) line AFTER the closing
// delimiter of the function that STARTS at `start`. For brace-based
// languages it tracks `{` / `}` depth while respecting strings, runes,
// and line/block comments. For Python (indent-based) it walks until a
// non-blank line's indentation drops to or below the function's own
// indent. If neither strategy finds a clean end, returns `len(lines)`
// so the caller still gets a sensible segment (whole rest of file).
func endOfFunctionBody(lines []string, start int, language string) int {
	if start < 0 || start >= len(lines) {
		return len(lines)
	}
	lang := strings.ToLower(strings.TrimSpace(language))
	if lang == "python" {
		return endOfPythonBody(lines, start)
	}
	return endOfBraceBody(lines, start)
}

// endOfBraceBody walks lines from `start`, counting balanced braces
// outside strings/comments. Stops one line past the line where depth
// returns to zero AFTER having been positive at least once. This is
// resilient to nested closures — the body of an outer function
// legitimately contains many `{}` pairs and only the outermost match
// closes it.
func endOfBraceBody(lines []string, start int) int {
	depth := 0
	opened := false
	inBlockComment := false
	for i := start; i < len(lines); i++ {
		line := lines[i]
		j := 0
		for j < len(line) {
			if inBlockComment {
				if j+1 < len(line) && line[j] == '*' && line[j+1] == '/' {
					inBlockComment = false
					j += 2
					continue
				}
				j++
				continue
			}
			// Line comment — rest of the line is not code.
			if j+1 < len(line) && line[j] == '/' && line[j+1] == '/' {
				break
			}
			// Block comment start.
			if j+1 < len(line) && line[j] == '/' && line[j+1] == '*' {
				inBlockComment = true
				j += 2
				continue
			}
			// Skip string / rune literals so their braces don't count.
			if c := line[j]; c == '"' || c == '\'' || c == '`' {
				j = skipStringLiteral(line, j)
				continue
			}
			if line[j] == '{' {
				depth++
				opened = true
			} else if line[j] == '}' {
				depth--
				if opened && depth <= 0 {
					return i + 1
				}
			}
			j++
		}
	}
	return len(lines)
}

// skipStringLiteral returns the index of the character AFTER the
// closing quote of a string/rune/backtick literal starting at
// `line[start]`. Respects escape sequences for "" and ”. Backtick
// (raw) strings don't honour escapes in Go. If the literal doesn't
// close on this line (multi-line raw strings), returns len(line).
func skipStringLiteral(line string, start int) int {
	if start >= len(line) {
		return start
	}
	quote := line[start]
	j := start + 1
	for j < len(line) {
		c := line[j]
		if quote != '`' && c == '\\' {
			j += 2
			continue
		}
		if c == quote {
			return j + 1
		}
		j++
	}
	return len(line)
}

// endOfPythonBody walks until a non-blank line whose indentation is
// ≤ the def's indentation. That line belongs to the enclosing scope,
// so the function ends the line before.
func endOfPythonBody(lines []string, start int) int {
	defIndent := leadingWhitespaceLen(lines[start])
	for i := start + 1; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			continue
		}
		if leadingWhitespaceLen(line) <= defIndent {
			return i
		}
	}
	return len(lines)
}

func leadingWhitespaceLen(s string) int {
	n := 0
	for n < len(s) && (s[n] == ' ' || s[n] == '\t') {
		n++
	}
	return n
}
