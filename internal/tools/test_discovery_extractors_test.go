package tools

import (
	"testing"
)

// ─── extractGoTests ───────────────────────────────────────────────────────────

func TestExtractGoTests(t *testing.T) {
	lines := []string{
		"package foo",
		"import \"testing\"",
		"",
		"func TestFoo(t *testing.T) {}",
		"func TestBar(t *testing.T) {}",
		"func BenchmarkBaz(b *testing.B) {}",
		"func ExampleQux() {}",
		"func helper() {}",
		"func TestLong(t *testing.T) {}",
	}
	result := extractGoTests(lines, "")

	if len(result) != 5 {
		t.Fatalf("expected 5 functions, got %d", len(result))
	}

	names := make(map[string]bool)
	for _, f := range result {
		names[f["name"].(string)] = true
	}

	for _, want := range []string{
		"TestFoo(t *testing.T)",
		"TestBar(t *testing.T)",
		"BenchmarkBaz(b *testing.B)",
		"ExampleQux()",
		"TestLong(t *testing.T)",
	} {
		if !names[want] {
			t.Errorf("expected %q in results, got %v", want, names)
		}
	}
	if names["helper()"] || names["func helper()"] {
		t.Error("helper() should not appear")
	}
}

func TestExtractGoTests_SymbolFilter(t *testing.T) {
	lines := []string{
		"func TestFoo(t *testing.T) {}",
		"    // uses foo",
		"func TestBar(t *testing.T) {}",
		"    // unrelated",
		"func TestFooBaz(t *testing.T) {}",
	}

	result := extractGoTests(lines, "foo")

	if len(result) != 3 {
		t.Fatalf("expected 3 functions matching 'foo', got %d", len(result))
	}

	names := make(map[string]bool)
	for _, f := range result {
		names[f["name"].(string)] = true
	}

	if !names["TestFoo(t *testing.T)"] {
		t.Error("TestFoo should match 'foo'")
	}
	if !names["TestBar(t *testing.T)"] {
		t.Error("TestBar should match 'foo' (has 'foo' in nearby line)")
	}
	if !names["TestFooBaz(t *testing.T)"] {
		t.Error("TestFooBaz should match 'foo'")
	}
}

func TestExtractGoTests_LineNumbers(t *testing.T) {
	lines := []string{
		"",
		"func TestStart(t *testing.T) {}",
		"",
		"func TestEnd(t *testing.T) {}",
	}
	result := extractGoTests(lines, "")

	if len(result) != 2 {
		t.Fatalf("expected 2, got %d", len(result))
	}
	if result[0]["line"].(int) != 2 {
		t.Errorf("TestStart line: expected 2, got %d", result[0]["line"])
	}
	if result[1]["line"].(int) != 4 {
		t.Errorf("TestEnd line: expected 4, got %d", result[1]["line"])
	}
}

func TestExtractGoTests_EmptyInput(t *testing.T) {
	if got := extractGoTests(nil, ""); len(got) != 0 {
		t.Errorf("nil: expected 0, got %d", len(got))
	}
	if got := extractGoTests([]string{}, ""); len(got) != 0 {
		t.Errorf("empty: expected 0, got %d", len(got))
	}
}

// ─── extractPythonTests ───────────────────────────────────────────────────────

func TestExtractPythonTests(t *testing.T) {
	lines := []string{
		"import pytest",
		"",
		"def test_foo(): pass",
		"async def test_bar(): pass",
		"def test_baz(): pass",
		"def helper(): pass",
		"def test_long_name(): pass",
	}
	result := extractPythonTests(lines, "")

	if len(result) != 4 {
		t.Fatalf("expected 4, got %d", len(result))
	}

	names := make(map[string]bool)
	for _, f := range result {
		names[f["name"].(string)] = true
	}

	for _, want := range []string{"test_foo", "test_bar", "test_baz", "test_long_name"} {
		if !names[want] {
			t.Errorf("expected %q in results", want)
		}
	}
	if names["helper"] {
		t.Error("helper should not appear")
	}
}

