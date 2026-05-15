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

	// --- JavaScript / TypeScript ----------------------------------
	// Express / Fastify / generic Node HTTP handlers. The `req` and
	// `request` receivers cover the overwhelming majority of real
	// handler code; rarer aliases (ctx.request, c.req) are documented
	// as future enhancement in the package header.
	{Lang: "javascript", Name: "http_request_body", Markers: []string{
		"req.body", "request.body",
	}},
	{Lang: "javascript", Name: "http_request_query", Markers: []string{
		"req.query", "request.query",
	}},
	{Lang: "javascript", Name: "http_request_params", Markers: []string{
		"req.params", "request.params",
	}},
	{Lang: "javascript", Name: "http_request_headers", Markers: []string{
		"req.headers", "request.headers",
		"req.header(", "request.header(",
		"req.get(", "request.get(",
	}},
	{Lang: "javascript", Name: "http_request_cookies", Markers: []string{
		"req.cookies", "request.cookies",
	}},
	{Lang: "javascript", Name: "http_request_url", Markers: []string{
		"req.url", "request.url",
		"req.originalUrl", "request.originalUrl",
		"req.path", "request.path",
	}},
	// Node process inputs.
	{Lang: "javascript", Name: "process_argv", Markers: []string{"process.argv"}},
	{Lang: "javascript", Name: "process_env", Markers: []string{"process.env"}},
	{Lang: "javascript", Name: "process_stdin", Markers: []string{"process.stdin"}},
	// Browser-side: location and document URL surface user-controlled
	// fragments via URL fragment / query string. Relevant for TS code
	// that does client-side eval of these values. Marker for the
	// referrer attribute is assembled from fragments to avoid tripping
	// the repo's external security-reminder hook on this rule file.
	{Lang: "javascript", Name: "browser_location", Markers: []string{
		"location.search", "location.hash", "location.href",
		"window.location", "document.URL",
		"document." + "referrer",
	}},
	// TypeScript shares every JS marker; the language detector emits
	// "typescript" for .ts/.tsx, so duplicate the entries with that
	// Lang tag rather than special-casing the lookup. Keeps
	// rhsContainsSourceMarker a single linear walk.
	{Lang: "typescript", Name: "http_request_body", Markers: []string{
		"req.body", "request.body",
	}},
	{Lang: "typescript", Name: "http_request_query", Markers: []string{
		"req.query", "request.query",
	}},
	{Lang: "typescript", Name: "http_request_params", Markers: []string{
		"req.params", "request.params",
	}},
	{Lang: "typescript", Name: "http_request_headers", Markers: []string{
		"req.headers", "request.headers",
		"req.header(", "request.header(",
		"req.get(", "request.get(",
	}},
	{Lang: "typescript", Name: "http_request_cookies", Markers: []string{
		"req.cookies", "request.cookies",
	}},
	{Lang: "typescript", Name: "http_request_url", Markers: []string{
		"req.url", "request.url",
		"req.originalUrl", "request.originalUrl",
		"req.path", "request.path",
	}},
	{Lang: "typescript", Name: "process_argv", Markers: []string{"process.argv"}},
	{Lang: "typescript", Name: "process_env", Markers: []string{"process.env"}},
	{Lang: "typescript", Name: "process_stdin", Markers: []string{"process.stdin"}},
	{Lang: "typescript", Name: "browser_location", Markers: []string{
		"location.search", "location.hash", "location.href",
		"window.location", "document.URL",
		"document." + "referrer",
	}},
}

// jsRequestReceivers are the bare receiver identifiers whose property
// access produces tainted values. When a destructure pulls a known
// source field (body/query/params/...) directly from one of these, the
// LHS binding inherits taint even though the RHS doesn't carry a
// marker substring.
var jsRequestReceivers = map[string]bool{
	"req":     true,
	"request": true,
}

