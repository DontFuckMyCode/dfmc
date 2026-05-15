package security

import (
	"testing"
)

// --- Taint tracker unit tests --------------------------------------------

func TestTaintTracker_GoSources(t *testing.T) {
	cases := []struct {
		name    string
		line    string
		want    string // identifier expected tainted; "" means none
		notWant string // identifier expected NOT tainted; "" skips check
	}{
		{"http body assign", "body, _ := io.ReadAll(r.Body)", "body", ""},
		{"req body short", "data := req.Body", "data", ""},
		{"request body explicit", "raw := request.Body", "raw", ""},
		{"form value", "v := r.Form.Get(\"q\")", "v", ""},
		{"post form", "m := req.PostForm", "m", ""},
		{"url query", "q := r.URL.Query()", "q", ""},
		{"url path", "p := r.URL.Path", "p", ""},
		{"header get", "h := r.Header.Get(\"X-Trace\")", "h", ""},
		{"os args", "args := os.Args", "args", ""},
		{"os stdin", "in := os.Stdin", "in", ""},
		{"flag string", "name := flag.String(\"n\", \"\", \"\")", "name", ""},
		// Negative: assignment from a string literal must NOT taint.
		{"literal not tainted", "body := \"safe\"", "", "body"},
		// Negative: unrelated struct that happens to have .Body must
		// not match the http-request source (we anchor on r/req/request).
		{"unrelated struct body", "x := response.Body", "", "x"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tr := newTaintTracker("go")
			tr.observeLine(c.line)
			if c.want != "" && !tr.IsTainted(c.want) {
				t.Fatalf("expected %q tainted after observe(%q); tainted=%v",
					c.want, c.line, tr.tainted)
			}
			if c.notWant != "" && tr.IsTainted(c.notWant) {
				t.Fatalf("did NOT expect %q tainted after observe(%q); tainted=%v",
					c.notWant, c.line, tr.tainted)
			}
		})
	}
}

func TestTaintTracker_GoPropagation(t *testing.T) {
	tr := newTaintTracker("go")
	tr.observeLine("body, _ := io.ReadAll(r.Body)") // body tainted
	tr.observeLine("s := string(body)")             // s should inherit
	tr.observeLine("u := strings.ToLower(s)")       // u should inherit
	tr.observeLine("safe := \"static\"")            // safe must NOT be tainted

	if !tr.IsTainted("body") {
		t.Error("body must be tainted (direct source)")
	}
	if !tr.IsTainted("s") {
		t.Error("s must be tainted via string(body) propagation")
	}
	if !tr.IsTainted("u") {
		t.Error("u must be tainted via strings.ToLower(s) propagation")
	}
	if tr.IsTainted("safe") {
		t.Error("safe must NOT be tainted; assignment is a literal")
	}
}

func TestTaintTracker_NilSafeIsTainted(t *testing.T) {
	var tr *taintTracker
	if tr.IsTainted("anything") {
		t.Fatal("nil tracker must return false")
	}
}

// --- End-to-end rule integration -----------------------------------------

// TestSmartScan_Go_ExecCommandTaintedBody pins the new R1 capability:
// a multi-step body -> sink flow that no concat-only rule could see is
// caught by the taint-aware exec.Command rule.
func TestSmartScan_Go_ExecCommandTaintedBody(t *testing.T) {
	src := "package main\n" +
		"import \"os/exec\"\n" +
		"func run(r *http.Request) {\n" +
		"	body, _ := io.ReadAll(r.Body)\n" +
		"	s := string(body)\n" +
		"	cmd := " + fxExecCmd + "(s)\n" +
		"	_ = cmd\n" +
		"}\n"
	findings := scanHelper(t, "sample.go", src)
	mustContainKind(t, findings, "Command injection via exec.Command with tainted input")
}

// TestSmartScan_Go_ExecCommandLiteralVarStaysSafe pins the negative
// case: a variable assigned from a literal is NOT tainted, so feeding
// it to exec.Command must not fire the taint-aware rule.
func TestSmartScan_Go_ExecCommandLiteralVarStaysSafe(t *testing.T) {
	src := "package main\n" +
		"import \"os/exec\"\n" +
		"func run() {\n" +
		"	bin := \"git\"\n" +
		"	cmd := " + fxExecCmd + "(bin, \"diff\")\n" +
		"	_ = cmd\n" +
		"}\n"
	findings := scanHelper(t, "sample.go", src)
	mustNotContainKind(t, findings, "tainted input")
}

// TestSmartScan_Go_ExecCommandPlainLiteralUnchanged guards against
// regressions where introducing the taint pass would somehow change
// the existing "all literals are safe" behaviour. The TUI git-diff
// helper relies on this -- a fresh taint tracker with no observed
// sources must leave literal calls untouched.
func TestSmartScan_Go_ExecCommandPlainLiteralUnchanged(t *testing.T) {
	src := "package main\nimport \"os/exec\"\n" +
		"func run() {\n" +
		"	cmd := " + fxExecCmd + "(\"git\", \"-C\", \"/some/path\", \"diff\", \"--\")\n" +
		"	_ = cmd\n}\n"
	findings := scanHelper(t, "sample.go", src)
	mustNotContainKind(t, findings, "tainted input")
	mustNotContainKind(t, findings, "Command injection")
}

// TestSmartScan_Go_ExecCommandWithUserArgsDirect pins the simplest
// shape: r.Body -> exec.Command on the next line, no intermediate
// propagation step. Should still flag.
func TestSmartScan_Go_ExecCommandWithUserArgsDirect(t *testing.T) {
	src := "package main\n" +
		"import \"os/exec\"\n" +
		"func run(r *http.Request) {\n" +
		"	body := r.Body\n" +
		"	cmd := " + fxExecCmd + "(body)\n" +
		"	_ = cmd\n" +
		"}\n"
	findings := scanHelper(t, "sample.go", src)
	mustContainKind(t, findings, "tainted input")
}
