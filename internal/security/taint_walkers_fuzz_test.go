package security

import (
	"strings"
	"testing"
)

// FuzzTaintCallWalkers drives the taint-analysis paren/quote walkers over
// arbitrary source-code lines. ScanASTRules feeds these every line of every
// scanned file, so they parse fully untrusted bytes: a stray panic in a
// security scanner would abort a scan on a hostile-looking but legal source
// file. The contract under test is "never panic for any (line, name)", and —
// for the args walkers — every returned argument must be reconstructable from
// the input (no invented bytes).
func FuzzTaintCallWalkers(f *testing.F) {
	seeds := []struct{ line, name string }{
		{`run("rm -rf " + x)`, "run"},
		{`db.Query(q, a, b)`, "Query"},
		{`open(path)`, "open"},
		{`reopen(path)`, "open"},   // shadowed — findBareCallArgs must reject
		{`obj.open(path)`, "open"}, // receiver-prefixed — bare must reject
		{`f("a, b", c)`, "f"},      // comma inside a string literal
		{`g('it\'s', x)`, "g"},     // escaped quote inside literal
		{"h(`back,tick`, y)", "h"}, // backtick string
		{`nested(a(b, c), d)`, "nested"},
		{`unbalanced(a, b`, "unbalanced"}, // missing close paren
		{`noparen`, "noparen"},
		{`weird())(`, "weird"},
		{`(`, ""},             // degenerate: empty name, lone paren
		{``, ``},              // both empty
		{`call(`, "call"},     // open paren then EOL
		{`call()`, "call"},    // empty arg list
		{`call(   )`, "call"}, // whitespace-only args
		{`x = req.body`, "req"},
	}
	for _, s := range seeds {
		f.Add(s.line, s.name)
	}

	f.Fuzz(func(t *testing.T, line, name string) {
		// All of these must tolerate any input without panicking.
		argsA := findCallArgs(line, name)
		argsB := findBareCallArgs(line, name)
		_ = splitLHS(line)
		_ = splitArgs(line)
		parseGoAssign(line)
		parsePythonAssign(line)
		parseJSAssign(line)
		parseJSDestructure(line)

		// NOTE: we do NOT assert any implication between the two walkers'
		// hit/miss. findCallArgs anchors on the FIRST substring occurrence of
		// the name and parses from there; findBareCallArgs scans forward to the
		// first identifier-anchored call site, which can be a LATER position. So
		// bare can match a valid call that findCallArgs misses (its first-
		// occurrence anchor lands on an unbalanced-paren dead end) — and vice
		// versa. They legitimately pick different sites; only the no-panic and
		// no-invented-bytes contracts below are real cross-walker invariants.

		// Every returned arg, stripped of surrounding whitespace, must be a
		// substring of the original line — the walkers slice, they never
		// fabricate. (splitArgs trims each element, so compare trimmed.)
		for _, set := range [][]string{argsA, argsB} {
			for _, a := range set {
				trimmed := strings.TrimSpace(a)
				if trimmed != "" && !strings.Contains(line, trimmed) {
					t.Fatalf("walker invented an arg not present in the line\n line=%q\n arg=%q", line, a)
				}
			}
		}
	})
}
