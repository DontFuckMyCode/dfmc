// Call-site extraction (dfmc_report_ast.md §R3, phase 1).
//
// The full call-graph item in the AST report has two phases:
//
//   Phase 1 -- per-file call sites: scan each line for
//              identifier(...) shapes and record (callee, line).
//              This file owns phase 1. No cross-function or
//              cross-file resolution; callers that want either
//              walk Symbols + Calls themselves.
//
//   Phase 2 -- workspace-wide resolution: take a corpus of files'
//              Symbols + ImportAliases + Calls and resolve each
//              edge to a concrete target. That's left for a
//              follow-up; the data this phase exposes is the
//              load-bearing prerequisite.
//
// We deliberately do NOT mutate ParseResult here. ExtractCalls is
// a stateless package function so the addition is purely additive:
// every existing consumer of Engine / ParseContent / Walk keeps
// working, and call-edge consumers opt in.

package ast

import (
	"regexp"
	"strings"
)

// Call is one call-site occurrence inside a parsed file. Callee is
// the identifier (possibly dotted) as written -- `f`, `obj.method`,
// `pkg.func`, `pkg.sub.func` all surface verbatim. Resolution to a
// concrete target (defined in which file, what receiver type) is
// deferred to phase 2; this struct just captures what the source
// said and where.
type Call struct {
	Callee string `json:"callee"`
	Line   int    `json:"line"`
}

// ExtractCalls walks `content` line by line and returns every
// identifier-followed-by-paren occurrence that looks like a call,
// in source order. Returns nil for languages with no call-site
// regex wired (Java / Ruby / Rust are intentionally postponed --
// they'd compound the keyword-list maintenance burden without
// pulling weight for the security-scanner consumers).
//
// Filtering: declaration lines (`func foo(`, `def foo(`) are
// skipped so the function being declared doesn't appear as a call
// to itself. Per-language keyword lists filter out control-flow
// shapes (`if (...)`, `for (...)`, `while (...)`) that share the
// `keyword(...)` skeleton with real calls. Comment lines are
// skipped via the shared isCommentLine helper.
func ExtractCalls(lang string, content []byte) []Call {
	pat := callPatternFor(lang)
	if pat == nil {
		return nil
	}
	keywords := languageCallKeywords(lang)
	lines := strings.Split(string(content), "\n")
	var out []Call
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if isCallCommentLine(trimmed, lang) {
			continue
		}
		if isDeclarationLine(trimmed, lang) {
			continue
		}
		matches := pat.FindAllStringSubmatchIndex(line, -1)
		for _, m := range matches {
			// m[2]:m[3] is the captured callee text.
			if m[2] < 0 || m[3] < 0 {
				continue
			}
			// Word-boundary anchor done in code rather than regex:
			// putting `(?:^|[^\w.$])` in the pattern consumes the
			// preceding char, which forces FindAll's non-overlapping
			// scan to skip immediately-adjacent calls (e.g.
			// `if(check(x))` would match only `if` because the
			// `(` between `if` and `check` is consumed by the prior
			// match). Checking the preceding byte by hand keeps the
			// regex non-consuming and lets back-to-back calls fire.
			start := m[2]
			if start > 0 && isCallIdentExtChar(line[start-1]) {
				continue
			}
			callee := line[start:m[3]]
			callee = strings.TrimSpace(callee)
			if callee == "" {
				continue
			}
			// Filter language keywords: `if`, `for`, `switch`,
			// `return`, `defer`, etc. all read as `keyword(` and
			// would otherwise count as calls to themselves.
			if keywords[callee] {
				continue
			}
			out = append(out, Call{Callee: callee, Line: i + 1})
		}
	}
	return out
}

// callPatternFor returns the per-language identifier-then-paren
// regex. The capture group covers the identifier (dotted-allowed
// for method / package calls). Returns nil for unsupported langs.
//
// Anchored on a non-word boundary before the identifier so an
// identifier embedded inside another doesn't double-fire (e.g.
// `myFoo(...)` doesn't ALSO report `Foo(...)`).
func callPatternFor(lang string) *regexp.Regexp {
	switch lang {
	case "go":
		return reGoCall
	case "python":
		return rePyCall
	case "javascript", "typescript", "jsx", "tsx":
		return reJSCall
	}
	return nil
}