func TestExtractPythonTests_SymbolFilter(t *testing.T) {
	lines := []string{
		"def test_foo(): pass",
		"def test_bar(): pass",
	}
	result := extractPythonTests(lines, "foo")
	if len(result) != 1 {
		t.Fatalf("expected 1, got %d", len(result))
	}
	if result[0]["name"].(string) != "test_foo" {
		t.Errorf("expected test_foo, got %s", result[0]["name"])
	}
}

func TestExtractPythonTests_TabIndent(t *testing.T) {
	lines := []string{
		"def\ttest_tab(): pass",
		"    def\ttest_space(): pass",
	}
	result := extractPythonTests(lines, "")
	if len(result) != 2 {
		t.Fatalf("expected 2, got %d", len(result))
	}
}

// ─── extractJSTests ──────────────────────────────────────────────────────────

func TestExtractJSTests(t *testing.T) {
	lines := []string{
		`describe('suite', () => {})`,
		`  it('should work', () => {})`,
		`  test('another test', () => {})`,
		`  spec('spec style', () => {})`,
		`  // not a test`,
	}
	result := extractJSTests(lines, "")

	if len(result) != 4 {
		t.Fatalf("expected 4, got %d", len(result))
	}

	byName := make(map[string]string)
	for _, f := range result {
		byName[f["name"].(string)] = f["kind"].(string)
	}

	if byName["suite"] != "describe" {
		t.Errorf("describe kind: expected 'describe', got %s", byName["suite"])
	}
	if byName["should work"] != "it" {
		t.Errorf("first it: expected 'it', got %s", byName["should work"])
	}
	if byName["another test"] != "test" {
		t.Errorf("test(): expected 'test', got %s", byName["another test"])
	}
	if byName["spec style"] != "spec" {
		t.Errorf("spec(): expected 'spec', got %s", byName["spec style"])
	}
}

func TestExtractJSTests_SymbolFilter(t *testing.T) {
	lines := []string{
		`it('foo works', () => {})`,
		`it('bar fails', () => {})`,
	}
	result := extractJSTests(lines, "foo")

	if len(result) != 1 {
		t.Fatalf("expected 1, got %d", len(result))
	}
	if result[0]["name"].(string) != "foo works" {
		t.Errorf("expected 'foo works', got %s", result[0]["name"])
	}
}

func TestExtractJSTests_DoubleQuotes(t *testing.T) {
	lines := []string{`it("double quote", () => {})`}
	result := extractJSTests(lines, "")
	if len(result) != 1 {
		t.Fatalf("expected 1, got %d", len(result))
	}
	if result[0]["name"].(string) != "double quote" {
		t.Errorf("expected 'double quote', got %s", result[0]["name"])
	}
}

// ─── extractJavaTests ────────────────────────────────────────────────────────

func TestExtractJavaTests(t *testing.T) {
	lines := []string{
		"@Test",
		"public void testFoo() {}",
		"",
		"void notATest() {}",
		"@Before",
		"public void setup() {}",
		"@Test",
		"public static void testBar() {}",
	}
	result := extractJavaTests(lines, "")

	if len(result) != 2 {
		t.Fatalf("expected 2, got %d", len(result))
	}

	names := make(map[string]bool)
	for _, f := range result {
		names[f["name"].(string)] = true
	}

	if !names["testFoo"] {
		t.Error("testFoo should be found")
	}
	if !names["testBar"] {
		t.Error("testBar should be found")
	}
	if names["notATest"] {
		t.Error("notATest (no @Test) should not appear")
	}
	if names["setup"] {
		t.Error("setup (@Before, not @Test) should not appear")
	}
}

func TestExtractJavaTests_SymbolFilter(t *testing.T) {
	lines := []string{
		"@Test",
		"public void testFoo() {}",
		"",
		"@Test",
		"public void testBar() {}",
	}
	result := extractJavaTests(lines, "foo")
	if len(result) != 1 {
		t.Fatalf("expected 1, got %d", len(result))
	}
	if result[0]["name"].(string) != "testFoo" {
		t.Errorf("expected testFoo, got %s", result[0]["name"])
	}
}

