// Taint analysis for the AST-aware scanner (dfmc_report_ast.md §R1).
//
// The line-based scanner in astscan.go is precise for single-line sinks
// but blind to the multi-step case that motivates real taint analysis:
//
//	body, _ := io.ReadAll(r.Body)
//	cmd := exec.Command(string(body))   // not flagged by concat rule
//
// No single line carries both a source and a sink. Existing rules look
// for `exec.Command(... + userInput)` (concat shape) and miss the
// assigned-then-used shape entirely.
//
// taintTracker plugs into the scanner's existing per-line loop. Before
// rules run on a line, the tracker observes assignments and remembers
// any identifier whose RHS contains a known source pattern. Rules then
// query IsTainted(argName) at sink call sites. Line-local in scope (no
// cross-function tracking), which is a deliberate tradeoff against full
// AST-visitor complexity -- the scanner still operates on a stream of
// lines, not a parsed AST.
//
// Scope: per-file tracker, lifetime = one ScanASTRules call. Variable
// names are global within the file (no function-scope distinction) so
// `body` reused in two functions can produce a stale taint. False
// positives from name reuse are accepted as the price of zero-AST-parse
// cost; the alternative is parsing the file with internal/ast.Engine
// here too, which would couple security to ast and slow line-rate
// scanning by ~30% on large files.

package security

import (
	"regexp"
	"strings"
)

// taintSource describes a substring marker that, when present on the
// RHS of an assignment, identifies the LHS identifiers as tainted.
// Substring matching (not anchored regex) is intentional: handlers
// frequently wrap sources in helpers like `io.ReadAll(r.Body)` or
// `strings.NewReader(r.Form.Get("k"))` and a substring marker catches
// every shape without enumerating wrapper signatures.
type taintSource struct {
	Lang string
	// Markers are substrings; at least one must appear in the RHS to
	// taint the LHS identifiers. Multiple alternatives capture
	// equivalent shapes (e.g. `r.Body` and `req.Body`).
	Markers []string
	// Name is used only in tests / debugging; not exposed to findings.
	Name string
}

// requestReceivers are the conventional *http.Request receiver names
// in Go handler code. The handful below catches the vast majority of
// real codebases without depending on a real AST. Compose with
// `.Body` / `.Form` / `.URL...` to form full marker substrings.
var requestReceivers = []string{"r.", "req.", "request."}

func buildHTTPRequestMarkers(fields ...string) []string {
	out := make([]string, 0, len(requestReceivers)*len(fields))
	for _, recv := range requestReceivers {
		for _, field := range fields {
			out = append(out, recv+field)
		}
	}
	return out
}

var taintSources = []taintSource{
	// --- Go --------------------------------------------------------
	// http.Request.Body / Form / PostForm / MultipartForm / URL.* /
	// Header.Get -- the conventional `r` or `req` receiver covers the
	// overwhelming majority of real handler code.
	{Lang: "go", Name: "http_request_body", Markers: buildHTTPRequestMarkers("Body")},
	{Lang: "go", Name: "http_request_form", Markers: buildHTTPRequestMarkers("Form", "PostForm", "MultipartForm")},
	{Lang: "go", Name: "http_request_url", Markers: buildHTTPRequestMarkers("URL.Query()", "URL.RawQuery", "URL.Path", "URL.Host", "URL.RawPath")},
	{Lang: "go", Name: "http_request_header", Markers: buildHTTPRequestMarkers("Header.Get")},
	// Process args + stdin.
	{Lang: "go", Name: "os_args", Markers: []string{"os.Args"}},
	{Lang: "go", Name: "os_stdin", Markers: []string{"os.Stdin"}},
	// flag.Arg / flag.String / etc. -- parsed command-line input.
	{Lang: "go", Name: "flag_arg", Markers: []string{
		"flag.Arg", "flag.Args", "flag.String", "flag.Int",
		"flag.Bool", "flag.Float64", "flag.Duration",
	}},

	// --- Python ---------------------------------------------------
	// Process args + stdin equivalents. sys.argv[N] is the typical
	// access pattern; the substring "sys.argv" catches both
	// `sys.argv` and `sys.argv[1]`.
	{Lang: "python", Name: "sys_argv", Markers: []string{"sys.argv"}},
	{Lang: "python", Name: "sys_stdin", Markers: []string{"sys.stdin"}},
	// input() returns user input; the parens distinguish it from
	// other identifiers named "input".
	{Lang: "python", Name: "input_call", Markers: []string{"input("}},
	// Flask request namespace. Catches request.args, request.form,
	// request.values, request.json, request.data, request.get_json(),
	// request.files, and request.cookies. We anchor on `request.`
	// rather than the more specific names so the marker stays robust
	// across attribute and method access shapes.
	{Lang: "python", Name: "flask_request", Markers: []string{
		"request.args", "request.form", "request.values",
		"request.json", "request.data", "request.get_json",
		"request.files", "request.cookies", "request.headers",
	}},
	// Django request namespace.
	{Lang: "python", Name: "django_request", Markers: []string{
		"request.GET", "request.POST", "request.FILES",
		"request.COOKIES", "request.META", "request.body",
	}},
	// os.environ is technically operator-controlled, but treating
	// it as tainted catches real CVEs where env vars flow into
	// shell calls without sanitization.
	{Lang: "python", Name: "os_environ", Markers: []string{"os.environ"}},
}

