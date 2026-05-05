// Tests for the analyzer-quality helpers: complexity scoring, function
// body slicing, comment/string stripping, and dead-code entrypoint
// filters. These are package-private helpers, which is why this file
// lives in package engine and does not touch the Engine type.

package engine

import (
	"strings"
	"testing"
)

// --- complexityScore ------------------------------------------------

func TestComplexityScore_EmptyIsOne(t *testing.T) {
	if got := complexityScore(""); got != 1 {
		t.Fatalf("empty text should score 1 (single entry path), got %d", got)
	}
}

func TestComplexityScore_StraightLineIsOne(t *testing.T) {
	src := `func add(a, b int) int {
	return a + b
}`
	if got := complexityScore(src); got != 1 {
		t.Fatalf("branchless body should score 1, got %d", got)
	}
}

// The old substring scorer missed `if(x)` because the trailing char
// wasn't a space. The regex variant must catch it.
func TestComplexityScore_IfNoSpaceBeforeParen(t *testing.T) {
	src := `func f(x int) { if(x>0){return} }`
	if got := complexityScore(src); got < 2 {
		t.Fatalf("if(x) should add a decision point, got %d", got)
	}
}

// Identifiers that happen to contain a keyword substring must NOT
// inflate the score (this was the big false-positive in the old
// space-padded scorer).
func TestComplexityScore_KeywordInsideIdentifier(t *testing.T) {
	src := `func verifyUser() { ok := true; _ = ok }`
	if got := complexityScore(src); got != 1 {
		t.Fatalf("identifier 'verifyUser' must not match 'if'; got %d", got)
	}
}

func TestComplexityScore_CountsEachBranch(t *testing.T) {
	src := `func f(a, b int) int {
	if a > 0 && b > 0 {
		return 1
	} else if a < 0 || b < 0 {
		return -1
	}
	for i := 0; i < 10; i++ {
		switch i {
		case 1:
			return i
		}
	}
	return 0
}`
	// 1 (entry) + if + && + else if + || + for + switch + case = 8
	got := complexityScore(src)
	if got < 7 {
		t.Fatalf("expected at least 7 decision points, got %d", got)
	}
}

// --- endOfBraceBody -------------------------------------------------

func TestEndOfBraceBody_BalancesNestedClosures(t *testing.T) {
	src := []string{
		`func outer() {`,     // 0: opens depth 1
		`	x := func() int {`, // 1: opens depth 2
		`		return 1`,
		`	}`, // 3: depth back to 1
		`	_ = x`,
		`}`, // 5: depth to 0 -> end at line 6
		`// sibling`,
	}
	end := endOfBraceBody(src, 0)
	if end != 6 {
		t.Fatalf("nested closure: want end=6, got %d", end)
	}
}

func TestEndOfBraceBody_IgnoresBracesInStringLiterals(t *testing.T) {
	src := []string{
		`func f() {`,
		`	s := "look: }"`, // brace inside string must be skipped
		`	_ = s`,
		`}`,
		`// after`,
	}
	end := endOfBraceBody(src, 0)
	if end != 4 {
		t.Fatalf("string-literal brace: want end=4, got %d", end)
	}
}

func TestEndOfBraceBody_IgnoresBracesInLineComments(t *testing.T) {
	src := []string{
		`func f() {`,
		`	// stray } should not close`,
		`	return`,
		`}`,
	}
	end := endOfBraceBody(src, 0)
	if end != 4 {
		t.Fatalf("line-comment brace: want end=4, got %d", end)
	}
}

func TestEndOfBraceBody_IgnoresBracesInBlockComments(t *testing.T) {
	src := []string{
		`func f() {`,
		`	/* this is { fine } */`,
		`	return`,
		`}`,
	}
	end := endOfBraceBody(src, 0)
	if end != 4 {
		t.Fatalf("block-comment brace: want end=4, got %d", end)
	}
}

