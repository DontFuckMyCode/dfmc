// Function-scope detection for the security scanner
// (dfmc_report_ast.md §R8 slice 2).
//
// The taint tracker grew a scope stack in slice 1 (astscan_taint.go);
// this file is the consumer side that decides when to Push and when
// to Pop. The current cut handles Go only; the other languages keep
// the file-scoped pre-R8 behaviour because the balancer never fires.
// Python / JS / TS / Ruby / Java boundary detection is queued for
// follow-up slices.
//
// Strategy: a deliberately simple heuristic that handles the
// canonical Go formatting and matches every existing taint-test
// shape:
//
//   * Function entry  -- line starts with `func ` and ends with `{`.
//                        This catches `func foo() {`, the canonical
//                        method form `func (r *X) bar() {`, and the
//                        one-liner `func foo() { return 1 }` (which
//                        also ends with `}` and is treated as a
//                        push-then-pop pair on the same line).
//
//   * Function exit   -- the trimmed line is exactly `}`. Nested
//                        block closers (`}` for `if` / `for` /
//                        struct literal etc.) are at deeper
//                        indentation but the trimmed-line text is
//                        also `}` -- we'd over-pop. The pushCount
//                        guard keeps us safe: we only Pop while
//                        there's an outstanding pushed scope, and
//                        the rules consumer treats unmatched `}`
//                        as no-ops.
//
// Multi-line function signatures (`func foo(\n    a int,\n) {`) and
// inner closures are deliberately not handled in this slice. Real
// codebases use them sparingly; the lost coverage is an acceptable
// tradeoff against a much more complex parser.

package security

import (
	"regexp"
	"strings"
)

// scopeBalancer tracks function-scope state across the per-line
// scan loop. It is nil-safe (every method returns the zero value
// on a nil receiver) so callers can construct one unconditionally.
//
// State carried:
//
//   - pushCount: brace-language counter (Go / JS / TS). Tracks the
//     number of currently-open function scopes we've pushed. We
//     don't need a stack of anything here because Push / Pop on
//     the taint tracker is itself stack-shaped; we just keep our
//     pushes and pops balanced.
//
//   - pythonFuncIndents: Python indent stack. Each entry is the
//     leading-whitespace count of a `def` line that we pushed a
//     scope for. When a later line's indent drops to or below the
//     top entry, we know the function ended (Python has no closing
//     brace; scope-end is implicit from indent drop).
type scopeBalancer struct {
	lang string
	// braceFuncIndents is the brace-language (Go/JS/TS) analogue of
	// pythonFuncIndents: each entry is the leading-whitespace count of a
	// `func`/`function` declaration line we pushed a scope for. A function
	// closes only when a lone `}` appears at the SAME indent — inner block
	// closers (`if`/`for`/`switch`/closure `}`) sit at deeper indent and
	// must NOT pop the function scope. The old pushCount-only guard popped
	// on the FIRST inner `}`, dropping all taint introduced before that
	// block for sinks after it (a security false-negative).
	braceFuncIndents  []int
	pythonFuncIndents []int
}

func newScopeBalancer(lang string) *scopeBalancer {
	return &scopeBalancer{lang: lang}
}

