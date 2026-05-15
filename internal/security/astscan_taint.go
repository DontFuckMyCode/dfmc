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
	rawLHS := m[1]
	rhs := strings.TrimSpace(m[2])
	parts := strings.Split(rawLHS, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		ident := strings.TrimSpace(p)
		if ident == "" || ident == "_" {
			continue
		}
		out = append(out, ident)
	}
	if len(out) == 0 {
		return nil, "", false
	}
	return out, rhs, true
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
	if t.lang != "go" {
		// Other languages (python/js) plug in later. Today's contract is
		// Go-only; documented limitation in astscan_taint.go header.
		return
	}
	lhs, rhs, ok := parseGoAssign(line)
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
// the propagation pass. Conservative: matches on the outermost
// identifiers only; nested-call RHS like `f(g(body))` still works
// because `body` appears as a bare token.
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
		for j < len(rhs) && isIdentChar(rhs[j]) {
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