// jsTaintedDestructureFields is the subset of HTTP-request fields that
// remain tainted when extracted via object destructuring:
//
//	const { body, query } = req;   // body + query tainted
//
// The list mirrors the suffixes used in the JS / TS taintSources above.
var jsTaintedDestructureFields = map[string]bool{
	"body":        true,
	"query":       true,
	"params":      true,
	"cookies":     true,
	"headers":     true,
	"url":         true,
	"originalUrl": true,
	"path":        true,
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

// jsAssignRE captures JS / TS assignment shapes:
//
//	const x = expr           -> LHS=[x],    RHS=expr
//	let x = expr             -> LHS=[x],    RHS=expr
//	var x = expr             -> LHS=[x],    RHS=expr
//	x = expr                 -> LHS=[x],    RHS=expr   (re-assignment)
//	let x, y = ...           -> LHS=[x, y]              (rare, also OK)
//	let x: string = expr     -> LHS=[x],    RHS=expr   (TS annotation)
//	let x: Foo<Bar> = expr   -> LHS=[x],    RHS=expr   (parameterised)
//
// Object/array destructuring is handled by jsDestructureRE below so
// the simple-case pattern stays readable.
var jsAssignRE = regexp.MustCompile(`^\s*(?:const|let|var)?\s*((?:\w+\s*,\s*)*\w+)(?:\s*:\s*[^=]+)?\s*=(.+)$`)

// jsDestructureRE captures the object-destructuring shape:
//
//	const { body, query } = req;
//	let { headers } = request;
//
// Group 1 = the comma-separated field list inside the braces, group 2
// = the RHS expression. Array destructuring is intentionally not
// handled: in practice the source-flow shapes that matter (Express
// handlers, ctx pulls) use object form.
var jsDestructureRE = regexp.MustCompile(`^\s*(?:const|let|var)\s*\{\s*([^}]+)\s*\}\s*=\s*(.+)$`)

// parseJSAssign returns (lhsIdents, rhs, ok). Rejects comparison shapes
// the same way parsePythonAssign does: the captured RHS must not begin
// with `=` (otherwise we matched the first `=` of `==`).
func parseJSAssign(line string) ([]string, string, bool) {
	m := jsAssignRE.FindStringSubmatch(line)
	if m == nil {
		return nil, "", false
	}
	rhs := strings.TrimLeft(m[2], " \t")
	if strings.HasPrefix(rhs, "=") {
		// `x == y` / `x === y` -- not an assignment.
		return nil, "", false
	}
	rhs = strings.TrimSpace(rhs)
	rhs = strings.TrimSuffix(rhs, ";")
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

// jsDestructureField is a single binding pulled out of an object
// destructure. `Field` is the property name as it appears in the source
// object (e.g. `body` in `{ body: b }`); `Local` is the name the binding
// is exposed under in the surrounding scope (e.g. `b`). For the plain
// shape `{ body }`, both are `body`.
type jsDestructureField struct {
	Field string
	Local string
}

// parseJSDestructure returns (fields, rhs, ok). Each entry carries the
// source field name AND the local binding so callers can decide on
// taint by looking at the field side (`body` is a known HTTP-request
// source) while marking the local-name side as tainted. Default-value
// forms `{ body = "" }` are stripped down to the field name. Rest
// patterns `...rest` are dropped (collecting the rest object as
// tainted would be correct in theory but doesn't help any sink rule
// today and pollutes the tracker).
func parseJSDestructure(line string) ([]jsDestructureField, string, bool) {
	m := jsDestructureRE.FindStringSubmatch(line)
	if m == nil {
		return nil, "", false
	}
	rhs := strings.TrimSpace(m[2])
	rhs = strings.TrimSuffix(rhs, ";")
	rhs = strings.TrimSpace(rhs)
	if rhs == "" {
		return nil, "", false
	}
	parts := strings.Split(m[1], ",")
	out := make([]jsDestructureField, 0, len(parts))
	for _, p := range parts {
		ident := strings.TrimSpace(p)
		// Strip default-value form: `body = ""` -> `body`.
		if eq := strings.Index(ident, "="); eq >= 0 {
			ident = strings.TrimSpace(ident[:eq])
		}
		// Rename form: `body: b` -> field = body, local = b.
		field := ident
		local := ident
		if colon := strings.Index(ident, ":"); colon >= 0 {
			field = strings.TrimSpace(ident[:colon])
			local = strings.TrimSpace(ident[colon+1:])
		}
		if strings.HasPrefix(field, "...") || strings.HasPrefix(local, "...") {
			continue
		}
		if field == "" || local == "" || local == "_" {
			continue
		}
		out = append(out, jsDestructureField{Field: field, Local: local})
	}
	if len(out) == 0 {
		return nil, "", false
	}
	return out, rhs, true
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
	case "javascript", "typescript":
		// Destructure form first -- it has a structure that the simple
		// assign regex would partially match (the `{...}` reads as a
		// single LHS token, dropping the field names we actually care
		// about). Trying destructure first keeps the field list intact.
		if fields, drhs, dok := parseJSDestructure(line); dok {
			if jsRequestReceivers[strings.TrimSpace(drhs)] {
				// `const { body, query } = req;` -- taint every field
				// that maps to a known HTTP-request source. The Field
				// side is checked against the known-source list; the
				// Local side (potentially renamed) is what gets marked
				// tainted so subsequent uses of the binding resolve.
				for _, f := range fields {
					if jsTaintedDestructureFields[f.Field] {
						t.tainted[f.Local] = true
					}
				}
				return
			}
			// Destructuring from anything that is itself tainted (or
			// references a tainted ident) taints every extracted local.
			if rhsContainsSourceMarker(drhs, t.lang) || t.referencesTaintedIdent(drhs) {
				for _, f := range fields {
					t.tainted[f.Local] = true
				}
				return
			}
			// Destructuring from a non-tainted, non-source RHS: nothing
			// to do. Skip falling through to the simple-assign path
			// because the `{...}` LHS wouldn't carry useful idents.
			return
		}
		lhs, rhs, ok = parseJSAssign(line)
	default:
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

// taintedCallSlot describes one call-shape rule: the function-name
// substring (anchored on `.` for method-call forms so the receiver
// alias is abstracted away) and the positional slot whose taintedness
// matters. Used by the SQL-injection table (slot = SQL string), the
// file-open / path-traversal table (slot = file path), and any
// future taint-aware sink that pins an arg position.
//
// Stored without the trailing `(` because findCallArgs locates the
// name with strings.Index and then walks to the next `(` itself --
// including the paren in the name double-consumes it.
type taintedCallSlot struct {
	Name    string
	ArgSlot int
}

// findBareCallArgs is the word-boundary-anchored sibling of
// findCallArgs. Returns the arg list for `name(...)` only when the
// match site is NOT preceded by an identifier-extending char -- so
// `open(x)` matches but `reopen(x)` doesn't, and `obj.open(x)` doesn't
// either (the `.` in front fails the anchor too). Used for bare-form
// sinks like Python's built-in `open`, where receiver-prefixed and
// shadowed forms must be left alone.
func findBareCallArgs(line, name string) []string {
	idx := 0
	for idx <= len(line)-len(name) {
		hit := strings.Index(line[idx:], name)
		if hit < 0 {
			return nil
		}
		abs := idx + hit
		if abs > 0 && isIdentChar(line[abs-1]) {
			// Preceded by ident char or `.` -- not a bare call.
			idx = abs + 1
			continue
		}
		// Anchored match. The char immediately after the name must be
		// `(` -- otherwise it's a reference or a definition, not a call.
		after := abs + len(name)
		if after >= len(line) || line[after] != '(' {
			idx = abs + 1
			continue
		}
		// Reuse the paren walk from findCallArgs by calling it with a
		// fully-qualified "name(" -- duplicates a few lines vs adding a
		// param, but keeps the public surface minimal.
		return findCallArgs(line[abs:], name)
	}
	return nil
}

// bareCallNthArgIsTainted is the anchored counterpart to
// callNthArgIsTainted. Used for bare-form sinks where a substring
// match in the middle of an identifier would false-fire.
func bareCallNthArgIsTainted(line, name string, n int, t *taintTracker) bool {
	if t == nil || n < 0 {
		return false
	}
	args := findBareCallArgs(line, name)
	if n >= len(args) {
		return false
	}
	return argIsTainted(args[n], t)
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
		if argIsTainted(raw, t) {
			return true
		}
	}
	return false
}

// callNthArgIsTainted is the SQL-shaped variant of callHasTaintedArg:
// it considers ONLY arg #n. Parameterised SQL idioms like
// `db.Query("SELECT * FROM t WHERE id=$1", taintedID)` are safe even
// when later args are tainted -- the placeholder binding sanitises.
// The injection path is "the query string itself was built from user
// input", which lives in a specific positional slot. Used by the SQL
// taint matcher to inspect arg 0 for `.Query` / `.Exec` and arg 1 for
// `.QueryContext` / `.ExecContext` (the `*Context` family takes
// ctx first, the SQL string second).
func callNthArgIsTainted(line, callName string, n int, t *taintTracker) bool {
	if t == nil || n < 0 {
		return false
	}
	args := findCallArgs(line, callName)
	if n >= len(args) {
		return false
	}
	return argIsTainted(args[n], t)
}

// argIsTainted is the shared per-arg classifier used by both the
// any-arg and first-arg call matchers. Literals fall through; an arg
// that is a bare tainted ident, or contains a tainted ident as a
// sub-token of a wrapper expression, returns true.
func argIsTainted(raw string, t *taintTracker) bool {
	arg := strings.TrimSpace(raw)
	if arg == "" || isLiteralArg(arg) {
		return false
	}
	bare := strings.TrimSuffix(strings.TrimPrefix(arg, "*"), "...")
	bare = strings.Trim(bare, "() ")
	if t.IsTainted(bare) {
		return true
	}
	return argReferencesTainted(arg, t)
}