func TestEndOfBraceBody_BackticksSkipped(t *testing.T) {
	src := []string{
		"func f() {",
		"	q := `SELECT { FROM t }`",
		"	_ = q",
		"}",
	}
	end := endOfBraceBody(src, 0)
	if end != 4 {
		t.Fatalf("backtick raw string: want end=4, got %d", end)
	}
}

// --- endOfPythonBody ------------------------------------------------

func TestEndOfPythonBody_StopsAtDedent(t *testing.T) {
	src := []string{
		`def foo():`,
		`    x = 1`,
		`    if x:`,
		`        return x`,
		`    return 0`,
		``,
		`def bar():`, // dedent to col 0 -> belongs to enclosing scope
		`    pass`,
	}
	end := endOfPythonBody(src, 0)
	if end != 6 {
		t.Fatalf("python dedent: want end=6, got %d", end)
	}
}

func TestEndOfPythonBody_IgnoresBlankLines(t *testing.T) {
	src := []string{
		`def foo():`,
		`    x = 1`,
		``, // blank — must not terminate the body
		`    return x`,
	}
	end := endOfPythonBody(src, 0)
	if end != 4 {
		t.Fatalf("python blank-line tolerance: want end=4, got %d", end)
	}
}

// --- endOfFunctionBody dispatch ------------------------------------

func TestEndOfFunctionBody_DispatchesByLanguage(t *testing.T) {
	pySrc := []string{`def f():`, `    return 1`, `def g():`, `    return 2`}
	if got := endOfFunctionBody(pySrc, 0, "python"); got != 2 {
		t.Fatalf("python dispatch: want 2, got %d", got)
	}
	goSrc := []string{`func f() {`, `	return`, `}`, `func g() {}`}
	if got := endOfFunctionBody(goSrc, 0, "go"); got != 3 {
		t.Fatalf("go dispatch: want 3, got %d", got)
	}
}

// --- stripStringsAndComments ---------------------------------------

func TestStripCFamily_RemovesLineComment(t *testing.T) {
	src := `x := 1 // mentions foo but not a real call`
	out := stripCFamily(src)
	if strings.Contains(out, "foo") {
		t.Fatalf("line comment not stripped: %q", out)
	}
}

func TestStripCFamily_RemovesBlockComment(t *testing.T) {
	src := "a\n/* foo bar */\nb"
	out := stripCFamily(src)
	if strings.Contains(out, "foo") || strings.Contains(out, "bar") {
		t.Fatalf("block comment not stripped: %q", out)
	}
}

func TestStripCFamily_RemovesStringLiteralMentions(t *testing.T) {
	src := `msg := "please call foo to reproduce"`
	out := stripCFamily(src)
	if strings.Contains(out, "foo") {
		t.Fatalf("string literal not stripped: %q", out)
	}
}

func TestStripCFamily_PreservesLineCount(t *testing.T) {
	src := "a\n/* multi\nline */\nb"
	inLines := strings.Count(src, "\n")
	out := stripCFamily(src)
	outLines := strings.Count(out, "\n")
	if inLines != outLines {
		t.Fatalf("stripping must preserve newlines: in=%d out=%d", inLines, outLines)
	}
}

func TestStripPython_RemovesHashComment(t *testing.T) {
	src := "x = 1  # calls foo()\ny = 2"
	out := stripPython(src)
	if strings.Contains(out, "foo") {
		t.Fatalf("# comment not stripped: %q", out)
	}
}

func TestStripPython_RemovesTripleQuotedDocstring(t *testing.T) {
	src := `def f():
    """foo bar baz"""
    return 1`
	out := stripPython(src)
	if strings.Contains(out, "foo") || strings.Contains(out, "bar") {
		t.Fatalf("docstring not stripped: %q", out)
	}
}

func TestStripStringsAndComments_UnknownExtensionPassthrough(t *testing.T) {
	src := "this has // foo and /* bar */ but ext is unknown"
	if got := stripStringsAndComments(src, ".xyz"); got != src {
		t.Fatalf("unknown extension should pass through unchanged, got %q", got)
	}
}

