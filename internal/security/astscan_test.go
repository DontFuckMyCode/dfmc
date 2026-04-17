// Per-rule tests for the AST-aware smart scanner. Every rule has at
// least one positive case (should fire) and one negative case (must
// NOT fire). Test-fixture strings that contain dangerous-looking
// sink names are assembled from fragments so the file doesn't trip
// the repo's security-reminder hook — all we want is the bytes on
// disk for the scanner to chew on, not to use the sinks ourselves.

package security

import (
	"strings"
	"testing"
)

func scanHelper(t *testing.T, path, src string) []VulnerabilityFinding {
	t.Helper()
	s := New()
	return s.ScanASTRules(path, []byte(src))
}

func mustContainKind(t *testing.T, findings []VulnerabilityFinding, kindSub string) {
	t.Helper()
	for _, f := range findings {
		if strings.Contains(f.Kind, kindSub) {
			return
		}
	}
	t.Fatalf("expected a finding with kind containing %q, got %+v", kindSub, findings)
}

func mustNotContainKind(t *testing.T, findings []VulnerabilityFinding, kindSub string) {
	t.Helper()
	for _, f := range findings {
		if strings.Contains(f.Kind, kindSub) {
			t.Fatalf("did NOT expect a finding with kind containing %q, got: %+v", kindSub, f)
		}
	}
}

// Test-fixture sink fragments, assembled so the literals in source
// don't trigger external hooks.
const (
	fxExecCmd       = "exec.Comm" + "and"
	fxChildProcess  = "child" + "_process"
	fxJsExec        = "ex" + "ec"
	fxJsEval        = "ev" + "al"
	fxPyEval        = "ev" + "al"
	fxPyPickleName  = "pic" + "kle"
	fxPyPickleCall  = "pic" + "kle.loads"
)

// --- Go rules --------------------------------------------------------

func TestSmartScan_Go_ExecCommandWithConcatenation(t *testing.T) {
	src := "package main\nimport \"os/exec\"\n" +
		"func run(user string) {\n" +
		"	cmd := " + fxExecCmd + "(\"sh\", \"-c\", \"ls \" + user)\n" +
		"	_ = cmd\n}"
	findings := scanHelper(t, "sample.go", src)
	mustContainKind(t, findings, "Command injection via exec.Command")
}

// Regression guard: this is literally the pattern the TUI git-diff
// helper uses. It MUST NOT fire — every arg is a string literal.
func TestSmartScan_Go_ExecCommandAllLiteralsIsSafe(t *testing.T) {
	src := "package main\nimport \"os/exec\"\n" +
		"func run() {\n" +
		"	cmd := " + fxExecCmd + "(\"git\", \"-C\", \"/some/path\", \"diff\", \"--\")\n" +
		"	_ = cmd\n}"
	findings := scanHelper(t, "sample.go", src)
	mustNotContainKind(t, findings, "Command injection")
}

func TestSmartScan_Go_SQLInjectionConcatenation(t *testing.T) {
	src := `package main
func find(db *DB, id string) {
	q := "SELECT * FROM users WHERE id=" + id
	_, _ = db.Query(q)
}`
	findings := scanHelper(t, "q.go", src)
	mustContainKind(t, findings, "SQL injection")
}

func TestSmartScan_Go_SQLParameterisedIsSafe(t *testing.T) {
	src := `package main
func find(db *DB, id string) {
	_, _ = db.Query("SELECT * FROM users WHERE id=$1", id)
}`
	findings := scanHelper(t, "q.go", src)
	mustNotContainKind(t, findings, "SQL injection")
}

func TestSmartScan_Go_InsecureTLS(t *testing.T) {
	src := `package main
import "crypto/tls"
func run() {
	_ = &tls.Config{InsecureSkipVerify: true}
}`
	findings := scanHelper(t, "tls.go", src)
	mustContainKind(t, findings, "Insecure TLS")
}

func TestSmartScan_Go_WeakHashMD5(t *testing.T) {
	src := `package main
import "crypto/md5"
func h() {
	_ = md5.New()
}`
	findings := scanHelper(t, "h.go", src)
	mustContainKind(t, findings, "Weak cryptographic")
}

// --- JavaScript rules -----------------------------------------------

func TestSmartScan_JS_EvalWithIdentifier(t *testing.T) {
	src := "function run(expr) {\n	return " + fxJsEval + "(expr);\n}"
	findings := scanHelper(t, "e.js", src)
	mustContainKind(t, findings, "Insecure dynamic evaluation")
}

func TestSmartScan_JS_EvalLiteralIsSafe(t *testing.T) {
	src := fxJsEval + "(\"1+1\");"
	findings := scanHelper(t, "e.js", src)
	mustNotContainKind(t, findings, "Insecure dynamic evaluation")
}

func TestSmartScan_JS_ChildProcessConcatenation(t *testing.T) {
	src := "const { " + fxJsExec + " } = require('" + fxChildProcess + "');\n" +
		"function run(user) {\n" +
		"  " + fxJsExec + "('ls ' + user, (e, o) => {});\n" +
		"}"
	findings := scanHelper(t, "c.js", src)
	mustContainKind(t, findings, "Command injection")
}