// goAssignRE captures the LHS list and RHS of a Go assignment line:
//
//	body, _ := io.ReadAll(r.Body)
//	|------|   |-----------------|
//	   1                 2
//
// Group 1 = the comma-separated LHS, group 2 = the rest of the line
// (the RHS expression). `:?=` matches both `:=` and `=`, so the regex
// also catches plain assignments like `x = r.Body` for re-assignment
// of an existing variable.
var goAssignRE = regexp.MustCompile(`^\s*((?:\w+\s*,\s*)*\w+)\s*:?=\s*(.+)$`)

// parseGoAssign returns (lhsIdents, rhs, ok). `ok=false` when the line
// is not an assignment. Underscores in the LHS list are filtered out
// (the blank identifier never gets tainted).
func parseGoAssign(line string) ([]string, string, bool) {
	m := goAssignRE.FindStringSubmatch(line)
	if m == nil {
		return nil, "", false
	}
	rhs := strings.TrimSpace(m[2])
	if rhs == "" {
		return nil, "", false
	}
	lhs := splitLHS(m[1])
	if len(lhs) == 0 {
		return nil, "", false
	}
	return lhs, rhs, true
}

// pyAssignRE captures Python assignment shapes:
//
//	x = expr              -> LHS=[x],     RHS=expr
//	x, y = a, b           -> LHS=[x, y],  RHS="a, b"
//	x: str = expr         -> LHS=[x],     RHS=expr   (type annotation)
//	x: List[int] = [...]  -> LHS=[x],     RHS=[...]  (parameterised annotation)
//
// The trailing `(.+)` deliberately captures everything from after the
// `=` so a post-match check can reject `==` / `<=` / etc. by looking
// at the first non-space byte of the captured RHS.
var pyAssignRE = regexp.MustCompile(`^\s*((?:\w+\s*,\s*)*\w+)(?:\s*:\s*[^=]+)?\s*=(.+)$`)

// parsePythonAssign returns (lhsIdents, rhs, ok). Rejects comparison
// shapes (`x == y`) by checking that the captured RHS does NOT begin
// with `=` after whitespace -- if it did, the match was actually the
// first `=` of a `==` operator.
func parsePythonAssign(line string) ([]string, string, bool) {
	m := pyAssignRE.FindStringSubmatch(line)
	if m == nil {
		return nil, "", false
	}
	rhs := strings.TrimLeft(m[2], " \t")
	if strings.HasPrefix(rhs, "=") {
		// `x == y` matched as `x = (= y)` -- not a real assignment.
		return nil, "", false
	}
	rhs = strings.TrimSpace(rhs)
	if rhs == "" {
		return nil, "", false
	}
	lhs := splitLHS(m[1])
	if len(lhs) == 0 {
		return nil, "", false
	}
	return lhs, rhs, true
}

// splitLHS turns a comma-separated identifier list into a clean slice.
// Underscores and empty tokens are filtered out.
func splitLHS(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		ident := strings.TrimSpace(p)
		if ident == "" || ident == "_" {
			continue
		}
		out = append(out, ident)
	}
	return out
}

// taintTracker is per-file state. Created once per ScanASTRules call,
// updated by observeLine on every non-comment line BEFORE rules run on
// that line, and queried by taint-aware rules through ctx.Taint.
type taintTracker struct {
	lang    string
	tainted map[string]bool
}

func newTaintTracker(lang string) *taintTracker {
	return &taintTracker{lang: lang, tainted: map[string]bool{}}
}

// observeLine inspects a single source line for taint-introducing
// assignments. If the RHS contains a source marker, every LHS
// identifier (multi-var assignments like `body, err := ...` get all
// their non-blank vars tainted) is recorded. Subsequent calls
// accumulate; the tracker has no notion of variable scope or
// re-assignment-as-cleanup -- once tainted, always tainted within
// the file.
//
// Also handles narrow propagation: if the RHS references an
// already-tainted variable, the LHS inherits taint. This catches
// `s := string(body)` after a `body, _ := io.ReadAll(r.Body)`
// upstream.
func (t *taintTracker) observeLine(line string) {
	if t == nil {
		return
	}
	var (
		lhs []string
		rhs string
		ok  bool
	)
	switch t.lang {
	case "go":
		lhs, rhs, ok = parseGoAssign(line)
	case "python":
		lhs, rhs, ok = parsePythonAssign(line)
	default:
		// JS/TS not yet wired; documented limitation in package header.
		return
	}
	if !ok {
		return
	}
	// Direct source match: RHS contains one of the markers for a
	// taint source. Mark every LHS ident as tainted.
	if rhsContainsSourceMarker(rhs, t.lang) {
		for _, id := range lhs {
			t.tainted[id] = true
		}
		return
	}
	// Propagation: RHS references an already-tainted ident.
	if t.referencesTaintedIdent(rhs) {
		for _, id := range lhs {
			t.tainted[id] = true
		}
	}
}

