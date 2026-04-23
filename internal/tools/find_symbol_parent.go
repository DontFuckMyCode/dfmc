// find_symbol_parent.go — parent-scope detection for find_symbol's
// `parent` arg. Given a symbol's line and language, figure out the
// enclosing class / receiver / impl block name so a caller can
// disambiguate `(s *Server) Start` from `(c *Client) Start` with
// `parent="Server"`. Four helpers:
//
//   - detectParent: the language dispatcher; picks Go's receiver regex
//     or one of the enclosing-by-indent / enclosing-by-braces walkers.
//   - parentMatches: exact-match check (no globbing) used at filter
//     time.
//   - enclosingByIndent: indent-scoped walk for Python/Ruby/PHP.
//   - enclosingByBraces: brace-balanced walk for JS/TS/Java/C#/Kotlin/
//     Scala/Swift/C++/Rust impl blocks. Best-effort: doesn't strip
//     string/comment braces, but handles the common one-class-per-file
//     case correctly.
//
// goReceiverRE stays in find_symbol.go next to its direct Go caller;
// the regexes used by the enclosing walkers are string literals at the
// call site so the dispatch is self-documenting.

package tools

import (
	"regexp"
	"strings"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// detectParent returns the enclosing-scope name for a symbol — receiver
// type for Go methods, enclosing class for class-shaped languages
// (Python/JS/TS/Java/C#/Kotlin/Scala/Swift/PHP/Ruby/Rust impl). Returns
// "" when the symbol is top-level or the language doesn't carry a parent
// notion that we recognise. Best-effort: regex-based, no AST traversal.
func detectParent(language string, sym types.Symbol, lines []string) string {
	if sym.Kind != types.SymbolMethod && sym.Kind != types.SymbolFunction {
		return ""
	}
	switch language {
	case "go":
		if m := goReceiverRE.FindStringSubmatch(sym.Signature); len(m) > 1 {
			return m[1]
		}
		// Signature may be empty (regex fallback) — peek at the symbol's
		// header line directly.
		if sym.Line >= 1 && sym.Line <= len(lines) {
			if m := goReceiverRE.FindStringSubmatch(lines[sym.Line-1]); len(m) > 1 {
				return m[1]
			}
		}
		return ""
	case "python":
		return enclosingByIndent(lines, sym.Line, `^\s*class\s+([A-Za-z_]\w*)`)
	case "javascript", "typescript", "tsx", "jsx":
		return enclosingByBraces(lines, sym.Line, `^\s*(?:export\s+(?:default\s+)?)?(?:abstract\s+)?class\s+([A-Za-z_$][\w$]*)`)
	case "java", "csharp", "kotlin", "scala", "swift":
		return enclosingByBraces(lines, sym.Line, `^\s*(?:public\s+|private\s+|protected\s+|internal\s+|abstract\s+|final\s+|static\s+|sealed\s+|open\s+)*(?:class|interface|object|trait|struct|enum)\s+([A-Za-z_]\w*)`)
	case "rust":
		// Rust methods live in `impl X { ... }` or `impl Trait for X { ... }`.
		// Pick the type after `for` if present, otherwise the type after `impl`.
		return enclosingByBraces(lines, sym.Line, `^\s*impl(?:<[^>]*>)?\s+(?:[\w:<>,\s']+for\s+)?([A-Za-z_]\w*)`)
	case "php", "ruby":
		return enclosingByIndent(lines, sym.Line, `^\s*class\s+([A-Za-z_]\w*)`)
	case "cpp", "c++", "c":
		return enclosingByBraces(lines, sym.Line, `^\s*(?:class|struct)\s+([A-Za-z_]\w*)`)
	}
	return ""
}

// parentMatches reports whether `have` is the enclosing scope the caller
// asked for. Match is exact and case-sensitive — receiver/class names are
// canonical identifiers, not free text.
func parentMatches(have, want string) bool {
	if want == "" {
		return true
	}
	return have == want
}

// enclosingByIndent walks backward from line `at` to find the most recent
// header that matches `pattern` AND has a smaller leading indent than the
// symbol itself. Used for indent-scoped languages (Python, Ruby).
func enclosingByIndent(lines []string, at int, pattern string) string {
	if at < 1 || at > len(lines) {
		return ""
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return ""
	}
	symIndent := leadingIndent(lines[at-1])
	for i := at - 2; i >= 0; i-- {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := leadingIndent(line)
		if indent >= symIndent {
			continue
		}
		if m := re.FindStringSubmatch(line); len(m) > 1 {
			return m[1]
		}
	}
	return ""
}

// enclosingByBraces walks backward from line `at` looking for a header
// line that matches `pattern` whose `{` block still encloses the symbol.
// The brace count is computed from the candidate line through `at` — if
// it ends > 0 the candidate's block is still open at `at`. Best-effort:
// strings/comments are not stripped, so weird literal braces in between
// can mislead the count, but the common case (one class per file, methods
// inside it) is handled correctly.
func enclosingByBraces(lines []string, at int, pattern string) string {
	if at < 1 || at > len(lines) {
		return ""
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return ""
	}
	for i := at - 2; i >= 0; i-- {
		m := re.FindStringSubmatch(lines[i])
		if len(m) <= 1 {
			continue
		}
		// Count braces from this header through the symbol's line.
		depth := 0
		seenOpen := false
		for j := i; j < at && j < len(lines); j++ {
			for _, c := range lines[j] {
				switch c {
				case '{':
					depth++
					seenOpen = true
				case '}':
					depth--
				}
			}
		}
		if seenOpen && depth > 0 {
			return m[1]
		}
	}
	return ""
}