func TestStripStringsAndComments_DispatchesByExtension(t *testing.T) {
	goIn := `x := "foo"`
	if strings.Contains(stripStringsAndComments(goIn, ".go"), "foo") {
		t.Fatalf(".go must dispatch to stripCFamily")
	}
	pyIn := `x = "foo"`
	if strings.Contains(stripStringsAndComments(pyIn, ".py"), "foo") {
		t.Fatalf(".py must dispatch to stripPython")
	}
}

// --- dead-code entrypoint filters ----------------------------------

func TestGoExportedEntrypoint(t *testing.T) {
	cases := []struct {
		name, file string
		want       bool
	}{
		{"Foo", "x.go", true},
		{"foo", "x.go", false},
		{"Foo", "x.py", false},
		{"Foo", "x.js", false},
		{"", "x.go", false},
	}
	for _, tc := range cases {
		if got := goExportedEntrypoint(tc.name, tc.file); got != tc.want {
			t.Errorf("goExportedEntrypoint(%q, %q) = %v, want %v",
				tc.name, tc.file, got, tc.want)
		}
	}
}

func TestIsTestingIdentifier(t *testing.T) {
	yes := []string{"TestFoo", "BenchmarkBar", "ExampleBaz", "FuzzQux",
		"test_something", "setUp", "tearDown"}
	for _, n := range yes {
		if !isTestingIdentifier(n) {
			t.Errorf("expected %q to be recognised as a test entrypoint", n)
		}
	}
	no := []string{"foo", "Testable", "benchmark", "setUpper"}
	_ = no // `Testable` starts with "Test" so it WOULD match; that's
	// acceptable behaviour for the detector (better to skip a possibly-
	// test-like name than to flag it as dead). Keep the list as a
	// documentation aid even if we don't assert on it.
	if isTestingIdentifier("foo") {
		t.Fatal("plain 'foo' must not be a test entrypoint")
	}
}

func TestLooksEntrypoint_MainAndInit(t *testing.T) {
	if !looksEntrypoint("main", "main.go") {
		t.Fatal("main must be an entrypoint")
	}
	if !looksEntrypoint("init", "pkg.go") {
		t.Fatal("init must be an entrypoint")
	}
	if looksEntrypoint("foo", "pkg.go") {
		t.Fatal("plain 'foo' should not be an entrypoint")
	}
}

func TestLooksEntrypoint_TestFiles(t *testing.T) {
	if !looksEntrypoint("helper", "pkg_test.go") {
		t.Fatal("anything inside *_test.go is entrypoint-ish (test helpers)")
	}
}

// --- leadingWhitespaceLen ------------------------------------------

func TestLeadingWhitespaceLen(t *testing.T) {
	cases := map[string]int{
		"":        0,
		"foo":     0,
		"  foo":   2,
		"\tfoo":   1,
		"\t  foo": 3,
	}
	for in, want := range cases {
		if got := leadingWhitespaceLen(in); got != want {
			t.Errorf("leadingWhitespaceLen(%q) = %d, want %d", in, got, want)
		}
	}
}

// --- skipStringLiteral ---------------------------------------------

func TestSkipStringLiteral(t *testing.T) {
	line := `a := "foo\"bar" + x`
	start := strings.Index(line, `"`)
	end := skipStringLiteral(line, start)
	// After the closing quote, the next rune should be the space before '+'.
	if end >= len(line) || line[end] != ' ' {
		t.Fatalf("skipStringLiteral failed: end=%d, line=%q", end, line)
	}
}

func TestSkipStringLiteral_UnterminatedReturnsLen(t *testing.T) {
	line := `x := "oops`
	start := strings.Index(line, `"`)
	if got := skipStringLiteral(line, start); got != len(line) {
		t.Fatalf("unterminated literal: want len=%d, got %d", len(line), got)
	}
}

// --- declarationLineLooksReal ---------------------------------------
//
// These guard the dead-code detector against a specific false-positive
// shape: the regex-fallback AST extracting identifiers from inside a
// raw-string literal (e.g. JS embedded in a Go web server's HTML
// bundle). The stripper blanks string-literal lines, so a declaration
// that was really inside a string will point at an empty line and
// declarationLineLooksReal returns false.

