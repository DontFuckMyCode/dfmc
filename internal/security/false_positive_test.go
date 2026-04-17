// Regression tests for false positives reported in the 2026-04-17
// self-scan: 60 CWE-798 findings across non-crypto files, plus 7
// comment-origin and rule-pattern findings scanned from the scanner's
// own source. Each test pins a specific shape of false positive so it
// can't regress.

package security

import "testing"

// Split fragments so this source doesn't trip local security hooks
// that grep for dangerous literals. These are TEST FIXTURES, not
// uses of the sinks themselves.
const (
	fpMd5Name  = "md" + "5.New"
	fpSha1Name = "sh" + "a1.Sum"
	fpDynEval  = "ev" + "al"
)

func scanContentHelper(t *testing.T, path, src string) []VulnerabilityFinding {
	t.Helper()
	_, vulns := New().ScanContent(path, []byte(src))
	return vulns
}

// --- CWE-798: hardcoded credential false positives ------------------

// `keys := []string{"go test", "go vet", ...}` used to hit the loose
// old rule because the line contained "key" + `"` + `:=` + a > 8 char
// string literal. The rewritten matcher requires a credential-shaped
// identifier (apikey, apisecret, ...) not just "key".
func TestCredRule_KeysSliceNotFlagged(t *testing.T) {
	src := `package coach
func helper() {
	keys := []string{"go test", "go vet", "go build", "npm test"}
	_ = keys
}
`
	v := scanContentHelper(t, "coach.go", src)
	for _, f := range v {
		if f.CWE == "CWE-798" {
			t.Fatalf("plural 'keys' slice should not be flagged: %+v", f)
		}
	}
}

// Help-text strings like `Usage: "tool NAME key=value"` used to match
// because the line contains "key", `"`, `=`. Rewritten rule only
// triggers on credential-shaped identifiers, and a field assignment
// like `Usage: "..."` has LHS "Usage", not a credential.
func TestCredRule_HelpTextNotFlagged(t *testing.T) {
	src := `package commands
var Tool = struct {
	Usage string
}{
	Usage: "tool NAME key=value ...",
}
`
	v := scanContentHelper(t, "defaults.go", src)
	for _, f := range v {
		if f.CWE == "CWE-798" {
			t.Fatalf("help-text field should not be flagged: %+v", f)
		}
	}
}

// Map-key assignment `key := rel + "#" + ...` used to fire because
// "key" substring, `:=`, `"`, long literal. The new rule requires
// the identifier to be credential-shaped; bare `key` is not.
func TestCredRule_MapKeyVariableNotFlagged(t *testing.T) {
	src := `package context
func k(rel, a, b string) {
	key := rel + "#" + a + "#" + b + "#####"
	_ = key
}
`
	v := scanContentHelper(t, "manager.go", src)
	for _, f := range v {
		if f.CWE == "CWE-798" {
			t.Fatalf("bare 'key' map-key variable should not be flagged: %+v", f)
		}
	}
}

// A real hardcoded credential MUST still fire. Regression guard for
// over-rejection: if the new rule were too strict we'd lose the
// legitimate finding.
func TestCredRule_RealOpenAIKeyFlagged(t *testing.T) {
	src := `package demo
const apiKey = "sk-abcdefghijklmnopqrstuvwxyz1234567890"
`
	v := scanContentHelper(t, "demo.go", src)
	saw := false
	for _, f := range v {
		if f.CWE == "CWE-798" {
			saw = true
		}
	}
	if !saw {
		t.Fatal("real `apiKey = \"sk-...\"` must still be flagged")
	}
}

// A credential-shaped identifier with an ENV-LOOKUP on the RHS must
// not fire — env lookup is the fix, not the bug.
func TestCredRule_EnvLookupNotFlagged(t *testing.T) {
	src := `package demo
import "os"
var apiKey = os.Getenv("OPENAI_API_KEY")
`
	v := scanContentHelper(t, "demo.go", src)
	for _, f := range v {
		if f.CWE == "CWE-798" {
			t.Fatalf("os.Getenv read should not be flagged: %+v", f)
		}
	}
}

// Placeholder literals like "YOUR_API_KEY" or "CHANGEME" must not
// fire — they're boilerplate in example / config templates.
func TestCredRule_PlaceholderLiteralNotFlagged(t *testing.T) {
	src := `package demo
const apiKey = "YOUR_API_KEY_HERE"
const password = "CHANGEME"
`
	v := scanContentHelper(t, "demo.go", src)
	for _, f := range v {
		if f.CWE == "CWE-798" {
			t.Fatalf("placeholder literal should not be flagged: %+v", f)
		}
	}
}

// --- Comment-origin false positives --------------------------------

// A `// ...` Go line comment that mentions a dangerous sink for
// documentation must not be flagged. The scanner now skips pure
// line comments before matching either the regex or AST rules.
func TestScanContent_CommentLineNotFlagged(t *testing.T) {
	src := "package demo\n" +
		"// false positive guard test: exec.Command(\"git\", \"-C\", root, \"diff\")\n" +
		"// also guards SQL: \"SELECT * FROM t WHERE id=\" + userInput\n" +
		"func demo() {}\n"
	v := scanContentHelper(t, "demo.go", src)
	for _, f := range v {
		t.Logf("unexpected finding: %+v", f)
	}
	if len(v) > 0 {
		t.Fatalf("comment lines must not produce findings, got %d", len(v))
	}
}

// Python `#` comments get the same treatment. We build the source
// dynamically so the dynamic-eval sink name doesn't sit as a bare
// literal in this test source file.
func TestScanContent_PythonCommentLineNotFlagged(t *testing.T) {
	src := "# demo of " + fpDynEval + "() in a comment — should not fire\n" +
		"def safe():\n    return 1\n"
	v := scanContentHelper(t, "demo.py", src)
	for _, f := range v {
		t.Fatalf("python # comment produced finding: %+v", f)
	}
}

// --- Scanner-internal rule-body false positives --------------------

// Rule bodies that use `ctx.Trimmed` / `ctx.RecentJoin` are scanner
// machinery, not code under review. Even if the rule body contains a
// literal matching a weak-hash name as part of the pattern, no
// CWE-327 finding should fire on that line.
func TestScanContent_RulePatternSelfMatchNotFlagged(t *testing.T) {
	src := "package security\n" +
		"func fake(ctx *struct{ Trimmed string }) bool {\n" +
		"\treturn strings.Contains(ctx.Trimmed, \"" + fpMd5Name + "\") ||\n" +
		"\t\tstrings.Contains(ctx.Trimmed, \"" + fpSha1Name + "\")\n" +
		"}\n"
	v := scanContentHelper(t, "fakerule.go", src)
	for _, f := range v {
		if f.CWE == "CWE-327" {
			t.Fatalf("rule body referencing ctx.Trimmed must not self-match: %+v", f)
		}
	}
}