func TestExtractJavaTests_NoModifier(t *testing.T) {
	lines := []string{"@Test", "void testNoModifier() {}"}
	result := extractJavaTests(lines, "")
	if len(result) != 1 {
		t.Fatalf("expected 1, got %d", len(result))
	}
	if result[0]["name"].(string) != "testNoModifier" {
		t.Errorf("expected testNoModifier, got %s", result[0]["name"])
	}
}

// ─── extractRustTests ────────────────────────────────────────────────────────

func TestExtractRustTests(t *testing.T) {
	lines := []string{
		"    #[test]",
		"    fn test_foo() {}",
		"    ",
		"    fn not_a_test() {}",
		"    #[test]",
		"    async fn test_bar() {}",
	}
	result := extractRustTests(lines, "")

	if len(result) != 2 {
		t.Fatalf("expected 2, got %d", len(result))
	}

	names := make(map[string]bool)
	for _, f := range result {
		names[f["name"].(string)] = true
	}

	if !names["test_foo"] {
		t.Error("test_foo should be found")
	}
	if !names["test_bar"] {
		t.Error("test_bar should be found (async)")
	}
	if names["not_a_test"] {
		t.Error("not_a_test should not appear")
	}
}

// ─── ExtractTestFunctions dispatcher ────────────────────────────────────────
// These test the public API by passing actual file contents inline,
// bypassing file system reads so tests are deterministic and portable.

func TestExtractTestFunctions_Go(t *testing.T) {
	fileContents := "func TestGo(t *testing.T) {}"
	result := ExtractTestFunctions("foo_test.go", fileContents, "")
	if len(result) != 1 {
		t.Fatalf("go dispatcher: expected 1, got %d", len(result))
	}
	if result[0]["name"].(string) != "TestGo(t *testing.T)" {
		t.Errorf("got %s", result[0]["name"])
	}
}

func TestExtractTestFunctions_Python(t *testing.T) {
	fileContents := "def test_py(): pass"
	result := ExtractTestFunctions("foo_test.py", fileContents, "")
	if len(result) != 1 {
		t.Fatalf("python dispatcher: expected 1, got %d", len(result))
	}
	if result[0]["name"].(string) != "test_py" {
		t.Errorf("got %s", result[0]["name"])
	}
}

func TestExtractTestFunctions_JS(t *testing.T) {
	fileContents := `it('js works', () => {})`
	result := ExtractTestFunctions("foo.spec.js", fileContents, "")
	if len(result) != 1 {
		t.Fatalf("js dispatcher: expected 1, got %d", len(result))
	}
	if result[0]["name"].(string) != "js works" {
		t.Errorf("got %s", result[0]["name"])
	}
}

func TestExtractTestFunctions_Java(t *testing.T) {
	fileContents := "@Test\npublic void testJava() {}"
	result := ExtractTestFunctions("FooTest.java", fileContents, "")
	if len(result) != 1 {
		t.Fatalf("java dispatcher: expected 1, got %d", len(result))
	}
	if result[0]["name"].(string) != "testJava" {
		t.Errorf("got %s", result[0]["name"])
	}
}

func TestExtractTestFunctions_Rust(t *testing.T) {
	fileContents := "#[test]\nfn test_rust() {}"
	result := ExtractTestFunctions("foo_test.rs", fileContents, "")
	if len(result) != 1 {
		t.Fatalf("rust dispatcher: expected 1, got %d", len(result))
	}
	if result[0]["name"].(string) != "test_rust" {
		t.Errorf("got %s", result[0]["name"])
	}
}

func TestExtractTestFunctions_UnknownExtension(t *testing.T) {
	fileContents := "fn main() {}"
	result := ExtractTestFunctions("foo.unknown", fileContents, "")
	if len(result) != 0 {
		t.Errorf("expected 0 for unknown extension, got %d", len(result))
	}
}

func TestExtractTestFunctions_GenericFallback(t *testing.T) {
	fileContents := "describe('my suite', () => {})"
	result := ExtractTestFunctions("foo.custom", fileContents, "")
	if len(result) != 1 {
		t.Fatalf("generic fallback: expected 1, got %d", len(result))
	}
	if result[0]["kind"].(string) != "describe" {
		t.Errorf("expected kind 'describe', got %s", result[0]["kind"])
	}
}
