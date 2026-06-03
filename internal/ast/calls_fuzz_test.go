package ast

import (
	"strings"
	"testing"
)

// FuzzExtractCalls drives the call-site extractor over arbitrary source for
// every wired language. ExtractCalls feeds the security scanner's call-graph
// consumers and slices the raw line by regex-derived byte offsets
// (line[start:m[3]], line[start-1]); a stray panic would abort a scan on a
// legal-but-odd source file. The contract: never panic, every reported callee
// is a non-empty substring physically present on its 1-based source line, and
// no reported line number falls outside the file.
func FuzzExtractCalls(f *testing.F) {
	langs := []string{"go", "python", "javascript", "typescript", "jsx", "tsx", "ruby", "java", ""}
	seeds := []struct {
		src  string
		lang uint8
	}{
		{"foo(x)\nbar(y, z)", 0},
		{"if(check(x)) { run() }", 2},      // back-to-back calls
		{"obj.method(a).chain(b)", 0},      // dotted + chained
		{"func foo() {}\nfoo()", 0},        // declaration then call
		{"  print('hi')\n  # comment", 1},  // python comment + call
		{"a()b()c()", 0},                   // adjacent
		{"(((", 0},                         // only opens
		{")))", 0},                         // only closes
		{"", 0},                            // empty
		{"\n\n\n", 0},                      // only newlines
		{"x.y.z.w(deep.dotted.call())", 0}, // deep dotted nesting
		{"$jq.fn$(a)", 2},                  // JS $ identifiers
		{"日本語(x)", 0},                      // non-ASCII before paren
		{"def(", 1},                        // keyword-ish open
	}
	for _, s := range seeds {
		f.Add(s.src, s.lang)
	}

	f.Fuzz(func(t *testing.T, src string, langSel uint8) {
		lang := langs[int(langSel)%len(langs)]

		calls := ExtractCalls(lang, []byte(src)) // must never panic

		if calls == nil {
			return
		}
		lines := strings.Split(src, "\n")
		for _, c := range calls {
			if c.Callee == "" {
				t.Fatalf("empty callee reported (lang=%q src=%q)", lang, src)
			}
			if c.Line < 1 || c.Line > len(lines) {
				t.Fatalf("callee %q has line %d outside [1,%d] (lang=%q)", c.Callee, c.Line, len(lines), lang)
			}
			// The callee text must physically appear on the line it was
			// reported from — the extractor slices, it never fabricates.
			if !strings.Contains(lines[c.Line-1], c.Callee) {
				t.Fatalf("callee %q not present on its source line %q (lang=%q)", c.Callee, lines[c.Line-1], lang)
			}
		}
	})
}