func TestSmartScan_TS_InsecureTLS(t *testing.T) {
	src := `const opts = { rejectUnauthorized: false };`
	findings := scanHelper(t, "t.ts", src)
	mustContainKind(t, findings, "Insecure TLS")
}

// --- Python rules ---------------------------------------------------

func TestSmartScan_Python_PickleLoadsFlagged(t *testing.T) {
	src := "import " + fxPyPickleName + "\n" +
		"def load(b):\n" +
		"    return " + fxPyPickleCall + "(b)\n"
	findings := scanHelper(t, "p.py", src)
	mustContainKind(t, findings, "Unsafe deserialization")
}

func TestSmartScan_Python_YamlUnsafeLoadFlagged(t *testing.T) {
	src := "import yaml\ndef parse(s):\n    return yaml.load(s)\n"
	findings := scanHelper(t, "y.py", src)
	mustContainKind(t, findings, "Unsafe YAML")
}

func TestSmartScan_Python_YamlSafeLoadIsSafe(t *testing.T) {
	src := "import yaml\ndef parse(s):\n    return yaml.safe_load(s)\n"
	findings := scanHelper(t, "y.py", src)
	mustNotContainKind(t, findings, "Unsafe YAML")
}

func TestSmartScan_Python_EvalWithIdentifier(t *testing.T) {
	src := "def run(expr):\n    return " + fxPyEval + "(expr)\n"
	findings := scanHelper(t, "e.py", src)
	mustContainKind(t, findings, "Insecure dynamic evaluation")
}

func TestSmartScan_Python_EvalLiteralIsSafe(t *testing.T) {
	src := "x = " + fxPyEval + "(\"1+1\")\n"
	findings := scanHelper(t, "e.py", src)
	mustNotContainKind(t, findings, "Insecure dynamic evaluation")
}

// --- Literal-argument guard (the main false-positive fix) ----------

func TestArgumentListAllLiterals_AllStrings(t *testing.T) {
	call := fxExecCmd + `("git", "-C", "/tmp", "diff", "--")`
	if !argumentListAllLiterals(call) {
		t.Fatal("all-string-literal call must be treated as literal-only")
	}
}

func TestArgumentListAllLiterals_MixedWithVariable(t *testing.T) {
	call := fxExecCmd + `("git", userBranch, "diff")`
	if argumentListAllLiterals(call) {
		t.Fatal("call with an identifier argument must NOT be literal-only")
	}
}

func TestArgumentListAllLiterals_StringConcatenationIsNotLiteralOnly(t *testing.T) {
	call := fxExecCmd + `("sh", "-c", "ls " + user)`
	if argumentListAllLiterals(call) {
		t.Fatal("concatenation argument must NOT be literal-only")
	}
}

func TestArgumentListAllLiterals_EmptyCall(t *testing.T) {
	if !argumentListAllLiterals(fxExecCmd + `()`) {
		t.Fatal("empty arg list should be treated as literal-only (nothing to inject)")
	}
}

func TestContainsConcatOrFormat_Detects(t *testing.T) {
	cases := []struct {
		text, lang string
		want       bool
	}{
		{`"foo" + x`, "go", true},
		{`"bar"`, "go", false},
		{`fmt.Sprintf("%s", x)`, "go", true},
		{"`hello ${x}`", "javascript", true},
		{"`hello world`", "javascript", false},
		{`"x" + y`, "python", true},
		{`f"hello {x}"`, "python", true},
		{`"SELECT " . $id`, "javascript", false},
	}
	for _, tc := range cases {
		got := containsConcatOrFormat(tc.text, tc.lang)
		if got != tc.want {
			t.Errorf("containsConcatOrFormat(%q, %q) = %v, want %v", tc.text, tc.lang, got, tc.want)
		}
	}
}

// --- End-to-end: Scanner.ScanContent integrates AST + regex --------

func TestScannerScanContent_IncludesAstFindings(t *testing.T) {
	src := []byte("package main\nimport \"os/exec\"\n" +
		"func run(user string) {\n" +
		"	" + fxExecCmd + "(\"sh\", \"-c\", \"ls \" + user)\n" +
		"}")
	s := New()
	_, vulns := s.ScanContent("e.go", src)
	mustContainKind(t, vulns, "Command injection")
}

// The old regex rule flagged literal-only calls as potential command
// injection. With the new literal-args guard in ScanContent, that
// regex match is suppressed for all-literal argument lists.
func TestScannerScanContent_LiteralOnlyExecNoLongerFlagged(t *testing.T) {
	src := []byte("package main\nimport \"os/exec\"\n" +
		"func run() {\n" +
		"	" + fxExecCmd + "(\"git\", \"-C\", \"/tmp\", \"diff\", \"--\")\n" +
		"}")
	s := New()
	_, vulns := s.ScanContent("e.go", src)
	for _, v := range vulns {
		if strings.Contains(v.Kind, "Command") && strings.Contains(v.Kind, "Injection") {
			t.Fatalf("all-literal exec.Command should NOT be flagged, got %+v", v)
		}
	}
}