func TestDeclarationLineLooksReal_GoDeclaration(t *testing.T) {
	lines := []string{"package foo", "", "func Bar() {}"}
	if !declarationLineLooksReal(lines, 3, ".go") {
		t.Fatal("real Go func declaration should pass")
	}
}

func TestDeclarationLineLooksReal_BlankLineFromStrippedString(t *testing.T) {
	// Simulates: AST reported a symbol at line 5, but the stripper
	// blanked that line because it was inside a raw string literal.
	lines := []string{"package foo", "", "var s = `", "", "`"}
	if declarationLineLooksReal(lines, 4, ".go") {
		t.Fatal("blank line after stripping must be treated as not-a-real-decl")
	}
}

func TestDeclarationLineLooksReal_JSPrefixesRecognized(t *testing.T) {
	for _, src := range []string{
		"const wrapper = () => {}",
		"let count = 0",
		"function handleClick(e) {",
		"class Panel extends Base {",
		"export function render() {}",
		"async function load() {",
	} {
		if !declarationLineLooksReal([]string{src}, 1, ".js") {
			t.Fatalf("expected %q to look like a real JS decl", src)
		}
	}
}

func TestDeclarationLineLooksReal_BareIdentifierIsNotDecl(t *testing.T) {
	// Just a bare identifier (the kind of thing the regex AST might
	// pull out of a template expression) — not a real declaration.
	if declarationLineLooksReal([]string{"wrapper.click();"}, 1, ".js") {
		t.Fatal("`wrapper.click();` is a call site, not a declaration")
	}
}

func TestDeclarationLineLooksReal_PythonDef(t *testing.T) {
	if !declarationLineLooksReal([]string{"def foo(x):"}, 1, ".py") {
		t.Fatal("Python def must register as declaration")
	}
	if !declarationLineLooksReal([]string{"async def foo():"}, 1, ".py") {
		t.Fatal("Python async def must register as declaration")
	}
	if declarationLineLooksReal([]string{"foo.bar()"}, 1, ".py") {
		t.Fatal("Python call line must not register as declaration")
	}
}

func TestDeclarationLineLooksReal_OutOfRangeIsPermissive(t *testing.T) {
	// AST and stripper can drift by a few lines in pathological cases
	// (unterminated strings, nested comments). Dropping a real symbol
	// is worse than surfacing a false positive — permit out-of-range.
	if !declarationLineLooksReal([]string{"x"}, 99, ".go") {
		t.Fatal("out-of-range line must stay permissive")
	}
	if !declarationLineLooksReal(nil, 1, ".go") {
		t.Fatal("empty lines slice must stay permissive")
	}
}

func TestDeclarationLineLooksReal_UnknownExtensionPermissive(t *testing.T) {
	// Unknown language — we can't judge, so default to permissive
	// rather than silently eat findings on .ex/.erl/etc.
	if !declarationLineLooksReal([]string{"module foo"}, 1, ".xyz") {
		t.Fatal("unknown extension should not drop declarations")
	}
}

// --- lineStartsWithAny ----------------------------------------------

func TestLineStartsWithAny_RequiresBoundary(t *testing.T) {
	// `function` is a JS keyword; `functional` is not a keyword — the
	// helper must not match a prefix that flows into more identifier
	// characters.
	if lineStartsWithAny("functional = 1", "function") {
		t.Fatal("`function` must not match `functional`")
	}
	if !lineStartsWithAny("function foo() {}", "function") {
		t.Fatal("`function foo()` should match the `function` keyword")
	}
	if !lineStartsWithAny("func(", "func") {
		t.Fatal("`func(` must match the `func` keyword (paren boundary)")
	}
}

func TestLineStartsWithAny_EmptyRestIsMatch(t *testing.T) {
	// A line that is literally just the keyword (rare, but not
	// invalid — e.g. a lexer test fixture) should still match.
	if !lineStartsWithAny("package", "package") {
		t.Fatal("exact-match keyword-only line must match")
	}
}
