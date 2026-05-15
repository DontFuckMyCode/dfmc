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

import "strings"

// scopeBalancer tracks function-scope state across the per-line
// scan loop. It is nil-safe (every method returns the zero value
// on a nil receiver) so callers can construct one unconditionally.
//
// State carried: only `pushCount`, the number of currently-open
// function scopes we've pushed onto the taint tracker. We don't
// need a stack of-anything because Push / Pop on the taint tracker
// is already stack-shaped; we just need to keep our pushes and
// pops balanced.
type scopeBalancer struct {
	lang      string
	pushCount int
}

func newScopeBalancer(lang string) *scopeBalancer {
	return &scopeBalancer{lang: lang}
}

// preObserve runs BEFORE the taint tracker's observeLine on the
// current source line. The only transition that fires here is
// function exit: a lone `}` line pops the topmost function scope
// so any assignments on the SAME line (rare in practice) live in
// the outer scope.
func (b *scopeBalancer) preObserve(trimmed string, taint *taintTracker) {
	if b == nil || b.lang != "go" {
		return
	}
	if isGoFunctionExit(trimmed) && b.pushCount > 0 {
		taint.PopScope()
		b.pushCount--
	}
}

// postObserve runs AFTER the taint tracker's observeLine. The
// function-entry transition fires here so the declaration itself
// is observed in the OUTER scope (the function name and parameters
// don't get tainted into the new inner scope just because of where
// the declaration sits). One-liner functions (`func foo() { return
// 1 }`) both push and pop, leaving the count unchanged but giving
// any assignments on the line their own scope in case a future
// rule cares.
func (b *scopeBalancer) postObserve(trimmed string, taint *taintTracker) {
	if b == nil || b.lang != "go" {
		return
	}
	if !isGoFunctionEntry(trimmed) {
		return
	}
	taint.PushScope()
	b.pushCount++
	if isGoOneLinerFunction(trimmed) {
		// Same-line close: balance the push immediately so the
		// next line's state is outer scope again.
		taint.PopScope()
		b.pushCount--
	}
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

// isGoFunctionExit reports whether `trimmed` is a lone closing
// brace -- the conventional Go formatting for "this function
// ends". Lines like `}, {` (struct-literal continuation) or
// `} else if x {` don't match and are correctly left alone.
func isGoFunctionExit(trimmed string) bool {
	return trimmed == "}"
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