// preObserve runs BEFORE the taint tracker's observeLine on the
// current source line. Two function-exit transitions can fire:
//
//   - Brace languages (Go / JS / TS): a lone `}` line pops the
//     topmost function scope.
//
//   - Python: any line whose indent has dropped to or below the
//     topmost open function's entry indent pops that function (and
//     keeps popping while the next-outer function also fits the
//     condition, since the indent can drop by multiple levels at
//     once -- e.g. a top-level statement after a method inside a
//     class).
//
// `line` carries the original (un-trimmed) source line so the
// Python path can measure leading whitespace; `trimmed` carries
// the cheap-comparison form for brace-language exit detection.
func (b *scopeBalancer) preObserve(line, trimmed string, taint *taintTracker) {
	if b == nil {
		return
	}
	if !b.handlesLang() {
		return
	}
	switch b.lang {
	case "go", "javascript", "typescript":
		// Pop the function scope only when this lone `}` is at the same
		// indent as the function declaration. Inner-block closers are at
		// deeper indent and leave the function scope intact.
		if isBraceLanguageFunctionExit(trimmed) && len(b.braceFuncIndents) > 0 &&
			b.braceFuncIndents[len(b.braceFuncIndents)-1] == countLeadingWS(line) {
			taint.PopScope()
			b.braceFuncIndents = b.braceFuncIndents[:len(b.braceFuncIndents)-1]
		}
	case "python":
		indent := countLeadingWS(line)
		for len(b.pythonFuncIndents) > 0 &&
			b.pythonFuncIndents[len(b.pythonFuncIndents)-1] >= indent {
			taint.PopScope()
			b.pythonFuncIndents = b.pythonFuncIndents[:len(b.pythonFuncIndents)-1]
		}
	}
}

// postObserve runs AFTER the taint tracker's observeLine. The
// function-entry transition fires here so the declaration itself
// is observed in the OUTER scope (the function name and parameters
// don't get tainted into the new inner scope just because of where
// the declaration sits). One-liner brace-language functions both
// push and pop, leaving the count unchanged but giving any
// assignments on the line their own scope in case a future rule
// cares. Python has no one-liner shape (PEP 8 requires the body to
// start on a new line) so the same-line balance issue doesn't
// arise.
func (b *scopeBalancer) postObserve(line, trimmed string, taint *taintTracker) {
	if b == nil {
		return
	}
	if !b.handlesLang() {
		return
	}
	switch b.lang {
	case "go", "javascript", "typescript":
		entry, oneLiner := b.detectFunctionEntry(trimmed)
		if !entry {
			return
		}
		taint.PushScope()
		b.braceFuncIndents = append(b.braceFuncIndents, countLeadingWS(line))
		if oneLiner {
			// Body opens and closes on the same line — net-zero scope.
			taint.PopScope()
			b.braceFuncIndents = b.braceFuncIndents[:len(b.braceFuncIndents)-1]
		}
	case "python":
		if !isPythonFunctionEntry(trimmed) {
			return
		}
		taint.PushScope()
		b.pythonFuncIndents = append(b.pythonFuncIndents, countLeadingWS(line))
	}
}

// handlesLang reports whether the balancer recognises the current
// file's language. Languages outside this set keep the file-scoped
// pre-R8 behaviour: the balancer skips both Pre and PostObserve,
// the tracker stays at scope depth 1, and behaviour matches the
// original flat-map implementation.
func (b *scopeBalancer) handlesLang() bool {
	switch b.lang {
	case "go", "javascript", "typescript", "python":
		return true
	}
	return false
}

// detectFunctionEntry returns (entry, oneLiner) for the current
// trimmed line. entry is true when the line opens a function body;
// oneLiner is true when the body also CLOSES on the same line.
func (b *scopeBalancer) detectFunctionEntry(trimmed string) (bool, bool) {
	switch b.lang {
	case "go":
		if !isGoFunctionEntry(trimmed) {
			return false, false
		}
		return true, isGoOneLinerFunction(trimmed)
	case "javascript", "typescript":
		if !isJSFunctionEntry(trimmed) {
			return false, false
		}
		return true, isJSOneLinerFunction(trimmed)
	}
	return false, false
}

// isBraceLanguageFunctionExit reports whether `trimmed` is a lone
// closing brace -- the conventional formatting for "this function
// ends" in Go and JS / TS alike. Shared between the languages
// because the textual shape is identical.
func isBraceLanguageFunctionExit(trimmed string) bool {
	return trimmed == "}"
}

