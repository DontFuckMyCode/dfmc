// AST-aware vulnerability scanner.
//
// The regex scanner in scanner.go is fast and broad but produces real
// false positives (observed: `exec.Command("git", "-C", root, "diff")`
// flagged as CWE-78 even though every argument is a literal and the
// Go runtime never spawns a shell). This scanner layers a second,
// smarter pass on top:
//
//   * For each sink (exec, sql query, eval, deserialize), inspect the
//     argument list. A call whose arguments are ALL string / numeric
//     literals is treated as safe — literal commands can't be
//     hijacked by user input.
//   * Concatenation and formatted strings (`+`, `fmt.Sprintf`,
//     template literals, f-strings, `.format(...)`) touching a sink's
//     arguments are kept as findings — they're the real exploit path.
//   * Per-language sinks capture the patterns the regex can't see
//     (e.g. `subprocess.call(..., shell=True)` where shell=True
//     IS the condition, not the command string).
//
// Not a replacement for the regex scanner; both run and their results
// merge through deduplicateFindings so a file/line never appears twice
// for the "same" issue. When a regex rule and an AST rule both match
// at the same line, the AST rule wins because it carries richer
// classification.

package security

import (
	"bufio"
	"bytes"
	"path/filepath"
	"strings"
)

// astRule is one smart-scan rule. The matcher receives the current
// line + a small "recent lines" ring so multi-line constructs like
// `q := "SELECT ... " + \n   userInput` can still be caught.
type astRule struct {
	Name     string
	Severity string
	CWE      string
	OWASP    string
	Langs    []string // applicable languages: "go", "javascript", "typescript", "python"
	Match    func(ctx *scanLineCtx) bool
}

// scanLineCtx is the per-line slice the matcher sees.
type scanLineCtx struct {
	Lang       string
	LineNo     int
	Line       string // current source line
	Trimmed    string // strings.TrimSpace(Line)
	RecentJoin string // last ~3 source lines joined on space, for multi-line patterns
}

// detectLanguageFromPath is a minimal extension-to-language map; the
// real ast.Engine has a richer one, but pulling that in here would
// force a cyclic dep (engine imports security). Keep in sync with
// the rules' Langs values.
func detectLanguageFromPath(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go":
		return "go"
	case ".js", ".jsx", ".mjs", ".cjs":
		return "javascript"
	case ".ts", ".tsx":
		return "typescript"
	case ".py", ".pyw":
		return "python"
	}
	return ""
}

// ScanASTRules runs the smart-scan pass and returns findings. The
// regex-based scanner is in scanner.go; this complements it and both
// feed Scanner.ScanContent.
func (s *Scanner) ScanASTRules(path string, content []byte) []VulnerabilityFinding {
	lang := detectLanguageFromPath(path)
	if lang == "" {
		return nil
	}
	var findings []VulnerabilityFinding

	// Ring buffer for "recent lines" context — cheap way to catch
	// multi-line concatenation. Three lines has been enough in
	// practice; patterns that span more are rare and usually dead.
	ring := newLineRing(3)

	scanner := bufio.NewScanner(bytes.NewReader(content))
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		ring.push(trimmed)

		if trimmed == "" || isCommentLine(trimmed, lang) {
			continue
		}
		if isPatternDefinitionLine(trimmed) {
			// Rule-definition lines inside our own source shouldn't
			// trigger on themselves. Same guard the regex scanner uses.
			continue
		}

		ctx := &scanLineCtx{
			Lang:       lang,
			LineNo:     lineNo,
			Line:       line,
			Trimmed:    trimmed,
			RecentJoin: ring.joined(),
		}
		for _, rule := range astRulesForLang(lang) {
			if !rule.Match(ctx) {
				continue
			}
			findings = append(findings, VulnerabilityFinding{
				Kind:     rule.Name,
				File:     toSlash(path),
				Line:     lineNo,
				Severity: rule.Severity,
				CWE:      rule.CWE,
				OWASP:    rule.OWASP,
				Snippet:  snippet(trimmed, 180),
			})
		}
	}
	return findings
}

// astRulesForLang returns the subset of registered rules that apply
// to a given language. Kept as a function call (not a map lookup)
// so tests can override easily without touching a package-level
// mutable state.
func astRulesForLang(lang string) []astRule {
	out := make([]astRule, 0, 16)
	for _, r := range allASTRules() {
		for _, l := range r.Langs {
			if l == lang {
				out = append(out, r)
				break
			}
		}
	}
	return out
}

// allASTRules collects every language's rules. Each language file
// (astscan_go.go, astscan_js.go, astscan_python.go) contributes via
// its own registration function.
func allASTRules() []astRule {
	var out []astRule
	out = append(out, goASTRules()...)
	out = append(out, jsASTRules()...)
	out = append(out, pythonASTRules()...)
	return out
}

// --- helpers shared across per-language rule files -------------------

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
	case "go", "javascript", "typescript":
		return strings.HasPrefix(trimmed, "//")
	case "python":
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