// Per-language call regexes. The capture group is the full identifier
// (possibly dotted). Trailing `\s*\(` confirms this is a call site,
// not a reference. The word-boundary check before the identifier is
// done in code (see isCallIdentExtChar) rather than in the regex so
// FindAll's non-overlapping scan can still report back-to-back calls
// like `if(check(x))`.
var (
	reGoCall = regexp.MustCompile(`([A-Za-z_]\w*(?:\.[A-Za-z_]\w*)*)\s*\(`)
	rePyCall = regexp.MustCompile(`([A-Za-z_]\w*(?:\.[A-Za-z_]\w*)*)\s*\(`)
	// JS / TS shares the same shape; we keep separate variables in
	// case future tweaks diverge (e.g. tagged template literals
	// `tag\`...\`` which look like calls but aren't paren-shaped).
	reJSCall = regexp.MustCompile(`([A-Za-z_$][\w$]*(?:\.[A-Za-z_$][\w$]*)*)\s*\(`)
)

// isCallIdentExtChar reports whether `c` can extend an identifier,
// i.e. whether seeing it immediately BEFORE a regex match indicates
// the match is really a suffix of a larger identifier rather than a
// fresh call. `.` is included so `obj.foo(` doesn't also match a
// stand-alone `foo(` starting mid-identifier. `$` is included for
// the JS / TS dialects where it's a valid identifier character.
func isCallIdentExtChar(c byte) bool {
	return c == '_' || c == '.' || c == '$' ||
		(c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9')
}

// languageCallKeywords returns the set of identifiers that should
// NOT be reported as calls even though they parse as `keyword(...)`.
// Includes control-flow keywords, declarations, and
// builtin "looks like a call but isn't" forms (Python `print` IS a
// call, so it's kept; `del` / `pass` / `lambda` are statements and
// excluded).
func languageCallKeywords(lang string) map[string]bool {
	switch lang {
	case "go":
		return goCallKeywords
	case "python":
		return pyCallKeywords
	case "javascript", "typescript", "jsx", "tsx":
		return jsCallKeywords
	}
	return nil
}

var (
	goCallKeywords = map[string]bool{
		"if": true, "for": true, "switch": true, "select": true,
		"return": true, "go": true, "defer": true, "func": true,
		"chan": true, "case": true,
	}
	pyCallKeywords = map[string]bool{
		"if": true, "elif": true, "while": true, "for": true,
		"return": true, "yield": true, "raise": true, "with": true,
		"def": true, "class": true, "lambda": true, "assert": true,
		"and": true, "or": true, "not": true, "in": true, "is": true,
		"async": true, "await": true,
	}
	jsCallKeywords = map[string]bool{
		"if": true, "for": true, "while": true, "switch": true,
		"return": true, "throw": true, "typeof": true, "instanceof": true,
		"new": true, "delete": true, "void": true, "in": true, "of": true,
		"function": true, "yield": true, "await": true, "async": true,
		"catch": true,
	}
)

// isDeclarationLine reports whether the line declares a function /
// method / class rather than calling one. The declaration's name
// reads as `name(` but isn't a call site -- skipping the whole line
// avoids reporting the function as a call to itself.
func isDeclarationLine(trimmed, lang string) bool {
	switch lang {
	case "go":
		return strings.HasPrefix(trimmed, "func ")
	case "python":
		return strings.HasPrefix(trimmed, "def ") ||
			strings.HasPrefix(trimmed, "async def ") ||
			strings.HasPrefix(trimmed, "class ")
	case "javascript", "typescript", "jsx", "tsx":
		// `function foo(`, `class Foo`, `interface Foo` (TS),
		// `export function foo(`, `async function foo(`, and the
		// method shorthand `foo() {` inside a class body. The
		// shorthand is genuinely ambiguous from a single-line view
		// (it could be either declaration or a chained call); we
		// err on the side of including it as a call. A rare false
		// "call" on method shorthand is a tolerable cost.
		s := trimmed
		for _, prefix := range []string{
			"function ", "async function ", "export function ",
			"export default function ", "export async function ",
			"class ", "export class ", "interface ", "export interface ",
		} {
			if strings.HasPrefix(s, prefix) {
				return true
			}
		}
	}
	return false
}

// isCallCommentLine is a thin wrapper that lets ExtractCalls call
// the existing isCommentLine helper without dragging that helper's
// package-internal scope out. Kept here so the call-extractor stays
// language-local.
func isCallCommentLine(trimmed, lang string) bool {
	switch lang {
	case "go", "javascript", "typescript", "jsx", "tsx", "java":
		return strings.HasPrefix(trimmed, "//")
	case "python", "ruby":
		return strings.HasPrefix(trimmed, "#")
	}
	return false
}