// isGoFunctionEntry reports whether `trimmed` is the opening line
// of a Go function or method declaration -- i.e. starts with
// `func ` and contains a brace that opens the body. The body
// brace may be the final char (`func foo() {`) or earlier in the
// line for one-liners (`func foo() { return 1 }`); either shape
// opens a scope. Multi-line signature shapes where the `{` lives
// on a later line are NOT detected -- a documented limitation of
// this slice.
func isGoFunctionEntry(trimmed string) bool {
	if !strings.HasPrefix(trimmed, "func ") {
		return false
	}
	return strings.Contains(trimmed, "{")
}

// isGoOneLinerFunction reports whether `trimmed` is a function
// declaration whose body fits on the same line: starts with
// `func `, ends with `}`, and contains at least one `{`. Rare in
// real code (gofmt prefers a multi-line body), but worth handling
// so the balance stays correct.
func isGoOneLinerFunction(trimmed string) bool {
	if !strings.HasPrefix(trimmed, "func ") {
		return false
	}
	if !strings.HasSuffix(trimmed, "}") {
		return false
	}
	return strings.Contains(trimmed, "{")
}

// jsFunctionEntryRE matches named-function declarations in JS / TS
// at a word boundary. Catches the common shapes:
//
//	function foo(...)            -- plain
//	async function foo(...)      -- async
//	export function foo(...)     -- ESM
//	export async function foo(...)
//	export default function foo(...)
//
// Anonymous functions (`function() {`) and arrow functions
// (`const foo = () => {`) are NOT matched in this slice; they're
// queued for a future iteration. Class-method shorthand
// (`methodName() {` inside a class body) is also intentionally
// skipped because detecting it correctly requires class-body
// context tracking which would complicate the line-by-line model.
var jsFunctionEntryRE = regexp.MustCompile(`(?:^|\s)function\s+[A-Za-z_$][\w$]*`)

// isJSFunctionEntry reports whether `trimmed` opens a function
// declaration AND the body brace is on the same line.
func isJSFunctionEntry(trimmed string) bool {
	if !strings.Contains(trimmed, "{") {
		return false
	}
	return jsFunctionEntryRE.MatchString(trimmed)
}

// isJSOneLinerFunction reports whether `trimmed` is a function
// declaration whose body fits on the same line. Same shape as the
// Go helper: starts with a function-declaration prefix, ends with
// `}`, contains `{`.
func isJSOneLinerFunction(trimmed string) bool {
	if !strings.HasSuffix(trimmed, "}") {
		return false
	}
	if !strings.Contains(trimmed, "{") {
		return false
	}
	return jsFunctionEntryRE.MatchString(trimmed)
}

// isPythonFunctionEntry reports whether `trimmed` is a Python
// function-definition line: starts with `def ` or `async def ` and
// ends with `:`. The `:` terminator is required so we don't match
// type-annotation shapes like `def_count: int = 0` (which doesn't
// actually appear because the prefix-match `def ` includes a space,
// but the explicit `:` requirement keeps the predicate robust).
//
// Decorators are NOT detected as function entries -- the `@decorator`
// line is at the same indent as the `def` it decorates and falls
// through to no-op, then the actual `def` line triggers the push.
// Multi-line function signatures spanning several lines via
// continuation are not handled in this slice (rare in real code).
func isPythonFunctionEntry(trimmed string) bool {
	if !strings.HasSuffix(trimmed, ":") {
		return false
	}
	return strings.HasPrefix(trimmed, "def ") ||
		strings.HasPrefix(trimmed, "async def ")
}

// countLeadingWS counts leading whitespace bytes (spaces and tabs)
// on a raw source line. Tab and space both count as one byte; mixed
// indentation files will see indent-comparison errors at the
// boundaries where a tab and four spaces happen to look "equal"
// numerically when they're not visually -- but PEP 8 forbids mixed
// indentation, and consistent files (all tabs or all spaces) work
// reliably under this counter.
func countLeadingWS(line string) int {
	n := 0
	for i := 0; i < len(line); i++ {
		if line[i] != ' ' && line[i] != '\t' {
			break
		}
		n++
	}
	return n
}