// rhsContainsSourceMarker reports whether the given RHS expression
// contains a known untrusted-input marker substring for the language.
func rhsContainsSourceMarker(rhs, lang string) bool {
	for _, src := range taintSources {
		if src.Lang != lang {
			continue
		}
		for _, m := range src.Markers {
			if strings.Contains(rhs, m) {
				return true
			}
		}
	}
	return false
}

// IsTainted reports whether the given identifier has been observed as
// the LHS of a tainted assignment. Whitespace-trimmed before lookup so
// callers don't have to.
func (t *taintTracker) IsTainted(name string) bool {
	if t == nil {
		return false
	}
	return t.tainted[strings.TrimSpace(name)]
}

// referencesTaintedIdent walks the RHS expression looking for a
// whole-word identifier match against the tainted set. Used only by
// the propagation pass. Conservative: matches on every bare
// identifier in the RHS; nested-call RHS like `f(g(body))` and
// attribute access like `s.strip()` both reveal their leading
// identifier because `.` and `(` are treated as separators (see
// isPlainIdentChar).
func (t *taintTracker) referencesTaintedIdent(rhs string) bool {
	// Walk byte-by-byte, pulling identifiers, and check each against
	// the tainted set. Cheap, no allocation per non-identifier byte.
	i := 0
	for i < len(rhs) {
		c := rhs[i]
		if !isIdentStart(c) {
			i++
			continue
		}
		j := i + 1
		for j < len(rhs) && isPlainIdentChar(rhs[j]) {
			j++
		}
		tok := rhs[i:j]
		if t.tainted[tok] {
			return true
		}
		i = j
	}
	return false
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// isPlainIdentChar is the stricter cousin of isIdentChar in
// astscan_helpers.go: it excludes `.` and `$` so attribute access
// like `s.strip()` is parsed as the two tokens `s` and `strip` rather
// than a single `s.strip` token. The helper-package version groups
// `.`-separated segments together because that's useful for rule
// matching at the call-name level (`request.args` is one "thing"),
// but for taint propagation we want each bare identifier to be its
// own checkable token.
func isPlainIdentChar(c byte) bool {
	return c == '_' ||
		(c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9')
}

// argReferencesTainted is the rule-side companion to
// referencesTaintedIdent: walks an arg-expression and returns true if
// any whole-word identifier inside has been observed as tainted. Used
// by sink-match rules that want to detect `string(body)` or
// `strings.ToLower(body)`-style compound args without enumerating
// wrapper signatures. Nil-tracker safe.
func argReferencesTainted(arg string, t *taintTracker) bool {
	if t == nil {
		return false
	}
	i := 0
	for i < len(arg) {
		c := arg[i]
		if !isIdentStart(c) {
			i++
			continue
		}
		j := i + 1
		for j < len(arg) && isPlainIdentChar(arg[j]) {
			j++
		}
		if t.IsTainted(arg[i:j]) {
			return true
		}
		i = j
	}
	return false
}

// findCallArgs locates a named call inside `line`, walks to its
// opening paren, finds the matching close paren (respecting nested
// parens), and returns the comma-split arg list. Returns nil when the
// call name is not in the line or parens don't balance.
//
// Used by taint-aware sink matchers across languages so each language
// file doesn't re-implement the paren walk.
func findCallArgs(line, callName string) []string {
	idx := strings.Index(line, callName)
	if idx < 0 {
		return nil
	}
	rest := line[idx+len(callName):]
	open := strings.Index(rest, "(")
	if open < 0 {
		return nil
	}
	rest = rest[open+1:]
	depth := 1
	end := -1
	inString := false
	var quote byte
	for i := 0; i < len(rest); i++ {
		c := rest[i]
		switch {
		case inString:
			if c == quote && (i == 0 || rest[i-1] != '\\') {
				inString = false
			}
		case c == '"' || c == '\'' || c == '`':
			inString = true
			quote = c
		case c == '(':
			depth++
		case c == ')':
			depth--
			if depth == 0 {
				end = i
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		return nil
	}
	return splitArgs(rest[:end])
}

// callHasTaintedArg reports whether any non-literal argument to the
// named call on this line resolves to a tainted identifier (either
// directly or as a sub-token of a wrapper expression like
// `string(body)`). Returns false when the tracker is nil so the
// helper is safe to call unconditionally.
func callHasTaintedArg(line, callName string, t *taintTracker) bool {
	if t == nil {
		return false
	}
	args := findCallArgs(line, callName)
	for _, raw := range args {
		arg := strings.TrimSpace(raw)
		if arg == "" || isLiteralArg(arg) {
			continue
		}
		// Strip a leading `*` / trailing `...` / outer parens so the
		// bare ident is recognised even when wrapped.
		bare := strings.TrimSuffix(strings.TrimPrefix(arg, "*"), "...")
		bare = strings.Trim(bare, "() ")
		if t.IsTainted(bare) {
			return true
		}
		if argReferencesTainted(arg, t) {
			return true
		}
	}
	return false
}
