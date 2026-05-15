// astscan_helpers.go — argument-list classifiers, concat/format
// sniffers, comment-line skip, and the small line-ring buffer used
// by the per-language smart-scan rules. Sibling of astscan.go which
// keeps the rule type, the dispatcher (ScanASTRules + astRulesForLang
// + allASTRules), and the language-from-path detector.
//
// Splitting these helpers out keeps astscan.go scoped to "what is
// the rule shape and how do we run a rule pass" while this file
// owns the language-agnostic primitives every per-language rule
// reaches for: "are all this call's arguments literals" (the false-
// positive guard), "is this line concatenating or formatting a
// string" (the real exploit smell), "is this a bare call to a
// destructured sink", "is this a comment line we should skip", and
// the 3-line ring buffer that gives the rule context across short
// multi-line constructs.

package security

import "strings"

// argumentListAllLiterals returns true when every comma-separated
// argument inside the first `(...)` of `callText` is a string
// literal, numeric literal, or named constant pattern that cannot
// carry user input. Whitespace-only and empty arg lists also return
// true (nothing to inject). Returns false when at least one argument
// is an identifier / expression / concatenation.
//
// This is the guard that fixes the false-positive problem: a call
// like `exec.Command("git", "-C", "/path", "diff", "--")` has all
// literal args and is NOT an injection.
func argumentListAllLiterals(callText string) bool {
	start := strings.Index(callText, "(")
	if start < 0 {
		return false
	}
	// Find the matching close-paren, respecting nested parens and
	// quoted strings. A crude hand-walk is plenty for single-line
	// detection; multi-line function calls fall through and get
	// handled by the concat detector elsewhere.
	depth := 0
	inString := false
	var quote rune
	end := -1
	for i, r := range callText[start:] {
		switch {
		case inString:
			if r == quote && callText[start+i-1] != '\\' {
				inString = false
			}
		case r == '"' || r == '\'' || r == '`':
			inString = true
			quote = r
		case r == '(':
			depth++
		case r == ')':
			depth--
			if depth == 0 {
				end = start + i
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		return false
	}
	inner := strings.TrimSpace(callText[start+1 : end])
	if inner == "" {
		return true
	}
	args := splitArgs(inner)
	for _, a := range args {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		if !isLiteralArg(a) {
			return false
		}
	}
	return true
}

// splitArgs splits a comma-separated argument list, respecting
// strings and nested parens / brackets / braces so a literal like
// `"foo, bar"` or `[]string{"a", "b"}` stays together. Not a real
// parser but handles the arg-list shapes found in practice.
func splitArgs(s string) []string {
	var out []string
	depth := 0
	inString := false
	var quote byte
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inString:
			if c == quote && (i == 0 || s[i-1] != '\\') {
				inString = false
			}
		case c == '"' || c == '\'' || c == '`':
			inString = true
			quote = c
		case c == '(' || c == '[' || c == '{':
			depth++
		case c == ')' || c == ']' || c == '}':
			depth--
		case c == ',' && depth == 0:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// isLiteralArg reports whether `a` is a "safe" argument: string
// literal, numeric literal, boolean, nil/null/None, or a `...` slice
// spread of a literal slice. Identifiers (variable names) return
// false so they don't accidentally pass the all-literals gate.
func isLiteralArg(a string) bool {
	a = strings.TrimSpace(a)
	if a == "" {
		return true
	}
	// String literals in either quote style.
	if (strings.HasPrefix(a, "\"") && strings.HasSuffix(a, "\"")) ||
		(strings.HasPrefix(a, "'") && strings.HasSuffix(a, "'")) ||
		(strings.HasPrefix(a, "`") && strings.HasSuffix(a, "`")) {
		return true
	}
	// Booleans, null/nil/None.
	switch a {
	case "true", "false", "True", "False", "nil", "null", "None":
		return true
	}
	// Numeric literals: integers, floats, hex, negatives. Loose
	// check, but safer than a regex we'd have to keep in sync.
	allDigitsOrNumeric := true
	for _, r := range a {
		if r >= '0' && r <= '9' {
			continue
		}
		if r == '-' || r == '.' || r == '_' ||
			r == 'x' || r == 'X' ||
			(r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') {
			continue
		}
		allDigitsOrNumeric = false
		break
	}
	if allDigitsOrNumeric && (a[0] >= '0' && a[0] <= '9' || a[0] == '-') {
		return true
	}
	return false
}

// containsConcatOrFormat reports whether `text` shows a sign that a
// string is being assembled from pieces — the classic injection
// smell. Language-specific rules call this before declaring a
// finding so we don't flag a plain literal.
func containsConcatOrFormat(text, lang string) bool {
	// Raw string concat — all four languages.
	if strings.Contains(text, "+") {
		// Require that at least one side of a `+` looks like a
		// string (has a quote) to cut false positives on arithmetic.
		if strings.ContainsAny(text, `"'`+"`") {
			return true
		}
	}
	switch lang {
	case "go":
		if strings.Contains(text, "fmt.Sprintf") || strings.Contains(text, "fmt.Sprint(") {
			return true
		}
	case "python":
		if strings.Contains(text, ".format(") || strings.Contains(text, "f\"") || strings.Contains(text, "f'") {
			return true
		}
		// %-style formatting when used with string literals.
		if strings.Contains(text, "%s") || strings.Contains(text, "%d") {
			return true
		}
	case "javascript", "typescript":
		if strings.Contains(text, "`") {
			// Template literal with ${...} interpolation.
			return strings.Contains(text, "${")
		}
	case "ruby":
		// Ruby string interpolation: `"foo #{bar}"`. The `#{` token
		// uniquely identifies it (Ruby has no shorthand). format()
		// is rarely used in Ruby; sprintf-style is, but those usually
		// land in the existing `+` concat check above.
		if strings.Contains(text, "#{") {
			return true
		}
	case "java":
		// Java string concatenation is plain `+` (already caught at
		// the top of this function). String.format / printf-style
		// land in code via `%s` / `%d`; flag those when paired with
		// a string literal context.
		if strings.Contains(text, "String.format") || strings.Contains(text, ".printf(") {
			return true
		}
	}
	return false
}

// hasBareCall reports whether `text` contains a call to a function
// named `name` that is NOT a method on some other identifier. Used
// to catch destructured sinks (where the call site omits the dot
// prefix yet is still the dangerous one). The character before
// `name` must be non-identifier (whitespace, `{`, `(`, `,`, `;`,
// `=`, `+` …) or start-of-line.
func hasBareCall(text, name string) bool {
	needle := name + "("
	idx := 0
	for idx < len(text) {
		hit := strings.Index(text[idx:], needle)
		if hit < 0 {
			return false
		}
		abs := idx + hit
		if abs == 0 {
			return true
		}
		prev := text[abs-1]
		if !isIdentChar(prev) {
			return true
		}
		idx = abs + 1
	}
	return false
}

func isIdentChar(c byte) bool {
	return (c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') ||
		c == '_' || c == '$' || c == '.'
}

// isCommentLine lets the caller skip lines that can't carry code.
// Kept intentionally conservative: block comments are harder to
// track across lines, and a comment that happens to contain a
// pattern-looking fragment is usually something like "TODO: guard
// against sql injection" — not itself a finding.
func isCommentLine(trimmed, lang string) bool {
	switch lang {
	case "go", "javascript", "typescript", "java":
		return strings.HasPrefix(trimmed, "//")
	case "python", "ruby":
		return strings.HasPrefix(trimmed, "#")
	}
	return false
}

// --- small ring buffer for recent-line context ----------------------

type lineRing struct {
	buf []string
	idx int
	cap int
}

func newLineRing(n int) *lineRing { return &lineRing{buf: make([]string, 0, n), cap: n} }

func (r *lineRing) push(s string) {
	if len(r.buf) < r.cap {
		r.buf = append(r.buf, s)
		return
	}
	r.buf[r.idx] = s
	r.idx = (r.idx + 1) % r.cap
}

func (r *lineRing) joined() string {
	if len(r.buf) == 0 {
		return ""
	}
	if len(r.buf) < r.cap {
		return strings.Join(r.buf, " ")
	}
	out := make([]string, 0, r.cap)
	for i := 0; i < r.cap; i++ {
		out = append(out, r.buf[(r.idx+i)%r.cap])
	}
	return strings.Join(out, " ")
}
